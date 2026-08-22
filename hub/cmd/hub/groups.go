package main

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/health"
)

// loadGroupsConfig loads the service-health config from AVURUOBS_GROUPS_CONFIG
// and returns a hot-reloading accessor for it (see loadHotReload). An unset
// path yields health.Default() (auto-only grouping).
func loadGroupsConfig(ctx context.Context) (func() health.Config, error) {
	return loadHotReload(ctx, "AVURUOBS_GROUPS_CONFIG", "service-health config",
		health.Default(), health.ParseConfig,
		func(cfg health.Config) []any { return []any{"groups", len(cfg.Groups)} })
}
