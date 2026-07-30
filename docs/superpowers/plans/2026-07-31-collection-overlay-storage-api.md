# Collection Overlay — Storage, API & Chart Scaffolding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin persist a runtime "collection overlay" (sensor signal toggles + namespace-exclude list) through a new hub API, gated by a default-off chart flag, with the RBAC/chart scaffolding a later applier will use — WITHOUT yet actually pushing the overlay to the cluster (that's a separate follow-up plan).

**Architecture:** A `collection_overlay` ClickHouse singleton row (migration `0014`) behind `storage.Store`; a pure `hub/internal/collection` package owning the closed overlay schema; `GET/PUT/DELETE /api/v1/collection/overlay` (admin-only) wired through a `collection.Applier` seam whose only implementation in this plan is a logging no-op; a new `collection.runtimeControl.enabled` chart value rendering the RBAC (ServiceAccount/Role/RoleBinding) and a curated non-secret base-values ConfigMap the real applier will read in the follow-up plan.

**Tech Stack:** Go (`hub/`, stdlib `net/http.ServeMux`), ClickHouse (`ReplacingMergeTree`), Helm chart (`deploy/helm/avuruobs`).

**Scope note:** This is deliberately the first of (at least) two plans for design/2026-07-27-collection-control-plane.md, split along this repo's own "one logical change per MR" convention (`AGENTS.md` → PR hygiene). The real applier (embed the chart via `go:embed`, render via `helm.sh/helm/v3/pkg/engine`, patch the sensor ConfigMaps + DaemonSet containers/volumes/annotation) and the UI writable card are separate follow-up plans, written once this one has landed — their exact interfaces depend on the `collection.Applier` seam this plan defines.

**Read first:** `design/2026-07-27-collection-control-plane.md` (the accepted AEP).

---

### Task 1: `collection_overlay` storage

**Files:**
- Create: `hub/internal/storage/migrations/0014_collection_overlay.sql`
- Modify: `hub/internal/storage/migrations/migrations.go`
- Modify: `hub/internal/storage/store.go`
- Create: `hub/internal/storage/clickhouse/collection.go`
- Create: `hub/internal/storage/clickhouse/collection_integration_test.go`
- Modify: `hub/internal/storage/storagetest/fake.go`

- [ ] **Step 1: Write the migration**

```sql
-- hub/internal/storage/migrations/0014_collection_overlay.sql
-- UI-managed runtime collection overlay (design/2026-07-27-collection-control-plane.md).
-- Exactly one logical row: Id is a constant so ReplacingMergeTree collapses
-- every write down to the single newest overlay — same mutable-state idiom
-- as alert_channel (0009), but keyed by a fixed Id instead of a natural list
-- key, since there is exactly one overlay per release, not a named list.
CREATE TABLE IF NOT EXISTS otel.collection_overlay
(
    `Id` LowCardinality(String) DEFAULT 'default',
    `Overlay` String,
    `UpdatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedBy` String
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);
```

- [ ] **Step 2: Register the migration**

Open `hub/internal/storage/migrations/migrations.go`. Find the `Ordered` slice literal (it ends with `"0013_auth_ingest_keys.sql",`) and add a new last element:

```go
	"0014_collection_overlay.sql",
```

Find the `ByModule` map literal (it has an entry `"0013_auth_ingest_keys.sql": {modules.Core},`) and add:

```go
	"0014_collection_overlay.sql": {modules.Core},
```

`modules.Core` is correct here (not a new module) — `collection.runtimeControl.enabled` is a plain feature flag, not one of the 8 `modules.Name` values, so the table exists on every install; only the API routes and RBAC are gated by the flag.

- [ ] **Step 3: Run the existing migration test to confirm it still passes**

Run: `cd hub && go test ./internal/storage/migrations/...`
Expected: PASS (this exercises `TestByModuleCoversOrdered`, which fails the build if `Ordered` and `ByModule` diverge — confirms Step 2 was done correctly on both sides).

- [ ] **Step 4: Add the `CollectionOverlay` type and `Store` interface methods**

Open `hub/internal/storage/store.go`. Near the `AlertChannel` type (search for `type AlertChannel struct`), add:

```go
// CollectionOverlay is the persisted runtime collection overlay (design/
// 2026-07-27-collection-control-plane.md). Overlay is an opaque JSON blob —
// its schema is owned and validated by package collection, not here.
type CollectionOverlay struct {
	Overlay   string
	UpdatedAt time.Time
	UpdatedBy string
}
```

In the `Store` interface, near the alert-channel methods, add:

```go
	// Collection overlay (runtime sensor toggle — design/
	// 2026-07-27-collection-control-plane.md). LoadCollectionOverlay returns
	// ErrNotFound when no overlay has ever been saved. SaveCollectionOverlay
	// upserts the singleton; saving an empty ("{}"-equivalent) Overlay is how
	// the API layer implements "reset to chart defaults" — there is no
	// separate delete method.
	LoadCollectionOverlay(ctx context.Context) (CollectionOverlay, error)
	SaveCollectionOverlay(ctx context.Context, ov CollectionOverlay) error
```

- [ ] **Step 5: Run the build to confirm the interface change compiles against nothing yet (it won't — that's expected)**

Run: `cd hub && go build ./...`
Expected: FAIL — `*clickhouse.Store` and `*storagetest.Fake` (and any other `storage.Store` implementer) no longer satisfy the interface. This confirms the compiler is tracking every implementer; the next two steps fix it.

