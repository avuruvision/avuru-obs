# Auth Ingest Keys + Gateway Enforcement (Plan C of 3) Implementation Plan

> **STATUS: COMPLETE (2026-07-30)** — shipped on `feature/auth-ingest-keys`.
> This file has been corrected in place against what was actually built, so it
> is now a record rather than a proposal. Two of its original instructions were
> **wrong** and would have shipped a silently broken enforce mode; both are
> called out inline as `CORRECTION`. Read those before reusing this plan's
> patterns for Phase 3.
>
> Summary of what changed during execution:
>
> | Where | Plan said | Reality |
> |---|---|---|
> | Task 1 | migration `0012` | **`0013`** — Phase 1 took `0012_projects.sql` |
> | Task 1 | `migrate_test.go`, `-run TestMigrate` | `TestMigrateIsIdempotent` in `clickhouse/integration_test.go`, integration-tagged |
> | Task 7 | stamp tenant **before** `resource/tenant` | **after** — `resource/tenant` upserts, so the static tenant would win |
> | Task 7 | wire `tenantfromauth` whenever ingest auth is on | **enforce only** — it is a no-op in `log`, and omitting it keeps `log` byte-identical |
> | Task 9 | mirror the "alert-channels settings component" | channels live in `ui/src/components/alerts/`; the sibling is `settings/project-settings-card.tsx` |
> | Task 11 | `deploy/helm/values.yaml`, `templates/*` | `deploy/helm/avuruops/values.yaml`, `avuruops/templates/*` |
> | Task 11 | `staticKeys` with `secretRef` indirection | dropped for `provisionSensorKey` + a hub seeder (see Task 11) |
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Depends on:** Plan A (`2026-07-21-auth-core.md`) merged. Independent of Plan B —
can land before or after OIDC. Reuses the auth storage seam, `securedAdmin`
middleware and the local-OCB-module pattern (`gateway/sentryreceiver`).

**Goal:** Per-project **ingest API keys** validated in the gateway by a custom
OpenTelemetry Collector auth extension, so a project's telemetry is authenticated
at the write side and the key's project **stamps the tenant** (replacing topology
trust of a client-supplied `avuru.tenant`) — rolled out safely through
`auth.ingest.mode: off | log | enforce` (default **`log`**), per the accepted AEP
`design/2026-07-21-auth-oidc-rbac.md`.

**Architecture:** A fourth auth table `auth_ingest_key` (SHA-256 hash, project,
prefix, name, revoked). Admin CRUD in the hub (`/projects/{id}/keys`, secret
shown once). A hub-internal `POST /internal/v1/ingest-keys/validate` guarded by a
chart-generated hub↔gateway token. In the gateway, a new in-repo module
`gateway/avuruingestauth` implements the collector's server-auth extension: it
reads the API key off each OTLP request, validates it against the hub (30 s
positive/negative cache, 5 min stale grace through hub blips), and per `mode`
either ignores, logs, or rejects. A companion in-repo processor
`gateway/tenantfromauth` stamps `avuru.tenant` from the validated key's project in
`enforce` mode. The hub is never in the telemetry byte-path — it only answers the
gateway's control-plane validation call.

**Tech Stack:** Go 1.26, ClickHouse ReplacingMergeTree + FINAL, OpenTelemetry
Collector 0.154.0 (`extension/extensionauth`, `confighttp`, `processor` helpers),
Next.js static SPA.

**Conventions:** identical to Plan A/B (handlers return `error`; SQL behind
`storage.Store`; fakes over mocks; ReplacingMergeTree + FINAL + tombstones;
conventional commits, **no AI trailer**; `go test ./...` + `go vet` before commit).

---

### Task 1: Migration — `auth_ingest_key`

**Files:**
- Create: `hub/internal/storage/migrations/0013_auth_ingest_keys.sql`
- Test: `hub/internal/storage/clickhouse/integration_test.go` (existing suite)

> **CORRECTION (stale).** The plan was written before Projects Phase 1 merged
> and claimed slot `0012`; that is `0012_projects.sql`. This migration is
> **`0013`**. The migration test is `TestMigrateIsIdempotent`, not
> `TestMigrate`, and it is integration-tagged — it needs a real ClickHouse.

- [x] **Step 1: Write the migration**

