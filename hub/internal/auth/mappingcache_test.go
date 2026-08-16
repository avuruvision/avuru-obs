package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// TestMappingCacheRefreshInstallsMergedMapper: Refresh merges the current
// config with whatever the store holds and Mapper (the closure handed to
// SetGroupMapper) returns grants derived from that merge.
func TestMappingCacheRefreshInstallsMergedMapper(t *testing.T) {
	fake := &storagetest.Fake{
		OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
			"oncall": {Group: "oncall", Role: "editor", Projects: []string{"default"}},
		},
	}
	cache := NewMappingCache(func() storage.Store { return fake })
	cache.SetConfig(MappingConfig{
		Mapping: []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}},
	})

	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	grants := cache.Mapper()([]string{"oncall", "platform"})
	want := []Grant{{Scope: "default", Role: RoleEditor}, {Scope: "*", Role: RoleAdmin}}
	if !grantsEqual(grants, want) {
		t.Fatalf("grants after refresh = %+v, want %+v", grants, want)
	}
}

// TestMappingCacheServesStaleSnapshotUntilRefresh is the proof this is a
// genuine cache and not a read-through: the store's rows change underneath
// it, but the mapper keeps answering from the OLD snapshot until Refresh
// runs again.
func TestMappingCacheServesStaleSnapshotUntilRefresh(t *testing.T) {
	fake := &storagetest.Fake{
		OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
			"oncall": {Group: "oncall", Role: "editor", Projects: []string{"default"}},
		},
	}
	cache := NewMappingCache(func() storage.Store { return fake })
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	mapper := cache.Mapper()

	before := mapper([]string{"oncall"})
	wantBefore := []Grant{{Scope: "default", Role: RoleEditor}}
	if !grantsEqual(before, wantBefore) {
		t.Fatalf("initial grants = %+v, want %+v", before, wantBefore)
	}

	// Mutate the store directly, WITHOUT calling Refresh.
	fake.OIDCGroupMappings["oncall"] = storage.OIDCGroupMapping{
		Group: "oncall", Role: "viewer", Projects: []string{"default"},
	}

	stillOld := mapper([]string{"oncall"})
	if !grantsEqual(stillOld, wantBefore) {
		t.Fatalf("mapper must keep serving the old snapshot before Refresh: got %+v, want %+v", stillOld, wantBefore)
	}

	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	after := mapper([]string{"oncall"})
	wantAfter := []Grant{{Scope: "default", Role: RoleViewer}}
	if !grantsEqual(after, wantAfter) {
		t.Fatalf("grants after Refresh = %+v, want %+v", after, wantAfter)
	}
}

// TestRefreshErrorKeepsLastGoodSnapshot is the case that matters most: an
// unreachable ClickHouse must not silently strip every SSO user's grants.
// That would be a lockout, not a degraded read.
func TestRefreshErrorKeepsLastGoodSnapshot(t *testing.T) {
	fake := &storagetest.Fake{
		OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
			"oncall": {Group: "oncall", Role: "editor", Projects: []string{"default"}},
		},
	}
	cache := NewMappingCache(func() storage.Store { return fake })
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := cache.Mapper()([]string{"oncall"})
	if len(want) == 0 {
		t.Fatal("test setup: expected a non-empty grant set before the failure")
	}

	fake.OIDCGroupMappingsErr = errors.New("clickhouse down")
	// Also change the underlying row so a pass-through bug (returning the
	// live row despite the error) would be caught, not masked by nothing
	// having changed.
	fake.OIDCGroupMappings["oncall"] = storage.OIDCGroupMapping{
		Group: "oncall", Role: "viewer", Projects: []string{"default"},
	}

	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("expected Refresh to report the store error")
	}

	got := cache.Mapper()([]string{"oncall"})
	if !grantsEqual(got, want) {
		t.Fatalf("grants must be UNCHANGED after a failed refresh: got %+v, want %+v", got, want)
	}
}

// TestMappingCacheDegradesToConfigOnlyWhenStoreUnavailable: the hub starts
// before ClickHouse is necessarily reachable. Refresh must not fail hard in
// that window - it degrades to the config-only mapping and the periodic poll
// picks up the overlay once the store comes up.
func TestMappingCacheDegradesToConfigOnlyWhenStoreUnavailable(t *testing.T) {
	cache := NewMappingCache(func() storage.Store { return nil })
	cache.SetConfig(MappingConfig{
		Mapping: []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}},
	})

	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh with no store yet must not fail hard: %v", err)
	}

	grants := cache.Mapper()([]string{"platform"})
	want := []Grant{{Scope: "*", Role: RoleAdmin}}
	if !grantsEqual(grants, want) {
		t.Fatalf("grants = %+v, want %+v", grants, want)
	}
}

// TestMappingCacheRaceRefreshVsRead pins that reading the snapshot
// concurrently with a Refresh is race-free (run with -race).
func TestMappingCacheRaceRefreshVsRead(t *testing.T) {
	fake := &storagetest.Fake{
		OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
			"oncall": {Group: "oncall", Role: "editor", Projects: []string{"default"}},
		},
	}
	cache := NewMappingCache(func() storage.Store { return fake })
	mapper := cache.Mapper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = cache.Refresh(context.Background())
		}
	}()
	for i := 0; i < 200; i++ {
		_ = mapper([]string{"oncall"})
		_ = cache.Snapshot()
	}
	<-done
}
