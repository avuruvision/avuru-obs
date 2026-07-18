package health

import (
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Status values reuse the hub's existing health vocabulary (see
// api/system_status.go) so the UI needs no new status strings.
const (
	StatusHealthy  = "healthy"
	StatusDegraded = "degraded"
	StatusDown     = "down"
	StatusIdle     = "idle"    // no / insufficient traffic — not an outage
	StatusUnknown  = "unknown" // used only for empty rollups
)

// severity orders statuses for worst-of comparisons. Idle/unknown sit below
// healthy: they never make a group look worse than a service that is actually
// serving. worst() therefore ignores them when a real status is present.
func severity(status string) int {
	switch status {
	case StatusHealthy:
		return 1
	case StatusDegraded:
		return 2
	case StatusDown:
		return 3
	default: // idle, unknown
		return 0
	}
}

// worst returns the higher-severity of two statuses. When both are off-scale
// (idle/unknown), it keeps a, so a run of idle members stays idle.
func worst(a, b string) string {
	if severity(b) > severity(a) {
		return b
	}
	return a
}

// serviceStatus computes a service's base status from its RED stats and the
// resolved thresholds, plus a human reason for the dominant breach. The
// traffic gate runs first: a quiet window is idle, never down.
func serviceStatus(s storage.ServiceStats, thr Thresholds) (status, reason string) {
	if s.SpanCount == 0 {
		return StatusIdle, "no traffic in window"
	}
	if s.SpanCount < thr.MinSampleCount {
		return StatusIdle, fmt.Sprintf("insufficient data (%d spans < %d)", s.SpanCount, thr.MinSampleCount)
	}

	errRate := float64(s.ErrorCount) / float64(s.SpanCount)
	p95ms := float64(s.P95) / float64(time.Millisecond)

	errStatus, errReason := StatusHealthy, ""
	switch {
	case errRate >= thr.ErrorRateCrit:
		errStatus = StatusDown
		errReason = fmt.Sprintf("error rate %.1f%% ≥ %.1f%% budget", errRate*100, thr.ErrorRateCrit*100)
	case errRate >= thr.ErrorRateWarn:
		errStatus = StatusDegraded
		errReason = fmt.Sprintf("error rate %.1f%% ≥ %.1f%% budget", errRate*100, thr.ErrorRateWarn*100)
	}

	latStatus, latReason := StatusHealthy, ""
	if thr.LatencyP95ObjectiveMs > 0 && p95ms >= thr.LatencyP95ObjectiveMs {
		latStatus = StatusDegraded
		latReason = fmt.Sprintf("p95 %.0fms ≥ %.0fms objective", p95ms, thr.LatencyP95ObjectiveMs)
	}

	// Combine: worst-of, reason follows the signal that produced the worst
	// status (error wins ties — a down/degraded error rate is the headline).
	status = worst(errStatus, latStatus)
	switch status {
	case StatusDown:
		reason = errReason
	case StatusDegraded:
		if errStatus == StatusDegraded {
			reason = errReason
		} else {
			reason = latReason
		}
	default:
		reason = "within error budget and latency objective"
	}
	return status, reason
}
