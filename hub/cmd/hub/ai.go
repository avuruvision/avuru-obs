package main

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/ai"
)

// loadAIConfig loads the AI-observability prices from AVURUOBS_AI_CONFIG and
// returns a hot-reloading accessor for it (see loadHotReload). An unset path
// yields ai.Default() — no prices, which the screens report as token counts
// rather than as free.
func loadAIConfig(ctx context.Context) (func() ai.Config, error) {
	return loadHotReload(ctx, "AVURUOBS_AI_CONFIG", "ai config",
		ai.Default(), ai.ParseConfig,
		func(cfg ai.Config) []any { return []any{"prices", len(cfg.Prices)} })
}
