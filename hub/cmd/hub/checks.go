package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/checks"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// checkTick is how often the scheduler looks for due checks. It is NOT the
// check interval — each check carries its own — but the granularity at which
// one can come due. Ten seconds keeps a 60s check honest without a tick that
// costs anything to run.
const checkTick = 10 * time.Second

// errStoreUnavailable is returned when a probe finishes while ClickHouse is
// down. The result is lost, which is the right trade: a probe that cannot be
// recorded must not block the next one.
var errStoreUnavailable = errors.New("store unavailable")

// storeRecorder adapts the store provider to the scheduler's Recorder. The
// store may not be connected yet (ClickHouse is allowed to be down without the
// hub crash-looping), in which case a result is dropped with a warning rather
// than stalling the probe that produced it.
type storeRecorder struct{ provider api.StoreProvider }

func (s storeRecorder) RecordCheckResult(ctx context.Context, r checks.Result) error {
	store := s.provider()
	if store == nil {
		return errStoreUnavailable
	}
	// The scheduler's Result and the stored row are deliberately separate
	// types: `checks` imports `health`, which imports `storage`, so a storage
	// method typed on checks.Result would close an import cycle. This is the
	// seam where the two meet, so the conversion lives here.
	return store.RecordCheckResult(ctx, storage.CheckResult{
		CheckID:   r.CheckID,
		Group:     r.Group,
		At:        r.At,
		OK:        r.OK,
		Status:    r.Status,
		LatencyMs: float64(r.Latency.Nanoseconds()) / 1e6,
		Error:     r.Error,
		TraceID:   r.TraceID,
		SpanID:    r.SpanID,
		Tenant:    r.Tenant,
	})
}

// runEndpointChecks starts the probe scheduler.
//
// Started only when the service-health module is active AND at least one check
// is declared at boot — but the loop then stays alive regardless, because the
// config is hot-reloadable and a check added by `kubectl edit` has to start
// running without a restart, exactly like the groups it is declared beside.
func runEndpointChecks(
	ctx context.Context, provider api.StoreProvider, gate *schemaGate,
	groupsCfg func() health.Config, emitter checks.Emitter, tenant string,
) {
	// Every result is an INSERT; without the schema the scheduler would warn on
	// every probe forever. Wait for the gate, as the alerting evaluator does.
	if !gate.wait(ctx) {
		return
	}
	s := checks.New(groupsCfg, storeRecorder{provider}, emitter, tenant, slog.Default())
	slog.Info("endpoint check scheduler started", "checks", len(groupsCfg().AllChecks()), "tick", checkTick)
	s.Run(ctx, checkTick)
}
