# API tokens (v0.5 W2c) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A signed-in user mints a bearer token in Settings → Access and uses it as `Authorization: Bearer <tok>` against the Hub API, authenticating **as themselves** with their grants read live.

**Design gate:** [`design/2026-08-13-api-tokens.md`](../../../design/2026-08-13-api-tokens.md). Read it before Task 1 — it fixes the decisions below and they are not up for re-litigation.

**Tech Stack:** Go 1.23 + ClickHouse (hub), Next.js static export + TypeScript strict + TanStack Query (ui), Playwright (e2e).

**Branch:** `feature/api-tokens` (worktree `.claude/worktrees/api-tokens`). All paths repo-relative; run commands from the worktree root.

---

## The three decisions that shape every task

Taken from the AEP. If an implementation choice seems to conflict with one of these, the AEP wins.

1. **Grants are read live, not snapshotted.** Resolution is `token → UserID → GetAuthUser → grants`, exactly as `IdentityFromToken` does for sessions ([`auth/service.go:320`](../../../hub/internal/auth/service.go#L320)). This is what makes "disable the user and their tokens die" true instead of aspirational. Do not cache and do not copy grants onto the token row.

2. **A bad bearer is a 401 and never falls through.** [`requestIdentity`](../../../hub/internal/api/auth_middleware.go#L103) currently falls back to `a.cfg.AnonymousIdentity` when there is no valid session. A *presented but invalid* token must not take that path — on a demo install it would turn a broken CI job into a silently-degraded one reading whatever anonymous can see. Note the existing distinction the session branch already draws: only `storage.ErrNotFound` means "credential is bad"; any other error is `errStoreUnavailable` (503), because answering 401 would tell every automated caller to re-authenticate against a hub that is merely ill. Tokens get the same treatment.

3. **`LastUsedAt` is debounced to at most once a minute per token.** Writing it per request means one ClickHouse INSERT per API call for precisely the traffic pattern tokens exist to enable. The column answers "is this still in use?" — nothing finer. Do not present it as an audit trail in the UI.

---

## File Structure

**Hub — created**

| File | Responsibility |
|---|---|
| `hub/internal/storage/migrations/0018_auth_tokens.sql` | the `auth_token` table |
| `hub/internal/storage/clickhouse/authtoken.go` | create / get-by-hash / list / revoke / touch |
| `hub/internal/storage/clickhouse/authtoken_integration_test.go` | FINAL + tombstone + expiry behaviour |
| `hub/internal/auth/apitoken.go` | generation (`avurut_`) + `IdentityFromAPIToken` |
| `hub/internal/auth/apitoken_test.go` | resolution: valid / unknown / revoked / expired / store error |
| `hub/internal/auth/lastused.go` | the debounce |
| `hub/internal/api/tokens.go` | CRUD handlers |
| `hub/internal/api/tokens_test.go` | ownership + admin visibility |

**Hub — modified**

| File | Change |
|---|---|
| `hub/internal/storage/store.go` | `AuthToken` type + 5 Store methods |
| `hub/internal/storage/storagetest/fake.go` | fake implementations |
| `hub/internal/storage/migrations/migrations.go` | register in `Ordered` **and** `ByModule` |
| `hub/internal/api/auth_middleware.go` | the bearer branch in `requestIdentity` |
| `hub/internal/api/auth_middleware_test.go` | the no-fallthrough assertion |
| `hub/internal/api/router.go` | 3 routes |

**UI — created**

| File | Responsibility |
|---|---|
| `ui/src/hooks/use-api-tokens.ts` | query + mutations |
| `ui/src/components/settings/api-tokens-card.tsx` | list + create + revoke |

**UI — modified**

| File | Change |
|---|---|
| `ui/src/lib/api-types.ts` | `ApiToken`, create-response shape |
| `ui/src/lib/query-keys.ts` | `apiTokens` key |
| `ui/src/components/settings/access-tab.tsx` | host the card |
| `ui/e2e/settings-config.spec.ts` (or a new spec) | coverage |

Note the naming: the card sits beside [`ingest-keys-card.tsx`](../../../ui/src/components/settings/ingest-keys-card.tsx), whose shape it copies — same one-time-secret disclosure, same revoke confirmation.

---

## House rules for every commit

- **NO `Co-Authored-By` trailer** (AI_POLICY.md).
- Conventional commits (`feat(hub):`, `feat(ui):`, `test:`, `docs:`).
- Before pushing Go: `cd hub && ~/go/bin/golangci-lint run`. Build and vet are not enough.
- Integration tests on this machine need `TESTCONTAINERS_RYUK_DISABLED=true`.
- The UI has **no unit-test runner** (Playwright only, by design). Do not add one.
- Never hardcode hex in components; use daisyUI semantic tokens.
- API types are hand-written to mirror the Go types — read them, never invent a shape.
- **No competitor names** anywhere user-facing, CHANGELOG included.

---

## Task 1: Storage — the token table

**Files:**
- Create: `hub/internal/storage/migrations/0018_auth_tokens.sql`, `hub/internal/storage/clickhouse/authtoken.go`, `hub/internal/storage/clickhouse/authtoken_integration_test.go`
- Modify: `hub/internal/storage/store.go`, `hub/internal/storage/storagetest/fake.go`, `hub/internal/storage/migrations/migrations.go`

Read [`0013_auth_ingest_keys.sql`](../../../hub/internal/storage/migrations/0013_auth_ingest_keys.sql) and its ClickHouse implementation first. This is the same shape with an owner instead of a project, plus expiry and last-use.

- [ ] **Step 1: Migration**

```sql
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
```

Register in `migrations.go` in **both** `Ordered` and `ByModule` as `{modules.Core}` (auth gates everything — matching 0010/0011/0013/0015), with a one-line comment. `TestByModuleCoversOrdered` fails if you forget one; run `go test ./internal/storage/migrations/`.

If W2b has already landed `0017_oidc_group_mapping.sql`, this is 0018. If not, rebase before renumbering — **do not** reuse 0017.

- [ ] **Step 2: Type + Store methods**

```go
// AuthToken is one personal API token. TokenHash is hex(sha256(raw)); the raw
// token is shown once at creation and never stored. It carries no grants of
// its own — resolution reads the owner's, so a role change reaches every token
// that user holds without hunting them down.
type AuthToken struct {
	TokenHash  string
	UserID     string
	Name       string
	Prefix     string
	ExpiresAt  time.Time // zero = never expires
	LastUsedAt time.Time // zero = never used
	Revoked    bool
	CreatedAt  time.Time
}
```

On the Store interface:

```go
	CreateAuthToken(ctx context.Context, t AuthToken) error
	GetAuthTokenByHash(ctx context.Context, tokenHash string) (AuthToken, error)
	ListAuthTokens(ctx context.Context, userID string) ([]AuthToken, error)
	RevokeAuthToken(ctx context.Context, userID, tokenHash string) error
	TouchAuthToken(ctx context.Context, tokenHash string, at time.Time) error
```

`RevokeAuthToken` takes the userID as well as the hash so a non-admin's revoke URL cannot tombstone someone else's token by hash guess — the same scoping `RevokeIngestKey` applies with its project.

- [ ] **Step 3: Implement in `clickhouse/authtoken.go`**

Mirror the ingest-key implementation. `GetAuthTokenByHash` returns `storage.ErrNotFound` for unknown **and revoked** (as the ingest-key getter does) but returns an **expired** token normally — expiry is decided in the auth layer, so the list can still show the owner *why* it stopped working. `ListAuthTokens` returns live tokens newest first, including expired ones, for that reason.

`TouchAuthToken` re-inserts the row with a new `LastUsedAt` and `UpdatedAt` — a ReplacingMergeTree upsert, not an ALTER.

- [ ] **Step 4: Fake + integration test**

`storagetest/fake.go`: the five methods, following the ingest-key ones. Integration test: create two tokens for one user and one for another → list scoped per user → touch one and read the new `LastUsedAt` back → revoke one (assert `GetAuthTokenByHash` now says not-found and the other survives) → an expired token still lists but is returned by the getter.

- [ ] **Step 5: Run**

```bash
cd hub && go build ./... && go test ./internal/storage/migrations/ 2>&1 | tail -5
TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./internal/storage/clickhouse/ -run AuthToken 2>&1 | tail -15
```

- [ ] **Step 6: Commit**

```
feat(hub): store personal API tokens

Same hashed-secret + tombstone shape as ingest keys, with an owner
instead of a project and no grants column: a token carries whatever its
owner holds at request time, so revoking a role revokes every token that
rode on it and there is no second permission set to audit.

An expired token still lists - its owner should be able to see why it
stopped working rather than watch it vanish.
```

---

## Task 2: Generation + resolution

**Files:**
- Create: `hub/internal/auth/apitoken.go`, `hub/internal/auth/apitoken_test.go`

Read [`auth/ingestkey.go`](../../../hub/internal/auth/ingestkey.go) — generation is the same recipe and should look like it.

- [ ] **Step 1: Write the failing tests**

Cover, in `apitoken_test.go`:
1. `NewAPIToken()` returns a raw token beginning `avurut_`, a 12-char prefix that is its first 12 chars, and a hash that `HashAPIToken(raw)` reproduces.
2. A valid token resolves to the owner's `Identity` — **with the owner's current grants**, not anything stored on the token. Prove this by changing the fake's grants between two resolutions and asserting the second reflects the change.
3. An unknown token → `storage.ErrNotFound`.
4. A revoked token → `storage.ErrNotFound`.
5. An **expired** token → `storage.ErrNotFound` (from the auth layer, even though the store returns the row).
6. A **disabled owner** → `storage.ErrNotFound`. Same rule `IdentityFromToken` already applies to sessions.
7. A store failure surfaces as something that is **not** `ErrNotFound`, so the middleware can tell "bad credential" from "backend down".

- [ ] **Step 2: Run, confirm red.**

- [ ] **Step 3: Implement**

```go
// APITokenPrefix is the human-visible prefix on every raw API token —
// deliberately one letter off IngestKeyPrefix so a secret found in a log or a
// repo says which kind of credential leaked, and therefore what to revoke.
const APITokenPrefix = "avurut_"
```

`NewAPIToken` / `HashAPIToken` mirror `NewIngestKey` / `HashIngestKey`. `IdentityFromAPIToken(ctx, raw)` does hash → `GetAuthTokenByHash` → expiry check → `GetAuthUser` → disabled check → `identityFor`, which is the same tail `IdentityFromToken` runs; reuse it rather than re-deriving grants.

- [ ] **Step 4: Green**, then `cd hub && go test ./internal/auth/ 2>&1 | tail -5`.

- [ ] **Step 5: Commit**

```
feat(hub): resolve an API token to its owner's identity

Resolution reads the owner and their grants on every request, so it is
the live answer rather than a snapshot taken at mint time: disable the
user or drop their role and every token they hold stops working, with
nothing to hunt down. Expired and revoked tokens are both ErrNotFound to
callers, which keeps "this credential is bad" distinguishable from "the
store is down" one layer up.
```

---

## Task 3: The middleware branch — the security-critical one

**Files:**
- Create: `hub/internal/auth/lastused.go` (+ test)
- Modify: `hub/internal/api/auth_middleware.go`, `hub/internal/api/auth_middleware_test.go`

- [ ] **Step 1: Write the failing tests** in `auth_middleware_test.go`

The assertions that matter, in order of importance:

1. **A bad bearer with an anonymous identity configured returns 401 — NOT the anonymous identity.** This is the one that only misbehaves on installs allowing anonymous access, which is exactly why it needs a test rather than a code comment. Set `cfg.AnonymousIdentity` to a non-nil viewer, present a garbage bearer, assert 401.
2. A valid bearer yields the owner's identity, and `holdsAnywhere` / `project()` treat it identically to a session identity.
3. A store failure during token resolution returns **503**, not 401.
4. **Bearer wins over a cookie** when both are present, and an invalid bearer alongside a *valid* cookie is still 401 — an explicit credential that fails is an error, not something to shrug off.
5. No `Authorization` header at all leaves today's behaviour byte-identical (cookie, then anonymous, then 401). Every existing middleware test must still pass untouched.

- [ ] **Step 2: Run, confirm red.**

- [ ] **Step 3: Implement the branch** at the top of `requestIdentity`, before the cookie lookup. Parse `Authorization` case-insensitively for the `Bearer ` scheme; a header present but not `Bearer` is ignored (falls through to the cookie path) — only a *Bearer* credential is a credential we claim to understand.

- [ ] **Step 4: Implement the debounce** in `hub/internal/auth/lastused.go`: a small mutex-guarded `map[hash]time.Time`, `ShouldTouch(hash, now) bool` returning true at most once per minute per token, with the map pruned so a long-lived hub does not grow one entry per token forever. Test it directly: two calls inside the window → one touch; a call after the window → another.

The touch itself must not fail the request. Fire it after a successful resolution and log at debug on error — a full ClickHouse disk should not lock every token client out.

- [ ] **Step 5: Verify**

```bash
cd hub && go build ./... && go test ./... 2>&1 | tail -10 && ~/go/bin/golangci-lint run 2>&1 | tail -3
```

- [ ] **Step 6: Commit**

```
feat(hub): accept Authorization: Bearer as a request identity

One branch in requestIdentity, before the cookie: an explicit credential
beats an ambient one. A bearer that fails does NOT fall through to the
anonymous identity - on an install that allows anonymous access that
would turn a broken CI job into a silently-degraded one reading whatever
anonymous can see, which is worse than a clean 401. A store failure is
503, matching what the session branch already does, so an automated
caller is not told to re-authenticate against a hub that is merely ill.

lastUsedAt is debounced to once a minute per token: writing it per
request would mean one INSERT per API call for exactly the traffic
tokens exist to enable.
```

---

## Task 4: The API

**Files:**
- Create: `hub/internal/api/tokens.go`, `hub/internal/api/tokens_test.go`
- Modify: `hub/internal/api/router.go`

Read [`ingest_keys.go`](../../../hub/internal/api/ingest_keys.go) first — same CRUD shape, same one-time-secret disclosure, and the handlers should read as its sibling.

- [ ] **Step 1: Write the failing tests**

- `GET /api/v1/tokens` returns **only the caller's** tokens, metadata only — never the hash-as-secret framing, never a raw token.
- `POST /api/v1/tokens` returns the raw token exactly once, 201. Name required, length-capped (reuse the ingest-key cap rather than inventing a second one). An `expiresInDays` of 0/absent means no expiry.
- `DELETE /api/v1/tokens/{hash}` revokes the caller's own; another user's hash is a **404, not a 403** — telling a caller "that hash exists but isn't yours" confirms the hash.
- `GET /api/v1/tokens?user=<id>` is **global-admin only**; a non-admin passing `user=` gets 403.
- A global admin can delete anyone's token.
- **A zero-grant user can still list and revoke their own** — these routes use `authenticated`, not `secured`, for the same reason logout does.

- [ ] **Step 2: Run, confirm red.**

- [ ] **Step 3: Implement + register routes** in `router.go` beside the ingest-key routes:

```go
mux.Handle("GET /api/v1/tokens", a.authenticated(a.handleListAPITokens))
mux.Handle("POST /api/v1/tokens", a.authenticated(a.handleCreateAPIToken))
mux.Handle("DELETE /api/v1/tokens/{hash}", a.authenticated(a.handleRevokeAPIToken))
```

The admin `?user=` widening happens **inside** the list handler (check `id.IsAdmin()`), not via a second route — one URL, one resource.

- [ ] **Step 4: Green + lint**, then commit:

```
feat(hub): mint, list and revoke API tokens

Deliberately `authenticated` rather than `secured`: a user whose grants
were all revoked must still be able to clean up the credentials they
handed out, exactly as they can still log out. Another user's token hash
is a 404 rather than a 403 - a 403 would confirm the hash exists.
```

---

## Task 5: The UI

**Files:**
- Create: `ui/src/hooks/use-api-tokens.ts`, `ui/src/components/settings/api-tokens-card.tsx`
- Modify: `ui/src/lib/api-types.ts`, `ui/src/lib/query-keys.ts`, `ui/src/components/settings/access-tab.tsx`

Copy the shape of [`ingest-keys-card.tsx`](../../../ui/src/components/settings/ingest-keys-card.tsx) — it already solves one-time-secret disclosure and revoke confirmation, and the two cards should feel like the same product because they are.

- [ ] **Step 1: Types + hook.** Mirror the Go DTO exactly.

- [ ] **Step 2: The card.** Requirements:
  - The secret appears once, in a copyable block that says plainly it will not be shown again.
  - The list shows name, prefix, created, last used, expiry. Render "never used" and "never expires" as words, not as an epoch or a dash.
  - An expired token is visibly expired **and still listed**, so its owner can see why their script broke.
  - Revoke is confirmed.
  - One line saying the token acts with the owner's current permissions — an admin handing one to a CI job should know they are handing over their own reach.
  - Do not describe "last used" as an audit trail; it is coarse by design.

- [ ] **Step 3: Mount in the Access tab**, hidden when auth is disabled (there is no identity to own a token).

- [ ] **Step 4: Verify**

```bash
cd ui && npm run lint && npx tsc --noEmit && npm run build 2>&1 | tail -5
```

- [ ] **Step 5: Commit**

```
feat(ui): create and revoke API tokens from Settings -> Access

The card says the token acts with the owner's current permissions,
because handing one to a CI job is handing over your own reach and that
should not be something you discover later. An expired token stays in
the list rather than vanishing - its owner needs to see why the script
broke.
```

---

## Task 6: e2e, changelog, docs

- [ ] **Step 1: Playwright.** Create a token, assert the secret shows once and is gone after dismissing, assert revoke is confirmed before firing.

**`make e2e-ui` does not work on this machine.** Bring the stack up with a scratch compose override (never commit it) setting `AVURUOBS_AUTH_ANONYMOUS_ROLE: admin` and `AVURUOBS_AUTH_ANONYMOUS_PROJECTS: "*"` on the hub, seed as the `e2e-ui` target does, then `cd ui && npx playwright test`. Expect only the 2 by-design anonymous-mode `auth.spec.ts` failures.

- [ ] **Step 2: A real end-to-end check with `curl`.** Worth doing by hand once, because it is the entire point of the feature: mint a token in the UI, `curl -H "Authorization: Bearer …" .../api/v1/services`, revoke it, confirm the next call is 401. Report the actual output.

- [ ] **Step 3: CHANGELOG.md**, `[Unreleased] / ### Added`. Match the neighbours' voice: say what it unblocks (scripts and CI against the same API the UI uses), that the token acts as its owner with live grants, and that revoking a role revokes the token. **No competitor names** — the release job extracts these notes verbatim.

- [ ] **Step 4: Docs.** Run the `docs-align` skill (EN + FR): changelog both locales, an API reference row for `/api/v1/tokens`, and the auth guide gains a "non-interactive access" section.

- [ ] **Step 5: Full gates.**

```bash
make check && make helm-check && cd hub && ~/go/bin/golangci-lint run
```

---

## Done means

A user opens Settings → Access, mints a token, copies it once, and a script authenticates with it and sees exactly what that user sees — no more. An admin revokes it and the script's next call is 401. Dropping the owner's role narrows the token with it, without touching the token. An install that never opens the panel behaves exactly as it does today.
