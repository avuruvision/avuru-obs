// tdp-estimator is the sensor DaemonSet's opt-in 5th container: on a node
// with no RAPL/powercap it estimates CPU power from utilization
// (P = P_idle + u*(P_max-P_idle)) and serves the SAME Kepler metric names
// Kepler itself would emit, stamped estimated (not measured) by the
// otel-agent scrape config — see design/2026-07-28-green-tdp-estimation.md.
// Deliberately dependency-free (stdlib only): pod discovery reuses the
// kubelet's own /pods endpoint (same trust model as the otel-agent's
// kubeletstats receiver), not client-go.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	var (
		listenAddr     = flag.String("listen", ":28283", "address to serve /metrics on (loopback-bound by the pod, see values.yaml)")
		nodeName       = flag.String("node-name", os.Getenv("NODE_NAME"), "this node's name, for the kubelet /pods lookup and node_name label")
		sampleInterval = flag.Duration("sample-interval", 5*time.Second, "sampler tick; independent of the otel-agent scrape interval")
		idleWatts      = flag.Float64("idle-watts", 0, "operator-set P_idle override (0 = defer to table/fallback)")
		maxWatts       = flag.Float64("max-watts", 0, "operator-set P_max override (0 = defer to table/fallback)")
	)
	flag.Parse()

	if *nodeName == "" {
		slog.Error("node-name is required (set --node-name or NODE_NAME env)")
		os.Exit(1)
	}

	if raplPresent(defaultRAPLGlob) {
		slog.Info("RAPL present, tdp-estimator staying dormant for this process's lifetime", "node", *nodeName)
		serveDormant(*listenAddr)
		return
	}
	slog.Info("no RAPL detected, estimating via TDP model", "node", *nodeName)

	coeff := Resolve(nodeAnnotations(*nodeName), *idleWatts, *maxWatts, cpuModelName())
	slog.Info("resolved power coefficients", "tier", coeff.Tier, "provenance", coeff.Provenance, "idleWatts", coeff.IdleWatts, "maxWatts", coeff.MaxWatts)

	reg := newRegistry()
	go runSampler(*nodeName, *sampleInterval, coeff, reg)

	http.Handle("/metrics", reg)
	slog.Info("serving /metrics", "addr", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		slog.Error("metrics server exited", "error", err)
		os.Exit(1)
	}
}

// serveDormant answers /metrics with an empty body forever — the container
// stays alive and healthy (no probes are configured on it either way) but
// contributes nothing, matching Kepler's own no-RAPL posture.
func serveDormant(addr string) {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(addr, nil); err != nil {
		slog.Error("dormant metrics server exited", "error", err)
		os.Exit(1)
	}
}
