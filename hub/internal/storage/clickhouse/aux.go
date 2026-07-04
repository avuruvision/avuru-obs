package clickhouse

import "sort"

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