- [ ] **Step 6: Implement the ClickHouse methods**

```go
// hub/internal/storage/clickhouse/collection.go
package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// LoadCollectionOverlay returns the current overlay, or storage.ErrNotFound
// if none has ever been saved. FINAL collapses the ReplacingMergeTree to the
// single row; the table is a deliberate singleton (Id is always 'default')
// so FINAL is cheap.
func (s *Store) LoadCollectionOverlay(ctx context.Context) (storage.CollectionOverlay, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Overlay, UpdatedAt, UpdatedBy
FROM collection_overlay FINAL
WHERE Id = 'default'`)
	if err != nil {
		return storage.CollectionOverlay{}, fmt.Errorf("load collection overlay: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return storage.CollectionOverlay{}, storage.ErrNotFound
	}
	var ov storage.CollectionOverlay
	if err := rows.Scan(&ov.Overlay, &ov.UpdatedAt, &ov.UpdatedBy); err != nil {
		return storage.CollectionOverlay{}, fmt.Errorf("scan collection overlay: %w", err)
	}
	return ov, rows.Err()
}

// SaveCollectionOverlay upserts the singleton row; ReplacingMergeTree keeps
// the newest write (UpdatedAt, defaulted server-side to now64(3)).
func (s *Store) SaveCollectionOverlay(ctx context.Context, ov storage.CollectionOverlay) error {
	err := s.conn.Exec(ctx, `
INSERT INTO collection_overlay (Id, Overlay, UpdatedBy)
VALUES ('default', ?, ?)`, ov.Overlay, ov.UpdatedBy)
	if err != nil {
		return fmt.Errorf("save collection overlay: %w", err)
	}
	return nil
}
```

- [ ] **Step 7: Write the integration test**

```go
// hub/internal/storage/clickhouse/collection_integration_test.go
//go:build integration

package clickhouse

