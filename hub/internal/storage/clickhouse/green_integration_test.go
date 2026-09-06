//go:build integration

package clickhouse

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Green fixtures use config-shaped names on purpose: the SQL must take metric
// names and attribute keys from the query, never hardcode Kepler naming.
const (
	testPodEnergyA  = "kepler_pod_cpu_joules_total"
	testPodEnergyB  = "kepler_pod_dram_joules_total" // second metric: zones split across metrics
	testNodeEnergyA = "kepler_node_cpu_joules_total"
	testNodeEnergyB = "kepler_node_dram_joules_total"
)

// greenQuery returns the default-config-shaped query over [start, end).
func greenQuery(tenant string, start, end time.Time, interval time.Duration) storage.GreenQuery {
	return storage.GreenQuery{
		Tenant:            tenant,
		Range:             storage.TimeRange{Start: start, End: end},
		PodEnergyMetrics:  []string{testPodEnergyA, testPodEnergyB},
		NodeEnergyMetrics: []string{testNodeEnergyA, testNodeEnergyB},
		PodNameAttr:       "pod_name",
		PodNamespaceAttr:  "pod_namespace",
		Interval:          interval,
	}
}

func podAttrs(pod, ns string, extra map[string]string) map[string]string {
	m := map[string]string{"pod_name": pod, "pod_namespace": ns}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// greenBase returns a window start in the past aligned to interval multiples,
// so toStartOfInterval bucket boundaries are deterministic in assertions.
func greenBase(interval time.Duration) time.Time {
	return time.Now().UTC().Add(-time.Hour).Truncate(interval)
}

func wantWh(t *testing.T, label string, got, wantJoules float64) {
	t.Helper()
	want := wantJoules / 3600
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v Wh, want %v Wh (%v J)", label, got, want, wantJoules)
	}
}

// TestServiceEnergyDeltaMath is the counter-delta spec: deltas are computed
// per unique series (metric + full attribute identity) per bucket and summed
// after, so multi-zone and multi-metric series never cross-cancel; a counter
// reset between buckets is contained by the per-bucket grouping (each
// bucket's max−min is independent, so the post-reset bucket contributes its
// own small delta and nothing goes negative — the greatest(…,0) clamp is
// defensive-only and cannot fire since max ≥ min); the kubeletstats join
// attributes pods to workloads with owner precedence deployment >
// statefulset; and energy from a pod absent from the map lands in the
// unattributed bucket (empty service).
func TestServiceEnergyDeltaMath(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 5 * time.Minute
	base := greenBase(interval)
	end := base.Add(10 * time.Minute) // two 5-minute buckets
	noRes := map[string]string{}      // Tenant defaults to "default"

	// web-1 (deployment web), three series in bucket 1 that would cross-cancel
	// if deltas were not per-series: zone attrs on metric A, plus metric B with
	// IDENTICAL attributes (only MetricName separates the series).
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 100)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 400) // Δ300
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "dram"}), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "dram"}), 150) // Δ150
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyB, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 1000)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyB, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 1090) // Δ90
	// web-1 bucket 2: only the package series advances.
	insertSum(t, store, base.Add(6*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 400)
	insertSum(t, store, base.Add(9*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 700) // Δ300

	// cart-1 (statefulset cart): counter reset between buckets. Naive
	// last-minus-first over the window would be 600-5000 < 0; per-bucket
	// deltas give 2000 + 500. (Had the reset landed WITHIN one bucket, the
	// bucket would OVERCOUNT — max keeps the pre-reset high-water mark — the
	// documented approximation.)
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes, podAttrs("cart-1", "shop", nil), 5000)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes, podAttrs("cart-1", "shop", nil), 7000)
	insertSum(t, store, base.Add(6*time.Minute), testPodEnergyA, noRes, podAttrs("cart-1", "shop", nil), 100) // reset
	insertSum(t, store, base.Add(9*time.Minute), testPodEnergyA, noRes, podAttrs("cart-1", "shop", nil), 600)

	// ghost-1: energy but NO kubeletstats row — the unattributed bucket.
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes, podAttrs("ghost-1", "shop", nil), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes, podAttrs("ghost-1", "shop", nil), 3600)

	// Decoys that must be pruned: out-of-window sample, other metric name.
	insertSum(t, store, base.Add(30*time.Minute), testPodEnergyA, noRes, podAttrs("web-1", "shop", map[string]string{"zone": "package"}), 99999)
	insertSum(t, store, base.Add(2*time.Minute), "k8s.node.network.io", noRes, podAttrs("web-1", "shop", nil), 77777)

	// kubeletstats pod→workload map. web-1 carries BOTH deployment and
	// statefulset owner attrs: deployment must win the precedence.
	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"k8s.pod.name": "web-1", "k8s.namespace.name": "shop",
		"k8s.deployment.name": "web", "k8s.statefulset.name": "web-ss",
	}, 0.2)
	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"k8s.pod.name": "cart-1", "k8s.namespace.name": "shop",
		"k8s.statefulset.name": "cart",
	}, 0.1)

	got, err := store.ServiceEnergy(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("ServiceEnergy: %v", err)
	}
	byService := map[string]storage.ServiceEnergy{}
	for _, se := range got {
		byService[se.Service] = se
	}
	if len(byService) != 3 {
		t.Fatalf("got %d services %+v, want web, cart and the unattributed bucket", len(byService), got)
	}

	web := byService["web"]
	wantWh(t, "web total", web.WattHours, 300+150+90+300)
	if len(web.Points) != 2 {
		t.Fatalf("web points = %+v, want 2 buckets", web.Points)
	}
	wantWh(t, "web bucket1", web.Points[0].WattHours, 540)
	wantWh(t, "web bucket2", web.Points[1].WattHours, 300)

	cart := byService["cart"]
	wantWh(t, "cart total (reset must not cancel)", cart.WattHours, 2500)
	for _, p := range cart.Points {
		if p.WattHours < 0 {
			t.Errorf("cart bucket %v went negative: %v", p.Time, p.WattHours)
		}
	}

	unattributed := byService[""]
	wantWh(t, "unattributed total", unattributed.WattHours, 3600)

	// Ordering contract: heaviest first.
	for i := 1; i < len(got); i++ {
		if got[i-1].WattHours < got[i].WattHours {
			t.Errorf("services not ordered by Wh desc: %+v", got)
		}
	}
}

