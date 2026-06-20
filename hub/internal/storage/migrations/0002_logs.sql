-- Logs schema v1.
-- Column contract frozen from clickhouseexporter 0.154.0 against ClickHouse
-- 26.3 (note: 0.154.0 has NO TimestampTime column; it materializes k8s
-- resource attributes and uses CH 26.x `text` indexes). Exporter columns are
-- verbatim; Avuru additions rely on DEFAULT.
-- Retention (TTL) is applied separately by `hub migrate` ApplyRetention
-- (env-driven), so it is NOT declared here.
CREATE DATABASE IF NOT EXISTS otel;

CREATE TABLE IF NOT EXISTS otel.otel_logs
(
    `Timestamp` DateTime64(9) COMMENT 'Event timestamp with nanosecond precision' CODEC(Delta(8), ZSTD(1)),
    `TraceId` String COMMENT 'W3C trace identifier' CODEC(ZSTD(1)),
    `SpanId` String COMMENT 'W3C span identifier' CODEC(ZSTD(1)),
    `TraceFlags` UInt8 COMMENT 'W3C trace flags',
    `SeverityText` LowCardinality(String) COMMENT 'Log severity as text' CODEC(ZSTD(1)),
    `SeverityNumber` UInt8 COMMENT 'Log severity as number (1-24)',
    `ServiceName` LowCardinality(String) COMMENT 'Service that emitted the log' CODEC(ZSTD(1)),
    `Body` String COMMENT 'Log message body' CODEC(ZSTD(1)),
    `ResourceSchemaUrl` LowCardinality(String) COMMENT 'Schema URL for the resource' CODEC(ZSTD(1)),
    `ResourceAttributes` Map(LowCardinality(String), String) COMMENT 'Resource attributes as key-value pairs' CODEC(ZSTD(1)),
    `ScopeSchemaUrl` LowCardinality(String) COMMENT 'Schema URL for the instrumentation scope' CODEC(ZSTD(1)),
    `ScopeName` String COMMENT 'Instrumentation scope name' CODEC(ZSTD(1)),
    `ScopeVersion` LowCardinality(String) COMMENT 'Instrumentation scope version' CODEC(ZSTD(1)),
    `ScopeAttributes` Map(LowCardinality(String), String) COMMENT 'Instrumentation scope attributes' CODEC(ZSTD(1)),
    `LogAttributes` Map(LowCardinality(String), String) COMMENT 'Log record attributes' CODEC(ZSTD(1)),
    `EventName` String COMMENT 'Event name for log records representing events' CODEC(ZSTD(1)),
    `__otel_materialized_k8s.cluster.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.cluster.name'] CODEC(ZSTD(1)),
    `__otel_materialized_k8s.container.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.container.name'] CODEC(ZSTD(1)),
    `__otel_materialized_k8s.deployment.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.deployment.name'] CODEC(ZSTD(1)),
    `__otel_materialized_k8s.namespace.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.namespace.name'] CODEC(ZSTD(1)),
    `__otel_materialized_k8s.node.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.node.name'] CODEC(ZSTD(1)),
    `__otel_materialized_k8s.pod.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.pod.name'] CODEC(ZSTD(1)),
    `__otel_materialized_k8s.pod.uid` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.pod.uid'] CODEC(ZSTD(1)),
    `__otel_materialized_deployment.environment.name` LowCardinality(String) MATERIALIZED ResourceAttributes['deployment.environment.name'] CODEC(ZSTD(1)),

    -- Avuru addition (enterprise seam).
    `Tenant` LowCardinality(String) DEFAULT if(ResourceAttributes['avuru.tenant'] != '', ResourceAttributes['avuru.tenant'], 'default') CODEC(ZSTD(1)),

    INDEX idx_trace_id TraceId TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_log_attr_value mapValues(LogAttributes) TYPE text(tokenizer = 'array') GRANULARITY 100000000,
    INDEX idx_lower_body lower(Body) TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 100000000
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
-- Avuru change vs exporter DDL: Tenant leads the sort key.
ORDER BY (Tenant, toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
