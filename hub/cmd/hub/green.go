package main

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/green"
)

// loadGreenConfig loads the green (energy/carbon) config from
// AVURUOBS_GREEN_CONFIG and returns a hot-reloading accessor for it (see
// loadHotReload). An unset path yields green.Default() (world-average factors,
// Kepler default metric names, no budgets).
func loadGreenConfig(ctx context.Context) (func() green.Config, error) {
	return loadHotReload(ctx, "AVURUOBS_GREEN_CONFIG", "green config",
		green.Default(), green.ParseConfig,
		func(cfg green.Config) []any { return []any{"budgets", len(cfg.Budgets)} })
}
