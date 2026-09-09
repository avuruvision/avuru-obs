-- Error fingerprint v2, span-exception view (module: error-tracking).
--
-- THE DEFECT: 0006 normalized only `0x`-hex and digit runs before hashing. Any
-- other per-request token survives, so an exception carrying a request id in
-- its message (and no stack trace to hash instead) forked into one issue per
-- request. 0024 fixes the same defect in the log-derived view, where it was
-- actually reported: 15 issues for one retry loop, because a bare 32-char trace
-- id is neither `0x`-prefixed nor all digits.
--
-- Normalization order is load-bearing. ISO timestamps first, because collapsing
-- digits would shred them. UUIDs and `0x`-hex before the bare-hex rule, because
-- only the 8- and 12-char groups of a UUID reach the length threshold and the
-- middle would survive. Bare hex before digits, because digits-first turns
-- `dd411d9cc936265b` into a mixed token no hex rule can then catch. Digits last
-- also neutralizes thread names like [http-nio-80-exec-3], so no separate rule
-- for those is needed.
--
-- MV 2 (error spans) is deliberately NOT changed here: it fingerprints on
-- SpanName and HTTP status, structured fields, not the free text where ids
-- live. A service emitting per-id span names is broken at the instrumentation
-- and wrecks every span-name aggregate too — do not "complete the set".
--
-- The normalizer is duplicated in 0024 because a ClickHouse SQL UDF is
-- server-global and cannot carry the {db} placeholder, so it would collide
-- between two installs sharing one server. KEEP THE TWO IN SYNC —
-- TestFingerprintNormalizerIsShared enforces it.
--
-- Redefining a materialized view means dropping and recreating it (0006:87-89).
-- Both views are `TO error_events`, so the DROP removes only the trigger and
-- never a row of data.
-- The migrator strips `--` comments then splits on `;`; no `;` inside a
-- statement, no `--` inside a string literal.
DROP VIEW IF EXISTS {db}.error_events_from_span_events_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS {db}.error_events_from_span_events_mv
TO {db}.error_events
AS
SELECT
    EvtTime AS Timestamp,
    if(ResourceAttributes['avuru.tenant'] != '', ResourceAttributes['avuru.tenant'], 'default') AS Tenant,
    ServiceName,
    cityHash64(
        ServiceName,
        EvtAttrs['exception.type'],
        replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(
            if(EvtAttrs['exception.stacktrace'] != '',
               arrayStringConcat(arraySlice(splitByChar('\n', EvtAttrs['exception.stacktrace']), 1, 8), '\n'),
               EvtAttrs['exception.message']),
            '[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}:[0-9]{2}([.,][0-9]+)?(Z|[+-][0-9]{2}:?[0-9]{2})?', 'T'),
            '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}', 'U'),
            '0x[0-9a-fA-F]+', 'A'),
            '[0-9a-fA-F]{8,}', 'H'),
            '[0-9]+', 'N')
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
FROM {db}.otel_traces
ARRAY JOIN
    Events.Timestamp AS EvtTime,
    Events.Name AS EvtName,
    Events.Attributes AS EvtAttrs
WHERE EvtName = 'exception';
