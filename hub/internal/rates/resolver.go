package rates

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Store is the slice of storage.Store the resolver reads. Narrow on purpose:
// main wires it from the live store provider, tests hand it a two-line fake,
// and this package stays free of the rest of storage.
type Store interface {
	LoadRatesOverlay(ctx context.Context) (storage.RatesOverlay, error)
}

// storedTTL memoizes the stored overlay for a few seconds. Reading a ClickHouse
// table on every priced row would be much heavier than reading an in-memory
// config, and the screens poll. Writes call Invalidate, so an admin's own edit
// is never stale to them.
const storedTTL = 5 * time.Second

// Resolver is the ONE place chart-declared rates (the hot-reloaded ConfigMap)
// and UI-authored rates (ClickHouse) are merged.
//
// main hands the SAME Resolver to the API and to the budget evaluator, which is
// the whole point: the evaluator does not go through the API, so merging inside
// a handler would let a budget be measured against a different price than the
// screen displays. Service groups taught this exact lesson when the alerting
// evaluator turned out to be reading different config than the API served.
//
// The zero value is not usable; a nil *Resolver is, and behaves as
// "chart-only, no store" so callers need no nil checks.
type Resolver struct {
	declared func() Table
	store    func() Store

	mu      sync.Mutex
	cached  Table
	fetched time.Time
	warm    bool // whether cached has ever been filled (distinguishes "empty" from "unread")
}

// NewResolver returns a Resolver over a config accessor and a store accessor.
// Either may be nil: a nil config accessor means an empty chart table, a nil
// store accessor means chart-only — which is what an install running without
// ClickHouse reachable degrades to.
func NewResolver(declared func() Table, store func() Store) *Resolver {
	return &Resolver{declared: declared, store: store}
}

// Declared returns the chart-declared table alone, unmerged. The CRUD endpoints
// use it to decide which entries are config-owned (and therefore read-only);
// nothing that prices anything should call it.
func (r *Resolver) Declared() Table {
	if r == nil || r.declared == nil {
		return Table{}
	}
	return r.declared()
}

// Resolve returns the merged table — chart-declared rates with UI-authored ones
// overlaid. This is what every priced number must use.
//
// A store outage never fails the call: the last known overlay is reused (stale
// beats missing), falling back to chart-only if none was ever read. A bill that
// cannot be computed because a table is briefly unreachable would be a worse
// answer than a slightly old price.
func (r *Resolver) Resolve(ctx context.Context) Resolved {
	chart := r.Declared()
	if r == nil {
		return Merge(chart, Table{})
	}
	return Merge(chart, r.storedCached(ctx))
}

// Stored returns the UI-authored overlay straight from the store, uncached and
// with the error surfaced. The CRUD handlers use it: a write must not be built
// on a five-second-old view, and a store outage must fail the write rather than
// silently drop it.
func (r *Resolver) Stored(ctx context.Context) (Table, error) {
	if r == nil || r.store == nil {
		return Table{}, nil
	}
	st := r.store()
	if st == nil {
		return Table{}, errors.New("rates store unavailable")
	}
	ov, err := st.LoadRatesOverlay(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		// Never saved is not an error: it is the empty overlay, i.e. exactly
		// what the chart declares.
		return Table{}, nil
	}
	if err != nil {
		return Table{}, err
	}
	return ParseOverlay([]byte(ov.Overlay))
}

// Invalidate drops the memoized overlay so the next read reflects a write that
// just happened.
func (r *Resolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fetched = time.Time{}
}

func (r *Resolver) storedCached(ctx context.Context) Table {
	if r.store == nil {
		return Table{}
	}
	if t, ok := r.fresh(); ok {
		return t
	}
	t, err := r.Stored(ctx)
	if err != nil {
		// Stale beats missing: a transient store failure must not silently
		// un-price the estate.
		return r.lastKnown()
	}
	r.mu.Lock()
	r.cached, r.fetched, r.warm = t, time.Now(), true
	r.mu.Unlock()
	return t
}

func (r *Resolver) fresh() (Table, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warm && time.Since(r.fetched) < storedTTL {
		return r.cached, true
	}
	return Table{}, false
}

func (r *Resolver) lastKnown() Table {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cached
}
