// Effective span status: classification plus a user-facing label. Many SDK
// auto-instrumentations leave the OTel status Unset even on failing HTTP
// calls, so the raw StatusCode alone both undercounts errors and shows the
// meaningless "Unset" to users. Derivation per the OTel HTTP semconv:
//
//	StatusCode  SpanKind    http status  effective
//	Error       any         any          error   (explicit status always wins)
//	Ok          any         any          ok      (developer-set, final per spec)
//	Unset/''    any         >= 500       error   (5xx errs on SERVER and CLIENT)
//	Unset/''    Client      400..499     error   (CLIENT 4xx is an error)
//	Unset/''    not Client  400..499     ok      (SERVER 4xx is NOT an error)
//	Unset/''    any         < 400/none   ok      (3xx is never an error)
//
// gRPC status codes are display-only (instrumentations set span status
// correctly for gRPC; the error mapping is per-code). KEEP IN SYNC with
// errorSpanExpr in hub/internal/storage/clickhouse/status.go.

import type { Span } from "@/lib/api-types";

export interface SpanStatus {
  kind: "ok" | "error" | "unset"; // effective classification ("unset" = ok with no signal)
  httpStatus?: number; // display code; new semconv key wins over the pre-1.21 one
  grpcStatus?: number;
  label: string; // badge text: "500" | "307" | "gRPC 4" | "ERR" | "OK"
}

type StatusInput = Pick<Span, "statusCode" | "kind" | "attributes">;

function parseCode(v: string | undefined): number | undefined {
  if (v === undefined || v === "") return undefined;
  const n = Number.parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : undefined;
}

export function spanStatus(span: StatusInput): SpanStatus {
  const attrs = span.attributes ?? {};
  const newKey = parseCode(attrs["http.response.status_code"]);
  const oldKey = parseCode(attrs["http.status_code"]);
  const httpStatus = newKey ?? oldKey;
  const grpcStatus = parseCode(attrs["rpc.grpc.status_code"]);

  // Same max-of-both-keys the SQL uses, so filter and badge always agree.
  const h = Math.max(newKey ?? 0, oldKey ?? 0);
  const isError =
    span.statusCode === "Error" ||
    (span.statusCode !== "Ok" && (h >= 500 || (span.kind === "Client" && h >= 400)));

  const kind: SpanStatus["kind"] = isError
    ? "error"
    : span.statusCode === "Ok" || httpStatus !== undefined || grpcStatus !== undefined
      ? "ok"
      : "unset";
  const label =
    httpStatus !== undefined
      ? String(httpStatus)
      : grpcStatus !== undefined && grpcStatus > 0
        ? `gRPC ${grpcStatus}`
        : isError
          ? "ERR"
          : "OK";
  return { kind, httpStatus, grpcStatus, label };
}

export function isSpanError(span: StatusInput): boolean {
  return spanStatus(span).kind === "error";
}
