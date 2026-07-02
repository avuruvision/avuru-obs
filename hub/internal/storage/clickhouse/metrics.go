package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// kubeletstats metric names (OTel semantic conventions) behind the infra view.
const (
	metricNodeCPU      = "k8s.node.cpu.usage"
	metricNodeMem      = "k8s.node.memory.usage"
	metricNodeMemAvail = "k8s.node.memory.available"
	metricNodeNet      = "k8s.node.network.io"
	metricPodCPU       = "k8s.pod.cpu.usage"
	metricPodMem       = "k8s.pod.memory.usage"
)

const (
	defaultSeriesPoints = 30
	defaultPodLimit     = 200
)

// nodeAttr extracts the node name; kubeletstats sets it on node metrics and
// the sensor's k8sattributes adds it to pod metrics.
const nodeAttr = "ResourceAttributes['k8s.node.name']"

// ListNodeStats returns per-node latest utilization, network rates averaged
// over the range, pod counts, and short CPU/memory series for sparklines.
func (s *Store) ListNodeStats(ctx context.Context, q storage.InfraQuery) ([]storage.NodeStat, error) {
	points := q.Points
	if points <= 0 {
		points = defaultSeriesPoints
	}
	bucket := q.Range.End.Sub(q.Range.Start) / time.Duration(points)
	if bucket < time.Second {
		bucket = time.Second
	}

	nodes := map[string]*storage.NodeStat{}
	get := func(name string) *storage.NodeStat {
		if n, ok := nodes[name]; ok {
			return n
		}
		n := &storage.NodeStat{Name: name}
		nodes[name] = n
		return n
	}

	// Latest gauge snapshot per node.
	latestQ := `
SELECT
    ` + nodeAttr + ` AS node,
    argMaxIf(Value, TimeUnix, MetricName = ?) AS cpu,
    argMaxIf(Value, TimeUnix, MetricName = ?) AS mem,
    argMaxIf(Value, TimeUnix, MetricName = ?) AS memAvail
FROM otel_metrics_gauge
WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
  AND MetricName IN (?, ?, ?)
  AND ` + nodeAttr + ` != ''
GROUP BY node`
	rows, err := s.conn.Query(ctx, latestQ,
		metricNodeCPU, metricNodeMem, metricNodeMemAvail,
		q.Tenant, q.Range.Start, q.Range.End,
		metricNodeCPU, metricNodeMem, metricNodeMemAvail)
	if err != nil {
		return nil, fmt.Errorf("node latest gauges: %w", err)
	}
	for rows.Next() {
		var (
			name          string
			cpu, mem, avl float64
		)
		if err := rows.Scan(&name, &cpu, &mem, &avl); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning node gauges: %w", err)
		}
		n := get(name)
		n.CPUUsage, n.MemoryUsage, n.MemoryAvailable = cpu, uint64(mem), uint64(avl)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Network rates: cumulative counter delta per (node, direction, interface),
	// summed over interfaces, averaged over the window.
	netQ := `
SELECT node, sumIf(delta, dir = 'receive') AS rx, sumIf(delta, dir = 'transmit') AS tx
FROM (
    SELECT
        ` + nodeAttr + ` AS node,
        Attributes['direction'] AS dir,
        Attributes['interface'] AS iface,
        max(Value) - min(Value) AS delta
    FROM otel_metrics_sum
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName = ?
      AND ` + nodeAttr + ` != ''
    GROUP BY node, dir, iface
)
GROUP BY node`
	windowSec := q.Range.End.Sub(q.Range.Start).Seconds()
	rows, err = s.conn.Query(ctx, netQ, q.Tenant, q.Range.Start, q.Range.End, metricNodeNet)
	if err != nil {
		return nil, fmt.Errorf("node network rates: %w", err)
	}
	for rows.Next() {
		var (
			name   string
			rx, tx float64
		)
		if err := rows.Scan(&name, &rx, &tx); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning node network: %w", err)
		}
		n := get(name)
		if windowSec > 0 {
			n.NetworkRxRate, n.NetworkTxRate = rx/windowSec, tx/windowSec
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Pods per node, from pod CPU gauges (k8s.node.name via k8sattributes).
	podCountQ := `
SELECT ` + nodeAttr + ` AS node, uniqExact(ResourceAttributes['k8s.pod.name']) AS pods
FROM otel_metrics_gauge
WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
  AND MetricName = ?
  AND ` + nodeAttr + ` != ''
GROUP BY node`
	rows, err = s.conn.Query(ctx, podCountQ, q.Tenant, q.Range.Start, q.Range.End, metricPodCPU)
	if err != nil {
		return nil, fmt.Errorf("node pod counts: %w", err)
	}
	for rows.Next() {
		var (
			name string
			pods uint64
		)
		if err := rows.Scan(&name, &pods); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning pod counts: %w", err)
		}
		get(name).PodCount = pods
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// CPU/memory series for sparklines (bucket width derives from Points, so
	// the integer seconds interpolation is safe).
	seriesQ := fmt.Sprintf(`
SELECT
    `+nodeAttr+` AS node,
    MetricName,
    toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS t,
    avg(Value) AS v
FROM otel_metrics_gauge
WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
  AND MetricName IN (?, ?)
  AND `+nodeAttr+` != ''
GROUP BY node, MetricName, t
ORDER BY t`, int(bucket.Seconds()))
	rows, err = s.conn.Query(ctx, seriesQ, q.Tenant, q.Range.Start, q.Range.End, metricNodeCPU, metricNodeMem)
	if err != nil {
		return nil, fmt.Errorf("node series: %w", err)
	}
	for rows.Next() {
		var (
			name, metric string
			t            time.Time
			v            float64
		)
		if err := rows.Scan(&name, &metric, &t, &v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning node series: %w", err)
		}
		n := get(name)
		p := storage.MetricPoint{Time: t, Value: v}
		if metric == metricNodeCPU {
			n.CPUSeries = append(n.CPUSeries, p)
		} else {
			n.MemorySeries = append(n.MemorySeries, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]storage.NodeStat, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListPodStats returns per-pod latest CPU/memory, busiest first.
func (s *Store) ListPodStats(ctx context.Context, q storage.InfraQuery) ([]storage.PodStat, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPodLimit
	}
	query := `
SELECT
    ResourceAttributes['k8s.pod.name'] AS pod,
    ResourceAttributes['k8s.namespace.name'] AS ns,
    anyLast(` + nodeAttr + `) AS node,
    anyLast(multiIf(
        ResourceAttributes['k8s.deployment.name'] != '', ResourceAttributes['k8s.deployment.name'],
        ResourceAttributes['k8s.statefulset.name'] != '', ResourceAttributes['k8s.statefulset.name'],
        ResourceAttributes['k8s.daemonset.name'] != '', ResourceAttributes['k8s.daemonset.name'],
        '')) AS workload,
    argMaxIf(Value, TimeUnix, MetricName = ?) AS cpu,
    argMaxIf(Value, TimeUnix, MetricName = ?) AS mem
FROM otel_metrics_gauge
WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
  AND MetricName IN (?, ?)
  AND ResourceAttributes['k8s.pod.name'] != ''`
	args := []any{
		metricPodCPU, metricPodMem,
		q.Tenant, q.Range.Start, q.Range.End,
		metricPodCPU, metricPodMem,
	}
	if q.Node != "" {
		query += `
  AND ` + nodeAttr + ` = ?`
		args = append(args, q.Node)
	}
	query += `
GROUP BY pod, ns
ORDER BY cpu DESC
LIMIT ?`
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing pod stats: %w", err)
	}
	defer rows.Close()

	var out []storage.PodStat
	for rows.Next() {
		var (
			p        storage.PodStat
			cpu, mem float64
		)
		if err := rows.Scan(&p.Name, &p.Namespace, &p.Node, &p.Workload, &cpu, &mem); err != nil {
			return nil, fmt.Errorf("scanning pod row: %w", err)
		}
		p.CPUUsage, p.MemoryUsage = cpu, uint64(mem)
		out = append(out, p)
	}
	return out, rows.Err()
}
