package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// k8s_cluster metric names (design/2026-08-26-cost-and-waste.md). The four
// container gauges are enabled by default upstream; the two node gauges only
// exist when allocatable_types_to_report names them, which the chart does.
const (
	metricContainerCPURequest = "k8s.container.cpu_request"
	metricContainerMemRequest = "k8s.container.memory_request"
	metricNodeAllocCPU        = "k8s.node.allocatable_cpu"
	metricNodeAllocMem        = "k8s.node.allocatable_memory"
)

// costBucketSeconds is the width used to line reserved capacity and usage up in
// time. Both receivers scrape on their own schedule (30s each by default) and
// neither timestamp lands on the other's, so the totals are summed WITHIN a
// bucket and then averaged ACROSS buckets. Summing raw rows instead would
// multiply every workload by however many scrapes fell in the window.
const costBucketSeconds = 60

const defaultCostLimit = 200

// WorkloadCosts returns, per workload, the capacity it reserved against the
// capacity it used.
//
// Reserved comes from the cluster-object gauges, which carry the owning
// workload directly (k8sclusterreceiver walks the owner chain), so this side
// needs no pod→workload join. Used comes from the kubeletstats pod series,
// which do not, so that side resolves the owner with workloadExpr — the same
// precedence ListPodStats and the green attribution already use, which is what
// makes the two halves name the same workload.
//
// The join is a FULL OUTER: a workload with usage and no reservation is the
// finding this whole surface exists for, and a workload that reserves capacity
// while sending no usage at all (crash-looping, scaled to zero but still
// requested) is the same story from the other end. Dropping either would turn
// the worst rows into blank space.
func (s *Store) WorkloadCosts(ctx context.Context, q storage.CostQuery) ([]storage.WorkloadCost, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultCostLimit
	}
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)

	query := fmt.Sprintf(`
WITH reserved AS (
    SELECT workload, ns,
        avg(cpu) AS cpu,
        avg(mem) AS mem
    FROM (
        SELECT
            %s                                       AS workload,
            ResourceAttributes['k8s.namespace.name'] AS ns,
            toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS t,
            sumIf(Value, MetricName = ?)             AS cpu,
            sumIf(Value, MetricName = ?)             AS mem
        FROM otel_metrics_gauge
        WHERE Tenant IN (?) AND TimeUnix >= ? AND TimeUnix < ?
          AND MetricName IN (?, ?)
          AND %s != ''
        GROUP BY workload, ns, t
    )
    GROUP BY workload, ns
),
used AS (
    SELECT workload, ns,
        max(cpu)   AS cpu_peak,
        avg(cpu)   AS cpu_mean,
        max(mem)   AS mem_peak,
        avg(mem)   AS mem_mean,
        max(pods)  AS pods
    FROM (
        SELECT
            %s                                       AS workload,
            ResourceAttributes['k8s.namespace.name'] AS ns,
            toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS t,
            sumIf(Value, MetricName = ?)             AS cpu,
            sumIf(Value, MetricName = ?)             AS mem,
            uniqExact(ResourceAttributes['k8s.pod.name']) AS pods
        FROM otel_metrics_gauge
        WHERE Tenant IN (?) AND TimeUnix >= ? AND TimeUnix < ?
          AND MetricName IN (?, ?)
          AND %s != ''
        GROUP BY workload, ns, t
    )
    GROUP BY workload, ns
)
SELECT
    if(u.workload != '', u.workload, r.workload) AS workload,
    if(u.ns != '', u.ns, r.ns)                   AS ns,
    r.cpu, r.mem, u.cpu_peak, u.cpu_mean, u.mem_peak, u.mem_mean, u.pods
FROM used AS u
FULL OUTER JOIN reserved AS r ON u.workload = r.workload AND u.ns = r.ns
ORDER BY greatest(r.cpu - u.cpu_peak, 0) DESC, workload
LIMIT ?`,
		workloadExpr, costBucketSeconds, workloadExpr,
		workloadExpr, costBucketSeconds, workloadExpr)

	args := []any{
		metricContainerCPURequest, metricContainerMemRequest,
		tenants, q.Range.Start, q.Range.End,
		metricContainerCPURequest, metricContainerMemRequest,
		metricPodCPU, metricPodMem,
		tenants, q.Range.Start, q.Range.End,
		metricPodCPU, metricPodMem,
		limit,
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("workload costs: %w", err)
	}
	defer rows.Close()

	var out []storage.WorkloadCost
	for rows.Next() {
		var w storage.WorkloadCost
		if err := rows.Scan(
			&w.Workload, &w.Namespace,
			&w.ReservedCPUCores, &w.ReservedMemBytes,
			&w.UsedCPUCoresPeak, &w.UsedCPUCoresMean,
			&w.UsedMemBytesPeak, &w.UsedMemBytesMean,
			&w.Pods,
		); err != nil {
			return nil, fmt.Errorf("scanning workload cost: %w", err)
		}
		if w.Workload == "" {
			continue // a pod with no recognized owner: not a workload's waste
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// NodeCosts returns per-node allocatable capacity, the share of it claimed by
// container requests, and the share actually in use.
//
// Allocatable and requested both come from the cluster-object gauges, so they
// agree by construction. Usage is the kubeletstats node series the infra view
// already reads, taken as the window's latest sample — a node's capacity does
// not move, and averaging its usage would hide the peak the requests are meant
// to cover.
func (s *Store) NodeCosts(ctx context.Context, q storage.CostQuery) ([]storage.NodeCost, error) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)

	query := fmt.Sprintf(`
WITH alloc AS (
    SELECT
        %s                            AS node,
        argMaxIf(Value, TimeUnix, MetricName = ?) AS cpu,
        argMaxIf(Value, TimeUnix, MetricName = ?) AS mem
    FROM otel_metrics_gauge
    WHERE Tenant IN (?) AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName IN (?, ?)
      AND %s != ''
    GROUP BY node
),
requested AS (
    SELECT node, avg(cpu) AS cpu, avg(mem) AS mem
    FROM (
        SELECT
            %s                        AS node,
            toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS t,
            sumIf(Value, MetricName = ?) AS cpu,
            sumIf(Value, MetricName = ?) AS mem
        FROM otel_metrics_gauge
        WHERE Tenant IN (?) AND TimeUnix >= ? AND TimeUnix < ?
          AND MetricName IN (?, ?)
          AND %s != ''
        GROUP BY node, t
    )
    GROUP BY node
),
usage AS (
    SELECT
        %s                            AS node,
        argMaxIf(Value, TimeUnix, MetricName = ?) AS cpu,
        argMaxIf(Value, TimeUnix, MetricName = ?) AS mem
    FROM otel_metrics_gauge
    WHERE Tenant IN (?) AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName IN (?, ?)
      AND %s != ''
    GROUP BY node
)
SELECT a.node, a.cpu, a.mem, r.cpu, r.mem, u.cpu, u.mem
FROM alloc AS a
LEFT JOIN requested AS r ON a.node = r.node
LEFT JOIN usage AS u ON a.node = u.node
ORDER BY a.node`,
		nodeAttr, nodeAttr,
		nodeAttr, costBucketSeconds, nodeAttr,
		nodeAttr, nodeAttr)

	args := []any{
		metricNodeAllocCPU, metricNodeAllocMem,
		tenants, q.Range.Start, q.Range.End,
		metricNodeAllocCPU, metricNodeAllocMem,

		metricContainerCPURequest, metricContainerMemRequest,
		tenants, q.Range.Start, q.Range.End,
		metricContainerCPURequest, metricContainerMemRequest,

		metricNodeCPU, metricNodeMem,
		tenants, q.Range.Start, q.Range.End,
		metricNodeCPU, metricNodeMem,
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("node costs: %w", err)
	}
	defer rows.Close()

	var out []storage.NodeCost
	for rows.Next() {
		var n storage.NodeCost
		if err := rows.Scan(
			&n.Node,
			&n.AllocatableCPUCores, &n.AllocatableMemBytes,
			&n.RequestedCPUCores, &n.RequestedMemBytes,
			&n.UsedCPUCores, &n.UsedMemBytes,
		); err != nil {
			return nil, fmt.Errorf("scanning node cost: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
