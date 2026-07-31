package collection

import (
	"context"
	"log/slog"
)

// Applier pushes an overlay change to the cluster. The real implementation
// (design/2026-07-27-collection-control-plane.md: render the chart via an
// embedded Helm engine, patch the sensor ConfigMaps + DaemonSet) ships in a
// follow-up plan; this seam lets storage + the API land and be fully tested
// first, per this repo's one-logical-change-per-MR convention (AGENTS.md).
type Applier interface {
	// Apply reconciles the cluster's sensor manifests to match overlay.
	// Called after every successful overlay write, including a reset (i.e.
	// an Empty() overlay, meaning "back to chart defaults").
	Apply(ctx context.Context, overlay Overlay) error
}

// NoopApplier logs that a real apply was skipped. Used while
// collection.runtimeControl.enabled is on but the cluster-side applier isn't
// wired yet: the overlay still persists correctly and the API is fully
// functional end to end — only "the sensor pods actually pick it up" is
// deferred to the follow-up plan.
type NoopApplier struct{}

func (NoopApplier) Apply(_ context.Context, _ Overlay) error {
	slog.Warn("collection overlay saved but not applied to the cluster — applier not implemented yet")
	return nil
}
