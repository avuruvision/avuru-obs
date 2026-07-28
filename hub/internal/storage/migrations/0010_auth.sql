-- Auth: local users, per-project grants, server-side sessions (core — auth
-- gates everything, so its schema is not module-toggleable). Mutable state
-- uses the alert_channel pattern: ReplacingMergeTree keeps the newest row per
-- key; deletes are tombstones so FINAL supersedes live rows without mutations.
-- Email uniqueness is enforced at the application layer (tables are tiny).
-- Note: auth_user has no Deleted column — users are disabled (Disabled=1), never hard-deleted, per the AEP.
CREATE TABLE IF NOT EXISTS otel.auth_user
(
    `Id` String,
    `Email` String,
    `Name` String,
    `PasswordHash` String,
    `Origin` LowCardinality(String) DEFAULT 'local',
    `Disabled` UInt8 DEFAULT 0,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);

CREATE TABLE IF NOT EXISTS otel.auth_grant
(
    `UserId` String,
    `Scope` String,
    `Role` LowCardinality(String),
    `Deleted` UInt8 DEFAULT 0,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (UserId, Scope);

CREATE TABLE IF NOT EXISTS otel.auth_session
(
    `TokenHash` String,
    `UserId` String,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `ExpiresAt` DateTime64(3),
    `Revoked` UInt8 DEFAULT 0,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (TokenHash)
-- Grace after expiry: revocation rewrites MUST keep ExpiresAt and readers filter ExpiresAt > now(), so a TTL race between tombstone and live parts can only touch already-expired sessions.
TTL toDateTime(ExpiresAt) + INTERVAL 7 DAY;