import (
	"context"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestCollectionOverlayRoundTrip(t *testing.T) {
	store := newTestStore(t) // matches the helper other *_integration_test.go files in this package use to obtain a *Store against the test ClickHouse
	ctx := context.Background()

	if _, err := store.LoadCollectionOverlay(ctx); err != storage.ErrNotFound {
		t.Fatalf("LoadCollectionOverlay before any save: got err=%v, want storage.ErrNotFound", err)
	}

	if err := store.SaveCollectionOverlay(ctx, storage.CollectionOverlay{
		Overlay:   `{"obiEnabled":false}`,
		UpdatedBy: "admin@example.com",
	}); err != nil {
		t.Fatalf("SaveCollectionOverlay: %v", err)
	}

	got, err := store.LoadCollectionOverlay(ctx)
	if err != nil {
		t.Fatalf("LoadCollectionOverlay after save: %v", err)
	}
	if got.Overlay != `{"obiEnabled":false}` || got.UpdatedBy != "admin@example.com" {
		t.Fatalf("LoadCollectionOverlay = %+v, want Overlay/UpdatedBy to match what was saved", got)
	}

	// A second save replaces the singleton (ReplacingMergeTree by UpdatedAt) —
	// FINAL must return the newest write, not both rows.
	if err := store.SaveCollectionOverlay(ctx, storage.CollectionOverlay{
		Overlay:   `{"obiEnabled":true}`,
		UpdatedBy: "someone-else@example.com",
	}); err != nil {
		t.Fatalf("SaveCollectionOverlay (second write): %v", err)
	}
	got, err = store.LoadCollectionOverlay(ctx)
	if err != nil {
		t.Fatalf("LoadCollectionOverlay after second save: %v", err)
	}
	if got.Overlay != `{"obiEnabled":true}` || got.UpdatedBy != "someone-else@example.com" {
		t.Fatalf("LoadCollectionOverlay after second save = %+v, want the newest write only", got)
	}
}
```

Before writing this, run `grep -n "func newTestStore\|func TestMain" hub/internal/storage/clickhouse/*_integration_test.go` to confirm the exact helper name/signature this package's other integration tests use to obtain a live `*Store` — use whatever that helper is actually called if it differs from `newTestStore`.

- [ ] **Step 8: Run the integration test**

Run: `cd hub && make test-integration` (or, if that Makefile target doesn't exist under that exact name, the command from `hub/Makefile`: `go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/...` — check `hub/Makefile` for the exact target name first and use it).
Expected: PASS — requires a running ClickHouse; check `agent_docs/development.md` or `hub/Makefile` for how other integration tests bring one up (likely `make dev` / a docker-compose ClickHouse) if one isn't already running.

- [ ] **Step 9: Add the fake store methods**

Open `hub/internal/storage/storagetest/fake.go`. Near the alert-channel fake fields, add:

```go
	// Collection overlay fake.
	Overlay       storage.CollectionOverlay
	OverlaySet    bool
	OverlayErr    error
	SavedOverlays []storage.CollectionOverlay
```

Near the alert-channel fake methods, add:

```go
func (f *Fake) LoadCollectionOverlay(_ context.Context) (storage.CollectionOverlay, error) {
	if f.OverlayErr != nil {
		return storage.CollectionOverlay{}, f.OverlayErr
	}
	if !f.OverlaySet {
		return storage.CollectionOverlay{}, storage.ErrNotFound
	}
	return f.Overlay, nil
}

func (f *Fake) SaveCollectionOverlay(_ context.Context, ov storage.CollectionOverlay) error {
	f.SavedOverlays = append(f.SavedOverlays, ov)
	f.Overlay = ov
	f.OverlaySet = true
	return nil
}
```

- [ ] **Step 10: Confirm everything builds**

Run: `cd hub && go build ./... && go vet ./...`
Expected: PASS (this is the step that confirms `*clickhouse.Store` and `*storagetest.Fake` both now satisfy `storage.Store` again).

- [ ] **Step 11: Commit**

```bash
git add hub/internal/storage/migrations/0014_collection_overlay.sql \
        hub/internal/storage/migrations/migrations.go \
        hub/internal/storage/store.go \
        hub/internal/storage/clickhouse/collection.go \
        hub/internal/storage/clickhouse/collection_integration_test.go \
        hub/internal/storage/storagetest/fake.go
git commit -m "feat(hub): collection_overlay storage (design/2026-07-27-collection-control-plane.md)"
```

---

### Task 2: Overlay schema/validation package (pure)

**Files:**
- Create: `hub/internal/collection/overlay.go`
- Test: `hub/internal/collection/overlay_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// hub/internal/collection/overlay_test.go
package collection

import "testing"

func TestParseOverlay_Empty(t *testing.T) {
	ov, err := ParseOverlay("")
	if err != nil {
		t.Fatalf("ParseOverlay(\"\"): %v", err)
	}
	if !ov.Empty() {
		t.Fatalf("ParseOverlay(\"\") = %+v, want Empty()", ov)
	}
}

func TestParseOverlay_Valid(t *testing.T) {
	ov, err := ParseOverlay(`{"obiEnabled":false,"excludeNamespaces":["payments"]}`)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	if ov.ObiEnabled == nil || *ov.ObiEnabled != false {
		t.Fatalf("ObiEnabled = %v, want pointer to false", ov.ObiEnabled)
	}
	if ov.ExcludeNamespaces == nil || len(*ov.ExcludeNamespaces) != 1 || (*ov.ExcludeNamespaces)[0] != "payments" {
		t.Fatalf("ExcludeNamespaces = %v, want [payments]", ov.ExcludeNamespaces)
	}
	if ov.Empty() {
		t.Fatalf("ParseOverlay with fields set reported Empty()")
	}
}

func TestParseOverlay_RejectsUnknownFields(t *testing.T) {
	if _, err := ParseOverlay(`{"freeformCollectorConfig":"whatever"}`); err == nil {
		t.Fatal("ParseOverlay accepted an unknown field — the schema must be closed")
	}
}

func TestParseOverlay_RejectsEmptyNamespaceEntry(t *testing.T) {
	if _, err := ParseOverlay(`{"excludeNamespaces":[""]}`); err == nil {
		t.Fatal("ParseOverlay accepted an empty namespace entry")
	}
}

func TestOverlay_EncodeParseRoundTrip(t *testing.T) {
	obi := false
	ns := []string{"payments", "billing"}
	want := Overlay{ObiEnabled: &obi, ExcludeNamespaces: &ns}

	encoded, err := want.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := ParseOverlay(encoded)
	if err != nil {
		t.Fatalf("ParseOverlay(Encode()): %v", err)
	}
	if got.ObiEnabled == nil || *got.ObiEnabled != false {
		t.Fatalf("round-trip ObiEnabled = %v, want false", got.ObiEnabled)
	}
	if got.ExcludeNamespaces == nil || len(*got.ExcludeNamespaces) != 2 {
		t.Fatalf("round-trip ExcludeNamespaces = %v, want 2 entries", got.ExcludeNamespaces)
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd hub && go test ./internal/collection/...`
Expected: FAIL with "package collection: no non-test Go files" or "undefined: ParseOverlay" (the package doesn't exist yet).

- [ ] **Step 3: Implement the package**

```go
// hub/internal/collection/overlay.go
// Package collection owns the runtime "collection overlay" — the closed,
// UI-editable subset of sensor config (design/
// 2026-07-27-collection-control-plane.md). It has no storage or Kubernetes
// dependency: it is pure data + validation, callable from both the API layer
// and (in the follow-up plan) the applier.
package collection

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Overlay is the closed, UI-editable subset of sensor collection config. A
// nil field means "not overridden — use the chart's base values". This is
// the JSON shape persisted in storage.CollectionOverlay.Overlay and returned
// by GET /api/v1/collection/overlay.
type Overlay struct {
	ObiEnabled          *bool     `json:"obiEnabled,omitempty"`
	LogsEnabled         *bool     `json:"logsEnabled,omitempty"`
	KubeletstatsEnabled *bool     `json:"kubeletstatsEnabled,omitempty"`
	ProfilerEnabled     *bool     `json:"profilerEnabled,omitempty"`
	GreenEnabled        *bool     `json:"greenEnabled,omitempty"`
	ExcludeNamespaces   *[]string `json:"excludeNamespaces,omitempty"`
}

// Empty reports whether every field is unset — the "reset to chart defaults" state.
func (o Overlay) Empty() bool {
	return o.ObiEnabled == nil && o.LogsEnabled == nil && o.KubeletstatsEnabled == nil &&
		o.ProfilerEnabled == nil && o.GreenEnabled == nil && o.ExcludeNamespaces == nil
}

// ParseOverlay decodes and validates the closed schema. An empty string
// decodes to the zero Overlay (Empty() == true). Unknown JSON keys are
// rejected so the API surface can never silently widen into free-form config
// (design doc, Goals: "bounded + schema-validated").
func ParseOverlay(raw string) (Overlay, error) {
	if raw == "" {
		return Overlay{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var o Overlay
	if err := dec.Decode(&o); err != nil {
		return Overlay{}, fmt.Errorf("parse overlay: %w", err)
	}
	if err := validateNamespaces(o.ExcludeNamespaces); err != nil {
		return Overlay{}, err
	}
	return o, nil
}

func validateNamespaces(ns *[]string) error {
	if ns == nil {
		return nil
	}
	for _, n := range *ns {
		if n == "" {
			return fmt.Errorf("excludeNamespaces: empty namespace name not allowed")
		}
	}
	return nil
}

// Encode serializes the overlay back to the JSON string storage persists.
func (o Overlay) Encode() (string, error) {
	b, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("encode overlay: %w", err)
	}
	return string(b), nil
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `cd hub && go test ./internal/collection/...`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add hub/internal/collection/overlay.go hub/internal/collection/overlay_test.go
git commit -m "feat(hub): collection overlay closed-schema validation package"
```

---

### Task 3: Applier seam (no-op for this plan)

**Files:**
- Create: `hub/internal/collection/applier.go`
- Test: `hub/internal/collection/applier_test.go`

- [ ] **Step 1: Write the failing test**

```go
// hub/internal/collection/applier_test.go
package collection

import (
	"context"
	"testing"
)

func TestNoopApplier_NeverErrors(t *testing.T) {
	var a Applier = NoopApplier{}
	if err := a.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("NoopApplier.Apply: %v", err)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test ./internal/collection/...`
Expected: FAIL with "undefined: Applier" / "undefined: NoopApplier".

- [ ] **Step 3: Implement the seam**

```go
// hub/internal/collection/applier.go
package collection

import (
	"context"
	"log/slog"
)

// Applier pushes an overlay change to the cluster. The real implementation
// (design/2026-07-27-collection-control-plane.md: render the chart via an
// embedded Helm engine, patch the sensor ConfigMaps + DaemonSet) ships in a
// follow-up plan; this seam lets storage + the API land and be fully tested
// first, per this repo's one-logical-change-per-MR convention (AGENTS.md).
type Applier interface {
	// Apply reconciles the cluster's sensor manifests to match overlay.
	// Called after every successful overlay write, including a reset (i.e.
	// an Empty() overlay, meaning "back to chart defaults").
	Apply(ctx context.Context, overlay Overlay) error
}

// NoopApplier logs that a real apply was skipped. Used while
// collection.runtimeControl.enabled is on but the cluster-side applier isn't
// wired yet: the overlay still persists correctly and the API is fully
// functional end to end — only "the sensor pods actually pick it up" is
// deferred to the follow-up plan.
type NoopApplier struct{}

func (NoopApplier) Apply(_ context.Context, _ Overlay) error {
	slog.Warn("collection overlay saved but not applied to the cluster — applier not implemented yet")
	return nil
}
```

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd hub && go test ./internal/collection/...`
Expected: PASS (6 tests total across the package now).

- [ ] **Step 5: Commit**

```bash
git add hub/internal/collection/applier.go hub/internal/collection/applier_test.go
git commit -m "feat(hub): collection.Applier seam with a logging no-op implementation"
```

---

### Task 4: API handlers + router wiring + capability flag

**Files:**
- Create: `hub/internal/api/collection.go`
- Test: `hub/internal/api/collection_test.go`
- Modify: `hub/internal/api/router.go`
- Modify: `hub/internal/api/capabilities.go`
- Test: `hub/internal/api/capabilities_test.go`

First, run `grep -n "func TestHandleCapabilities\|func newTestAPI\|func setupAPI" hub/internal/api/capabilities_test.go hub/internal/api/router_test.go` to find the exact helper this package's existing tests use to build an `*API` + fake store for a handler test (e.g. a `newTestAPI(t, cfg, fake)`-shaped helper) — reuse it rather than hand-rolling a new one; the handler tests below assume such a helper exists and is called `newTestAPI` only as a placeholder name — use whatever the real one is called.

- [ ] **Step 1: Write the failing handler tests**

```go
// hub/internal/api/collection_test.go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestCollectionOverlay_GetEmpty(t *testing.T) {
	fake := &storagetest.Fake{}
	a := newTestAdminAPI(t, fake, Config{CollectionRuntimeControlEnabled: true, CollectionApplier: collection.NoopApplier{}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/collection/overlay", nil)
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req) // adjust to however this package's other tests dispatch through the mux/handler under admin auth

	if rec.Code != http.StatusOK {
		t.Fatalf("GET overlay (none saved) = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"overlay":{}`) {
		t.Fatalf("GET overlay (none saved) body = %s, want an empty overlay object", rec.Body.String())
	}
}

func TestCollectionOverlay_PutThenGet(t *testing.T) {
	fake := &storagetest.Fake{}
	a := newTestAdminAPI(t, fake, Config{CollectionRuntimeControlEnabled: true, CollectionApplier: collection.NoopApplier{}})

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/collection/overlay", strings.NewReader(`{"obiEnabled":false}`))
	putRec := httptest.NewRecorder()
	a.mux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT overlay = %d, want 200: %s", putRec.Code, putRec.Body.String())
	}
	if len(fake.SavedOverlays) != 1 {
		t.Fatalf("SavedOverlays = %d entries, want 1", len(fake.SavedOverlays))
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/collection/overlay", nil)
	getRec := httptest.NewRecorder()
	a.mux.ServeHTTP(getRec, getReq)
	if !strings.Contains(getRec.Body.String(), `"obiEnabled":false`) {
		t.Fatalf("GET overlay after PUT = %s, want obiEnabled:false", getRec.Body.String())
	}
}

func TestCollectionOverlay_PutRejectsUnknownField(t *testing.T) {
	fake := &storagetest.Fake{}
	a := newTestAdminAPI(t, fake, Config{CollectionRuntimeControlEnabled: true, CollectionApplier: collection.NoopApplier{}})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/collection/overlay", strings.NewReader(`{"freeformCollectorConfig":"x"}`))
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT overlay with unknown field = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCollectionOverlay_Delete(t *testing.T) {
	fake := &storagetest.Fake{Overlay: storage.CollectionOverlay{Overlay: `{"obiEnabled":false}`}, OverlaySet: true}
	a := newTestAdminAPI(t, fake, Config{CollectionRuntimeControlEnabled: true, CollectionApplier: collection.NoopApplier{}})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/collection/overlay", nil)
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE overlay = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := fake.SavedOverlays[len(fake.SavedOverlays)-1]
	if last.Overlay != "{}" && last.Overlay != "" {
		t.Fatalf("DELETE overlay saved %q, want an empty overlay", last.Overlay)
	}
}

func TestCollectionOverlay_RoutesAbsentWhenFlagOff(t *testing.T) {
	fake := &storagetest.Fake{}
	a := newTestAdminAPI(t, fake, Config{CollectionRuntimeControlEnabled: false})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/collection/overlay", nil)
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET overlay with flag off = %d, want 404", rec.Code)
	}
}
```

This test file deliberately uses placeholder helper/field names (`newTestAdminAPI`, `a.mux`, the `storagetest` import path) that must be corrected to match whatever this package's *existing* tests (`alerts_test.go`, `capabilities_test.go`) actually use — read one of those first and mirror its exact setup helper and how it dispatches a request through admin auth, rather than inventing a new pattern.

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd hub && go test ./internal/api/... -run TestCollectionOverlay`
Expected: FAIL — `Config.CollectionRuntimeControlEnabled` / `Config.CollectionApplier` undefined, and the handlers don't exist.

- [ ] **Step 3: Add the `Config` fields and `API` field**

Open `hub/internal/api/router.go`. In the `Config` struct (after `IngestInternalToken string`), add:

```go
	// CollectionRuntimeControlEnabled gates GET/PUT/DELETE
	// /api/v1/collection/overlay and is echoed in GET /api/v1/capabilities
	// (design/2026-07-27-collection-control-plane.md). Chart-generated,
	// injected as AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED.
	CollectionRuntimeControlEnabled bool
	// CollectionApplier pushes an accepted overlay to the cluster. nil
	// defaults to collection.NoopApplier{} in Register.
	CollectionApplier collection.Applier
```

Add the import `"github.com/avuru/avuru-obs/hub/internal/collection"` to this file's import block.

In the `API` struct, add:

```go
	collectionApplier collection.Applier
```

In `Register`, right after `a := &API{provider: provider, cfg: cfg, modules: active}`, add:

```go
	if cfg.CollectionApplier != nil {
		a.collectionApplier = cfg.CollectionApplier
	} else {
		a.collectionApplier = collection.NoopApplier{}
	}
```

Near the other flag-gated route blocks (next to the `if cfg.IngestInternalToken != ""` block), add:

```go
	// Runtime collection overlay (design/2026-07-27-collection-control-plane.md)
	// — default off; the route set (and its RBAC in the chart) only exists
	// when an install opts in.
	if cfg.CollectionRuntimeControlEnabled {
		mux.Handle("GET /api/v1/collection/overlay", a.securedAdmin(a.handleGetCollectionOverlay))
		mux.Handle("PUT /api/v1/collection/overlay", a.securedAdmin(a.handlePutCollectionOverlay))
		mux.Handle("DELETE /api/v1/collection/overlay", a.securedAdmin(a.handleDeleteCollectionOverlay))
	}
```

- [ ] **Step 4: Implement the handlers**

```go
// hub/internal/api/collection.go
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type collectionOverlayResponse struct {
	Overlay   collection.Overlay `json:"overlay"`
	UpdatedAt string             `json:"updatedAt,omitempty"`
	UpdatedBy string             `json:"updatedBy,omitempty"`
}

func (a *API) handleGetCollectionOverlay(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	rec, err := store.LoadCollectionOverlay(r.Context())
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, collectionOverlayResponse{})
		return nil
	}
	if err != nil {
		return err
	}
	ov, err := collection.ParseOverlay(rec.Overlay)
	if err != nil {
		// A stored row that fails to parse is a server-side bug (it passed
		// validation on the way in), not a client error — surface as 500 via
		// the default error path.
		return fmt.Errorf("parse stored collection overlay: %w", err)
	}
	writeJSON(w, http.StatusOK, collectionOverlayResponse{
		Overlay: ov, UpdatedAt: rec.UpdatedAt.Format(time.RFC3339), UpdatedBy: rec.UpdatedBy,
	})
	return nil
}

func (a *API) handlePutCollectionOverlay(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	var ov collection.Overlay
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&ov); err != nil {
		return decodeJSONError(err)
	}
	// Re-encode + re-parse through ParseOverlay so PUT gets the exact same
	// closed-schema + namespace validation GET's round-trip relies on.
	encoded, err := ov.Encode()
	if err != nil {
		return badRequest("%s", err)
	}
	if _, err := collection.ParseOverlay(encoded); err != nil {
		return badRequest("%s", err)
	}

	updatedBy := requestedBy(r)
	if err := store.SaveCollectionOverlay(r.Context(), storage.CollectionOverlay{
		Overlay: encoded, UpdatedBy: updatedBy,
	}); err != nil {
		return err
	}
	if err := a.collectionApplier.Apply(r.Context(), ov); err != nil {
		return &apiError{status: http.StatusBadGateway, message: fmt.Sprintf("overlay saved but applying it failed: %s", err)}
	}
	writeJSON(w, http.StatusOK, collectionOverlayResponse{Overlay: ov, UpdatedBy: updatedBy})
	return nil
}

func (a *API) handleDeleteCollectionOverlay(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	updatedBy := requestedBy(r)
	empty := collection.Overlay{}
	encoded, err := empty.Encode()
	if err != nil {
		return fmt.Errorf("encode empty overlay: %w", err)
	}
	if err := store.SaveCollectionOverlay(r.Context(), storage.CollectionOverlay{
		Overlay: encoded, UpdatedBy: updatedBy,
	}); err != nil {
		return err
	}
	if err := a.collectionApplier.Apply(r.Context(), empty); err != nil {
		return &apiError{status: http.StatusBadGateway, message: fmt.Sprintf("overlay reset but applying it failed: %s", err)}
	}
	writeJSON(w, http.StatusOK, collectionOverlayResponse{})
	return nil
}

// requestedBy resolves the audit actor from the authenticated identity
// (set on the request context by a.securedAdmin), falling back to "unknown"
// when auth is disabled (a.cfg.Auth == nil, so securedAdmin never populates
// an identity).
func requestedBy(r *http.Request) string {
	id, _ := r.Context().Value(identityKey{}).(*auth.Identity)
	if id == nil || id.Email == "" {
		return "unknown"
	}
	return id.Email
}
```

Before finalizing this file, run `grep -n "type identityKey" hub/internal/api/auth_middleware.go` to confirm the exact context-key type name and that it's unexported-but-same-package-accessible from `collection.go` (it will be, since both files are `package api`) — it should match `identityKey{}` as used in `auth_middleware.go`'s `secured`/`securedAdmin`. If the real type differs, use the real name.

- [ ] **Step 5: Run the handler tests to confirm they pass**

Run: `cd hub && go test ./internal/api/... -run TestCollectionOverlay`
Expected: PASS (5 tests).

- [ ] **Step 6: Extend `capabilities.go`**

```go
// hub/internal/api/capabilities.go
package api

import "net/http"

// capabilitiesResponse is the client-agnostic module-discovery contract: the
// SPA builds its sidebar from it, and future clients (Grafana, CLI) use it to
// know which signal APIs exist on this install.
type capabilitiesResponse struct {
	Version                  string   `json:"version"`
	Modules                  []string `json:"modules"`
	CollectionRuntimeControl bool     `json:"collectionRuntimeControl"`
}

func (a *API) handleCapabilities(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		Version:                  Version,
		Modules:                  a.modules.Names(),
		CollectionRuntimeControl: a.cfg.CollectionRuntimeControlEnabled,
	})
	return nil
}
```

- [ ] **Step 7: Extend the capabilities test**

Open `hub/internal/api/capabilities_test.go`, read its existing test(s) for the exact setup pattern, and add one asserting `collectionRuntimeControl` reflects `Config.CollectionRuntimeControlEnabled` (both `true` and the default `false` case).

- [ ] **Step 8: Run the full API package test suite**

Run: `cd hub && go test ./internal/api/...`
Expected: PASS, no regressions in existing tests.

- [ ] **Step 9: Commit**

```bash
git add hub/internal/api/collection.go hub/internal/api/collection_test.go \
        hub/internal/api/router.go hub/internal/api/capabilities.go hub/internal/api/capabilities_test.go
git commit -m "feat(hub): GET/PUT/DELETE /api/v1/collection/overlay + capability flag"
```

---

### Task 5: `main.go` wiring (env → Config)

**Files:**
- Modify: `hub/cmd/hub/main.go`

- [ ] **Step 1: Read the file and locate the insertion points**

Run: `grep -n "envOr\|api.Config{\|DemoEnabled" hub/cmd/hub/main.go`

- [ ] **Step 2: Add the env parse next to the other `envOr` boolean reads (near where `demoEnabled` is computed)**

```go
	collectionRuntimeControlEnabled := envOr("AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED", "false") == "true"
```

- [ ] **Step 3: Add the two new fields to the `api.Config{...}` literal**

```go
		CollectionRuntimeControlEnabled: collectionRuntimeControlEnabled,
		CollectionApplier:               collection.NoopApplier{},
```

Add the import `"github.com/avuru/avuru-obs/hub/internal/collection"` to `main.go`'s import block.

- [ ] **Step 4: Build**

Run: `cd hub && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hub/cmd/hub/main.go
git commit -m "feat(hub): wire AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED into api.Config"
```

---

### Task 6: Chart — flag, base-values ConfigMap, RBAC

**Files:**
- Modify: `deploy/helm/avuruobs/values.yaml`
- Modify: `deploy/helm/avuruobs/values.schema.json`
- Create: `deploy/helm/avuruobs/templates/collection-rbac.yaml`
- Modify: `deploy/helm/avuruobs/templates/hub-deploy.yaml`

- [ ] **Step 1: Add the value**

In `deploy/helm/avuruobs/values.yaml`, after the `sensor:` block (or as its own top-level section — do NOT nest this under `sensor.collection`, which is a different, existing key), add:

```yaml
# Runtime collection control (design/2026-07-27-collection-control-plane.md):
# lets an admin toggle sensor signals + sensor.collection.excludeNamespaces
# from Settings -> Collection instead of only via `helm upgrade`. Default off
# — an install opts in deliberately (grants the hub a narrow, resourceName-
# scoped Role on its own sensor ConfigMaps + DaemonSet; see collection-rbac.
# yaml). v1 has no applier wired yet (a separate follow-up plan) — the API
# persists overlays but does not yet push them to the cluster.
collection:
  runtimeControl:
    enabled: false
```

- [ ] **Step 2: Add the schema**

In `deploy/helm/avuruobs/values.schema.json`, add a new top-level property (sibling to `"sensor"`, matching the existing `"modules"` leaf-object style):

```json
    "collection": {
      "type": "object",
      "properties": {
        "runtimeControl": {
          "type": "object",
          "properties": { "enabled": { "type": "boolean" } },
          "required": ["enabled"]
        }
      },
      "required": ["runtimeControl"]
    },
```

Place it among the other top-level properties (order doesn't matter functionally; put it near `"sensor"` for readability). If a top-level `"required"` array exists listing the other top-level keys, add `"collection"` to it for consistency — check first with `grep -n '"required"' deploy/helm/avuruobs/values.schema.json` and only add it there if an equivalent top-level entry pattern already exists for siblings like `"sensor"` or `"modules"`.

- [ ] **Step 3: Write the base-values ConfigMap + RBAC template**

```yaml
{{- /* Runtime collection control (design/2026-07-27-collection-control-plane.md).
       Nothing to control without a sensor. */}}
{{- if and .Values.collection.runtimeControl.enabled (not .Values.sensor.enabled) }}
{{- fail "collection.runtimeControl.enabled requires sensor.enabled — there is no sensor to control." }}
{{- end }}
{{- if .Values.collection.runtimeControl.enabled }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "avuruobs.fullname" . }}-collection-control
  labels:
    {{- include "avuruobs.labels" . | nindent 4 }}
    app.kubernetes.io/component: hub
