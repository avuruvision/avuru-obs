-- 0012_projects.sql  (module: Core)
-- UI-managed projects. Id is the immutable tenant slug (= partition key on all
-- telemetry); Label is the editable display name; Members is the multi-cluster
-- aggregate set (empty until Phase 3). Delete is a tombstone. Same
-- ReplacingMergeTree + FINAL + tombstone pattern as auth_grant / alert_channel.
CREATE TABLE IF NOT EXISTS otel.project
(
    `Id`        String,
    `Label`     String,
    `Members`   Array(String) DEFAULT [],
    `Deleted`   UInt8 DEFAULT 0,
    `CreatedBy` String,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);
