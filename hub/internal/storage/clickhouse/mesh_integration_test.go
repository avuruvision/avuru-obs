//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The whole control-plane read against real ClickHouse: a gauge that must be
// read as "latest", two counters summed, and a histogram walked for p95.
func TestMeshControlPlaneIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	res := map[string]string{"service.name": "istiod"}

	// Connected proxies is a GAUGE. Two scrapes: 9 then 12. The answer is 12,
	// not 21 — summing scrapes would report a proxy fleet that grows with the
	// scrape interval.
	insertGauge(t, store, base.Add(1*time.Minute), "pilot_xds", res, 9)
	insertGauge(t, store, base.Add(2*time.Minute), "pilot_xds", res, 12)
	// Counters.
	insertSum(t, store, base.Add(1*time.Minute), "pilot_xds_pushes", res, map[string]string{}, 300)
	insertSum(t, store, base.Add(2*time.Minute), "pilot_xds_pushes", res, map[string]string{}, 100)
	insertSum(t, store, base.Add(1*time.Minute), "pilot_total_xds_rejects", res, map[string]string{}, 3)
	// Convergence histogram: bounds in seconds, cumulative [5,15,19,20] of 20;
	// p95 (>=19) first reached at bucket 3 -> bound 0.25s -> 250ms.
	insertHistogram(t, store, base.Add(1*time.Minute), "pilot_proxy_convergence_time", res,
		map[string]string{}, []uint64{5, 10, 4, 1, 0}, []float64{0.05, 0.1, 0.25, 1})

	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}
	cp, err := store.MeshControlPlane(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("MeshControlPlane: %v", err)
	}
	if !cp.Available {
		t.Fatal("control plane reported unavailable with series present")
	}
	if cp.ConnectedProxies != 12 {
		t.Errorf("connected proxies = %d, want 12 (the LATEST gauge, not a sum)", cp.ConnectedProxies)
	}
	if cp.Pushes != 400 {
		t.Errorf("pushes = %d, want 400", cp.Pushes)
	}
	if cp.RejectedConfigs != 3 {
		t.Errorf("rejected configs = %d, want 3", cp.RejectedConfigs)
	}
	if cp.ConvergenceP95Ms != 250 {
		t.Errorf("convergence p95 = %v ms, want 250", cp.ConvergenceP95Ms)
	}
	if cp.LastSeen.IsZero() {
		t.Error("no lastSeen on an available control plane")
	}
}

// Nothing scraped must come back as "not available", never as a healthy-looking
// set of zeros — a control plane reported as "0 rejected configs" while nobody
// watches it is the exact failure this surface exists to prevent.
func TestMeshControlPlaneAbsent(t *testing.T) {
	store := startClickHouse(t)
	tr := storage.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}

	cp, err := store.MeshControlPlane(context.Background(), storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("MeshControlPlane with no series: %v", err)
	}
	if cp.Available {
		t.Errorf("reported available with nothing scraped: %+v", cp)
	}
}