---
# Curated, non-secret subset of .Values the applier (follow-up plan) needs to
# re-render sensor-config.yaml / sensor-daemonset.yaml server-side. Deliberately
# NOT `toJson .Values` wholesale: several values subtrees (clickhouse.auth.
# password, auth.adminPassword) hold plaintext secrets by default and must
# never land in a ConfigMap (Helm's own release storage is a Secret, not a
# ConfigMap — this must not regress that). Extend this list only with values
# the sensor templates actually read; never widen it to "everything".
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "avuruobs.fullname" . }}-collection-base-values
  labels:
    {{- include "avuruobs.labels" . | nindent 4 }}
    app.kubernetes.io/component: hub
data:
  values.json: |
    {{- $baseValues := dict
          "sensor" .Values.sensor
          "modules" .Values.modules
          "image" (dict "registry" .Values.image.registry "pullPolicy" .Values.image.pullPolicy)
          "auth" (dict "ingest" .Values.auth.ingest)
          "nameOverride" .Values.nameOverride
          "fullnameOverride" .Values.fullnameOverride
    }}
    {{- $baseValues | toJson | nindent 4 }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "avuruobs.fullname" . }}-collection-control
  labels:
    {{- include "avuruobs.labels" . | nindent 4 }}
    app.kubernetes.io/component: hub
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames:
      - {{ include "avuruobs.fullname" . }}-collection-base-values
      - {{ include "avuruobs.fullname" . }}-sensor-obi
      - {{ include "avuruobs.fullname" . }}-sensor-agent
      - {{ include "avuruobs.fullname" . }}-sensor-profiler
      - {{ include "avuruobs.fullname" . }}-sensor-kepler
    verbs: ["get", "update", "patch"]
  - apiGroups: ["apps"]
    resources: ["daemonsets"]
    resourceNames:
      - {{ include "avuruobs.fullname" . }}-sensor
    verbs: ["get", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "avuruobs.fullname" . }}-collection-control
  labels:
    {{- include "avuruobs.labels" . | nindent 4 }}
    app.kubernetes.io/component: hub
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ include "avuruobs.fullname" . }}-collection-control
subjects:
  - kind: ServiceAccount
    name: {{ include "avuruobs.fullname" . }}-collection-control
    namespace: {{ .Release.Namespace }}
{{- end }}
```

Save as `deploy/helm/avuruobs/templates/collection-rbac.yaml` (mirrors `sensor-rbac.yaml`'s single-file, multi-doc convention).

Note the 4 sensor ConfigMap names and the DaemonSet name (`{{ include "avuruobs.fullname" . }}-sensor`) are only ever *referenced by name* here, not required to exist — they may not render if e.g. `sensor.obi.enabled=false` at install; that's fine, the Role grant is simply inert for objects that don't exist yet until an overlay (applied by the follow-up plan's applier) creates the crossover need.

- [ ] **Step 4: Wire the ServiceAccount + env var into the hub Deployment**

In `deploy/helm/avuruobs/templates/hub-deploy.yaml`, add `serviceAccountName` right after `spec:` under `template:` (before the existing `{{- with .Values.image.pullSecrets }}` block):

```yaml
    spec:
      {{- if .Values.collection.runtimeControl.enabled }}
      serviceAccountName: {{ include "avuruobs.fullname" . }}-collection-control
      {{- end }}
      {{- with .Values.image.pullSecrets }}
