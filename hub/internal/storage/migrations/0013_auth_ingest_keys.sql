-- 0013_auth_ingest_keys.sql  (module: Core)
-- Per-project ingest keys (auth Plan C). The raw key is shown once at creation;
-- only its SHA-256 hex is stored. Prefix is the key's first 12 chars, kept in
-- clear for UI identification ("avuruk_ab12…"). Revocation is a tombstone. Same
-- ReplacingMergeTree + FINAL + tombstone pattern as auth_grant / project.
CREATE TABLE IF NOT EXISTS otel.auth_ingest_key
(
    `KeyHash`   String,
    `Project`   String,
    `Name`      String,
    `Prefix`    String,
    `CreatedBy` String,
    `Revoked`   UInt8 DEFAULT 0,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (KeyHash);
