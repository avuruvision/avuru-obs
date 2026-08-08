package health

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// fakeGroupStore counts reads so the memoization can be observed.
type fakeGroupStore struct {
	groups []storage.ServiceGroup
	err    error
	reads  int
}

func (f *fakeGroupStore) ListServiceGroups(context.Context) ([]storage.ServiceGroup, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	return f.groups, nil
}

func configWith(groups ...Group) Config {
	c := Default()
	c.Groups = groups
	return c
}

func groupNames(c Config) []string {
	out := make([]string, 0, len(c.Groups))
	for _, g := range c.Groups {
		out = append(out, g.Name)
	}
	return out
}

func TestResolverMergesStoredGroups(t *testing.T) {
	store := &fakeGroupStore{groups: []storage.ServiceGroup{
		{Name: "checkout", Tier: "T1", Namespaces: []string{"shop"}},
	}}
	r := NewResolver(
		func() Config {
			return configWith(Group{Name: "payments", Tier: TierT0, Selector: Selector{Services: []string{"pay"}}})
		},
		func() GroupStore { return store },
	)

	cfg := r.Config(context.Background())
	if got := groupNames(cfg); len(got) != 2 || got[0] != "payments" || got[1] != "checkout" {
		t.Fatalf("merged groups = %v, want [payments checkout]", got)
	}
	// Declared() must stay unmerged — the CRUD endpoints decide what is
	// read-only from it, so a stored group leaking in would make itself
	// un-editable.
	if got := groupNames(r.Declared()); len(got) != 1 || got[0] != "payments" {
		t.Fatalf("declared groups = %v, want [payments]", got)
	}
}

// Config wins a name collision: a Git-managed install must not be silently
// overridden from a browser (design/2026-08-07-service-groups-crud.md).
func TestResolverConfigWinsNameCollision(t *testing.T) {
	store := &fakeGroupStore{groups: []storage.ServiceGroup{
		{Name: "payments", Tier: "T3", Namespaces: []string{"nope"}},
	}}
	r := NewResolver(
		func() Config {
			return configWith(Group{Name: "payments", Tier: TierT0, Selector: Selector{Namespaces: []string{"pay"}}})
		},
		func() GroupStore { return store },
	)

	cfg := r.Config(context.Background())
	if len(cfg.Groups) != 1 {
		t.Fatalf("groups = %v, want the config one only", groupNames(cfg))
	}
	if cfg.Groups[0].Tier != TierT0 || cfg.Groups[0].Selector.Namespaces[0] != "pay" {
		t.Fatalf("stored row overrode the config group: %+v", cfg.Groups[0])
	}
}

// Ordering is part of precedence: matchGroup takes the first matching group,
// so a service both sides claim must land in the config's.
func TestResolverConfigGroupsComeFirst(t *testing.T) {
	store := &fakeGroupStore{groups: []storage.ServiceGroup{
		{Name: "from-ui", Tier: "T3", Namespaces: []string{"shop"}},
	}}
	r := NewResolver(
		func() Config {
			return configWith(Group{Name: "from-chart", Tier: TierT0, Selector: Selector{Namespaces: []string{"shop"}}})
		},
		func() GroupStore { return store },
	)

	assigned := Assign(r.Config(context.Background()),
		[]storage.ServiceStats{{Name: "web"}},
		[]storage.ServiceLabel{{Service: "web", K8sNamespace: "shop"}})
	if got := assigned["web"].Group; got != "from-chart" {
		t.Fatalf("web grouped into %q, want from-chart", got)
	}
}

func TestResolverMemoizesAndInvalidates(t *testing.T) {
	store := &fakeGroupStore{groups: []storage.ServiceGroup{{Name: "a", Tier: "T1", Namespaces: []string{"x"}}}}
	r := NewResolver(Default, func() GroupStore { return store })

	for range 3 {
		r.Config(context.Background())
	}
	if store.reads != 1 {
		t.Fatalf("store reads = %d, want 1 (memoized within the TTL)", store.reads)
	}

	// A write invalidates, so the admin's own edit is never stale to them.
	store.groups = append(store.groups, storage.ServiceGroup{Name: "b", Tier: "T2", Namespaces: []string{"y"}})
	r.Invalidate()
	if got := groupNames(r.Config(context.Background())); len(got) != 2 {
		t.Fatalf("after Invalidate groups = %v, want both", got)
	}
	if store.reads != 2 {
		t.Fatalf("store reads = %d, want 2", store.reads)
	}
}

// /health is the screen you open when things are broken, so a store outage
// must not fail the read — the last known set is reused.
func TestResolverKeepsLastKnownOnStoreError(t *testing.T) {
	store := &fakeGroupStore{groups: []storage.ServiceGroup{{Name: "a", Tier: "T1", Namespaces: []string{"x"}}}}
	r := NewResolver(Default, func() GroupStore { return store })
	if got := groupNames(r.Config(context.Background())); len(got) != 1 {
		t.Fatalf("warm-up groups = %v", got)
	}

	store.err = errors.New("clickhouse down")
	r.Invalidate()
	if got := groupNames(r.Config(context.Background())); len(got) != 1 || got[0] != "a" {
		t.Fatalf("groups during outage = %v, want the last known [a]", got)
	}
}

// A never-read resolver with an unreachable store answers config-only rather
// than an empty group set.
func TestResolverFallsBackToConfigWhenStoreUnavailable(t *testing.T) {
	declared := configWith(Group{Name: "payments", Tier: TierT0, Selector: Selector{Namespaces: []string{"pay"}}})
	r := NewResolver(func() Config { return declared }, func() GroupStore { return nil })
	if got := groupNames(r.Config(context.Background())); len(got) != 1 || got[0] != "payments" {
		t.Fatalf("groups = %v, want the config one", got)
	}
}

// A nil resolver is the "nothing wired" case (tests, config-less installs) and
// must behave like the zero-config default rather than panic.
func TestNilResolverIsConfigDefault(t *testing.T) {
	var r *Resolver
	if got := r.Config(context.Background()).DefaultTier; got != TierT2 {
		t.Fatalf("nil resolver defaultTier = %q, want T2", got)
	}
	if got := r.Declared().Groups; got != nil {
		t.Fatalf("nil resolver groups = %v, want none", got)
	}
	r.Invalidate() // must not panic
	if _, err := r.Stored(context.Background()); err != nil {
		t.Fatalf("nil resolver Stored: %v", err)
	}
}
