package health

import (
	"context"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// GroupStore is the slice of storage.Store the resolver reads. Narrow on
// purpose: main wires it from the live store provider, tests hand it a
// two-line fake, and the health package stays free of the rest of storage.
type GroupStore interface {
	ListServiceGroups(ctx context.Context) ([]storage.ServiceGroup, error)
}

// storedGroupsTTL memoizes the stored group set for a few seconds. Reading a
// ClickHouse table on every health request is much heavier than reading an
// in-memory config, and the /health screen polls. Writes call Invalidate, so
// an admin's own edit is never stale to them.
const storedGroupsTTL = 5 * time.Second

// Resolver is the ONE place chart-declared groups (the hot-reloaded ConfigMap)
// and UI-authored groups (ClickHouse) are merged. main hands the same Resolver
// to the API and to the alerting evaluator, which is the whole point: the
// evaluator does not go through the API, so merging inside an API handler
// would show a UI-created T0 group as critical on /health while alerting
// never paged on it. See design/2026-08-07-service-groups-crud.md.
//
// The zero value is not usable; a nil *Resolver is, and behaves as
// "config-only, no store" so callers need no nil checks.
type Resolver struct {
	declared func() Config
	store    func() GroupStore

	mu      sync.Mutex
	cached  []storage.ServiceGroup
	fetched time.Time
	warm    bool // whether cached has ever been filled (distinguishes "empty" from "unread")
}

// NewResolver returns a Resolver over a config accessor and a store accessor.
// Either may be nil: a nil config accessor means Default() (auto-grouping
// only), a nil store accessor means config-only — which is what an install
// running without ClickHouse reachable degrades to.
func NewResolver(declared func() Config, store func() GroupStore) *Resolver {
	return &Resolver{declared: declared, store: store}
}

// Declared returns the chart-declared configuration alone, unmerged. The CRUD
// endpoints use it to decide which names are config-owned (and therefore
// read-only); nothing that computes health should call it.
func (r *Resolver) Declared() Config {
	if r == nil || r.declared == nil {
		return Default()
	}
	return r.declared()
}

// Config returns the merged configuration — chart-declared groups plus the
// UI-authored ones the config does not already name. This is what every health
// computation must use.
//
// A store outage never fails the call: the last known stored set is reused
// (stale beats missing), falling back to config-only if none was ever read.
// /health is the screen you open when things are broken, so it has to answer.
func (r *Resolver) Config(ctx context.Context) Config {
	cfg := r.Declared()
	if r == nil {
		return cfg
	}
	return Merge(cfg, r.storedCached(ctx))
}

// Stored returns the UI-authored groups straight from the store, uncached and
// with the error surfaced. The CRUD handlers use it: a create must not decide
// "that name is free" from a five-second-old view, and a store outage must
// fail the write rather than silently drop it.
func (r *Resolver) Stored(ctx context.Context) ([]storage.ServiceGroup, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	st := r.store()
	if st == nil {
		return nil, nil
	}
	groups, err := st.ListServiceGroups(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cached, r.fetched, r.warm = groups, time.Now(), true
	r.mu.Unlock()
	return groups, nil
}

// Invalidate drops the memoized stored set. Every write path calls it so the
// next read reflects the change immediately.
func (r *Resolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.fetched = time.Time{}
	r.mu.Unlock()
}

// storedCached returns the stored groups, refreshing at most every
// storedGroupsTTL and degrading to the previous value on a store error.
func (r *Resolver) storedCached(ctx context.Context) []storage.ServiceGroup {
	if r.store == nil {
		return nil
	}
	if cached, ok := r.fresh(); ok {
		return cached
	}
	st := r.store()
	if st == nil {
		return r.lastKnown()
	}
	groups, err := st.ListServiceGroups(ctx)
	if err != nil {
		return r.lastKnown()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cached, r.fetched, r.warm = groups, time.Now(), true
	return groups
}

// fresh returns the memoized set while it is inside the TTL.
func (r *Resolver) fresh() ([]storage.ServiceGroup, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warm && time.Since(r.fetched) < storedGroupsTTL {
		return r.cached, true
	}
	return nil, false
}

func (r *Resolver) lastKnown() []storage.ServiceGroup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cached
}

// Merge appends stored groups to cfg for every name the config does not
// already define — config wins a collision. A Git-managed install must not be
// silently overridden from a browser; making the conflict loud at write time
// (the API answers 409) beats discovering the drift at the next helm upgrade.
//
// Order matters as much as precedence: matchGroup takes the FIRST group whose
// selector matches, so config groups going in first means a service claimed by
// both a chart selector and a stored one lands in the chart's group.
func Merge(cfg Config, stored []storage.ServiceGroup) Config {
	if len(stored) == 0 {
		return cfg
	}
	declared := make(map[string]bool, len(cfg.Groups))
	for _, g := range cfg.Groups {
		declared[g.Name] = true
	}
	merged := make([]Group, 0, len(cfg.Groups)+len(stored))
	merged = append(merged, cfg.Groups...)
	for _, s := range stored {
		if declared[s.Name] {
			continue
		}
		merged = append(merged, StoredGroup(s))
	}
	cfg.Groups = merged
	return cfg
}

// StoredGroup converts a persisted row into the config vocabulary. Tier is
// carried through as-is: writes are validated, and a row that somehow holds an
// unknown tier is better shown in the UI's unassigned lane than silently
// dropped from the group set.
func StoredGroup(s storage.ServiceGroup) Group {
	return Group{
		Name: s.Name,
		Tier: Tier(s.Tier),
		Selector: Selector{
			Namespaces: s.Namespaces,
			Services:   s.Services,
		},
	}
}
