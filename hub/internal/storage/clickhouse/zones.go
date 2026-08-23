package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// interZoneMetric is the sensor-exported OTLP counter carrying bytes that
// crossed an availability-zone boundary. Its only attributes are the cluster
// name and the two zones — the sensor resolves each flow's endpoints to their
// node's topology.kubernetes.io/zone label and drops same-zone flows before
// export, so this metric is cross-zone by construction and its cardinality is
// bounded by zone pairs rather than workload pairs.
const interZoneMetric = "obi.network.inter.zone.bytes"

// ZoneTraffic returns bytes exchanged per zone pair over the window.
//
// sum(Value) over a cumulative Sum counter is the same APPROXIMATION
// NetworkEdges documents: it does not reconstruct a true byte-rate (that would
// need a per-series max()-min() delta). It is used here for relative weighting
// between zone pairs — which crossing dominates — not as a billing figure.
//
// Rows whose zone is empty are dropped: a node missing the zone label
// contributes flows the cluster cannot attribute, and a pair with a blank half
// is worse than absent, since it reads as a real zone named "".
//
// No self-pair filter is needed — the sensor never emits one.
//
// This reads the otel_metrics_* tables, which exist only when the infra-metrics
// module is active; callers must gate accordingly.
func (s *Store) ZoneTraffic(ctx context.Context, q storage.ServiceQuery) ([]storage.ZoneTraffic, error) {
	const query = `
SELECT
    Attributes['src.zone']  AS srcZone,
    Attributes['dst.zone']  AS dstZone,
    toUInt64(sum(Value))    AS bytes
FROM otel_metrics_sum
WHERE Tenant IN (?)
  AND MetricName = ?
  AND TimeUnix >= ? AND TimeUnix < ?
  AND Attributes['src.zone'] != ''
  AND Attributes['dst.zone'] != ''
GROUP BY srcZone, dstZone
ORDER BY bytes DESC`

	rows, err := s.conn.Query(ctx, query, tenantsOrDefault(q.Tenants, q.Tenant), interZoneMetric, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("zone traffic: %w", err)
	}
	defer rows.Close()

	var out []storage.ZoneTraffic
	for rows.Next() {
		var z storage.ZoneTraffic
		if err := rows.Scan(&z.SrcZone, &z.DstZone, &z.Bytes); err != nil {
			return nil, fmt.Errorf("scanning zone traffic row: %w", err)
		}
		out = append(out, z)
	}
	return out, rows.Err()
}
