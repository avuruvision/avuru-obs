package main

// wattSample is one instantaneous node-power reading at a point in the
// sampler's elapsed-time timeline (seconds since the estimator started).
type wattSample struct {
	atSeconds float64
	watts     float64
}

// nodePower is the AEP's power curve: P = P_idle + u*(P_max-P_idle), a
// straight-line interpolation between the two coefficient tiers by
// utilization u in [0,1]. Documented limitation (AEP): real power curves are
// convex; a curve exponent is a post-v1 refinement, not implemented here.
func nodePower(c Coefficients, util float64) float64 {
	return c.IdleWatts + util*(c.MaxWatts-c.IdleWatts)
}

// integrateJoules is the trapezoidal integral of a wattage series over
// elapsed time — joules = watts * seconds, summed trapezoid-by-trapezoid so
// uneven sample gaps (a missed tick, GC pause, whatever) are handled
// correctly using only the two samples that straddle the gap, never
// extrapolating beyond what was actually observed. A single sample has no
// interval to integrate over and contributes 0 — the caller's next tick
// completes the first interval.
func integrateJoules(samples []wattSample) float64 {
	var joules float64
	for i := 1; i < len(samples); i++ {
		dt := samples[i].atSeconds - samples[i-1].atSeconds
		avgW := (samples[i].watts + samples[i-1].watts) / 2
		joules += avgW * dt
	}
	return joules
}

// podShareOfActive is a pod's fraction of the node's non-idle CPU-seconds in
// a window: podActiveSeconds / nodeActiveSeconds. Returns 0 when the node
// reported no active time (guards div-by-zero; also correctly yields 0 pod
// dynamic power, since there is no dynamic power to share when nothing was
// busy).
func podShareOfActive(podActiveSeconds, nodeActiveSeconds float64) float64 {
	if nodeActiveSeconds <= 0 {
		return 0
	}
	return podActiveSeconds / nodeActiveSeconds
}

// podDynamicPower is a pod's share of the node's DYNAMIC power only
// (nodeWatts - idleWatts), scaled by its share of active CPU time. Idle
// power is deliberately excluded and stays node-only (lands in the hub's
// existing unattributed bucket) — no pod caused the idle draw, and
// attributing it would corrupt per-service comparisons (AEP non-goal).
func podDynamicPower(nodeWatts, idleWatts, shareOfActive float64) float64 {
	dynamic := nodeWatts - idleWatts
	if dynamic < 0 {
		dynamic = 0
	}
	return dynamic * shareOfActive
}