// TestServiceEnergyBucketing pins bucket boundaries and the joules→Wh
// conversion: samples land in their toStartOfInterval bucket, bucket
// timestamps are the interval starts, and each bucket's Wh is its own
// max−min delta / 3600 (delta across bucket boundaries is intentionally
// dropped — the documented approximation).
func TestServiceEnergyBucketing(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 2 * time.Minute
	base := greenBase(interval)
	end := base.Add(6 * time.Minute) // three 2-minute buckets
	noRes := map[string]string{}

	// Boundary samples repeat the previous value so no delta is lost and the
	// expected numbers stay exact.
	insertSum(t, store, base, testPodEnergyA, noRes, podAttrs("p-1", "apps", nil), 0)
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes, podAttrs("p-1", "apps", nil), 600) // bucket 0: Δ600
	insertSum(t, store, base.Add(2*time.Minute), testPodEnergyA, noRes, podAttrs("p-1", "apps", nil), 600)
	insertSum(t, store, base.Add(3*time.Minute), testPodEnergyA, noRes, podAttrs("p-1", "apps", nil), 1800) // bucket 1: Δ1200
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes, podAttrs("p-1", "apps", nil), 1800)
	insertSum(t, store, base.Add(5*time.Minute), testPodEnergyA, noRes, podAttrs("p-1", "apps", nil), 5400) // bucket 2: Δ3600

	insertGauge(t, store, base.Add(1*time.Minute), metricPodCPU, map[string]string{
		"k8s.pod.name": "p-1", "k8s.namespace.name": "apps", "k8s.daemonset.name": "p",
	}, 0.1)

	got, err := store.ServiceEnergy(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("ServiceEnergy: %v", err)
	}
	if len(got) != 1 || got[0].Service != "p" {
		t.Fatalf("got %+v, want the single daemonset workload p", got)
	}
	wantWh(t, "p total", got[0].WattHours, 5400) // = 1.5 Wh
	if len(got[0].Points) != 3 {
		t.Fatalf("points = %+v, want 3 buckets", got[0].Points)
	}
	wantJ := []float64{600, 1200, 3600}
	for i, p := range got[0].Points {
		wantT := base.Add(time.Duration(i) * interval)
		if !p.Time.Equal(wantT) {
			t.Errorf("bucket %d time = %v, want %v", i, p.Time, wantT)
		}
		wantWh(t, "bucket", p.WattHours, wantJ[i])
	}
}

