# Auth Ingest Keys + Gateway Enforcement (Plan C of 3) Implementation Plan

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
prefix, name, revoked). Admin CRUD in the hub (`/projects/{project}/keys`, secret
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
- Create: `hub/internal/storage/migrations/0012_auth_ingest_keys.sql`
- Test: `hub/internal/storage/clickhouse/migrate_test.go` (existing suite)

- [ ] **Step 1: Write the migration**

```sql
-- Per-project ingest keys (auth Plan C). The raw key is shown once at creation;
-- only its SHA-256 hex is stored. Prefix is the key's first 12 chars, kept in
-- clear for UI identification ("avuruk_ab12cd…"). Revocation is a tombstone.
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

- [ ] **Step 2: Run the migration test**

Run: `cd hub && go test ./internal/storage/clickhouse/ -run TestMigrate -v`
Expected: PASS (`0001`–`0012` apply cleanly).

- [ ] **Step 3: Commit**

```bash
git add hub/internal/storage/migrations/0012_auth_ingest_keys.sql
git commit -m "feat(hub): migration — auth_ingest_key table"
```

---

### Task 2: Storage — ingest-key type + store methods

**Files:**
- Modify: `hub/internal/storage/store.go` (type + interface)
- Create: `hub/internal/storage/clickhouse/ingestkeys.go`
- Modify: `hub/internal/storage/storagetest/fake.go`
- Test: `hub/internal/storage/clickhouse/ingestkeys_test.go`

- [ ] **Step 1: Add the type + interface methods**

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

- [ ] **Step 2: Write the failing integration test**

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

- [ ] **Step 3: Run to verify it fails**

Run: `cd hub && go test ./internal/storage/... -run TestIngestKey -v`
Expected: FAIL (methods undefined).

- [ ] **Step 4: Implement** `clickhouse/ingestkeys.go` and the fake

Follow the `alert_channel` / `auth_grant` implementations: INSERT for create and
revoke (revoke re-inserts the row with `Revoked=1`, newer `UpdatedAt`); SELECT
`... FINAL WHERE KeyHash = ? AND Revoked = 0` for get; `... FINAL WHERE Project =
? AND Revoked = 0 ORDER BY CreatedAt` for list. The fake stores keys in a
`map[string]AuthIngestKey` keyed by hash and filters revoked on read.

- [ ] **Step 5: Run to verify it passes**

Run: `cd hub && go test ./internal/storage/... -run TestIngestKey -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/storage/
git commit -m "feat(hub): storage — ingest key store (create/get/list/revoke)"
```

---

### Task 3: `auth` — key generation + hashing

**Files:**
- Create: `hub/internal/auth/ingestkey.go`
- Create: `hub/internal/auth/ingestkey_test.go`

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/auth/ -run TestNewIngestKey -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

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

- [ ] **Step 4: Run to verify it passes**

Run: `cd hub && go test ./internal/auth/ -run TestNewIngestKey -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/auth/ingestkey.go hub/internal/auth/ingestkey_test.go
git commit -m "feat(hub): ingest key generation + hashing"
```

---

### Task 4: API — admin key CRUD (`/projects/{project}/keys`)

**Files:**
- Create: `hub/internal/api/ingest_keys.go`
- Create: `hub/internal/api/ingest_keys_test.go`
- Modify: `hub/internal/api/router.go` (register routes under `securedAdmin`)

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run TestCreateIngestKey -v`
Expected: FAIL (handlers undefined).

- [ ] **Step 3: Implement `ingest_keys.go`**

`handleCreateIngestKey`: decode `{name}`, `raw, prefix, hash := auth.NewIngestKey()`,
`CreateIngestKey`, respond `201 {key: raw, prefix, name, project}` — the ONLY time
`raw` is returned. `handleListIngestKeys`: `ListIngestKeys(project)` → `[{prefix,
name, createdBy, createdAt}]`. `handleRevokeIngestKey`: `RevokeIngestKey(project,
hash)` → 204 (`ErrNotFound` → 404). The `{project}` path value is authorized by
`securedAdmin` (global admin manages keys, per the AEP).

- [ ] **Step 4: Register routes**

```go
mux.Handle("GET /api/v1/projects/{project}/keys", a.securedAdmin(a.handleListIngestKeys))
mux.Handle("POST /api/v1/projects/{project}/keys", a.securedAdmin(a.handleCreateIngestKey))
mux.Handle("DELETE /api/v1/projects/{project}/keys/{hash}", a.securedAdmin(a.handleRevokeIngestKey))
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run TestIngestKey -v && go test ./internal/api/ -run TestCreateIngestKey -v`
Expected: PASS.

- [ ] **Step 6: Commit**

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

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run TestValidateIngestKey -v`
Expected: FAIL (handler undefined).

- [ ] **Step 3: Implement**

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

- [ ] **Step 4: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run TestValidateIngestKey -v`
Expected: PASS.

- [ ] **Step 5: Commit**

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

- [ ] **Step 1: Scaffold the module**

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

- [ ] **Step 2: Write the failing extension test** (fake hub)

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

- [ ] **Step 3: Run to verify it fails**

Run: `cd gateway/avuruingestauth && go test ./... -run TestAuthenticate -v`
Expected: FAIL (extension undefined).

- [ ] **Step 4: Implement `extension.go`**

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

- [ ] **Step 5: Run to verify it passes**

Run: `cd gateway/avuruingestauth && go test ./... -v`
Expected: PASS.

- [ ] **Step 6: Register in the OCB manifest**

Add under `extensions:` and `replaces:` in `ocb-manifest.yaml`:
```yaml
  - gomod: github.com/avuru/avuru-obs/gateway/avuruingestauth v0.1.0
# ...
  - github.com/avuru/avuru-obs/gateway/avuruingestauth => ../avuruingestauth
```

- [ ] **Step 7: Commit**

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

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run to verify it fails**

Run: `cd gateway/tenantfromauth && go test ./... -v`
Expected: FAIL (processor undefined).

- [ ] **Step 3: Implement**

A `processorhelper`-based processor for all three signals. Each record's resource
attributes get `avuru.tenant` set from `client.FromContext(ctx).Auth.GetAttribute("project")`
when that attribute is present. When absent (auth off, or `log` mode with no
verdict attribute), the record passes through untouched — so `log`/`off` behavior
is byte-identical to today and the existing `resource/tenant` +
`transform/tenant` processors remain the fallback. Reuse the **same collector
version pins** as `sentryreceiver`.

- [ ] **Step 4: Wire the pipeline**

In the chart's collector config template, place `avuruingestauth` on the OTLP
receiver's `auth:` and add `tenantfromauth` to the pipelines **after** the
receiver and before `resource/tenant`, gated by `auth.ingest.enabled`.

- [ ] **Step 5: Run + register**

Run: `cd gateway/tenantfromauth && go test ./... -v` → PASS. Add the `gomod` +
`replaces` entries to `ocb-manifest.yaml`.

- [ ] **Step 6: Commit**

```bash
git add gateway/tenantfromauth/ gateway/ocb-manifest.yaml
git commit -m "feat(gateway): tenantfromauth processor — key project overrides avuru.tenant"
```

---

### Task 8: OCB build + main.go wiring

**Files:**
- Modify: `hub/cmd/hub/main.go` (config: `IngestInternalToken`)
- Build check: the gateway image

- [ ] **Step 1: Hub env wiring**

Read `AVURUOPS_INGEST_INTERNAL_TOKEN` in `run()` and pass into
`api.Config{IngestInternalToken: …}`. Empty → the internal endpoint is not
registered (Task 5 guard) and gateway enforcement is simply unused.

- [ ] **Step 2: Build the gateway image (OCB resolves both new modules)**

Run: `make gateway-image` (or the `gateway/Dockerfile` build)
Expected: OCB builds `avuru-gateway` with both local modules resolved via
`replaces`; no version-drift errors against the 0.154.0 line.

- [ ] **Step 3: Build + vet the hub**

Run: `cd hub && go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add hub/cmd/hub/main.go
git commit -m "feat(hub): wire ingest internal token"
```

---

### Task 9: UI — Settings → project → API keys

**Files:**
- Create: `ui/src/components/settings/ingest-keys.tsx`
- Modify: `ui/app/settings/page.tsx` (mount the panel per selected project)
- Modify: `ui/src/lib/api-types.ts`
- Test: `ui/e2e/ingest-keys.spec.ts`

- [ ] **Step 1: Build the panel**

Admin-only panel: list keys (prefix, name, created-by, created-at), a "Create
key" action that POSTs and shows the returned raw key **once** in a copy-once
dialog with a clear "you won't see this again" warning, and a revoke action.
Mirror the existing alert-channels settings component for structure/styling.

- [ ] **Step 2: Build**

Run: `cd ui && npm run lint && npm run build`
Expected: static export succeeds.

- [ ] **Step 3: Playwright**

`ingest-keys.spec.ts`: create shows the secret once; reload hides it; revoke
removes the row.

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/settings/ingest-keys.tsx ui/app/settings/page.tsx ui/src/lib/api-types.ts ui/e2e/ingest-keys.spec.ts
git commit -m "feat(ui): Settings → project → API keys (one-time secret display)"
```

---

### Task 10: e2e — enforce mode rejects unkeyed OTLP; log mode never drops

**Files:**
- Create: `e2e/ingest_keys_test.go` (`//go:build e2e`)
- Modify: `deploy/compose/docker-compose.yaml`

- [ ] **Step 1: Write the e2e**

In an **isolated compose project** (`-p avuru-obs-e2e`, `ports: !override`): boot
the stack with `auth.ingest.mode=enforce`, a seeded key for `payments`; assert (a)
an OTLP export with the key lands and is stamped `avuru.tenant=payments` (query
the hub), (b) an unkeyed export is rejected, (c) flipping the compose env to
`log` makes the unkeyed export land again (drop-in promise holds).

- [ ] **Step 2: Run**

Run: `make e2e`
Expected: PASS — enforce rejects unkeyed, log accepts, tenant stamped from the key.

- [ ] **Step 3: Commit**

```bash
git add e2e/ingest_keys_test.go deploy/compose/docker-compose.yaml
git commit -m "test(e2e): ingest-key enforce rejects unkeyed OTLP; log never drops"
```

---

### Task 11: Helm — secrets, sensor key, mode value

**Files:**
- Modify: `deploy/helm/values.yaml`, `deploy/helm/templates/*`
- Modify: `deploy/helm/README.md`

- [ ] **Step 1: values + secrets**

```yaml
auth:
  ingest:
    mode: log            # off | log | enforce
    internalToken: ""    # auto-generated when empty (like the admin password)
    staticKeys: []       # [{project, name, secretRef:{name,key}}] chart-declared
```

Templates: generate `internalToken` when empty (persisted like the bootstrap
admin secret); inject it into both the hub (`AVURUOPS_INGEST_INTERNAL_TOKEN`) and
the gateway extension config; auto-generate the **sensor's** key for the default
project and mount it into the sensor's OTLP exporter headers; render
`staticKeys` from their secretRefs into `auth_ingest_key` seed rows via a hub
init hook OR document them as admin-created (choose the seed-hook path; note it).

- [ ] **Step 2: helm template test**

Run: `cd deploy/helm && ./template-test.sh`
Expected: renders for `mode: off|log|enforce`; internal token appears only in
Secrets, never a ConfigMap; sensor gets a key header.

- [ ] **Step 3: Commit**

```bash
git add deploy/helm/
git commit -m "feat(deploy): ingest-key Helm values, internal token, sensor key, mode"
```

---

### Task 12: docs + changelog + verification

**Files:**
- Modify: `CHANGELOG.md`, `agent_docs/architecture.md`, `ROADMAP.md`, `deploy/helm/README.md`

- [ ] **Step 1: Changelog + architecture + roadmap**

Add the ingest-keys entry under `## [Unreleased] / ### Added` (note default `log`,
the drop-in promise, the gateway extension). Update the auth section of
`architecture.md` (control-plane validation, hub never in byte-path). Tick the
two roadmap boxes (ingest keys; gateway extension + modes).

- [ ] **Step 2: docs-align (EN + FR)**

Run the `docs-align` skill: changelog, feature-status matrix, API reference for
the key CRUD endpoints, and a short "authenticating ingest" guide.

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md agent_docs/architecture.md ROADMAP.md deploy/helm/README.md
git commit -m "docs: ingest keys + gateway enforcement (changelog, architecture, roadmap)"
```

---

## Final verification for Plan C

- [ ] `cd hub && go build ./... && go test -race ./... && go vet ./... && make lint` — green.
- [ ] `cd gateway/avuruingestauth && go test ./...` and `cd gateway/tenantfromauth && go test ./...` — green.
- [ ] `make gateway-image` — OCB builds with both new local modules.
- [ ] `cd ui && npm run lint && npm run build` — static export succeeds.
- [ ] `make e2e` (isolated project) — enforce rejects unkeyed OTLP; log accepts and never drops; tenant stamped from the key.
- [ ] `helm template` — internal token only in Secrets; renders for every mode.
- [ ] **The drop-in promise holds:** with default `mode: log`, an existing unkeyed OTLP sender keeps landing after upgrade.
- [ ] `docs-align` run (EN + FR) committed.

## Open spike (carry into execution)

The one genuinely uncertain integration is how `avuruingestauth`'s validated
project reaches `tenantfromauth` via `client.FromContext(ctx).Auth` under
collector **0.154.0** (Task 6 Step 4, Task 7 Step 3). Resolve it first — it gates
the enforce-mode tenant stamping — using the contrib `bearertokenauth` extension
as the reference. `log` and `off` modes do not depend on it and can ship first.
