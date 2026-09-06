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
	// The optional series were not inserted, so they must come back nil. This
	// is the install still running the shorter keep-list: healthy, and simply
	// not publishing them.
	if cp.PushP95Ms != nil || cp.WriteTimeouts != nil || cp.ConfigEvents != nil {
		t.Errorf("optional metrics materialised from nothing: push=%v timeouts=%v events=%v",
			cp.PushP95Ms, cp.WriteTimeouts, cp.ConfigEvents)
	}
}

// The widened keep-list, on an install that collects it. The write-timeout case
// is the one that matters: a measured ZERO must arrive as a present zero, or it
// is indistinguishable from never having looked.
func TestMeshControlPlaneOptionalSeries(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	res := map[string]string{"service.name": "istiod"}

	insertGauge(t, store, base.Add(1*time.Minute), "pilot_xds", res, 12)
	insertSum(t, store, base.Add(1*time.Minute), "pilot_xds_write_timeout", res, map[string]string{}, 0)
	insertSum(t, store, base.Add(1*time.Minute), "pilot_k8s_cfg_events", res, map[string]string{}, 40)
	insertSum(t, store, base.Add(2*time.Minute), "pilot_k8s_cfg_events", res, map[string]string{}, 48)
	// Same bucket shape as convergence: p95 first reached at bound 0.25s.
	insertHistogram(t, store, base.Add(1*time.Minute), "pilot_xds_push_time", res,
		map[string]string{}, []uint64{5, 10, 4, 1, 0}, []float64{0.05, 0.1, 0.25, 1})

	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}
	cp, err := store.MeshControlPlane(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("MeshControlPlane: %v", err)
	}
	if cp.PushP95Ms == nil || *cp.PushP95Ms != 250 {
		t.Errorf("push p95 = %v, want 250ms", cp.PushP95Ms)
	}
	if cp.WriteTimeouts == nil {
		t.Fatal("a measured zero came back nil, which reads as 'not collected'")
	}
	if *cp.WriteTimeouts != 0 {
		t.Errorf("write timeouts = %d, want 0", *cp.WriteTimeouts)
	}
	if cp.ConfigEvents == nil || *cp.ConfigEvents != 88 {
		t.Errorf("config events = %v, want 88 summed", cp.ConfigEvents)
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

// The state that did not exist before: the scrape ran, the target answered, and
// nothing this product reads came back. Indistinguishable from "nobody is
// scraping" until now, and a completely different problem.
func TestControlPlaneAnsweredButUnrecognised(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)
	// `up = 1` for the scrape job, and not one pilot_* series. Prometheus's
	// scrape-report series bypass metric_relabel_configs, which is why they are
	// here at all when the keep-list dropped everything else.
	insertGauge(t, s, now.Add(-time.Minute), "up",
		map[string]string{"service.name": "istiod"}, 1)

	cp, err := s.MeshControlPlane(context.Background(), storage.ServiceQuery{
		Tenant:        "default",
		Range:         storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Minute)},
		MeshScrapeJob: "istiod",
	})
	if err != nil {
		t.Fatalf("MeshControlPlane: %v", err)
	}
	if cp.Available {
		t.Fatal("an unrecognised control plane reported available")
	}
	if cp.State != storage.MeshControlPlaneUnrecognised {
		t.Errorf("state = %q, want unrecognised", cp.State)
	}
}

// Scraped and not answering: the endpoint is wrong or the control plane is
// down. Also invisible before — it looked exactly like nothing being
// configured.
func TestControlPlaneScrapedButUnreachable(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)
	insertGauge(t, s, now.Add(-2*time.Minute), "up",
		map[string]string{"service.name": "istiod"}, 1)
	// The latest sample is what counts: a target that answered and then stopped
	// is unreachable now, not healthy because it once was.
	insertGauge(t, s, now.Add(-time.Minute), "up",
		map[string]string{"service.name": "istiod"}, 0)

	cp, err := s.MeshControlPlane(context.Background(), storage.ServiceQuery{
		Tenant:        "default",
		Range:         storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Minute)},
		MeshScrapeJob: "istiod",
	})
	if err != nil {
		t.Fatalf("MeshControlPlane: %v", err)
	}
	if cp.State != storage.MeshControlPlaneUnreachable {
		t.Errorf("state = %q, want unreachable", cp.State)
	}
}

// No scrape at all stays "unconfigured" — and a scrape under a DIFFERENT job
// name must not be mistaken for this one, which is why the job travels as a
// value rather than being assumed.
func TestControlPlaneUnconfiguredIgnoresOtherJobs(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)
	insertGauge(t, s, now.Add(-time.Minute), "up",
		map[string]string{"service.name": "some-other-scrape"}, 1)

	cp, err := s.MeshControlPlane(context.Background(), storage.ServiceQuery{
		Tenant:        "default",
		Range:         storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Minute)},
		MeshScrapeJob: "istiod",
	})
	if err != nil {
		t.Fatalf("MeshControlPlane: %v", err)
	}
	if cp.State != storage.MeshControlPlaneUnconfigured {
		t.Errorf("state = %q, want unconfigured — another job's `up` was read as ours", cp.State)
	}
}
