package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ServiceLabels returns each service's dominant grouping labels
// (k8s.namespace.name, service.namespace) over the same entry-span population
// as ListServices, so the two line up service-for-service. A service whose
// spans carry different label values collapses to its most common one via
// argMax weighted by span count — the SQL analogue of the service-map
// "dominant representative" rule. The ResourceAttributes bloom indexes
// (migration 0001) keep the map lookups cheap at staging volumes.
func (s *Store) ServiceLabels(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceLabel, error) {
	query := `
SELECT
    ServiceName,
    argMax(k8sns, w) AS k8s_namespace,
    argMax(svcns, w) AS service_namespace,
    argMax(env, w)   AS environment,
    argMax(tier, w)  AS declared_tier
FROM (
    SELECT
        ServiceName,
        ResourceAttributes['k8s.namespace.name'] AS k8sns,
        ResourceAttributes['service.namespace']  AS svcns,
        if(ResourceAttributes['deployment.environment.name'] != '',
           ResourceAttributes['deployment.environment.name'],
           ResourceAttributes['deployment.environment']) AS env,
        ResourceAttributes['avuru.tier']         AS tier,
        count()                                   AS w
    FROM otel_traces
    WHERE Tenant IN (?)
      AND Timestamp >= ? AND Timestamp < ?
      AND SpanKind IN ('Server', 'Consumer')`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
    GROUP BY ServiceName, k8sns, svcns, env, tier
)
GROUP BY ServiceName`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("service labels: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceLabel
	for rows.Next() {
		var l storage.ServiceLabel
		if err := rows.Scan(&l.Service, &l.K8sNamespace, &l.ServiceNamespace, &l.Environment, &l.DeclaredTier); err != nil {
			return nil, fmt.Errorf("scanning service label row: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