```

Add the env var right after the existing `AVURUOBS_AUTH_ENABLED` line (same unconditional pattern — always present, `"true"`/`"false"` string):

```yaml
            - name: AVURUOBS_AUTH_ENABLED
              value: {{ .Values.auth.enabled | quote }}
            - name: AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED
              value: {{ .Values.collection.runtimeControl.enabled | quote }}
```

- [ ] **Step 5: Render the chart to confirm it's valid Helm/YAML**

Run: `cd deploy/helm && helm template test avuruobs --set collection.runtimeControl.enabled=true | head -100`
Expected: Renders without error; the `test-avuruobs-collection-control` ServiceAccount/ConfigMap/Role/RoleBinding appear.

Run: `cd deploy/helm && helm template test avuruobs`
Expected: Renders without error, and none of the `collection-control` objects appear (flag defaults to `false`).

Run: `cd deploy/helm && helm lint avuruobs`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/avuruobs/values.yaml deploy/helm/avuruobs/values.schema.json \
        deploy/helm/avuruobs/templates/collection-rbac.yaml deploy/helm/avuruobs/templates/hub-deploy.yaml
git commit -m "feat(chart): collection.runtimeControl.enabled flag + RBAC + base-values ConfigMap"
```

---

### Task 7: Chart-render tests