// TestNodeEnergyIntegration: per-node totals/series from the node counters —
// multiple node metrics sum per node, the node comes from the standard
// k8s.node.name resource attribute, and rows without it are excluded.
func TestNodeEnergyIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 10 * time.Minute
	base := greenBase(interval)
	end := base.Add(10 * time.Minute)
	resA := map[string]string{"k8s.node.name": "node-a"}
	resB := map[string]string{"k8s.node.name": "node-b"}

	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA, resA, map[string]string{}, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA, resA, map[string]string{}, 1800) // Δ1800
	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyB, resA, map[string]string{}, 100)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyB, resA, map[string]string{}, 400) // Δ300
	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA, resB, map[string]string{}, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA, resB, map[string]string{}, 7200) // Δ7200
	// No node attribute: must not surface as a phantom node.
	insertSum(t, store, base.Add(2*time.Minute), testNodeEnergyA, map[string]string{}, map[string]string{}, 500)

	got, err := store.NodeEnergy(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("NodeEnergy: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d nodes %+v, want node-a and node-b", len(got), got)
	}
	// Heaviest first: node-b (7200 J) before node-a (2100 J).
	if got[0].Node != "node-b" || got[1].Node != "node-a" {
		t.Fatalf("order wrong: %+v", got)
	}
	wantWh(t, "node-b total", got[0].WattHours, 7200) // = 2 Wh
	wantWh(t, "node-a total", got[1].WattHours, 1800+300)
	if len(got[1].Points) != 1 || !got[1].Points[0].Time.Equal(base) {
		t.Errorf("node-a points = %+v, want one bucket at %v", got[1].Points, base)
	}
}

// TestGreenTenantIsolation: tenant A's energy — pod and node — never
// surfaces for tenant B, and the pod→workload map is tenant-scoped too.
func TestGreenTenantIsolation(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 10 * time.Minute
	base := greenBase(interval)
	end := base.Add(10 * time.Minute)
	acme := map[string]string{"avuru.tenant": "acme"}
	acmeNode := map[string]string{"avuru.tenant": "acme", "k8s.node.name": "node-a"}

	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, acme, podAttrs("web-1", "shop", nil), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, acme, podAttrs("web-1", "shop", nil), 720)
	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA, acmeNode, map[string]string{}, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA, acmeNode, map[string]string{}, 3600)
	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"avuru.tenant": "acme", "k8s.pod.name": "web-1", "k8s.namespace.name": "shop",
		"k8s.deployment.name": "web",
	}, 0.2)

	for _, tenant := range []string{"default", "other"} {
		svc, err := store.ServiceEnergy(ctx, greenQuery(tenant, base, end, interval))
		if err != nil {
			t.Fatalf("ServiceEnergy(%s): %v", tenant, err)
		}
		if len(svc) != 0 {
			t.Errorf("tenant leak: %s sees service energy %+v", tenant, svc)
		}
		nodes, err := store.NodeEnergy(ctx, greenQuery(tenant, base, end, interval))
		if err != nil {
			t.Fatalf("NodeEnergy(%s): %v", tenant, err)
		}
		if len(nodes) != 0 {
			t.Errorf("tenant leak: %s sees node energy %+v", tenant, nodes)
		}
	}

	svc, err := store.ServiceEnergy(ctx, greenQuery("acme", base, end, interval))
	if err != nil {
		t.Fatalf("ServiceEnergy(acme): %v", err)
	}
	if len(svc) != 1 || svc[0].Service != "web" {
		t.Fatalf("acme service energy = %+v, want attributed workload web", svc)
	}
	wantWh(t, "acme web", svc[0].WattHours, 720) // = 0.2 Wh

	nodes, err := store.NodeEnergy(ctx, greenQuery("acme", base, end, interval))
	if err != nil {
		t.Fatalf("NodeEnergy(acme): %v", err)
	}
	if len(nodes) != 1 || nodes[0].Node != "node-a" {
		t.Fatalf("acme node energy = %+v, want node-a", nodes)
	}
	wantWh(t, "acme node-a", nodes[0].WattHours, 3600)
}

