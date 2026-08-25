package clickhouse

import (
	"errors"
	"sort"
	"strings"
)

// tenantsOrDefault owns the resolved-tenant default rule for the query
// structs: Tenants when the API filled it, else the single legacy Tenant.
// Every tenant filter binds the full set (Tenant IN) and must resolve through
// here so the default lives in one place. The clickhouse-go driver renders a
// []string bound to IN (?) as an array literal, which ClickHouse accepts on
// the right side of IN.
func tenantsOrDefault(tenants []string, tenant string) []string {
	if len(tenants) > 0 {
		return tenants
	}
	return []string{tenant}
}

// requireTenants guards the explicit-tenants reads (GetTrace &co), whose
// callers resolve the set themselves. An empty set is a caller bug, surfaced
// as an error rather than a query that silently matches no tenant.
func requireTenants(tenants []string) error {
	if len(tenants) == 0 {
		return errors.New("empty tenant set")
	}
	return nil
}

// repTuple selects a trace's effective-root span inside a GROUP BY TraceId:
// argMin(col, repTuple) prefers the real root (a parentless span — its empty
// ParentSpanId makes the tuple's first element 0, sorting it first), else
// falls back to the earliest span by Timestamp, with SpanId as a stable
// final tiebreaker. Every
// representative field in SearchTraces must use this SAME tuple so they all
// come from ONE span — the effective root — even when the true root span was
// exported to a different backend and the trace is an orphan here.
const repTuple = `(ParentSpanId != '', Timestamp, SpanId)`

// auxExclusion returns a SQL predicate that drops auxiliary traffic — health
// checks, metrics scrapes, control-plane probes, and connection-keepalive DB
// commands — that otherwise drowns real requests in every view. Appended to
// trace/overview/service queries when the query sets ExcludeAux (the
// default). Tuned here in one place; it matches the span name, its
// http.route attribute, or (for client spans) well-known DB health commands:
// drivers and actuator health indicators ping Redis/SQL outside any request
// (Lettuce INFO/PING, `SELECT 1`, ...), and each parentless ping otherwise
// lists as its own one-span "trace".
//
// The platform's OWN endpoint checks are in the same category and are matched
// by the attribute they stamp rather than by their span name: a check is
// synthetic traffic on purpose (that is what puts it on the map and in the
// trace explorer), and it must not be counted as a user's request in RED. One
// mechanism, reused — see design/2026-07-20-endpoint-checks.md.
//
// prefix qualifies the columns for joined queries (e.g. "server."); pass "" for
// a single-table query.
func auxExclusion(prefix string) string {
	return ` AND NOT (
     positionCaseInsensitive(` + prefix + `SpanName, '/actuator') > 0
  OR positionCaseInsensitive(` + prefix + `SpanName, '/health') > 0
  OR positionCaseInsensitive(` + prefix + `SpanName, '/healthz') > 0
  OR positionCaseInsensitive(` + prefix + `SpanName, '/livez') > 0
  OR positionCaseInsensitive(` + prefix + `SpanName, '/readyz') > 0
  OR positionCaseInsensitive(` + prefix + `SpanName, '/metrics') > 0
  OR positionCaseInsensitive(` + prefix + `SpanName, '/ping') > 0
  OR positionCaseInsensitive(` + prefix + `SpanAttributes['http.route'], '/actuator') > 0
  OR ` + prefix + `SpanAttributes['avuru.check.id'] != ''
  OR (` + prefix + `SpanKind = 'Client'
      AND ` + prefix + `SpanAttributes['db.system'] != ''
      AND (upperUTF8(` + prefix + `SpanName) IN ('PING', 'INFO', 'HELLO', 'ISMASTER', 'SELECT 1')
        OR upperUTF8(` + prefix + `SpanAttributes['db.operation']) IN ('PING', 'INFO', 'HELLO', 'ISMASTER'))))`
}

// TagPrefix is the namespace business tags are mapped into at collection (chart
// values `tags.labels`). It is reserved: nothing else writes it, and a tag only
// ever exists as a RESOURCE attribute — it describes the workload, not the
// operation. So a filter on a key under this prefix is answered from
// ResourceAttributes while every other key stays a span-attribute match, which
// is what lets one filter vocabulary read whichever signal you are looking at.
const TagPrefix = "avuru.tag."

func isResourceTag(key string) bool { return strings.HasPrefix(key, TagPrefix) }

// sortedKeys returns a map's keys in order, so a query string built from them
// is stable (and therefore cacheable and diffable).
func sortedKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tagFilters appends `AND SpanAttributes['k'] = ?` for each tag (keys sorted for
// a stable query string) and returns the extended SQL plus the value args.
// Business tags read ResourceAttributes instead — see TagPrefix.
func tagFilters(query string, tags map[string]string, args []any) (string, []any) {
	for _, k := range sortedKeys(tags) {
		if isResourceTag(k) {
			query += " AND ResourceAttributes[?] = ?"
		} else {
			query += " AND SpanAttributes[?] = ?"
		}
		args = append(args, k, tags[k])
	}
	return query, args
}

// auxExclusionRep is auxExclusion rephrased for SearchTraces' grouped query: it
// tests the effective-root span exposed as SELECT aliases (RootOperation is its
// SpanName; RepKind/RepHttpRoute/RepDbSystem/RepDbOp its other fields) so aux
// traffic is judged by the trace's representative, not any child. Emitted into
// HAVING. KEEP IN SYNC with auxExclusion above.
func auxExclusionRep() string {
	return ` AND NOT (
     positionCaseInsensitive(RootOperation, '/actuator') > 0
  OR positionCaseInsensitive(RootOperation, '/health') > 0
  OR positionCaseInsensitive(RootOperation, '/healthz') > 0
  OR positionCaseInsensitive(RootOperation, '/livez') > 0
  OR positionCaseInsensitive(RootOperation, '/readyz') > 0
  OR positionCaseInsensitive(RootOperation, '/metrics') > 0
  OR positionCaseInsensitive(RootOperation, '/ping') > 0
  OR positionCaseInsensitive(RepHttpRoute, '/actuator') > 0
  OR (RepKind = 'Client'
      AND RepDbSystem != ''
      AND (upperUTF8(RootOperation) IN ('PING', 'INFO', 'HELLO', 'ISMASTER', 'SELECT 1')
        OR upperUTF8(RepDbOp) IN ('PING', 'INFO', 'HELLO', 'ISMASTER'))))`
}

// tagFiltersRep is tagFilters for the grouped query: each tag must match the
// effective-root span's attribute, so it compares argMin(SpanAttributes[k],
// repTuple) rather than a raw row value. Emitted into HAVING.
func tagFiltersRep(query string, tags map[string]string, args []any) (string, []any) {
	for _, k := range sortedKeys(tags) {
		if isResourceTag(k) {
			// A business tag describes the SERVICE behind a span, and a trace
			// crosses several. The useful question is "did a service carrying
			// this tag take part?" — the same participation rule the service
			// filter uses — rather than "does the root service carry it",
			// which would hide every trace a tagged service joined downstream.
			query += " AND countIf(ResourceAttributes[?] = ?) > 0"
		} else {
			query += " AND argMin(SpanAttributes[?], " + repTuple + ") = ?"
		}
		args = append(args, k, tags[k])
	}
	return query, args
}
