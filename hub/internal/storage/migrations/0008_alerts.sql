-- Alerting schema v1 (module `alerting`).
-- Two tables: the current alert state per (rule, target) — the evaluator's
-- durable memory across restarts and re-fires — and an append-only history for
-- the UI timeline. Retention (TTL) on alert_history is applied separately by
-- `hub migrate` ApplyRetention (env-driven), so it is NOT declared here.

-- Current state per rule×target. ReplacingMergeTree keeps the newest row
-- (largest UpdatedAt) — the same mutable-state pattern as error_issue_status.
-- The evaluator loads this each tick, computes transitions, and writes back.
CREATE TABLE IF NOT EXISTS {db}.alert_state
(
    `Tenant` LowCardinality(String),
    `RuleName` String,
    `Target` String,
    `Status` Enum8('ok' = 0, 'pending' = 1, 'firing' = 2),
    `Since` DateTime64(3),
    `LastNotifiedAt` DateTime64(3) DEFAULT toDateTime64(0, 3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Tenant, RuleName, Target);

-- Append-only fire/resolve log for the Alerts UI timeline.
CREATE TABLE IF NOT EXISTS {db}.alert_history
(
    `Tenant` LowCardinality(String),
    `RuleName` String,
    `Target` String,
    `Kind` Enum8('fired' = 0, 'resolved' = 1),
    `Status` LowCardinality(String),
    `Reason` String,
    `FiredAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = MergeTree
PARTITION BY toDate(FiredAt)
ORDER BY (Tenant, FiredAt)
SETTINGS ttl_only_drop_parts = 1;