**Files:**
- Modify: `deploy/helm/template-test.sh`

- [ ] **Step 1: Read the existing script's structure**

Run: `cat deploy/helm/template-test.sh` — confirm the exact `render()`/`ok()`/`fail()` helper names and where sections are appended (append near the end, before whatever final summary/exit-0 line the script ends with).

- [ ] **Step 2: Add the new assertions**

```bash
echo "== collection runtime control off by default"
out="$(render)"
grep -q "collection-control" <<<"$out" && fail "collection-control objects rendered with collection.runtimeControl.enabled=false (the default)"
ok "no collection-control objects by default"

echo "== collection runtime control on"
out="$(render --set collection.runtimeControl.enabled=true)"
grep -q "name: test-avuruobs-collection-control" <<<"$out" || fail "collection-control ServiceAccount/Role/RoleBinding missing"
grep -q "test-avuruobs-collection-base-values" <<<"$out" || fail "collection-base-values ConfigMap missing"
grep -q "kind: Role" <<<"$out" || fail "namespaced Role missing"
grep -q "test-avuruobs-sensor-obi" <<<"$out" || fail "Role does not reference the sensor-obi ConfigMap by name"
grep -q "test-avuruobs-sensor$" <<<"$out" || fail "Role does not reference the sensor DaemonSet by name"
ok "collection-control objects render when enabled"

echo "== collection runtime control requires sensor enabled"
render --set collection.runtimeControl.enabled=true --set sensor.enabled=false >/dev/null 2>&1 \
  && fail "collection.runtimeControl.enabled rendered with sensor.enabled=false (should fail loud, per the guard in collection-rbac.yaml)"
ok "guard fires when sensor is off"
```

