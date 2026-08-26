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
	httpStatus := httpStatusExpr(prefix)
	return `(` + prefix + `StatusCode = 'Error'
  OR (` + prefix + `StatusCode != 'Ok'
      AND (` + httpStatus + ` >= 500
        OR (` + prefix + `SpanKind = 'Client' AND ` + httpStatus + ` >= 400))))`
}

// httpStatusExpr returns the span's HTTP status code as SQL, reading both the
// current semconv key (http.response.status_code) and the pre-1.21 one
// (http.status_code) — greatest() of the two resolves the duality
// deterministically, and a span carrying neither yields 0.
func httpStatusExpr(prefix string) string {
	return `greatest(
        toUInt16OrZero(` + prefix + `SpanAttributes['http.response.status_code']),
        toUInt16OrZero(` + prefix + `SpanAttributes['http.status_code']))`
}

// refusedSpanExpr classifies a span as REFUSED — a server that answered 4xx.
//
// This is the third class the ok/error pair could not express. Per the HTTP
// semconv a server 4xx is not an error: the fault is the caller's, and making
// it one would put every 401 auth challenge and every 404 from a crawler into
// the error rate that RED, the map's health ring and alerting fire on. But it
// is not a success either, and calling it "ok" hides the request a WAF blocked
// or an authorization layer turned away — for those services the 4xx IS the
// event worth seeing.
//
// So it stays OUT of errorSpanExpr and every aggregate built on it, and is
// counted alongside instead. The two are mutually exclusive by construction:
// an explicit Error status, a 5xx, and a CLIENT 4xx are all claimed by
// errorSpanExpr first, and an explicit Ok is final per spec.
//
//	StatusCode  SpanKind    http status  effective
//	Unset/''    not Client  400..499     refused
//	anything else                        see errorSpanExpr
//
// KEEP IN SYNC with ui/src/lib/span-status.ts.
func refusedSpanExpr(prefix string) string {
	return `(` + prefix + `StatusCode NOT IN ('Error', 'Ok')
      AND ` + prefix + `SpanKind != 'Client'
      AND ` + httpStatusExpr(prefix) + ` BETWEEN 400 AND 499)`
}