```sql
-- Per-project ingest keys (auth Plan C). The raw key is shown once at creation;
-- only its SHA-256 hex is stored. Prefix is the key's first 12 chars, kept in
-- clear for UI identification ("avuruk_ab12cd…"). Revocation is a tombstone.
-- NOTE: file is 0013_auth_ingest_keys.sql (0012 is Projects Phase 1).
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
```

- [x] **Step 2: Run the migration test**

Run (needs a real ClickHouse; on this box Ryuk must be off):
```bash
cd hub && TESTCONTAINERS_RYUK_DISABLED=true \
  go test -tags=integration ./internal/storage/clickhouse/ -run TestMigrateIsIdempotent -v
```
Expected: PASS (`0001`–`0013` apply cleanly, and re-applying is a no-op).

- [x] **Step 3: Commit**

```bash
git add hub/internal/storage/migrations/0013_auth_ingest_keys.sql
git commit -m "feat(hub): migration 0013 — auth_ingest_key table"
```

---

### Task 2: Storage — ingest-key type + store methods

**Files:**
- Modify: `hub/internal/storage/store.go` (type + interface)
- Create: `hub/internal/storage/clickhouse/ingestkeys.go`
- Modify: `hub/internal/storage/storagetest/fake.go`
- Test: `hub/internal/storage/clickhouse/ingestkeys_test.go`

- [x] **Step 1: Add the type + interface methods**

```go
// AuthIngestKey is one per-project ingest credential. KeyHash is hex(sha256(raw)).
type AuthIngestKey struct {
	KeyHash   string
	Project   string
	Name      string
	Prefix    string
	CreatedBy string
	Revoked   bool
	CreatedAt time.Time
}
```

Add to the `Store` interface (near the other auth methods):

```go
// Ingest keys (auth Plan C). GetIngestKeyByHash returns ErrNotFound for an
// unknown OR revoked key. RevokeIngestKey tombstones by hash (ErrNotFound when
// no live key matches). ListIngestKeys returns live keys for one project.
CreateIngestKey(ctx context.Context, k AuthIngestKey) error
GetIngestKeyByHash(ctx context.Context, keyHash string) (AuthIngestKey, error)
ListIngestKeys(ctx context.Context, project string) ([]AuthIngestKey, error)
RevokeIngestKey(ctx context.Context, project, keyHash string) error
```

- [x] **Step 2: Write the failing integration test**

```go
func TestIngestKeyLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	k := storage.AuthIngestKey{KeyHash: "abc123", Project: "payments",
		Name: "prod-exporter", Prefix: "avuruk_ab12", CreatedBy: "admin"}
	if err := st.CreateIngestKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetIngestKeyByHash(ctx, "abc123")
	if err != nil || got.Project != "payments" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	if err := st.RevokeIngestKey(ctx, "payments", "abc123"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetIngestKeyByHash(ctx, "abc123"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("revoked key still resolves: %v", err)
	}
}
```

- [x] **Step 3: Run to verify it fails**

Run: `cd hub && go test ./internal/storage/... -run TestIngestKey -v`
Expected: FAIL (methods undefined).

- [x] **Step 4: Implement** `clickhouse/ingestkeys.go` and the fake

Follow the `alert_channel` / `auth_grant` implementations: INSERT for create and
revoke (revoke re-inserts the row with `Revoked=1`, newer `UpdatedAt`); SELECT
`... FINAL WHERE KeyHash = ? AND Revoked = 0` for get; `... FINAL WHERE Project =
? AND Revoked = 0 ORDER BY CreatedAt` for list. The fake stores keys in a
`map[string]AuthIngestKey` keyed by hash and filters revoked on read.

- [x] **Step 5: Run to verify it passes**

Run: `cd hub && go test ./internal/storage/... -run TestIngestKey -v`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add hub/internal/storage/
git commit -m "feat(hub): storage — ingest key store (create/get/list/revoke)"
```

---

### Task 3: `auth` — key generation + hashing

**Files:**
- Create: `hub/internal/auth/ingestkey.go`
- Create: `hub/internal/auth/ingestkey_test.go`

- [x] **Step 1: Write the failing test**

```go
func TestNewIngestKey(t *testing.T) {
	raw, prefix, hash := NewIngestKey()
	if !strings.HasPrefix(raw, "avuruk_") {
		t.Fatalf("raw missing prefix: %s", raw)
	}
	if prefix != raw[:12] {
		t.Fatalf("prefix = %s, want %s", prefix, raw[:12])
	}
	if hash != HashIngestKey(raw) {
		t.Fatal("hash mismatch")
	}
	// Distinct each call.
	raw2, _, _ := NewIngestKey()
	if raw == raw2 {
		t.Fatal("keys not random")
	}
}
```

- [x] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/auth/ -run TestNewIngestKey -v`
Expected: FAIL (undefined).

