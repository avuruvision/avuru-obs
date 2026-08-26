//go:build integration

package clickhouse

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const giB = 1024 * 1024 * 1024

// clusterRes builds the resource attributes k8sclusterreceiver puts on a
// container gauge: the container's own identity plus the pod, namespace, node
// and — the reason the reserved side needs no join — the owning workload.
func clusterRes(workload, ns, pod, container, node string) map[string]string {
	return map[string]string{
		"k8s.deployment.name": workload,
		"k8s.namespace.name":  ns,
		"k8s.pod.name":        pod,
		"k8s.container.name":  container,
		"k8s.node.name":       node,
		"container.id":        pod + "/" + container,
	}
}

// kubeletRes builds the resource attributes the kubeletstats pod metrics carry
// after the sensor's k8sattributes has decorated them.
func kubeletRes(workload, ns, pod, node string) map[string]string {
	return map[string]string{
		"k8s.deployment.name": workload,
		"k8s.namespace.name":  ns,
		"k8s.pod.name":        pod,
		"k8s.node.name":       node,
	}
}

func costQuery(tenant string, start, end time.Time) storage.CostQuery {
	return storage.CostQuery{Tenant: tenant, Range: storage.TimeRange{Start: start, End: end}}
}

func nearly(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// The reservation is the sum ACROSS a pod's containers and ACROSS the
// workload's pods — and the total must not multiply by the number of scrapes
// that happened to fall inside the window. That is the whole reason the query
// sums within a bucket and averages across buckets, so it is the first thing
// asserted.
func TestWorkloadCostsSumContainersNotScrapes(t *testing.T) {
	s := startClickHouse(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	// Two pods, two containers each, 0.25 cores requested per container:
	// 1 core reserved for the workload. Written at three separate scrapes.
	for i := range 3 {
		ts := base.Add(time.Duration(i) * 5 * time.Minute)
		for _, pod := range []string{"api-1", "api-2"} {
			for _, c := range []string{"app", "sidecar"} {
				insertGauge(t, s, ts, metricContainerCPURequest,
					clusterRes("api", "shop", pod, c, "node-a"), 0.25)
				insertGauge(t, s, ts, metricContainerMemRequest,
					clusterRes("api", "shop", pod, c, "node-a"), 0.5*giB)
			}
			insertGauge(t, s, ts, metricPodCPU, kubeletRes("api", "shop", pod, "node-a"), 0.1)
			insertGauge(t, s, ts, metricPodMem, kubeletRes("api", "shop", pod, "node-a"), 0.25*giB)
		}
	}

	rows, err := (&Store{conn: s.conn}).WorkloadCosts(context.Background(),
		costQuery("default", base.Add(-time.Minute), base.Add(time.Hour)))
	if err != nil {
		t.Fatalf("WorkloadCosts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d workloads, want 1: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Workload != "api" || got.Namespace != "shop" {
		t.Fatalf("workload = %s/%s, want shop/api", got.Namespace, got.Workload)
	}
	// 2 pods × 2 containers × 0.25 = 1 core, NOT 3 (once per scrape).
	nearly(t, "reserved cpu", got.ReservedCPUCores, 1)
	nearly(t, "reserved mem", got.ReservedMemBytes, 2*giB)
	// Usage is per-pod, summed across the two pods.
	nearly(t, "used cpu peak", got.UsedCPUCoresPeak, 0.2)
	if got.Pods != 2 {
		t.Errorf("pods = %d, want 2", got.Pods)
	}
}

// Peak and mean are different numbers and both are reported, because only the
// peak bounds what can be given back.
func TestWorkloadCostsSeparatesPeakFromMean(t *testing.T) {
	s := startClickHouse(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	insertGauge(t, s, base, metricContainerCPURequest,
		clusterRes("spiky", "shop", "spiky-1", "app", "node-a"), 2)
	// Three buckets: 0.1, 1.9, 0.1 → peak 1.9, mean ~0.7.
	for i, v := range []float64{0.1, 1.9, 0.1} {
		insertGauge(t, s, base.Add(time.Duration(i)*2*time.Minute), metricPodCPU,
			kubeletRes("spiky", "shop", "spiky-1", "node-a"), v)
	}

	rows, err := (&Store{conn: s.conn}).WorkloadCosts(context.Background(),
		costQuery("default", base.Add(-time.Minute), base.Add(time.Hour)))
	if err != nil {
		t.Fatalf("WorkloadCosts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d workloads, want 1", len(rows))
	}
	nearly(t, "peak", rows[0].UsedCPUCoresPeak, 1.9)
	if rows[0].UsedCPUCoresMean >= rows[0].UsedCPUCoresPeak {
		t.Errorf("mean %v is not below peak %v — the two collapsed into one number",
			rows[0].UsedCPUCoresMean, rows[0].UsedCPUCoresPeak)
	}
}

// A workload that declared no request must still appear, with zero reserved.
// An inner join would delete the worst finding on the screen.
func TestWorkloadWithNoRequestStillAppears(t *testing.T) {
	s := startClickHouse(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	// Only usage — no container-request rows at all for this workload.
	insertGauge(t, s, base, metricPodCPU, kubeletRes("rogue", "jobs", "rogue-1", "node-a"), 1.2)
	insertGauge(t, s, base, metricPodMem, kubeletRes("rogue", "jobs", "rogue-1", "node-a"), 3*giB)

	rows, err := (&Store{conn: s.conn}).WorkloadCosts(context.Background(),
		costQuery("default", base.Add(-time.Minute), base.Add(time.Hour)))
	if err != nil {
		t.Fatalf("WorkloadCosts: %v", err)
	}
	var found *storage.WorkloadCost
	for i := range rows {
		if rows[i].Workload == "rogue" {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("the workload that reserves nothing was dropped: %+v", rows)
	}
	nearly(t, "reserved cpu", found.ReservedCPUCores, 0)
	nearly(t, "used cpu peak", found.UsedCPUCoresPeak, 1.2)
}

// A pod with no recognized owner is not a workload's waste and must not be
// reported as one under an empty name.
func TestUnownedPodsAreNotAWorkload(t *testing.T) {
	s := startClickHouse(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	insertGauge(t, s, base, metricPodCPU,
		map[string]string{"k8s.namespace.name": "kube-system", "k8s.pod.name": "static-pod", "k8s.node.name": "node-a"}, 0.4)

	rows, err := (&Store{conn: s.conn}).WorkloadCosts(context.Background(),
		costQuery("default", base.Add(-time.Minute), base.Add(time.Hour)))
	if err != nil {
		t.Fatalf("WorkloadCosts: %v", err)
	}
	for _, r := range rows {
		if r.Workload == "" {
			t.Fatalf("an unowned pod was reported as a nameless workload: %+v", r)
		}
	}
}

// Node allocation: capacity is the latest sample (it does not move), requests
// are summed per bucket and averaged, usage is the latest. A node can be fully
// requested and almost idle, and both numbers have to survive the query.
func TestNodeCostsSeparateRequestedFromUsed(t *testing.T) {
	s := startClickHouse(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	nodeRes := map[string]string{"k8s.node.name": "node-a"}

	insertGauge(t, s, base, metricNodeAllocCPU, nodeRes, 8)
	insertGauge(t, s, base, metricNodeAllocMem, nodeRes, 32*giB)
	insertGauge(t, s, base, metricNodeCPU, nodeRes, 0.8)
	insertGauge(t, s, base, metricNodeMem, nodeRes, 4*giB)
	// Two containers on the node claiming 3.6 cores each.
	for _, pod := range []string{"api-1", "api-2"} {
		insertGauge(t, s, base, metricContainerCPURequest,
			clusterRes("api", "shop", pod, "app", "node-a"), 3.6)
	}

	rows, err := (&Store{conn: s.conn}).NodeCosts(context.Background(),
		costQuery("default", base.Add(-time.Minute), base.Add(time.Hour)))
	if err != nil {
		t.Fatalf("NodeCosts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d nodes, want 1: %+v", len(rows), rows)
	}
	n := rows[0]
	nearly(t, "allocatable cpu", n.AllocatableCPUCores, 8)
	nearly(t, "requested cpu", n.RequestedCPUCores, 7.2)
	nearly(t, "used cpu", n.UsedCPUCores, 0.8)
	if n.RequestedCPUCores <= n.UsedCPUCores {
		t.Error("requested collapsed into used — the whole point is that they differ")
	}
}
