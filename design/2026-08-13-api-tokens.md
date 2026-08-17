# AEP: API tokens — non-interactive access on the same identity

- **Date:** 2026-08-13
- **Author(s):** Berny ryders
- **Status:** Draft

> Narrows and supersedes the **API-token seam** section of
> [2026-07-27 Additional clients](2026-07-27-clients-grafana-cli.md), which
> bundled tokens with a Grafana data source and a CLI. Those two clients stay
> in that AEP and stay unbuilt; tokens ship on their own because they are the
> thing everything else waits on. That AEP remains the record for the clients.

## Summary

Let a signed-in user mint a **bearer token** from Settings and use it as
`Authorization: Bearer <token>` against the Hub API, so scripts, CI jobs and
anything that is not a browser can call the same endpoints the UI calls. The
token authenticates **as its owner**: it resolves to the owner's `Identity`,
with the owner's grants, evaluated live on every request. It adds a second way
to *arrive at* an identity and changes nothing about what an identity may do.

## Motivation

Today the only way to authenticate to the Hub API is a session cookie obtained
from a login form or an OIDC redirect. That is fine for the SPA and unusable
for everything else: a CI job cannot complete an OIDC redirect, and a cron
script should not be storing someone's password. Anyone automating against the
hub right now has exactly two options, and both are bad — run the whole install
with auth off, or scrape a session cookie out of a browser and watch it expire.

This is also the credential the rest of the roadmap is blocked on. The Grafana
data source and the `avuruobs` CLI in
[the clients AEP](2026-07-27-clients-grafana-cli.md) both need it, and the
[auth AEP](2026-07-21-auth-oidc-rbac.md) deferred it explicitly as "additive
later on the same session layer". This is that addition, and nothing more.

It upholds the [enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass):
tokens reuse the auth provider and the grant model. There is no second
authorization path to audit. It costs the [wedge](../AGENTS.md) nothing — an
install that never opens the panel behaves exactly as it does today, and no new
component is introduced.

### Goals

- A user creates a named token, sees the secret **once**, and uses it as
  `Authorization: Bearer`.
- The token carries **the owner's grants, read live** — not a snapshot. Change
  the owner's role and the token's reach changes with it; disable the owner and
  the token stops working immediately.
- Revocable, listable, with `lastUsedAt` so a stale token is visible as stale.
- Optional expiry.
- Admins can see and revoke **anyone's** tokens; a user manages their own.
- Presenting a bad token is a **401**, never a silent downgrade.

### Non-goals

- **Narrowing a token below its owner's grants.** Tempting for least-privilege
  CI, but it creates a second permission set that has to be reasoned about and
  re-checked whenever the owner's changes. If a CI job should be a viewer, make
  it a viewer user. Revisit once there is real demand.
- **Service identities / synthetic users.** The clients AEP floated them. A
  token owned by a real user is enough to ship, and a service identity is a
  *user-management* feature, not a token feature.
- **The Grafana data source and the CLI.** They stay in
  [their AEP](2026-07-27-clients-grafana-cli.md). This one only builds what they
  will need.
- **Replacing ingest keys.** `auth_ingest_key` authenticates *telemetry going
  in*, scoped to one project, held by a collector. An API token authenticates
  *a caller reading and writing the API* as a person. Two credentials because
  they are two things; collapsing them would mean a collector's key could read
  every trace it ever shipped.
- **OAuth device-code flow.** Paste the token into config. Device flow is a
  usability improvement on top, not a prerequisite.

## Solution

### Storage

`auth_token`, following `auth_ingest_key` (0013) exactly — ReplacingMergeTree +
`FINAL` + tombstone, the pattern every UI-authored table here uses:

```sql
CREATE TABLE IF NOT EXISTS {db}.auth_token
(
    `TokenHash` String,           -- hex sha256 of the raw token; the lookup key
    `UserID`    String,           -- owner; grants are read from the owner, live
    `Name`      String,           -- what the human called it
    `Prefix`    String,           -- first 12 chars, kept clear for identification
    `ExpiresAt` DateTime64(3),    -- zero value = no expiry
    `LastUsedAt` DateTime64(3),   -- coarsened; see below
    `Revoked`   UInt8 DEFAULT 0,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (TokenHash);
```

Token generation reuses the ingest-key recipe verbatim
([`auth/ingestkey.go`](../hub/internal/auth/ingestkey.go)): 24 bytes from
`crypto/rand`, base64url, a fixed human-visible prefix, sha256-hex at rest, no
per-key salt (these are high-entropy randoms, not passwords). The prefix is
**`avurut_`**, deliberately one letter off `avuruk_`, so a secret found in a log
or a repo announces which kind of credential leaked and therefore what to
revoke.

### The seam