- [x] **Step 3: Implement**

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// NewIngestKey returns (raw, prefix, hash). The raw key is shown to the admin
// exactly once; only hash is persisted. prefix (first 12 chars) is stored clear
// for UI identification.
func NewIngestKey() (raw, prefix, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	raw = "avuruk_" + base64.RawURLEncoding.EncodeToString(b)
	return raw, raw[:12], HashIngestKey(raw)
}

// HashIngestKey is the storage/lookup hash (hex sha256). Lookups are by exact
// hash — no per-key salt (keys are high-entropy randoms, not passwords).
func HashIngestKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
```

- [x] **Step 4: Run to verify it passes**

Run: `cd hub && go test ./internal/auth/ -run TestNewIngestKey -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add hub/internal/auth/ingestkey.go hub/internal/auth/ingestkey_test.go
git commit -m "feat(hub): ingest key generation + hashing"
```

---

### Task 4: API — admin key CRUD (`/projects/{id}/keys`)

**Files:**
- Create: `hub/internal/api/ingest_keys.go`
- Create: `hub/internal/api/ingest_keys_test.go`
- Modify: `hub/internal/api/router.go` (register routes under `securedAdmin`)

- [x] **Step 1: Write the failing test**

```go
func TestCreateIngestKeyReturnsSecretOnce(t *testing.T) {
	a := newTestAPIAdmin(t) // helper: API with a fake store + admin identity
	rec := doJSON(t, a, "POST", "/api/v1/projects/payments/keys",
		`{"name":"prod-exporter"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct{ Key, Prefix string }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.HasPrefix(resp.Key, "avuruk_") {
		t.Fatalf("no raw key in create response: %s", rec.Body)
	}
	// List never returns the raw key — only prefix + metadata.
	rec = doJSON(t, a, "GET", "/api/v1/projects/payments/keys", "")
	if strings.Contains(rec.Body.String(), resp.Key) {
		t.Fatal("list leaked the raw key")
	}
	if !strings.Contains(rec.Body.String(), resp.Prefix) {
		t.Fatal("list missing the prefix")
	}
}
```

- [x] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run TestCreateIngestKey -v`
Expected: FAIL (handlers undefined).

- [x] **Step 3: Implement `ingest_keys.go`**

`handleCreateIngestKey`: decode `{name}`, `raw, prefix, hash := auth.NewIngestKey()`,
`CreateIngestKey`, respond `201 {key: raw, prefix, name, project}` — the ONLY time
`raw` is returned. `handleListIngestKeys`: `ListIngestKeys(project)` → `[{prefix,
name, createdBy, createdAt}]`. `handleRevokeIngestKey`: `RevokeIngestKey(project,
hash)` → 204 (`ErrNotFound` → 404). The `{project}` path value is authorized by
`securedAdmin` (global admin manages keys, per the AEP).

- [x] **Step 4: Register routes**

```go
// NOTE: the path value is {id}, matching the existing /api/v1/projects/{id}
// routes from Phase 1 — not {project}.
mux.Handle("GET /api/v1/projects/{id}/keys", a.securedAdmin(a.handleListIngestKeys))
mux.Handle("POST /api/v1/projects/{id}/keys", a.securedAdmin(a.handleCreateIngestKey))
mux.Handle("DELETE /api/v1/projects/{id}/keys/{hash}", a.securedAdmin(a.handleRevokeIngestKey))
```

- [x] **Step 5: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run TestIngestKey -v && go test ./internal/api/ -run TestCreateIngestKey -v`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add hub/internal/api/ingest_keys.go hub/internal/api/ingest_keys_test.go hub/internal/api/router.go
git commit -m "feat(hub): admin ingest-key CRUD with one-time secret display"
```

---

### Task 5: API — internal validation endpoint

**Files:**
- Create: `hub/internal/api/ingest_validate.go`
- Create: `hub/internal/api/ingest_validate_test.go`
- Modify: `hub/internal/api/router.go`

- [x] **Step 1: Write the failing test**

```go
func TestValidateIngestKey(t *testing.T) {
	a := newTestAPIWithInternalToken(t, "sekret") // seeds one live key for "payments"
	// Wrong internal token → 401 regardless of key.
	rec := doAuthed(t, a, "POST", "/internal/v1/ingest-keys/validate", "", "Bearer nope")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad internal token status = %d", rec.Code)
	}
	// Valid internal token + valid key → {valid:true, project:"payments"}.
	rec = doAuthed(t, a, "POST", "/internal/v1/ingest-keys/validate",
		`{"key":"`+seededRawKey+`"}`, "Bearer sekret")
	var resp struct {
		Valid   bool   `json:"valid"`
		Project string `json:"project"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Valid || resp.Project != "payments" {
		t.Fatalf("resp = %+v", resp)
	}
	// Unknown key → {valid:false}.
	rec = doAuthed(t, a, "POST", "/internal/v1/ingest-keys/validate",
		`{"key":"avuruk_bogus"}`, "Bearer sekret")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Valid {
		t.Fatal("bogus key validated")
	}
}
```

- [x] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run TestValidateIngestKey -v`
Expected: FAIL (handler undefined).

- [x] **Step 3: Implement**

`handleValidateIngestKey`: constant-time compare of the `Authorization: Bearer`
against `a.cfg.IngestInternalToken` (401 on mismatch); decode `{key}`;
`GetIngestKeyByHash(HashIngestKey(key))`; respond `{valid, project}`
(`ErrNotFound` → `{valid:false}`, HTTP 200 — the gateway caches negatives). This
route is registered raw (NOT behind the session middleware — it is a
service-to-service call) and only when `a.cfg.IngestInternalToken != ""`.

```go
if a.cfg.IngestInternalToken != "" {
	mux.Handle("POST /internal/v1/ingest-keys/validate", handle(a.handleValidateIngestKey))
}
```

- [x] **Step 4: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run TestValidateIngestKey -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add hub/internal/api/ingest_validate.go hub/internal/api/ingest_validate_test.go hub/internal/api/router.go
git commit -m "feat(hub): internal ingest-key validation endpoint (gateway-facing)"
```

---

### Task 6: Gateway — `avuruingestauth` server-auth extension

**Files:**
- Create: `gateway/avuruingestauth/{go.mod,factory.go,config.go,extension.go,extension_test.go}`
- Modify: `gateway/ocb-manifest.yaml`

Model the module on `gateway/sentryreceiver` (local OCB module + `replaces`
entry). The collector's `extensionauth.Server` interface is the seam.

- [x] **Step 1: Scaffold the module**

`go.mod` mirrors `sentryreceiver/go.mod` module path
`github.com/avuru/avuru-obs/gateway/avuruingestauth`, requiring
`go.opentelemetry.io/collector/extension`,
`go.opentelemetry.io/collector/extension/extensionauth`,
`go.opentelemetry.io/collector/client`, `config/confighttp` (for the hub client),
all at the pinned `v0.154.0`/`v1.60.0` line.

`config.go`:
```go
type Config struct {
	// HubValidateURL is the hub's internal validate endpoint.
	HubValidateURL string `mapstructure:"hub_validate_url"`
	// InternalToken authenticates the gateway→hub call (chart-generated).
	InternalToken configopaque.String `mapstructure:"internal_token"`
	// Mode is off | log | enforce (default log).
	Mode string `mapstructure:"mode"`
	// CacheTTL / StaleGrace bound the verdict cache (defaults 30s / 5m).
	CacheTTL   time.Duration `mapstructure:"cache_ttl"`
	StaleGrace time.Duration `mapstructure:"stale_grace"`
}
```

`factory.go`: `extension.NewFactory(component.MustNewType("avuruingestauth"),
createDefaultConfig, createExtension, component.StabilityLevelAlpha)`.

- [x] **Step 2: Write the failing extension test** (fake hub)

```go
func TestAuthenticateModes(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back valid for "good", project payments.
		var body struct{ Key string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Key == "good" {
			io.WriteString(w, `{"valid":true,"project":"payments"}`)
		} else {
			io.WriteString(w, `{"valid":false}`)
		}
	}))
	defer hub.Close()

	// enforce: unknown key rejected, good key accepted with project in auth data.
	ext := newTestExt(t, hub.URL, "enforce")
	if _, err := ext.Authenticate(ctx, hdr("bad")); err == nil {
		t.Fatal("enforce accepted a bad key")
	}
	cl, err := ext.Authenticate(ctx, hdr("good"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cl.Auth.GetAttribute("project"); got != "payments" {
		t.Fatalf("project attr = %v", got)
	}

	// log: bad key accepted (counted, not rejected).
	logExt := newTestExt(t, hub.URL, "log")
	if _, err := logExt.Authenticate(ctx, hdr("bad")); err != nil {
		t.Fatalf("log mode rejected: %v", err)
	}
}
```

- [x] **Step 3: Run to verify it fails**

Run: `cd gateway/avuruingestauth && go test ./... -run TestAuthenticate -v`
Expected: FAIL (extension undefined).

- [x] **Step 4: Implement `extension.go`**

Implement `extensionauth.Server`. `Authenticate(ctx, headers)`:
1. Read the key from `Authorization: Bearer <k>` or `X-Avuru-Api-Key`.
2. `off` → return ctx unchanged.
3. Look up the verdict cache (30 s TTL). On miss, POST `{key}` to
   `HubValidateURL` with the internal token; cache `{valid, project}` (positive
   AND negative). On hub error, serve a stale cached verdict if within
   `StaleGrace`, else fail-open in `log`, fail-closed in `enforce`.
4. `log` → always accept; increment a would-deny counter (self-metric) when the
   verdict is invalid.
5. `enforce` → reject invalid keys (`return ctx, errors.New("invalid ingest key")`);
   on accept, attach `client.NewContext` auth data exposing
   `GetAttribute("project") == verdict.Project`.

Use a `sync.Mutex`-guarded `map[string]cachedVerdict` (tiny key set). Cache key is
`HashIngestKey`-independent here — cache on the raw key string the sender sent
(the hub hashes it). Emit an `otelcol` counter `avuru_ingest_auth_denied_total`
via the extension's telemetry settings.

**Spike (verify against collector 0.154.0):** the exact `extensionauth.Server`
method set and how auth attributes surface to processors via
`client.FromContext(ctx).Auth`. Confirm with:
`go doc go.opentelemetry.io/collector/extension/extensionauth.Server` and the
contrib `basicauth`/`bearertokenauth` extensions as reference implementations.
Adjust the attribute-exposure code to match the confirmed API before proceeding.

- [x] **Step 5: Run to verify it passes**

Run: `cd gateway/avuruingestauth && go test ./... -v`
Expected: PASS.

- [x] **Step 6: Register in the OCB manifest**

Add under `extensions:` and `replaces:` in `ocb-manifest.yaml`:
```yaml
  - gomod: github.com/avuru/avuru-obs/gateway/avuruingestauth v0.1.0
# ...
  - github.com/avuru/avuru-obs/gateway/avuruingestauth => ../avuruingestauth
```

- [x] **Step 7: Commit**

```bash
git add gateway/avuruingestauth/ gateway/ocb-manifest.yaml
git commit -m "feat(gateway): avuruingestauth server-auth extension (cache, stale grace, modes)"
```

---

### Task 7: Gateway — stamp `avuru.tenant` from the validated key (`tenantfromauth`)

**Files:**
- Create: `gateway/tenantfromauth/{go.mod,factory.go,config.go,processor.go,processor_test.go}`
- Modify: `gateway/ocb-manifest.yaml`

The auth extension authenticates; a processor must do the mutation. This tiny
processor reads the `project` auth attribute set by `avuruingestauth` and, in
`enforce` mode, overwrites the `avuru.tenant` resource attribute on every
traces/metrics/logs record — so the key's project wins over any client-supplied
tenant.

> **CORRECTION 1 (design bug — would have shipped a broken enforce mode).**
> Step 4 below originally said to place `tenantfromauth` "**after** the receiver
> and **before** `resource/tenant`". That is wrong. `resource/tenant` is an
> **`upsert`** of the static `gateway.tenant`, so stamping the key's project
> first means the static value overwrites it moments later. The key would never
> win, enforce mode would look configured and do nothing, and nothing in the
> pipeline would report a problem. `tenantfromauth` must run **LAST** in the
> tenant stage: `transform/tenant, resource/tenant, tenantfromauth, batch`.
>
> **CORRECTION 2 (design bug — caught by an existing assertion).** The plan
> wired this processor whenever ingest auth was enabled. Wire it in
> **`enforce` only**. In `log` mode the extension attaches no validated project,
> so the processor is a no-op hop on every record; worse, adding it to the
> pipeline broke `template-test.sh`'s pre-existing
> `processors: [resource/tenant, batch]` assertion — which is precisely the
> assertion guarding that an upgrade does not change the pipeline. Gating on
> `enforce` keeps `log` **byte-identical** to a pre-ingest-keys install, which
> is the whole reason `log` is the default.

- [x] **Step 1: Write the failing test**

```go
func TestStampsTenantFromAuth(t *testing.T) {
	p := newProcessor(t)
	ctx := client.NewContext(context.Background(), client.Info{
		Auth: fakeAuthData{project: "payments"},
	})
	td := tracesWithResourceAttr("avuru.tenant", "attacker-claimed")
	out, err := p.processTraces(ctx, td)
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceAttr(out, "avuru.tenant"); got != "payments" {
		t.Fatalf("tenant = %q, want payments", got)
	}
}
```

- [x] **Step 2: Run to verify it fails**

Run: `cd gateway/tenantfromauth && go test ./... -v`
Expected: FAIL (processor undefined).

- [x] **Step 3: Implement**

A `processorhelper`-based processor for all three signals. Each record's resource
attributes get `avuru.tenant` set from `client.FromContext(ctx).Auth.GetAttribute("project")`
when that attribute is present. When absent (auth off, or `log` mode with no
verdict attribute), the record passes through untouched — so `log`/`off` behavior
is byte-identical to today and the existing `resource/tenant` +
`transform/tenant` processors remain the fallback. Reuse the **same collector
version pins** as `sentryreceiver`.

- [x] **Step 4: Wire the pipeline**

In the chart's collector config template, place `avuruingestauth` on the OTLP
receiver's `auth:` for **both** protocols (grpc and http), gated by
`mode != off`.

Add `tenantfromauth` **last in the tenant stage** and **only in `enforce`**
(see the two CORRECTIONs above). As shipped, via two helpers in `_helpers.tpl`:

```
avuruops.ingestAuthEnabled   -> mode != "off"      (extension + Secret + env)
avuruops.ingestTenantStamp   -> mode == "enforce"  (processor + pipeline stage)
```

which renders, with `gateway.tenant` set:

```yaml
processors: [transform/tenant, resource/tenant, tenantfromauth, batch]  # enforce
processors: [transform/tenant, resource/tenant, batch]                  # log / off
```

- [x] **Step 5: Run + register**

Run: `cd gateway/tenantfromauth && go test ./... -v` → PASS. Add the `gomod` +
`replaces` entries to `ocb-manifest.yaml`.

- [x] **Step 6: Commit**

```bash
git add gateway/tenantfromauth/ gateway/ocb-manifest.yaml
git commit -m "feat(gateway): tenantfromauth processor — key project overrides avuru.tenant"
```

---

### Task 8: OCB build + main.go wiring

**Files:**
- Modify: `hub/cmd/hub/main.go` (config: `IngestInternalToken`)
- Build check: the gateway image

- [x] **Step 1: Hub env wiring**

Read `AVURUOPS_INGEST_INTERNAL_TOKEN` in `run()` and pass into
`api.Config{IngestInternalToken: …}`. Empty → the internal endpoint is not
registered (Task 5 guard) and gateway enforcement is simply unused.

- [x] **Step 2: Build the gateway image (OCB resolves both new modules)**

Run: `make gateway-image` (or the `gateway/Dockerfile` build)
Expected: OCB builds `avuru-gateway` with both local modules resolved via
`replaces`; no version-drift errors against the 0.154.0 line.

- [x] **Step 3: Build + vet the hub**

Run: `cd hub && go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add hub/cmd/hub/main.go
git commit -m "feat(hub): wire ingest internal token"
```

---

### Task 9: UI — Settings → project → API keys

**Files:**
- Create: `ui/src/components/settings/ingest-keys-card.tsx`
- Create: `ui/src/hooks/use-ingest-keys.ts`
- Modify: `ui/src/components/settings/general-tab.tsx` (mount beside `project-settings-card.tsx`)
- Modify: `ui/src/lib/api-types.ts`, `ui/src/lib/query-keys.ts`
- Test: `ui/e2e/ingest-keys.spec.ts`

> **CORRECTION (stale).** Step 1 says to mirror "the existing alert-channels
> **settings** component". Alert channels are not in settings — they live in
> `ui/src/components/alerts/channels-panel.tsx`. The right sibling to mirror is
> `settings/project-settings-card.tsx` (Phase 1). Also, Phase 1 restructured
> settings into `settings-screen.tsx` with in-place `?tab=` tabs, so the mount
> point is `general-tab.tsx`, not `app/settings/page.tsx`.

- [x] **Step 1: Build the panel**

Admin-only panel: list keys (prefix, name, created-by, created-at), a "Create
key" action that POSTs and shows the returned raw key **once** in a copy-once
dialog with a clear "you won't see this again" warning, and a revoke action.
Mirror `settings/project-settings-card.tsx` for structure/styling.

As shipped, the list DTO also carries `keyHash` — a deliberate departure from
Task 4's sketch, which listed only `{prefix, name, createdBy, createdAt}`. The
hash is the stable delete handle (preimage-resistant, not a secret), so revoke
needs no extra lookup and the raw key still appears in exactly one response.

- [x] **Step 2: Build**

Run: `cd ui && npm run lint && npm run build`
Expected: static export succeeds.

- [x] **Step 3: Playwright**

`ingest-keys.spec.ts`: create shows the secret once; reload hides it; revoke
removes the row.

- [x] **Step 4: Commit**

```bash
git add ui/src/components/settings/ingest-keys.tsx ui/app/settings/page.tsx ui/src/lib/api-types.ts ui/e2e/ingest-keys.spec.ts
git commit -m "feat(ui): Settings → project → API keys (one-time secret display)"
```

---

### Task 10: e2e — enforce mode rejects unkeyed OTLP; log mode never drops

**Files:**
- Create: `e2e/ingest_keys_test.go` (`//go:build e2e`)
- Modify: `deploy/compose/docker-compose.yaml`

- [x] **Step 1: Write the e2e**

In an **isolated compose project** (`-p avuru-obs-e2e`, `ports: !override`): boot
the stack with `auth.ingest.mode=enforce`, a seeded key for `payments`; assert (a)
an OTLP export with the key lands and is stamped `avuru.tenant=payments` (query
the hub), (b) an unkeyed export is rejected, (c) flipping the compose env to
`log` makes the unkeyed export land again (drop-in promise holds).

- [x] **Step 2: Run**

Run: `make e2e`
Expected: PASS — enforce rejects unkeyed, log accepts, tenant stamped from the key.

- [x] **Step 3: Commit**

```bash
git add e2e/ingest_keys_test.go deploy/compose/docker-compose.yaml
git commit -m "test(e2e): ingest-key enforce rejects unkeyed OTLP; log never drops"
```

---

### Task 11: Helm — secrets, sensor key, mode value

**Files:**
- Modify: `deploy/helm/avuruops/values.yaml`, `deploy/helm/avuruops/templates/*`
- Create: `deploy/helm/avuruops/templates/ingest-secret.yaml`
- Modify: `deploy/helm/README.md`, `deploy/helm/template-test.sh`
- Create: `hub/cmd/hub/ingestseed.go` (+ test) — see the seeding note below

> **CORRECTION (stale).** The chart lives at `deploy/helm/avuruops/`, not
> `deploy/helm/`. `template-test.sh` and `README.md` are one level up, at
> `deploy/helm/`.

- [x] **Step 1: values + secrets**

As shipped:

```yaml
auth:
  ingest:
    mode: log                 # off | log | enforce
    internalToken: ""         # generated when empty, reused on upgrade (lookup)
    provisionSensorKey: true  # mint + seed the sensor's own key
    sensorKeyProject: ""      # defaults to gateway.tenant, else "default"
    cacheTTL: 30s
    staleGrace: 5m
```

**`staticKeys` was dropped.** The plan's `secretRef` indirection cannot work at
render time — Helm cannot read an arbitrary user Secret to build the seed
payload, and `lookup` is empty under `helm template`. Operator-managed keys are
therefore **admin-created** through the UI/API, which is the honest boundary.

**What replaced it — and why it is not optional.** `enforce` rejects unkeyed
OTLP, and the sensor is itself an unkeyed OTLP sender, so a naive enforce flip
silences avuru's own agent. The chart mints the sensor's key (it is the only
place that can hand the *same raw value* to both the sensor and the hub, since
the hub stores only the hash) and writes two Secret keys: `sensor-key` for the
exporter header, and `seed-keys` (JSON) which the hub seeds idempotently by
hash at startup — `hub/cmd/hub/ingestseed.go`, retrying like `bootstrapAdmin`
because `auth_ingest_key` may not be migrated yet on first boot.

**Both** sensor containers need the credential, not just one: the agent gets it
via its exporter `headers`, and OBI via `OTEL_EXPORTER_OTLP_HEADERS` (it exports
straight to the gateway, bypassing the agent's collector config).

- [x] **Step 2: helm template test**

Run: `make helm-check` (wraps `deploy/helm/template-test.sh`)
Expected: renders for `mode: off|log|enforce`; internal token appears only in
Secrets, never a ConfigMap; sensor gets a key header.

Five assertion blocks were added, including one that decodes the rendered
Secret and proves those exact bytes appear in no other object kind. That one
was mutation-tested — plant a leak and it fails; it is not vacuous.

- [x] **Step 3: Commit**

```bash
git add deploy/helm/
git commit -m "feat(deploy): ingest-key Helm values, internal token, sensor key, mode dial"
```

---

### Task 12: docs + changelog + verification

**Files:**
- Modify: `CHANGELOG.md`, `agent_docs/architecture.md`, `ROADMAP.md`, `deploy/helm/README.md`

- [x] **Step 1: Changelog + architecture + roadmap**

Add the ingest-keys entry under `## [Unreleased] / ### Added` (note default `log`,
the drop-in promise, the gateway extension). Update the auth section of
`architecture.md` (control-plane validation, hub never in byte-path). Tick the
two roadmap boxes (ingest keys; gateway extension + modes).

- [x] **Step 2: docs-align (EN + FR)**

Run the `docs-align` skill: changelog, feature-status matrix, API reference for
the key CRUD endpoints, and a short "authenticating ingest" guide.

- [x] **Step 3: Commit**

```bash
git add CHANGELOG.md agent_docs/architecture.md ROADMAP.md deploy/helm/README.md
git commit -m "docs: ingest keys + gateway enforcement (changelog, architecture, roadmap)"
```

---

## Final verification for Plan C — ALL GREEN (2026-07-30)

- [x] `cd hub && go build ./... && go vet ./... && go test -race ./...` — green (10 packages).
- [x] `golangci-lint run` (hub) — 0 issues. Fixed one real SA4000 in `internal/auth/ingestkey_test.go`.
- [x] All three gateway modules build/vet/test — green.
- [x] `make gateway-image` — OCB builds with both new local modules (98.7MB image).
- [x] `cd ui && npm run lint && npm run build` — static export succeeds; 3 Playwright specs pass.
- [x] e2e (isolated compose project) — enforce rejects unkeyed AND wrong keys; log accepts and never drops; tenant stamped from the key. **Actually run against a live stack**, not compile-only.
- [x] `make helm-check` — 49 assertions; internal token only in Secrets; renders for every mode.
- [x] **The drop-in promise holds:** `log` renders the identical pipeline, asserted at render time.
- [x] `docs-align` run (EN + FR) committed on `docs/ingest-keys`.

**CI gaps closed while doing this** (neither was in the plan): the in-repo
gateway modules are separate Go modules whose tests had **never run in CI** —
true of `sentryreceiver` too, they were only compiled as a side effect of the
image build — and `template-test.sh` never ran in CI either. Both are now gated,
and `make check` loops the same module list so local and CI agree.

## Open spike — RESOLVED

How `avuruingestauth`'s validated project reaches `tenantfromauth` via
`client.FromContext(ctx).Auth` under collector **0.154.0** was the one genuinely
uncertain integration. It works as sketched: the extension attaches auth data
whose `GetAttribute("project")` the processor reads, and `PutStr` overwrites
`avuru.tenant` unconditionally when that attribute is present, passing through
untouched when it is absent.

That `PutStr` is *unconditional* is exactly what made CORRECTION 1 in Task 7
necessary — it overwrites, but it can equally *be* overwritten by a later
`resource/tenant` upsert. Ordering, not capability, was the real risk here.
