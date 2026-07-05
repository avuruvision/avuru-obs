package clickhouse

import (
	"context"
	"fmt"
	"time"
)

// tenantLookback bounds tenant discovery: a project that stopped sending
// data ages out of the auto-discovered list (config-defined projects never
// age out — the API layer merges those).
const tenantLookback = 7 * 24 * time.Hour

// ListTenants returns distinct tenants seen in traces or logs recently.
// Tenant leads every sort key (LowCardinality prefix), so DISTINCT runs
// in-order and stays cheap even on large tables.
func (s *Store) ListTenants(ctx context.Context) ([]string, error) {
	since := time.Now().UTC().Add(-tenantLookback)
	const q = `
SELECT DISTINCT Tenant FROM otel_traces WHERE Timestamp >= ?
UNION DISTINCT
SELECT DISTINCT Tenant FROM otel_logs WHERE Timestamp >= ?`
	rows, err := s.conn.Query(ctx, q, since, since)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("scanning tenant: %w", err)
		}
		if t != "" {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}
