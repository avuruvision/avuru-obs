package clickhouse

import "sort"

// auxExclusion returns a SQL predicate that drops auxiliary traffic — health
// checks, metrics scrapes and control-plane probes — that otherwise drowns real
// requests in every view. Appended to trace/overview/service queries when the
// query sets ExcludeAux (the default). Tuned here in one place; it matches the
// span name or its http.route attribute.
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
  OR positionCaseInsensitive(` + prefix + `SpanAttributes['http.route'], '/actuator') > 0)`
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
