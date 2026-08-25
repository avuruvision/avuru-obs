-- 0020_endpoint_checks.sql  (module: ServiceHealth)
-- Results of the scheduled HTTP probes declared alongside the service-health
-- groups (design/2026-07-20-endpoint-checks.md). Checks attach to a group and
-- feed its status, so service-health owns the table: an install without that
-- module never creates it, and the scheduler never runs.
--
-- One row per probe, never updated — this is an append-only signal like the
-- others, not state. It is queried two ways: the latest N results for one check
-- (the results panel), and the recent window for every check (the health
-- evaluation's consecutive-failure rule), which is why CheckId leads the sort
-- key and time descends within it.
--
-- TraceId/SpanId carry the span the probe emitted for its own request. That is
-- the join the feature exists for: a failing check clicks straight through to
-- the trace explaining WHY it failed. Empty when no gateway endpoint is
-- configured — the results still stand on their own.
CREATE TABLE IF NOT EXISTS {db}.endpoint_check_result
(
    `Tenant`    LowCardinality(String),
    `CheckId`   LowCardinality(String),
    `GroupName` LowCardinality(String),
    `Timestamp` DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    `Ok`        UInt8,
    `Status`    UInt16,
    `LatencyMs` Float64,
    `Error`     String CODEC(ZSTD(1)),
    `TraceId`   String CODEC(ZSTD(1)),
    `SpanId`    String CODEC(ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Tenant, CheckId, Timestamp);

-- No TTL clause here, and none in ApplyRetention: the volume is one row per
-- check per interval (a single 60s check is ~1.4k rows a day), so this follows
-- alert_history rather than the telemetry tables — bounded by construction, and
-- aged out per project by the tenant trim instead of by a global signal knob it
-- does not fit under.