// TestServiceEnergyQualitySplit seeds one service with BOTH a measured
// (Kepler) and an estimated (tdp-estimator) series on distinct pods, and
// asserts the per-quality split is exact — quality never gets blended into
// one number, and a series carrying no quality attribute at all (pre-AEP
// data, or a misconfigured install) reads as an empty Quality string rather
// than silently dropping. The delta math itself is unaffected: quality
// rides the grouping, not the series identity (seriesIDExpr).
func TestServiceEnergyQualitySplit(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 5 * time.Minute
	base := greenBase(interval)
	end := base.Add(interval)
	noRes := map[string]string{}

	// web-1 on a RAPL node: measured.
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-1", "shop", map[string]string{"avuruobs_quality": "measured"}), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-1", "shop", map[string]string{"avuruobs_quality": "measured"}), 360) // Δ360 J

	// web-2, same service (workload), on a RAPL-less node: estimated.
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-2", "shop", map[string]string{"avuruobs_quality": "estimated"}), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-2", "shop", map[string]string{"avuruobs_quality": "estimated"}), 720) // Δ720 J

	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"k8s.pod.name": "web-1", "k8s.namespace.name": "shop", "k8s.deployment.name": "web",
	}, 0.1)
	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"k8s.pod.name": "web-2", "k8s.namespace.name": "shop", "k8s.deployment.name": "web",
	}, 0.2)

	rows, err := store.ServiceEnergy(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("ServiceEnergy: %v", err)
	}

	var measuredWh, estimatedWh float64
	var sawQualities []string
	for _, row := range rows {
		if row.Service != "web" {
			t.Errorf("unexpected service row %+v, want only \"web\"", row)
			continue
		}
		sawQualities = append(sawQualities, row.Quality)
		switch row.Quality {
		case "measured":
			measuredWh = row.WattHours
		case "estimated":
			estimatedWh = row.WattHours
		default:
			t.Errorf("unexpected quality %q on row %+v", row.Quality, row)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows %+v, want exactly 2 (one per quality)", len(rows), rows)
	}
	wantWh(t, "measured", measuredWh, 360)
	wantWh(t, "estimated", estimatedWh, 720)
}

// TestNodeCoverage seeds one node reporting measured energy, one reporting
// estimated, and asserts a THIRD known-but-silent node (present via a
// kubeletstats-style gauge row, the same resource attribute NodeEnergy joins
// on) is counted as absent — the exact gap the green-carbon AEP review
// flagged as invisible before this feature.
func TestNodeCoverage(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 5 * time.Minute
	base := greenBase(interval)
	end := base.Add(interval)

	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-measured"}, map[string]string{"avuruobs_quality": "measured"}, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-measured"}, map[string]string{"avuruobs_quality": "measured"}, 100)

	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-estimated"}, map[string]string{"avuruobs_quality": "estimated"}, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-estimated"}, map[string]string{"avuruobs_quality": "estimated"}, 100)

	// node-silent: seed a kubeletstats-style presence row so it is a KNOWN
	// node, but never any green energy series for it — must count as absent.
	insertGauge(t, store, base.Add(2*time.Minute), "k8s.node.cpu.utilization",
		map[string]string{"k8s.node.name": "node-silent"}, 0.1)

	cov, err := store.NodeCoverage(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("NodeCoverage: %v", err)
	}
	if cov.KnownNodes != 3 || cov.MeasuredNodes != 1 || cov.EstimatedNodes != 1 || cov.AbsentNodes != 1 {
		t.Errorf("NodeCoverage = %+v, want {Known:3 Measured:1 Estimated:1 Absent:1}", cov)
	}
	// The known-node names ride the same query, sorted — the silent node must
	// be listed even though it reported no energy.
	if strings.Join(cov.Nodes, ",") != "node-estimated,node-measured,node-silent" {
		t.Errorf("NodeCoverage.Nodes = %v, want the sorted known universe", cov.Nodes)
	}
}

// greenQueryTenants is greenQuery with an explicit resolved tenant set.
func greenQueryTenants(tenant string, tenants []string, start, end time.Time, interval time.Duration) storage.GreenQuery {
	q := greenQuery(tenant, start, end, interval)
	q.Tenants = tenants
	return q
}

