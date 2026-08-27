package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// breakdownDimExpr renders the grouping dimension as SQL.
//
// The dimension NEVER reaches the query as caller-supplied text: it is matched
// against the closed set below, and the two map-backed dimensions bind their
// key as a positional argument like any other value. This is the seam that
// would otherwise turn "group by anything" into "run any SQL", so it is one
// function with one place to audit.
//
// The second return is the args the expression consumes, in placeholder order.
func breakdownDimExpr(q storage.BreakdownQuery) (string, []any, error) {
	switch q.GroupBy {
	case storage.BreakdownService:
		return "ServiceName", nil, nil
	case storage.BreakdownOperation:
		return "SpanName", nil, nil
	case storage.BreakdownKind:
		return "SpanKind", nil, nil
	case storage.BreakdownStatus:
		// The product's three-state answer to "did it work?", not the raw OTel
		// StatusCode: most auto-instrumentation leaves that Unset, so a chart
		// of it would report an estate as almost entirely "unset" while the
		// error rate beside it said otherwise. Order matters — errorSpanExpr
		// claims explicit errors, 5xx and client 4xx first, exactly as it does
		// everywhere else (see status.go).
		return `multiIf(` + errorSpanExpr("") + `, 'error', ` + refusedSpanExpr("") + `, 'refused', 'ok')`, nil, nil
	case storage.BreakdownAttribute:
		if q.Key == "" {
			return "", nil, fmt.Errorf("breakdown by attribute needs a key")
		}
		return "SpanAttributes[?]", []any{q.Key}, nil
	case storage.BreakdownResource:
		if q.Key == "" {
			return "", nil, fmt.Errorf("breakdown by resource needs a key")
		}
		return "ResourceAttributes[?]", []any{q.Key}, nil
	default:
		return "", nil, fmt.Errorf("unknown breakdown dimension %q", q.GroupBy)
	}
}

// breakdownScopeFilter restricts WHICH spans are counted. See
// storage.BreakdownScope for why the three are not interchangeable.
func breakdownScopeFilter(scope storage.BreakdownScope) (string, error) {
	switch scope {
	case storage.ScopeEntry, "":
		return " AND SpanKind IN ('Server', 'Consumer')", nil
	case storage.ScopeRoot:
		// Parentless spans only. A trace whose true root was exported
		// elsewhere has none here and is absent from this scope — deliberately:
		// SearchTraces can substitute an effective root because it is listing
		// traces it already grouped, while this is a straight span scan with no
		// per-trace grouping to lean on. Counting an arbitrary child as an
		// entry point would invent traffic that never entered there.
		return " AND ParentSpanId = ''", nil
	case storage.ScopeAll:
		return "", nil
	default:
		return "", fmt.Errorf("unknown breakdown scope %q", scope)
	}
}

// TraceBreakdown groups spans by one dimension and returns each group's RED
// numbers plus the totals over EVERY matching span.
//
// The totals come from ClickHouse's WITH TOTALS, computed before LIMIT, so a
// top-N view still knows the size of the tail it is not drawing. That is what
// keeps a part-of-whole chart honest: without it, a treemap of the top 20
// namespaces would redraw those 20 as the entire estate.
func (s *Store) TraceBreakdown(ctx context.Context, q storage.BreakdownQuery) (storage.Breakdown, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	dim, dimArgs, err := breakdownDimExpr(q)
	if err != nil {
		return storage.Breakdown{}, err
	}
	scope, err := breakdownScopeFilter(q.Scope)
	if err != nil {
		return storage.Breakdown{}, err
	}

	// uniqExact over the same expression is 1 in every grouped row and the true
	// distinct count in the TOTALS row, where ClickHouse merges the aggregate
	// states of all groups. One extra column instead of a second scan.
	query := `
SELECT
    ` + dim + `                                     AS k,
    count()                                         AS reqs,
    countIf(` + errorSpanExpr("") + `)              AS errors,
    countIf(` + refusedSpanExpr("") + `)            AS refused,
    sum(Duration)                                   AS durSum,
    uniqExact(` + dim + `)                          AS groups,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration)) AS qs
FROM otel_traces
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?`
	// Placeholder order is statement order: the dimension's key (twice — the
	// SELECT expression and the uniqExact over it) precedes the WHERE args.
	args := append([]any{}, dimArgs...)
	args = append(args, dimArgs...)
	args = append(args, tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End)

	query += scope
	if q.Service != "" {
		query += " AND ServiceName = ?"
		args = append(args, q.Service)
	}
	if q.Operation != "" {
		query += " AND SpanName = ?"
		args = append(args, q.Operation)
	}
	switch q.Status {
	case "error":
		query += " AND " + errorSpanExpr("")
	case "refused":
		query += " AND " + refusedSpanExpr("")
	case "ok":
		query += " AND NOT (" + errorSpanExpr("") + " OR " + refusedSpanExpr("") + ")"
	}
	if q.MinDuration > 0 {
		query += " AND Duration >= ?"
		args = append(args, uint64(q.MinDuration.Nanoseconds()))
	}
	if q.MaxDuration > 0 {
		query += " AND Duration <= ?"
		args = append(args, uint64(q.MaxDuration.Nanoseconds()))
	}
	query, args = tagFilters(query, q.Tags, args)
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
GROUP BY k WITH TOTALS
ORDER BY reqs DESC, k ASC
LIMIT ?`
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return storage.Breakdown{}, fmt.Errorf("trace breakdown: %w", err)
	}
	defer rows.Close()

	var out storage.Breakdown
	for rows.Next() {
		// The per-group uniqExact is always 1 and is discarded; the column
		// exists for the totals row, where it becomes the distinct-group count.
		g, _, err := scanBreakdown(rows.Scan)
		if err != nil {
			return storage.Breakdown{}, err
		}
		out.Groups = append(out.Groups, g)
	}
	if err := rows.Err(); err != nil {
		return storage.Breakdown{}, err
	}

	// The totals row, in the same column order. A query that matched nothing
	// has no totals to read, so a failure here leaves Total zeroed rather than
	// failing the whole read: an empty breakdown is a legitimate answer.
	if total, groupCount, err := scanBreakdown(rows.Totals); err == nil {
		out.Total = total
		out.GroupCount = groupCount
	}
	return out, nil
}

// scanBreakdown converts one breakdown row into a group. It takes the scan
// FUNCTION rather than the rows, so the grouped rows (rows.Scan) and the
// WITH TOTALS row (rows.Totals) — identical in shape, reached through different
// methods — share one conversion instead of drifting apart.
func scanBreakdown(scan func(dest ...any) error) (storage.BreakdownGroup, uint64, error) {
	var (
		g          storage.BreakdownGroup
		durSum     uint64
		groupCount uint64
		quant      []float64
	)
	if err := scan(&g.Key, &g.Count, &g.ErrorCount, &g.RefusedCount, &durSum, &groupCount, &quant); err != nil {
		return g, 0, fmt.Errorf("scanning breakdown row: %w", err)
	}
	g.DurationSum = time.Duration(durSum)
	g.P50, g.P95, g.P99 = nsQuantiles(quant)
	return g, groupCount, nil
}
