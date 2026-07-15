package clickhouse

import "sort"

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
  OR (` + prefix + `SpanKind = 'Client'
      AND ` + prefix + `SpanAttributes['db.system'] != ''
      AND (upperUTF8(` + prefix + `SpanName) IN ('PING', 'INFO', 'HELLO', 'ISMASTER', 'SELECT 1')
        OR upperUTF8(` + prefix + `SpanAttributes['db.operation']) IN ('PING', 'INFO', 'HELLO', 'ISMASTER'))))`
}

// tagFilters appends `AND SpanAttributes['k'] = ?` for each tag (keys sorted for
// a stable query string) and returns the extended SQL plus the value args.
func tagFilters(query string, tags map[string]string, args []any) (string, []any) {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		query += " AND SpanAttributes[?] = ?"
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
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		query += " AND argMin(SpanAttributes[?], " + repTuple + ") = ?"
		args = append(args, k, tags[k])
	}
	return query, args
}
