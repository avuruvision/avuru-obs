-- Error fingerprint v2, log-derived view (modules: error-tracking AND logs).
--
-- THE DEFECT, as reported: the Errors screen listed one issue per REQUEST
-- instead of one per error kind. 0007 hashed the raw log Body with only
-- `0x`-hex and digit-run normalization, and a bare 32-char trace id is neither,
-- so every request minted its own fingerprint. One hotrod retry loop produced
-- 15 separate issues of three events each — the three being the retries inside
-- a single trace, which is the only thing that shared a fingerprint. A Java
-- service fragmented the same way on the trace id inside its log prefix.
--
-- Split from 0023 for the reason 0007 is split from 0006: this view reads
-- otel_logs, so it must not be created when the logs module is off. ByModule
-- tags whole files, so one file could not carry both module sets.
--
-- The normalizer below is byte-identical to 0023's and must stay that way, or
-- the same error reported through a span event and through a log becomes two
-- issues. See 0023 for why the order of the five rules is load-bearing and why
-- this is duplicated rather than a UDF.
--
-- ExceptionMessage stays the RAW body: normalization is for grouping only, so
-- the issue title remains a real log line someone can search for.
--
-- Redefining a materialized view means dropping and recreating it (0006:87-89).
-- The view is `TO error_events`, so the DROP removes only the trigger and never
-- a row of data.
-- The migrator strips `--` comments then splits on `;`; no `;` inside a
-- statement, no `--` inside a string literal.
DROP VIEW IF EXISTS {db}.error_events_from_logs_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS {db}.error_events_from_logs_mv
TO {db}.error_events
AS
SELECT
    Timestamp,
    if(ResourceAttributes['avuru.tenant'] != '', ResourceAttributes['avuru.tenant'], 'default') AS Tenant,
    ServiceName,
    cityHash64(
        ServiceName,
        if(LogAttributes['exception.type'] != '', LogAttributes['exception.type'], 'log-error'),
        replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(
            if(LogAttributes['exception.stacktrace'] != '',
               arrayStringConcat(arraySlice(splitByChar('\n', LogAttributes['exception.stacktrace']), 1, 8), '\n'),
               if(LogAttributes['exception.message'] != '', LogAttributes['exception.message'], Body)),
            '[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}:[0-9]{2}([.,][0-9]+)?(Z|[+-][0-9]{2}:?[0-9]{2})?', 'T'),
            '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}', 'U'),
            '0x[0-9a-fA-F]+', 'A'),
            '[0-9a-fA-F]{8,}', 'H'),
            '[0-9]+', 'N')
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
FROM {db}.otel_logs
WHERE SeverityNumber >= 17;
