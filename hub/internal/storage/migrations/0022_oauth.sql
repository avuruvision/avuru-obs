-- 0022_oauth.sql  (module: MCP)
-- OAuth 2.1 authorization server, so a hosted MCP client can sign in.
--
-- Tagged MCP rather than Core: only an install running the MCP server has a
-- resource to protect. The OAuth ROUTES are separately flag-gated
-- (modules.mcp.oauth.enabled) — the same split 0014_collection_overlay.sql
-- makes between owning a table and switching the feature on.
--
-- Tokens are OPAQUE, stored only as SHA-256 hex, exactly like auth_token. Not
-- JWTs: audience, scope and project are read from these rows on EVERY request,
-- so revoking a grant or disabling a user takes effect on the next call. A JWT
-- would freeze authorization into a claim, which is what design/2026-08-13-
-- api-tokens.md refuses. Revocation is a tombstone throughout.
--
-- ReplacingMergeTree + FINAL reads, like the other auth tables: these are tiny
-- next to telemetry, and correctness at read time is worth more than the merge.

-- Registered clients. Registering grants NOTHING: a client can do nothing at
-- all until a human consents. Name and logo are SELF-DECLARED and unverified,
-- which is why the consent screen says so.
CREATE TABLE IF NOT EXISTS {db}.oauth_client
(
    `ClientID`        String,
    `Name`            String,
    `RedirectURIs`    Array(String),
    `GrantTypes`      Array(String),
    `TokenAuthMethod` LowCardinality(String) DEFAULT 'none',
    `SecretHash`      String DEFAULT '',
    `Scope`           String DEFAULT '',
    `SoftwareID`      String DEFAULT '',
    `ClientURI`       String DEFAULT '',
    `LogoURI`         String DEFAULT '',
    `RegisteredIP`    String DEFAULT '',
    `LastUsedAt`      DateTime64(3) DEFAULT toDateTime64(0, 3),
    `Revoked`         UInt8 DEFAULT 0,
    `CreatedAt`       DateTime64(3) DEFAULT now64(3),
    `UpdatedAt`       DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (ClientID);

-- One consent, by one person, to one client, for one project. This is the unit
-- a user revokes in "connected applications", and revoking it kills every token
-- minted under it.
CREATE TABLE IF NOT EXISTS {db}.oauth_grant
(
    `GrantID`   String,
    `ClientID`  String,
    `UserID`    String,
    `Scope`     String,
    `Project`   String,
    `Resource`  String,
    `Revoked`   UInt8 DEFAULT 0,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (GrantID);

-- Authorization codes: one shot, 60 seconds. Bound to the client, the exact
-- redirect URI, the PKCE challenge and the resource, so a code stolen in
-- transit cannot be redeemed anywhere else. The challenge METHOD is not stored
-- because only S256 is ever accepted.
CREATE TABLE IF NOT EXISTS {db}.oauth_auth_code
(
    `CodeHash`    String,
    `ClientID`    String,
    `UserID`      String,
    `GrantID`     String,
    `RedirectURI` String,
    `Resource`    String,
    `Scope`       String,
    `Project`     String,
    `Challenge`   String,
    `ExpiresAt`   DateTime64(3),
    `Consumed`    UInt8 DEFAULT 0,
    `CreatedAt`   DateTime64(3) DEFAULT now64(3),
    `UpdatedAt`   DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (CodeHash)
TTL toDateTime(ExpiresAt) + INTERVAL 1 DAY;

-- Access and refresh tokens, hashed. Resource lives HERE, in a row read on
-- every request, which is what makes an MCP token unusable against the rest of
-- the API rather than merely discouraged from it.
CREATE TABLE IF NOT EXISTS {db}.oauth_token
(
    `TokenHash`  String,
    `Kind`       LowCardinality(String),
    `GrantID`    String,
    `ClientID`   String,
    `UserID`     String,
    `Resource`   String,
    `Scope`      String,
    `Project`    String,
    `ExpiresAt`  DateTime64(3),
    `LastUsedAt` DateTime64(3) DEFAULT toDateTime64(0, 3),
    `Revoked`    UInt8 DEFAULT 0,
    `CreatedAt`  DateTime64(3) DEFAULT now64(3),
    `UpdatedAt`  DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (TokenHash)
TTL toDateTime(ExpiresAt) + INTERVAL 7 DAY;
