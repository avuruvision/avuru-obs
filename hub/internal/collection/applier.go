package collection

import (
	"context"
	"log/slog"
)

// Applier pushes an overlay change to the cluster. The real implementation is
// K8sApplier in this package (render the chart from the install's published
// base values, then reconcile the sensor ConfigMaps + DaemonSet — design/
// 2026-07-27-collection-control-plane.md); this seam is what lets the storage
// and API layers be tested with no cluster at all.
type Applier interface {
	// Apply reconciles the cluster's sensor manifests to match overlay.
	// Called after every successful overlay write, including a reset (i.e.
	// an Empty() overlay, meaning "back to chart defaults").
	Apply(ctx context.Context, overlay Overlay) error
}

// NoopApplier logs that a real apply was skipped. It is the deliberate
// fallback on every install where there is no cluster to reconcile: runtime
// control switched off, compose/local runs, and an in-cluster hub whose
// applier could not be built (missing release identity env, no service
// account). The overlay still persists and reads back correctly — only "the
// sensor pods actually pick it up" is missing, and the log line says so.
type NoopApplier struct{}

func (NoopApplier) Apply(_ context.Context, _ Overlay) error {
	slog.Warn("collection overlay saved but not applied to the cluster — no cluster applier is wired on this install")
	return nil
}