Adjust the exact `test-avuruobs-...` name prefix if the script's `render()` helper uses a release name other than `test` (check the `render()` function definition — the design assumes `helm template test avuruobs ...`, matching the existing script's own convention. `test-avuruobs` comes from `avuruobs.fullname`'s logic: release name `test` doesn't contain chart name `avuruobs`, so fullname = `<release>-<chart>` = `test-avuruobs`).

- [ ] **Step 3: Run the full chart test suite**

Run: `make helm-check` (from repo root)
Expected: PASS, including the 3 new sections.

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/template-test.sh
git commit -m "test(chart): assert collection.runtimeControl.enabled render + guard"
```

---

### Task 8: Final verification

- [ ] **Step 1: Full hub validation**

Run: `cd hub && go build ./... && go test -race ./... && golangci-lint run`
Expected: PASS. (`go test -race ./...` will skip the `integration`-tagged test from Task 1 unless `-tags=integration` is also passed — that's expected; it was already verified in Task 1 Step 8.)

- [ ] **Step 2: Chart validation**

Run: `make helm-check` (from repo root)
Expected: PASS.

- [ ] **Step 3: Full repo check**

Run: `make check` (from repo root)
Expected: PASS.

- [ ] **Step 4: Sweep for competitor-name leakage in anything shipped in this plan**

This plan doesn't touch `CHANGELOG.md`, `README.md`, `ROADMAP.md`, or `docs/` — skip the grep from `AGENTS.md`'s "No competitor names" section; it doesn't apply to this plan's file set.

- [ ] **Step 5: Review the full diff against `design/2026-07-27-collection-control-plane.md`'s v1 roadmap**

Confirm this plan covers: `collection_overlay` store + validated `GET/PUT/DELETE /collection/overlay` (yes — Tasks 1–5); `collection.runtimeControl.enabled` flag + chart ServiceAccount/Role/RoleBinding + `capabilities` field (yes — Tasks 4–6). Confirm it deliberately does NOT cover: the applier's actual cluster-apply logic, and the Settings → Collection writable UI — both explicitly deferred to follow-up plans per the Scope note at the top of this document.

No new commit for this task — it's a verification checkpoint only.
