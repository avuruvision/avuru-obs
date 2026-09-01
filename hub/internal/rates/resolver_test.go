package rates

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type fakeStore struct {
	overlay Table
	err     error
	saved   bool
	reads   int
}

func (f *fakeStore) LoadRatesOverlay(context.Context) (storage.RatesOverlay, error) {
	f.reads++
	if f.err != nil {
		return storage.RatesOverlay{}, f.err
	}
	if !f.saved {
		return storage.RatesOverlay{}, storage.ErrNotFound
	}
	b, _ := json.Marshal(f.overlay)
	return storage.RatesOverlay{Overlay: string(b)}, nil
}

func resolverOver(t *testing.T, chart Table, st *fakeStore) *Resolver {
	t.Helper()
	return NewResolver(func() Table { return chart }, func() Store { return st })
}

// The guarantee the whole package exists for: the API and the budget evaluator
// hold the SAME resolver, so they cannot price a model differently. This is the
// lesson service groups taught when the alerting evaluator turned out to be
// reading different config than the API served.
func TestResolveIsTheSameForEveryHolder(t *testing.T) {
	st := &fakeStore{saved: true, overlay: Table{
		Models: []ModelPrice{{Model: "gpt-4o", InputPer1MTokens: 99}},
	}}
	r := resolverOver(t, chartTable(), st)

	api := r.Resolve(context.Background())
	evaluator := r.Resolve(context.Background())

	pa, _, oka := api.Lookup("gpt-4o")
	pe, _, oke := evaluator.Lookup("gpt-4o")
	if !oka || !oke || pa.InputPer1MTokens != pe.InputPer1MTokens {
		t.Errorf("two holders resolved differently: %v vs %v", pa, pe)
	}
	if pa.InputPer1MTokens != 99 {
		t.Errorf("price = %v, want the overlay's 99", pa.InputPer1MTokens)
	}
}

// Never saved is not an error: it is the empty overlay, i.e. exactly what the
// chart declares. Every install starts here.
func TestResolveNeverSavedIsTheChart(t *testing.T) {
	r := resolverOver(t, chartTable(), &fakeStore{})
	got := r.Resolve(context.Background())
	if len(got.Models) != 1 || got.Models[0].Source != FromChart {
		t.Errorf("resolved = %+v, want the chart alone", got)
	}
}

// Stale beats missing. A bill that cannot be computed because a table is
// briefly unreachable is a worse answer than a slightly old price.
func TestResolveKeepsTheLastKnownOverlayThroughAnOutage(t *testing.T) {
	st := &fakeStore{saved: true, overlay: Table{
		Models: []ModelPrice{{Model: "claude-sonnet", InputPer1MTokens: 3}},
	}}
	r := resolverOver(t, chartTable(), st)

	if got := r.Resolve(context.Background()); len(got.Models) != 2 {
		t.Fatalf("warm-up resolved %d models, want 2", len(got.Models))
	}

	// The store fails, and the memo is expired.
	st.err = errors.New("clickhouse unreachable")
	r.Invalidate()

	got := r.Resolve(context.Background())
	if len(got.Models) != 2 {
		t.Errorf("resolved %d models during an outage, want the last known 2: %+v", len(got.Models), got.Models)
	}
	if _, _, ok := got.Lookup("claude-sonnet"); !ok {
		t.Error("the overlay price vanished during a store outage")
	}
}

// With nothing ever read, an outage degrades to chart-only rather than to
// nothing at all.
func TestResolveFallsBackToChartWhenNothingWasEverRead(t *testing.T) {
	st := &fakeStore{err: errors.New("down")}
	r := resolverOver(t, chartTable(), st)

	got := r.Resolve(context.Background())
	if len(got.Models) != 1 || got.Models[0].Model != "gpt-4o" {
		t.Errorf("resolved = %+v, want chart-only", got.Models)
	}
}

// The memo spares the database on a polling screen, and Invalidate is what
// makes an admin's own write visible to them immediately.
func TestResolveMemoizesUntilInvalidated(t *testing.T) {
	st := &fakeStore{saved: true}
	r := resolverOver(t, chartTable(), st)

	for i := 0; i < 3; i++ {
		r.Resolve(context.Background())
	}
	if st.reads != 1 {
		t.Errorf("store reads = %d within the TTL, want 1", st.reads)
	}

	r.Invalidate()
	r.Resolve(context.Background())
	if st.reads != 2 {
		t.Errorf("store reads = %d after Invalidate, want 2", st.reads)
	}
}

// Declared() is the chart alone — what the CRUD endpoints use to decide which
// entries are config-owned and therefore read-only.
func TestDeclaredIsUnmerged(t *testing.T) {
	st := &fakeStore{saved: true, overlay: Table{
		Models: []ModelPrice{{Model: "claude-sonnet", InputPer1MTokens: 3}},
	}}
	r := resolverOver(t, chartTable(), st)
	r.Resolve(context.Background())

	if got := r.Declared(); len(got.Models) != 1 || got.Models[0].Model != "gpt-4o" {
		t.Errorf("Declared() = %+v, want the chart alone", got.Models)
	}
}

// A nil resolver is usable and means "chart-only, no store", so callers need no
// nil checks.
func TestNilResolverIsUsable(t *testing.T) {
	var r *Resolver
	if got := r.Declared(); !got.Empty() {
		t.Errorf("Declared() = %+v, want empty", got)
	}
	if got := r.Resolve(context.Background()); got.Priced() {
		t.Errorf("Resolve() = %+v, want nothing priced", got)
	}
	r.Invalidate() // must not panic
}
