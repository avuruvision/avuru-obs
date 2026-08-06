-- UI-managed alerting channels (module `alerting`). Global (no Tenant):
-- channels are delivery endpoints shared by all projects; the notification
-- payload carries the tenant so one receiver can demux environments.
-- ReplacingMergeTree keeps the newest row per Name (same mutable-state pattern
-- as alert_state); deletes are tombstones (Deleted=1) so FINAL supersedes the
-- live row without ClickHouse mutations.
CREATE TABLE IF NOT EXISTS {db}.alert_channel
(
    `Name` String,
    `Type` LowCardinality(String),
    `URL` String,
    `Secret` String,
    `Deleted` UInt8 DEFAULT 0,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Name);
