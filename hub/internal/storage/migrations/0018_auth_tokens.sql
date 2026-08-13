-- 0018_auth_tokens.sql  (module: Core)
-- Personal API tokens: non-interactive access that resolves to the OWNER's
-- identity. The raw token is shown once at creation; only its SHA-256 hex is
-- stored. Prefix is the first 12 chars, kept clear for UI identification
-- ("avurut_ab12…") and distinct from the ingest-key prefix so a leaked secret
-- announces which credential to revoke. Revocation is a tombstone.
--
-- No grants column, deliberately: a token carries whatever its owner holds at
-- request time, so revoking a role revokes every token that rode on it.
CREATE TABLE IF NOT EXISTS {db}.auth_token
(
    `TokenHash`  String,
    `UserID`     String,
    `Name`       String,
    `Prefix`     String,
    `ExpiresAt`  DateTime64(3) DEFAULT toDateTime64(0, 3),
    `LastUsedAt` DateTime64(3) DEFAULT toDateTime64(0, 3),
    `Revoked`    UInt8 DEFAULT 0,
    `CreatedAt`  DateTime64(3) DEFAULT now64(3),
    `UpdatedAt`  DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (TokenHash);
