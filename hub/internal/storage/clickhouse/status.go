package clickhouse

// errorSpanExpr returns a SQL boolean classifying a span as an effective
// error. Many SDK auto-instrumentations leave the OTel status Unset even on
// failing HTTP calls, so relying on StatusCode alone undercounts errors
// everywhere (trace search, error counts, RED, heatmap, service map). The
// derivation follows the OTel HTTP semantic conventions:
//
//	StatusCode  SpanKind    http status  effective
//	Error       any         any          error   (explicit status always wins)
//	Ok          any         any          ok      (developer-set, final per spec)
//	Unset/''    any         >= 500       error   (5xx errs on SERVER and CLIENT)
//	Unset/''    Client      400..499     error   (CLIENT 4xx is an error)
//	Unset/''    not Client  400..499     ok      (SERVER 4xx is NOT an error)
//	Unset/''    any         < 400/none   ok      (3xx is never an error)
//
// The HTTP status is read from both the current semconv key
// (http.response.status_code) and the pre-1.21 one (http.status_code);
// greatest() of the two resolves the duality deterministically. gRPC status
// codes are deliberately NOT classified here (instrumentations set span
// status correctly for gRPC, and the error mapping is per-code); they are
// display-only in the UI. KEEP IN SYNC with ui/src/lib/span-status.ts.
//
// Reading SpanAttributes costs one map-column decompression per row, which
// these range-scanning aggregates mostly pay already via auxExclusion. If it
// ever shows up in profiles, the fix is an Avuru-owned MATERIALIZED UInt16
// column (legal next to the exporter's frozen insert list, like Tenant).
//
// prefix qualifies the columns for joined queries (e.g. "server."); pass ""
// for a single-table query.
func errorSpanExpr(prefix string) string {
	httpStatus := `greatest(
        toUInt16OrZero(` + prefix + `SpanAttributes['http.response.status_code']),
        toUInt16OrZero(` + prefix + `SpanAttributes['http.status_code']))`
	return `(` + prefix + `StatusCode = 'Error'
  OR (` + prefix + `StatusCode != 'Ok'
      AND (` + httpStatus + ` >= 500
        OR (` + prefix + `SpanKind = 'Client' AND ` + httpStatus + ` >= 400))))`
}
