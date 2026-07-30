// tdp-estimator is the sensor DaemonSet's opt-in 5th container: on a node
// with no RAPL/powercap it estimates CPU power from utilization
// (P = P_idle + u*(P_max-P_idle)) and serves the SAME Kepler metric names
// Kepler itself would emit, stamped estimated (not measured) by the
// otel-agent scrape config — see design/2026-07-28-green-tdp-estimation.md.
// Deliberately dependency-free (stdlib only): pod discovery reuses the
// kubelet's own /pods endpoint (same trust model as the otel-agent's
// kubeletstats receiver), not client-go.
//
// This file is a placeholder until Task 7 (metrics.go/sampler_loop.go) — its
// dependencies (Resolve, newRegistry, runSampler) don't exist until then, and
// Go compiles a package as one unit, so a premature full body here would
// block every other file's tests from building standalone.
package main

func main() {}
