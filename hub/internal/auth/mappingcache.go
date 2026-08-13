package auth

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// MappingConfig is the chart-declared half of the OIDC group mapping - the
// rules read from the ConfigMap, plus the default-role fallback used when
// nothing matches. It is kept separate from the merged snapshot so a
// ConfigMap hot-reload can replace it (SetConfig) without disturbing the
// mapping the hub is currently enforcing until the next Refresh actually
// merges it in.
type MappingConfig struct {
	Mapping         []GroupMap
	DefaultRole     Role
	DefaultProjects []string
}

// mappingSnapshot is the merged mapping paired with the default-role
// fallback it was computed against. The two travel together so a Refresh
// racing a concurrent SetConfig can never let Mapper read a mapping built
// from one config alongside defaults taken from a newer one - Refresh reads
// both config fields once and swaps them in as a single unit.
type mappingSnapshot struct {
	mapping         []EffectiveMapping
	defaultRole     Role
	defaultProjects []string
}

// MappingCache holds the merged (chart config + DB overlay) OIDC group→role
// mapping behind an atomic snapshot. It exists because SSO grants are
// derived on every authenticated request (see identityFor in service.go),
// unlike the collection overlay, which is read per admin-screen request - a
// per-request ClickHouse read is not acceptable here.
//
// The snapshot refreshes on three triggers - startup, ConfigMap hot-reload,
// and a local overlay write - plus a periodic poll (cmd/hub/oidc.go), which
// is the ONLY thing that bounds staleness on another hub replica: an admin's
// write lands in ClickHouse immediately, but replica B's cache does not know
// until it next calls Refresh. Refresh-on-write alone would leave replica B
// serving the old mapping indefinitely.
type MappingCache struct {
	store func() storage.Store

	cfg      atomic.Pointer[MappingConfig]
	snapshot atomic.Pointer[mappingSnapshot]
}

// NewMappingCache returns a cache with an empty config and an empty (but
// non-nil) snapshot, so Mapper is safe to call before the first Refresh -
// which matters because SetGroupMapper installs it once at startup, before
// loadOIDCConfig has necessarily had a chance to run Refresh even once.
func NewMappingCache(store func() storage.Store) *MappingCache {
	c := &MappingCache{store: store}
	c.cfg.Store(&MappingConfig{})
	c.snapshot.Store(&mappingSnapshot{})
	return c
}

// SetConfig replaces the config half after a ConfigMap hot-reload. It does
// NOT touch the merged snapshot by itself - call Refresh right after (the
// hot-reload path in cmd/hub/oidc.go does) so the edit takes effect
// immediately instead of waiting for the next poll tick.
func (c *MappingCache) SetConfig(cfg MappingConfig) {
	c.cfg.Store(&cfg)
}

// Refresh loads the current overlay rows from the store, merges them with
// the current config half, and swaps in the resulting snapshot.
//
// A nil store (ClickHouse not connected yet - the hub starts before the
// store is necessarily reachable) degrades to the config-only mapping rather
// than failing: there is no overlay to lose yet, and the periodic poll picks
// up any DB rows once the store comes up.
//
// A non-nil store whose query FAILS is different: there may already be a
// good snapshot carrying real DB-authored grants, and losing those on a
// transient ClickHouse blip would silently strip every SSO user relying on
// an authored (not chart-declared) rule - a lockout. So a query error leaves
// the previous snapshot exactly as it was, is logged at warn, and is also
// returned so a caller that cares (the write handlers in Task 4) can surface
// it to the admin who just wrote.
func (c *MappingCache) Refresh(ctx context.Context) error {
	cfg := c.cfg.Load()

	var rows []storage.OIDCGroupMapping
	if st := c.store(); st != nil {
		var err error
		rows, err = st.ListOIDCGroupMappings(ctx)
		if err != nil {
			slog.Warn("oidc mapping refresh failed, keeping last good snapshot", "error", err)
			return fmt.Errorf("listing oidc group mappings: %w", err)
		}
	}

	c.snapshot.Store(&mappingSnapshot{
		mapping:         MergeMapping(cfg.Mapping, rows),
		defaultRole:     cfg.DefaultRole,
		defaultProjects: cfg.DefaultProjects,
	})
	return nil
}

// Mapper returns the closure to hand to auth.Service.SetGroupMapper. It
// reads the live snapshot on every call rather than closing over one at
// install time, so SetGroupMapper only ever needs to be called once - a
// Refresh anywhere else becomes visible on the very next authenticated
// request without re-installing anything.
func (c *MappingCache) Mapper() func(groups []string) []Grant {
	return func(groups []string) []Grant {
		s := c.snapshot.Load()
		return GrantsFor(s.mapping, groups, s.defaultRole, s.defaultProjects)
	}
}

// Snapshot returns the merged mapping currently being enforced, for the
// admin list endpoint (Task 4) - the same rules Mapper derives grants from,
// so what an admin sees is exactly what this replica is enforcing right now.
func (c *MappingCache) Snapshot() []EffectiveMapping {
	return c.snapshot.Load().mapping
}