// TestMultiTenantGreenReads proves the Tenant IN (?) fan-out for the three
// green reads. Energy is additive, so an aggregate project's number is the
// sum over its members — including a node reporting under two tenants, whose
// per-tenant deltas add up rather than one shadowing the other.
func TestMultiTenantGreenReads(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 10 * time.Minute
	base := greenBase(interval)
	end := base.Add(interval)
	both := []string{"m1", "m2"}

	m1 := map[string]string{"avuru.tenant": "m1"}
	m2 := map[string]string{"avuru.tenant": "m2"}
	m1NodeA := map[string]string{"avuru.tenant": "m1", "k8s.node.name": "node-a"}
	m2NodeA := map[string]string{"avuru.tenant": "m2", "k8s.node.name": "node-a"}
	m2NodeB := map[string]string{"avuru.tenant": "m2", "k8s.node.name": "node-b"}

	// Pod energy + the kubeletstats rows that attribute it to a workload.
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, m1, podAttrs("web-1", "shop", nil), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, m1, podAttrs("web-1", "shop", nil), 720)
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, m2, podAttrs("api-1", "shop", nil), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, m2, podAttrs("api-1", "shop", nil), 1440)
	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"avuru.tenant": "m1", "k8s.node.name": "node-a", "k8s.pod.name": "web-1",
		"k8s.namespace.name": "shop", "k8s.deployment.name": "web",
	}, 0.2)
	insertGauge(t, store, base.Add(2*time.Minute), metricPodCPU, map[string]string{
		"avuru.tenant": "m2", "k8s.node.name": "node-b", "k8s.pod.name": "api-1",
		"k8s.namespace.name": "shop", "k8s.deployment.name": "api",
	}, 0.4)

	// node-a reports in BOTH tenants (measured); node-b only in m2 (estimated).
	measured := map[string]string{"avuruobs_quality": "measured"}
	estimated := map[string]string{"avuruobs_quality": "estimated"}
	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA, m1NodeA, measured, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA, m1NodeA, measured, 3600)
	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA, m2NodeA, measured, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA, m2NodeA, measured, 1800)
	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA, m2NodeB, estimated, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA, m2NodeB, estimated, 7200)

	t.Run("ServiceEnergy", func(t *testing.T) {
		one, err := store.ServiceEnergy(ctx, greenQueryTenants("m1", []string{"m1"}, base, end, interval))
		if err != nil {
			t.Fatalf("ServiceEnergy one: %v", err)
		}
		if len(one) != 1 || one[0].Service != "web" {
			t.Fatalf("single-tenant service energy = %+v, want just web", one)
		}
		wantWh(t, "m1 web", one[0].WattHours, 720)

		merged, err := store.ServiceEnergy(ctx, greenQueryTenants("m1", both, base, end, interval))
		if err != nil {
			t.Fatalf("ServiceEnergy merged: %v", err)
		}
		if len(merged) != 2 || merged[0].Service != "api" || merged[1].Service != "web" {
			t.Fatalf("merged service energy = %+v, want api then web (heaviest first)", merged)
		}
		wantWh(t, "merged api", merged[0].WattHours, 1440)
		wantWh(t, "merged web", merged[1].WattHours, 720)
	})

	t.Run("NodeEnergy", func(t *testing.T) {
		one, err := store.NodeEnergy(ctx, greenQueryTenants("m1", []string{"m1"}, base, end, interval))
		if err != nil {
			t.Fatalf("NodeEnergy one: %v", err)
		}
		if len(one) != 1 || one[0].Node != "node-a" {
			t.Fatalf("single-tenant node energy = %+v, want node-a", one)
		}
		wantWh(t, "m1 node-a", one[0].WattHours, 3600)

		merged, err := store.NodeEnergy(ctx, greenQueryTenants("m1", both, base, end, interval))
		if err != nil {
			t.Fatalf("NodeEnergy merged: %v", err)
		}
		if len(merged) != 2 || merged[0].Node != "node-b" || merged[1].Node != "node-a" {
			t.Fatalf("merged node energy = %+v, want node-b then node-a", merged)
		}
		wantWh(t, "merged node-b", merged[0].WattHours, 7200)
		// node-a's two per-tenant series each keep their own delta and sum:
		// the series identity includes ResourceAttributes, so they never
		// cross-cancel through max−min.
		wantWh(t, "merged node-a", merged[1].WattHours, 3600+1800)
	})

	t.Run("NodeCoverage", func(t *testing.T) {
		one, err := store.NodeCoverage(ctx, greenQueryTenants("m1", []string{"m1"}, base, end, interval))
		if err != nil {
			t.Fatalf("NodeCoverage one: %v", err)
		}
		if one.KnownNodes != 1 || one.MeasuredNodes != 1 || one.EstimatedNodes != 0 || one.AbsentNodes != 0 {
			t.Fatalf("single-tenant coverage = %+v, want {Known:1 Measured:1}", one)
		}

		merged, err := store.NodeCoverage(ctx, greenQueryTenants("m1", both, base, end, interval))
		if err != nil {
			t.Fatalf("NodeCoverage merged: %v", err)
		}
		// node-a is measured in both tenants — counted once, not twice.
		if merged.KnownNodes != 2 || merged.MeasuredNodes != 1 || merged.EstimatedNodes != 1 || merged.AbsentNodes != 0 {
			t.Errorf("merged coverage = %+v, want {Known:2 Measured:1 Estimated:1 Absent:0}", merged)
		}
	})
}
