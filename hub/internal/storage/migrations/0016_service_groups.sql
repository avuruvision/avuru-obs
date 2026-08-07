-- 0016_service_groups.sql  (module: ServiceHealth)
-- UI-authored service health groups. Chart-declared groups keep living in the
-- ConfigMap and are NOT copied here; the resolver merges the two and lets the
-- config win a name collision (design/2026-08-07-service-groups-crud.md).
--
-- Name is the identity, as it already is in the config schema, so an edit is an
-- upsert and a delete is a tombstone. Same ReplacingMergeTree + FINAL +
-- tombstone pattern as project / alert_channel.
CREATE TABLE IF NOT EXISTS {db}.service_group
(
    `Name`       String,
    `Tier`       String,
    `Namespaces` Array(String) DEFAULT [],
    `Services`   Array(String) DEFAULT [],
    `Deleted`    UInt8 DEFAULT 0,
    `CreatedBy`  String,
    `CreatedAt`  DateTime64(3) DEFAULT now64(3),
    `UpdatedAt`  DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Name);
