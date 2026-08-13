-- 0017_oidc_group_mapping.sql
-- UI-authored OIDC group->role rules. Chart-declared rules keep living in the
-- OIDC ConfigMap and are NOT copied here; the resolver merges the two and lets
-- the config win a name collision, exactly as service_group does.
--
-- Group is the identity, as it already is in the config schema, so an edit is
-- an upsert and a delete is a tombstone. Same ReplacingMergeTree + FINAL +
-- tombstone pattern as service_group / project / alert_channel.
CREATE TABLE IF NOT EXISTS {db}.oidc_group_mapping
(
    `Group`     String,
    `Role`      String,
    `Projects`  Array(String) DEFAULT [],
    `Deleted`   UInt8 DEFAULT 0,
    `CreatedBy` String,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (`Group`);