One branch in
[`requestIdentity`](../hub/internal/api/auth_middleware.go#L103), which is the
only place any credential becomes an `Identity`:

```
Authorization: Bearer <tok>  ──▶ resolve token ──▶ owner's Identity
        (absent)             ──▶ session cookie ──▶ (today's path)
        (neither)            ──▶ anonymous, or 401
```

Three properties of that branch matter more than the code:

1. **Bearer is checked first and does not fall through.** An explicit credential
   beats an ambient one, and a *failed* explicit credential is an error. If a
   presented token is unknown, revoked or expired, the request gets 401 — it
   must not quietly become the anonymous identity, which on a demo install would
   turn a broken CI job into a silently-degraded one that reads someone else's
   idea of "public". This is the same stance the session path already takes, and
   for the same reason.
2. **A store failure is 503, not 401.** Identical to the session branch's
   existing handling: only `storage.ErrNotFound` means "this credential is bad".
   Anything else means ClickHouse is unreachable, and answering 401 would tell
   every automated caller to go re-authenticate against a hub that is merely ill.
3. **The owner is re-read every request.** Resolution is
   token → `UserID` → `GetAuthUser` → grants, exactly as
   `IdentityFromToken` does for sessions. That is what makes "grants are live"
   true rather than aspirational, and it is why a disabled user's tokens die
   without anyone hunting them down. Sessions already pay this cost per request,
   so tokens introduce no new caching problem and this AEP deliberately adds no
   cache.

CSRF needs no new handling. Browsers do not attach `Authorization` headers
across origins on their own, so a bearer request is not forgeable the way a
cookie request is; `checkOrigin` keeps guarding the cookie path unchanged.

### `lastUsedAt`, honestly

Writing `LastUsedAt` on every authenticated request would mean **one ClickHouse
INSERT per API call** for token clients — precisely the traffic pattern tokens
exist to enable. So it is coarsened: an in-memory per-token stamp, flushed at
most **once a minute**. The column answers "is this token still in use?", which
is what it is for; it is not an audit log and the UI must not imply it is. A
replica restart loses at most a minute of it, which is fine for that question.

### API

Beside the existing ingest-key handlers, same shape:

| Route | Who |
|---|---|
| `GET /api/v1/tokens` | any authenticated user — **their own** tokens |
| `POST /api/v1/tokens` | any authenticated user — creates their own; secret returned **once** |
| `DELETE /api/v1/tokens/{id}` | the owner, or a global admin |
| `GET /api/v1/tokens?user=<id>` | global admin — someone else's |

A user with zero grants can still list and revoke their own tokens
(`authenticated`, not `secured`), for the same reason they can still log out:
someone whose access was just removed must still be able to clean up.

The response never contains the secret after creation, and never contains the
hash at all — only `id`, `name`, `prefix`, `createdAt`, `lastUsedAt`,
`expiresAt`, `revoked`.

### UI

Settings → Access grows a **Tokens** panel, next to ingest keys, whose
vocabulary it copies. Creation shows the secret once in a copyable block that
says plainly it will not be shown again. The list shows prefix, last use, and
expiry. Revoke is confirmed. Nothing renders when auth is disabled — there is no
identity to own a token.

### Alternatives considered

- **Long-lived session cookies for non-browser clients.** No new table, no new
  code. But a session is bound to a login event and a browser's cookie jar; you
  cannot name one, list one, or revoke one selectively without inventing exactly
  this metadata around it. That is the same feature with a worse noun.
- **A separate token service or gateway.** Rejected in the clients AEP and still
  rejected: a token that resolves to the existing `Identity` reuses all authz
  and adds no component.
- **Per-token scopes/permissions.** See non-goals. It is the obvious next
  request and the obvious wrong first move — two permission systems is how
  authorization bugs get written.
- **Reusing `auth_ingest_key` with a nullable project.** Would save a table and
  destroy the distinction between "may ship telemetry into project X" and "may
  read everything this person can read". The blast radius of confusing those is
  the whole point of having both.
- **JWT instead of an opaque random.** Self-contained tokens avoid the lookup,
  but they cannot be revoked without a denylist — which is the lookup, with
  extra steps and a cryptography surface. Opaque + hashed is smaller and honest.

## Verification

- **Unit (hub)**: token generation shape and prefix; resolution of
  valid / unknown / revoked / expired tokens (identity or 401); a store error
  yielding 503 not 401; a bad Bearer **not** falling through to anonymous when
  an anonymous identity is configured — that last one is the security assertion
  most worth pinning, since it only misbehaves on installs that allow anonymous
  access; `lastUsedAt` debounce (two calls inside the window write once).
- **Integration (ClickHouse)**: create / list / revoke over FINAL + tombstone;
  `lastUsedAt` update lands; an expired token is excluded from resolution but
  still listed (so its owner can see why it stopped working).
- **API**: a non-admin cannot read another user's tokens; an admin can; a
  zero-grant user can still list and revoke their own.
- **e2e (compose)**: create a token in the UI, use it from `curl` to call
  `/api/v1/services`, revoke it, confirm the next call is 401.
- **Done** = a user creates a token in Settings, a script authenticates with it
  and sees exactly what that user sees, an admin revokes it, and the script is
  locked out on its next request — with no change to how the SPA authenticates.

## Roadmap

- [ ] AEP accepted
- [ ] `auth_token` store (migration, Store methods, fake, integration test)
- [ ] Bearer branch in `requestIdentity` + resolution in the auth service
- [ ] `GET/POST/DELETE /api/v1/tokens`, one-time secret, admin visibility
- [ ] Settings → Access tokens panel
- [ ] e2e, CHANGELOG, docs-align (EN + FR)
