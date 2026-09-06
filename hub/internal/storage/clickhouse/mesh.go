package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Istio control-plane metric names, verified against the istio source rather
// than a dashboard: pilot_xds and pilot_proxy_convergence_time in
// pilot/pkg/xds/monitoring.go, pilot_total_xds_rejects in
// pkg/xds/monitoring.go.
//
// Names arrive verbatim: the collector's prometheus receiver does not rewrite
// them, which the green module's Kepler series (kepler_node_cpu_joules_total,
// queried under exactly that name) already demonstrates on a real cluster.
const (
	meshConnectedProxiesMetric = "pilot_xds"                    // gauge: proxies connected to this istiod
	meshConvergenceMetric      = "pilot_proxy_convergence_time" // histogram, seconds
	meshRejectsMetric          = "pilot_total_xds_rejects"      // counter: config the proxies REFUSED
	meshPushesMetric           = "pilot_xds_pushes"             // counter: pushes attempted

	// Added later, and therefore OPTIONAL: they arrive only from a widened
	// keep-list, so an install on an older chart publishes none of them while
	// being perfectly healthy. Their readers must distinguish nil from zero.
	meshPushTimeMetric     = "pilot_xds_push_time"     // histogram, seconds: istiod's own send latency
	meshWriteTimeoutMetric = "pilot_xds_write_timeout" // counter: pushes that never landed
	meshConfigEventsMetric = "pilot_k8s_cfg_events"    // counter: Kubernetes config churn
)

// meshKindIstio names the one control plane whose metrics are understood.
// Reported rather than assumed, so the screen can say which one it read — and
// so an operator running something else learns it from the product.
const meshKindIstio = "istio"

// defaultMeshScrapeJob mirrors the chart's mesh.controlPlane.jobName default.
// The value travels from the chart; this is the fallback for a hub that was
// never told, which would otherwise look up the empty job and report
// "unconfigured" on a perfectly configured install.
const defaultMeshScrapeJob = "istiod"

// MeshControlPlane summarises whether the mesh's control plane is still
// programming the data plane over the window.
//
// The honest-absence rule lives here: with no series in the window, Available
// is false and every number stays zero — because "0 rejected configs" from a
// control plane nobody is scraping is precisely the reassuring lie this surface
// exists to prevent. Same discipline as the green module reporting "no RAPL"
// rather than 0 W.
//
// Reads the otel_metrics_* tables; callers must gate on infra-metrics.
func (s *Store) MeshControlPlane(ctx context.Context, q storage.ServiceQuery) (storage.MeshControlPlane, error) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	out := storage.MeshControlPlane{State: storage.MeshControlPlaneUnconfigured}

	// Connected proxies is a gauge: the last value in the window is the answer,
	// not a sum over scrapes (which would multiply by however often we scraped).
	// count() decides availability, NOT the timestamp: an aggregate over no rows
	// returns max(TimeUnix) as the Unix epoch, which is a perfectly non-zero
	// time.Time in Go — so testing the timestamp would report every unscraped
	// control plane as available and healthy. The absence test exists because
	// that is exactly what this code did first.
	const gaugeQuery = `
SELECT argMax(Value, TimeUnix) AS latest, max(TimeUnix) AS newest, count() AS rows
FROM otel_metrics_gauge
WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?`
	row := s.conn.QueryRow(ctx, gaugeQuery, tenants, meshConnectedProxiesMetric, q.Range.Start, q.Range.End)
	var (
		proxies     float64
		newest      time.Time
		gaugePoints uint64
	)
	if err := row.Scan(&proxies, &newest, &gaugePoints); err != nil {
		return out, fmt.Errorf("mesh connected proxies: %w", err)
	}
	if gaugePoints > 0 {
		out.Available = true
		out.LastSeen = newest
		out.ConnectedProxies = uint64(proxies)
	}

	// Counters: sum over the window, the same cumulative-counter approximation
	// the flow and TCP-stats reads document.
	for _, c := range []struct {
		metric string
		set    func(uint64)
	}{
		{meshRejectsMetric, func(v uint64) { out.RejectedConfigs = v }},
		{meshPushesMetric, func(v uint64) { out.Pushes = v }},
		{meshWriteTimeoutMetric, func(v uint64) { out.WriteTimeouts = &v }},
		{meshConfigEventsMetric, func(v uint64) { out.ConfigEvents = &v }},
	} {
		const sumQuery = `
SELECT toUInt64(sum(Value)) AS total, max(TimeUnix) AS newest, count() AS rows
FROM otel_metrics_sum
WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?`
		var (
			total    uint64
			latest   time.Time
			seenRows uint64
		)
		if err := s.conn.QueryRow(ctx, sumQuery, tenants, c.metric, q.Range.Start, q.Range.End).
			Scan(&total, &latest, &seenRows); err != nil {
			return out, fmt.Errorf("mesh %s: %w", c.metric, err)
		}
		if seenRows == 0 {
			continue
		}
		c.set(total)
		out.Available = true
		if latest.After(out.LastSeen) {
			out.LastSeen = latest
		}
	}

	// Convergence p95 from the merged histogram, the same bucket walk
	// NetworkEdgeHealth uses for RTT: sum bucket counts element-wise, then take
	// the upper bound where the cumulative count first reaches 95%.
	convergence, err := s.meshHistogramP95(ctx, tenants, meshConvergenceMetric, q)
	if err != nil {
		return out, err
	}
	if convergence != nil {
		out.ConvergenceP95Ms = *convergence
		out.Available = true
	}
	// Push time is optional (widened keep-list), so it stays a pointer and it
	// does NOT make an otherwise-silent control plane look available.
	pushP95, err := s.meshHistogramP95(ctx, tenants, meshPushTimeMetric, q)
	if err != nil {
		return out, err
	}
	out.PushP95Ms = pushP95

	if out.Available {
		out.State = storage.MeshControlPlaneOK
		out.Kind = meshKindIstio
		return out, nil
	}

	// Nothing recognised. The scrape-report series says whether that is because
	// nobody is scraping, because the target did not answer, or because it
	// answered with something we cannot read — three problems with three
	// different fixes, which "not available" could never tell apart.
	state, err := s.meshScrapeState(ctx, tenants, q)
	if err != nil {
		return out, err
	}
	out.State = state
	return out, nil
}

