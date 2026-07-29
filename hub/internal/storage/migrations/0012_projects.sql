-- UI-managed projects. Id is the immutable tenant slug (= the partition key on
-- all telemetry); Label is the editable display name; Members is the
-- multi-cluster aggregate set (empty for a normal project, unused until the
-- member-projects phase). Delete is a tombstone. Same ReplacingMergeTree +
-- FINAL + tombstone pattern as auth_* / alert_channel; the table is tiny so
-- FINAL is cheap. Not module-toggleable — projects are core.
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
