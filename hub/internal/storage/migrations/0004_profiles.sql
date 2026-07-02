-- Profiles schema v1 (continuous CPU profiling, Lite tier) — Coroot-style
-- stack dedup: unique stacks live once in profiling_stacks (keyed by a
-- 64-bit hash), samples reference them by hash. UNLIKE 0001-0003 this schema
-- is Avuru-OWNED (no exporter contract): the OTLP Profiles signal is alpha
-- and clickhouseexporter has no profiles support, so ingestion goes through
-- hub/internal/storage/profilesadapter (see agent_docs/tech_stack.md).
-- Retention (TTL) on samples is applied by `hub migrate` ApplyRetention;
-- stacks are small and self-deduplicating (ReplacingMergeTree), no TTL.
CREATE DATABASE IF NOT EXISTS otel;

CREATE TABLE IF NOT EXISTS otel.profiling_stacks
(
    `Tenant` LowCardinality(String),
    `StackHash` UInt64,
    -- Leaf-first frame names (function name, or hex address when unsymbolized).
    `Frames` Array(String) CODEC(ZSTD(1)),
    `LastSeen` DateTime DEFAULT now()
)
ENGINE = ReplacingMergeTree(LastSeen)
ORDER BY (Tenant, StackHash);

CREATE TABLE IF NOT EXISTS otel.profiling_samples
(
    `Timestamp` DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    `Tenant` LowCardinality(String),
    `ServiceName` LowCardinality(String) CODEC(ZSTD(1)),
    -- "<type>:<unit>" from the profile's sample_type (e.g. "samples:count").
    `SampleType` LowCardinality(String),
    `StackHash` UInt64,
    `Value` UInt64,
    `NodeName` LowCardinality(String) CODEC(ZSTD(1)),
    `PodName` String CODEC(ZSTD(1)),
    `ContainerName` LowCardinality(String) CODEC(ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Tenant, ServiceName, Timestamp)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
