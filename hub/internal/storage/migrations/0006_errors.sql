-- Error tracking schema v1 (module: error-tracking).
-- Derived signal: exceptions already in the data (span exception events, error
-- spans, ERROR/FATAL logs) become deduplicated issues. Populated at insert
-- time by the three materialized views below; NEVER written directly by an
-- exporter, so the column list is Avuru-owned (no frozen-contract constraint).
-- Retention (TTL) is applied by `hub migrate` ApplyRetention, not here.
--
-- The migrator strips `--` comments then splits on `;`, so there is no `;`
-- inside any statement below and no `--` inside a string literal.
CREATE DATABASE IF NOT EXISTS otel;

-- One row per error occurrence.
CREATE TABLE IF NOT EXISTS otel.error_events
(
    `Timestamp` DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    `Tenant` LowCardinality(String) CODEC(ZSTD(1)),
    `ServiceName` LowCardinality(String) CODEC(ZSTD(1)),
    `Fingerprint` UInt64 CODEC(ZSTD(1)),
    `Source` Enum8('span' = 1, 'log' = 2, 'sentry' = 3),
    `ExceptionType` LowCardinality(String) CODEC(ZSTD(1)),
    `ExceptionMessage` String CODEC(ZSTD(1)),
    `ExceptionStacktrace` String CODEC(ZSTD(1)),
    `TraceId` String CODEC(ZSTD(1)),
    `SpanId` String CODEC(ZSTD(1)),
    `Environment` LowCardinality(String) CODEC(ZSTD(1)),
    `SdkName` LowCardinality(String) CODEC(ZSTD(1)),
    `SdkVersion` LowCardinality(String) CODEC(ZSTD(1)),
    `Attributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Tenant, Fingerprint, Timestamp)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- Triage state per issue. ReplacingMergeTree keeps the newest row per
-- (Tenant, Fingerprint) by UpdatedAt — last write wins (see profiling_stacks
-- for the same pattern). Regression is computed at read time, so there is no
-- state machine here to drift.
CREATE TABLE IF NOT EXISTS otel.error_issue_status
(
    `Tenant` LowCardinality(String),
    `Fingerprint` UInt64,
    `Status` Enum8('unresolved' = 0, 'resolved' = 1, 'ignored' = 2),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Tenant, Fingerprint);

-- MV 1 of 3: span exception events. Exceptions recorded via span.recordException
-- arrive as parallel Events.* arrays; ARRAY JOIN pairs them positionally.
-- Fingerprint = service + type + top-8 normalized stack frames (addresses and
-- numbers collapsed so line numbers don't fork the group), message when there
-- is no stack. Tenant is recomputed here: the table DEFAULT does not propagate
-- into a materialized-view SELECT. Column order matches error_events exactly
-- (MV-to-table is matched by position).
CREATE MATERIALIZED VIEW IF NOT EXISTS otel.error_events_from_span_events_mv
TO otel.error_events
AS
SELECT
    EvtTime AS Timestamp,
    if(ResourceAttributes['avuru.tenant'] != '', ResourceAttributes['avuru.tenant'], 'default') AS Tenant,
    ServiceName,
    cityHash64(
        ServiceName,
        EvtAttrs['exception.type'],
        if(EvtAttrs['exception.stacktrace'] != '',
           arrayStringConcat(arraySlice(arrayMap(l -> replaceRegexpAll(replaceRegexpAll(l, '0x[0-9a-fA-F]+', 'A'), '[0-9]+', 'N'), splitByChar('\n', EvtAttrs['exception.stacktrace'])), 1, 8), '\n'),
           replaceRegexpAll(replaceRegexpAll(EvtAttrs['exception.message'], '0x[0-9a-fA-F]+', 'A'), '[0-9]+', 'N'))
    ) AS Fingerprint,
    'span' AS Source,
    EvtAttrs['exception.type'] AS ExceptionType,
    EvtAttrs['exception.message'] AS ExceptionMessage,
    EvtAttrs['exception.stacktrace'] AS ExceptionStacktrace,
    TraceId,
    SpanId,
    ResourceAttributes['deployment.environment.name'] AS Environment,
    '' AS SdkName,
    '' AS SdkVersion,
    EvtAttrs AS Attributes
FROM otel.otel_traces
ARRAY JOIN
    Events.Timestamp AS EvtTime,
    Events.Name AS EvtName,
    Events.Attributes AS EvtAttrs
WHERE EvtName = 'exception';

-- MV 2 of 3: error spans WITHOUT an exception event (already covered by MV 1).
-- The WHERE is a copy of errorSpanExpr() (Go) / span-status.ts (TS) — KEEP THE
-- THREE IN SYNC; changing it needs a new migration that DROPs and recreates
-- this view. No stack trace exists, so the fingerprint keys on the operation
-- and HTTP status.
CREATE MATERIALIZED VIEW IF NOT EXISTS otel.error_events_from_error_spans_mv
TO otel.error_events
AS
SELECT
    Timestamp,
    if(ResourceAttributes['avuru.tenant'] != '', ResourceAttributes['avuru.tenant'], 'default') AS Tenant,
    ServiceName,
    cityHash64(ServiceName, 'span-error', SpanName,
        toString(greatest(toUInt16OrZero(SpanAttributes['http.response.status_code']), toUInt16OrZero(SpanAttributes['http.status_code'])))) AS Fingerprint,
    'span' AS Source,
    if(greatest(toUInt16OrZero(SpanAttributes['http.response.status_code']), toUInt16OrZero(SpanAttributes['http.status_code'])) > 0,
       concat('HTTP ', toString(greatest(toUInt16OrZero(SpanAttributes['http.response.status_code']), toUInt16OrZero(SpanAttributes['http.status_code'])))),
       'SpanError') AS ExceptionType,
    if(StatusMessage != '', StatusMessage, SpanName) AS ExceptionMessage,
    '' AS ExceptionStacktrace,
    TraceId,
    SpanId,
    ResourceAttributes['deployment.environment.name'] AS Environment,
    '' AS SdkName,
    '' AS SdkVersion,
    SpanAttributes AS Attributes
FROM otel.otel_traces
WHERE (StatusCode = 'Error'
    OR (StatusCode != 'Ok'
        AND (greatest(toUInt16OrZero(SpanAttributes['http.response.status_code']), toUInt16OrZero(SpanAttributes['http.status_code'])) >= 500
          OR (SpanKind = 'Client' AND greatest(toUInt16OrZero(SpanAttributes['http.response.status_code']), toUInt16OrZero(SpanAttributes['http.status_code'])) >= 400))))
  AND NOT has(Events.Name, 'exception');

-- MV 3 of 3: ERROR/FATAL logs (SeverityNumber >= 17). Sentry-originated records
-- (the gateway receiver tags them avuru.error.source=sentry and fills the
-- exception.* / sentry.* attributes) are picked up here too, so they need no
-- separate path and also appear in the Logs explorer.
CREATE MATERIALIZED VIEW IF NOT EXISTS otel.error_events_from_logs_mv
TO otel.error_events
AS
SELECT
    Timestamp,
    if(ResourceAttributes['avuru.tenant'] != '', ResourceAttributes['avuru.tenant'], 'default') AS Tenant,
    ServiceName,
    cityHash64(
        ServiceName,
        if(LogAttributes['exception.type'] != '', LogAttributes['exception.type'], 'log-error'),
        if(LogAttributes['exception.stacktrace'] != '',
           arrayStringConcat(arraySlice(arrayMap(l -> replaceRegexpAll(replaceRegexpAll(l, '0x[0-9a-fA-F]+', 'A'), '[0-9]+', 'N'), splitByChar('\n', LogAttributes['exception.stacktrace'])), 1, 8), '\n'),
           replaceRegexpAll(replaceRegexpAll(if(LogAttributes['exception.message'] != '', LogAttributes['exception.message'], Body), '0x[0-9a-fA-F]+', 'A'), '[0-9]+', 'N'))
    ) AS Fingerprint,
    if(LogAttributes['avuru.error.source'] = 'sentry', 'sentry', 'log') AS Source,
    if(LogAttributes['exception.type'] != '', LogAttributes['exception.type'], SeverityText) AS ExceptionType,
    if(LogAttributes['exception.message'] != '', LogAttributes['exception.message'], Body) AS ExceptionMessage,
    LogAttributes['exception.stacktrace'] AS ExceptionStacktrace,
    TraceId,
    SpanId,
    ResourceAttributes['deployment.environment.name'] AS Environment,
    LogAttributes['sentry.sdk.name'] AS SdkName,
    LogAttributes['sentry.sdk.version'] AS SdkVersion,
    LogAttributes AS Attributes
FROM otel.otel_logs
WHERE SeverityNumber >= 17;
