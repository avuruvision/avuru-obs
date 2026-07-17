-- Error tracking, log-derived view (modules: error-tracking AND logs).
-- Split from 0006 because it reads otel_logs: it must only be created when the
-- logs module is active (otherwise its source table does not exist). ERROR and
-- FATAL logs (SeverityNumber >= 17) become error issues; Sentry-originated
-- records (the gateway receiver tags them avuru.error.source=sentry and fills
-- exception.* / sentry.* attributes) are picked up here too, so they need no
-- separate path and also appear in the Logs explorer.
-- The migrator strips `--` comments then splits on `;`; no `;` inside a
-- statement, no `--` inside a string literal.
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
