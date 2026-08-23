package main

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/topology"
)

// loadTopologyConfig loads the service-map topology config from
// AVURUOBS_TOPOLOGY_CONFIG and returns a hot-reloading accessor for it (see
// loadHotReload). An unset path yields topology.Default() — the built-in mesh
// and gateway patterns, which is what most installs want.
func loadTopologyConfig(ctx context.Context) (func() topology.Config, error) {
	return loadHotReload(ctx, "AVURUOBS_TOPOLOGY_CONFIG", "topology config",
		topology.Default(), topology.ParseConfig,
		func(cfg topology.Config) []any {
			return []any{"transportPatterns", len(cfg.TransportPatterns()), "applications", len(cfg.Applications)}
		})
}