// meshHistogramP95 walks one merged histogram to its 95th percentile, in
// milliseconds, or returns nil when the window holds no such series.
//
// nil rather than 0 because these metrics are the difference between "nothing
// took any time" and "nothing measured it", and two different callers here need
// to tell those apart.
func (s *Store) meshHistogramP95(
	ctx context.Context, tenants []string, metric string, q storage.ServiceQuery,
) (*float64, error) {
	const histQuery = `
SELECT
    arrayElement(bounds, least(greatest(
        arrayFirstIndex(c -> c >= 0.95 * arraySum(buckets), arrayCumSum(buckets)), 1),
        length(bounds))) * 1000 AS p95_ms
FROM (
    SELECT sumForEach(BucketCounts) AS buckets, any(ExplicitBounds) AS bounds
    FROM otel_metrics_histogram
    WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?
)
WHERE length(bounds) > 0 AND arraySum(buckets) > 0`
	rows, err := s.conn.Query(ctx, histQuery, tenants, metric, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("mesh %s: %w", metric, err)
	}
	defer rows.Close()
	var out *float64
	for rows.Next() {
		var p95 float64
		if err := rows.Scan(&p95); err != nil {
			return nil, fmt.Errorf("scanning %s row: %w", metric, err)
		}
		v := p95
		out = &v
	}
	return out, rows.Err()
}

// meshScrapeState reads Prometheus's synthetic `up` series for the
// control-plane scrape job.
//
// It exists because those series BYPASS metric_relabel_configs by design: the
// keep-list that drops everything but pilot_* cannot drop them, so they are
// already in the tables on every install that switched the scrape on. The
// receiver maps the Prometheus `job` label to service.name (verified in the
// receiver's CreateResource at the pinned collector tag) — hence the job name
// being one configured value that reaches both the scrape config and this
// query.
//
// Matched on ResourceAttributes['service.name'], not the ServiceName column:
// the receiver definitively sets the ATTRIBUTE, while filling that column is
// the exporter's business and nothing else in the product reads it on the
// metrics tables. Reading what we know is set beats reading what we assume is.
func (s *Store) meshScrapeState(
	ctx context.Context, tenants []string, q storage.ServiceQuery,
) (storage.MeshControlPlaneState, error) {
	job := q.MeshScrapeJob
	if job == "" {
		job = defaultMeshScrapeJob
	}
	const upQuery = `
SELECT argMax(Value, TimeUnix) AS latest, count() AS rows
FROM otel_metrics_gauge
WHERE Tenant IN (?) AND MetricName = 'up'
  AND ResourceAttributes['service.name'] = ?
  AND TimeUnix >= ? AND TimeUnix < ?`
	var (
		latest float64
		points uint64
	)
	if err := s.conn.QueryRow(ctx, upQuery, tenants, job, q.Range.Start, q.Range.End).
		Scan(&latest, &points); err != nil {
		return storage.MeshControlPlaneUnconfigured, fmt.Errorf("mesh scrape state: %w", err)
	}
	switch {
	case points == 0:
		return storage.MeshControlPlaneUnconfigured, nil
	case latest == 0:
		return storage.MeshControlPlaneUnreachable, nil
	default:
		// Scraped, answered, and nothing we know how to read came back.
		return storage.MeshControlPlaneUnrecognised, nil
	}
}
