# Project Management — CRUD + Demo (Phase 1 + Phase 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let admins create / rename / delete projects from the UI, and give
prospects a one-click read-only **demo** (viewer @ `demo`) backed by live data.

**Architecture:** A new `project` ClickHouse table (`ReplacingMergeTree` + `FINAL`
+ tombstones, the auth/`alert_channel` pattern) behind the `storage.Store` seam;
admin CRUD handlers reusing `securedAdmin`; the derived project list gains a `db`
source. The demo is a bootstrapped `viewer@demo` user reached through a
rate-limited, server-side `POST /api/v1/auth/demo` that reuses the existing login
machinery — the shared password never reaches the browser. Data comes from the
already-scaffolded OpenTelemetry Astronomy Shop tagged `gateway.tenant: demo`.

**Tech Stack:** Go 1.26, ClickHouse (clickhouse-go), `net/http` ServeMux,
Next.js 16 static SPA + TanStack Query, Playwright.

**Scope:** This plan covers spec Phases 1 + 4 (the shippable "projects + demo"
increment). Phase 2 (ingest API keys) has its own plan
(`2026-07-27-auth-ingest-keys.md`, migration renumbered to `0013`); Phase 3
(member projects) gets a separate plan. `Members` ships as an unused column here.

**Conventions:** handlers return `error` (mapped centrally by `handle()`); all
SQL behind `storage.Store`; fakes over mocks; `ReplacingMergeTree(UpdatedAt)` +
`FINAL` + tombstones; conventional commits, **no AI trailer**; `cd hub && go build
./... && go vet ./... && go test ./...` before each Go commit; `cd ui && npm run
typecheck && npm run lint` before each UI commit. Work stays on the current branch
`feature/project-management`.

---

## File structure

| File | Responsibility |
|---|---|
| `hub/internal/storage/migrations/0012_projects.sql` | `project` table DDL |
| `hub/internal/storage/migrations/migrations.go` | register `0012` in `Ordered` + `ByModule` |
| `hub/internal/storage/store.go` | `Project` type + 4 interface methods |
| `hub/internal/storage/clickhouse/projects.go` | ClickHouse impl of the 4 methods |
| `hub/internal/storage/storagetest/fake.go` | in-memory impl of the 4 methods |
| `hub/internal/api/projects.go` | merged list (db source) + create/rename/delete handlers |
| `hub/internal/api/router.go` | register the 3 admin routes + demo route |
| `hub/internal/auth/service.go` | `EnsureDemoUser` bootstrap |
| `hub/internal/api/auth_handlers.go` | `handleDemoLogin` + `demoEnabled` in config |
| `hub/cmd/hub/main.go` | wire demo env + call `EnsureDemoUser` |
| `ui/src/lib/api-types.ts` | extend `Project`; `AuthConfig.demoEnabled` |
| `ui/src/hooks/use-projects.ts` | create/rename/delete mutation hooks |
| `ui/src/components/settings/general-tab.tsx` | project CRUD UI |
| `ui/src/components/layout/project-switcher.tsx` | show `label || id` |
| `ui/app/login/page.tsx` | "Try the demo" CTA |
| `ui/e2e/projects.spec.ts`, `ui/e2e/auth.spec.ts` | e2e coverage |
| `deploy/…`, `CHANGELOG.md` | demo wiring + docs |

---

## PHASE 1 — PROJECT CRUD

### Task 1: Migration — `project` table

**Files:**
- Create: `hub/internal/storage/migrations/0012_projects.sql`
- Modify: `hub/internal/storage/migrations/migrations.go`
- Test: `hub/internal/storage/migrations/migrations_test.go` (existing `TestByModuleCoversOrdered`)

- [ ] **Step 1: Write the migration**

`hub/internal/storage/migrations/0012_projects.sql`:

```sql
-- UI-managed projects. Id is the immutable tenant slug (= the partition key on
-- all telemetry); Label is the editable display name; Members is the
-- multi-cluster aggregate set (empty for a normal project, unused until the
-- member-projects phase). Delete is a tombstone. Same ReplacingMergeTree +
-- FINAL + tombstone pattern as auth_* / alert_channel; the table is tiny so
-- FINAL is cheap. Not module-toggleable — projects are core.
CREATE TABLE IF NOT EXISTS otel.project
(
    `Id`        String,
    `Label`     String,
    `Members`   Array(String) DEFAULT [],
    `Deleted`   UInt8 DEFAULT 0,
    `CreatedBy` String,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);
```

- [ ] **Step 2: Register it in the ordered list + module map**

In `migrations.go`, append to `Ordered` (after `"0011_auth_oidc_groups.sql"`):

```go
	"0011_auth_oidc_groups.sql",
	"0012_projects.sql",
}
```

And add to `ByModule` (after the `0011` entry):

```go
	"0011_auth_oidc_groups.sql": {modules.Core},
	// UI-managed projects — core (projects underpin every tenant view).
	"0012_projects.sql": {modules.Core},
}
```

- [ ] **Step 3: Run the coverage test to verify it passes**

Run: `cd hub && go test ./internal/storage/migrations/ -run TestByModuleCoversOrdered -v`
Expected: PASS (every `Ordered` entry, including `0012`, is tagged).

- [ ] **Step 4: (integration, if ClickHouse available) apply the migration**

Run: `cd hub && go test -tags integration ./internal/storage/clickhouse/ -run TestMigrate -v`
Expected: PASS — `0001`–`0012` apply cleanly. (Skips without a ClickHouse; the
coverage test above is the CI-less gate.)

- [ ] **Step 5: Commit**

```bash
git add hub/internal/storage/migrations/0012_projects.sql hub/internal/storage/migrations/migrations.go
git commit -m "feat(hub): migration — project table"
```

---

### Task 2: Storage — `Project` type + store methods

**Files:**
- Modify: `hub/internal/storage/store.go`
- Create: `hub/internal/storage/clickhouse/projects.go`
- Modify: `hub/internal/storage/storagetest/fake.go`
- Test: `hub/internal/storage/storagetest/fake_projects_test.go`

- [ ] **Step 1: Add the type + interface methods**

In `store.go`, near the `AuthSession` type, add:

```go
// Project is one UI-managed project. ID is immutable (the tenant slug used on
// every telemetry row and in X-Avuru-Tenant); Label is display-only; Members is
// the multi-cluster aggregate set (empty for a leaf project, unused until the
// member-projects phase). Delete is a tombstone (never a hard row delete).
type Project struct {
	ID        string
	Label     string
	Members   []string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

In the `Store` interface, after the auth block (`RevokeAuthSessionsForUser`), add:

```go
	// UI-managed projects (core). ListProjects returns live (non-tombstoned)
	// rows ordered by Id. GetProject returns ErrNotFound for an absent or
	// deleted id. SaveProject upserts by Id. DeleteProject tombstones by Id and
	// returns ErrNotFound when no live row matches.
	ListProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, id string) (Project, error)
	SaveProject(ctx context.Context, p Project) error
	DeleteProject(ctx context.Context, id string) error
```

- [ ] **Step 2: Write the failing fake test**

`hub/internal/storage/storagetest/fake_projects_test.go`:

```go
package storagetest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestFakeProjectLifecycle(t *testing.T) {
	f := &storagetest.Fake{}
	ctx := context.Background()

	if err := f.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.GetProject(ctx, "staging")
	if err != nil || got.Label != "Staging" {
		t.Fatalf("get: %+v err=%v", got, err)
	}

	// Upsert by ID replaces the label.
	if err := f.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging EU"}); err != nil {
		t.Fatal(err)
	}
	list, err := f.ListProjects(ctx)
	if err != nil || len(list) != 1 || list[0].Label != "Staging EU" {
		t.Fatalf("list: %+v err=%v", list, err)
	}

	// Delete tombstones — get and list no longer see it.
	if err := f.DeleteProject(ctx, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetProject(ctx, "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted project still resolves: %v", err)
	}
	if list, _ := f.ListProjects(ctx); len(list) != 0 {
		t.Fatalf("deleted project still listed: %+v", list)
	}
	// Deleting a missing project is ErrNotFound.
	if err := f.DeleteProject(ctx, "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd hub && go test ./internal/storage/storagetest/ -run TestFakeProject -v`
Expected: FAIL (methods undefined).

- [ ] **Step 4: Implement the fake**

In `fake.go`, add a field to the `Fake` struct (in the Auth fakes block):

```go
	// Projects keyed by ID (UI-managed projects).
	Projects map[string]storage.Project
```

And append the methods (near the auth fake methods):

```go
func (f *Fake) ListProjects(context.Context) ([]storage.Project, error) {
	out := make([]storage.Project, 0, len(f.Projects))
	for _, p := range f.Projects {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *Fake) GetProject(_ context.Context, id string) (storage.Project, error) {
	p, ok := f.Projects[id]
	if !ok {
		return storage.Project{}, storage.ErrNotFound
	}
	return p, nil
}

func (f *Fake) SaveProject(_ context.Context, p storage.Project) error {
	if f.Projects == nil {
		f.Projects = make(map[string]storage.Project)
	}
	f.Projects[p.ID] = p
	return nil
}

func (f *Fake) DeleteProject(_ context.Context, id string) error {
	if _, ok := f.Projects[id]; !ok {
		return storage.ErrNotFound
	}
	delete(f.Projects, id)
	return nil
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd hub && go test ./internal/storage/storagetest/ -run TestFakeProject -v`
Expected: PASS.

- [ ] **Step 6: Implement the ClickHouse backend**

`hub/internal/storage/clickhouse/projects.go` (mirrors `clickhouse/auth.go`):

```go
package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListProjects returns live (non-tombstoned) UI-managed projects, ordered by Id.
func (s *Store) ListProjects(ctx context.Context) ([]storage.Project, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Id, Label, Members, CreatedBy, CreatedAt, UpdatedAt
FROM project FINAL
WHERE Deleted = 0
ORDER BY Id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []storage.Project
	for rows.Next() {
		var p storage.Project
		if err := rows.Scan(&p.ID, &p.Label, &p.Members, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns one live project by Id, or ErrNotFound.
func (s *Store) GetProject(ctx context.Context, id string) (storage.Project, error) {
	var p storage.Project
	err := s.conn.QueryRow(ctx, `
SELECT Id, Label, Members, CreatedBy, CreatedAt, UpdatedAt
FROM project FINAL
WHERE Id = ? AND Deleted = 0`, id).
		Scan(&p.ID, &p.Label, &p.Members, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.Project{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// SaveProject upserts by Id (new row; ReplacingMergeTree keeps the newest by
// UpdatedAt). Members defaults to an empty array when nil.
func (s *Store) SaveProject(ctx context.Context, p storage.Project) error {
	members := p.Members
	if members == nil {
		members = []string{}
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, CreatedBy, Deleted)
VALUES (?, ?, ?, ?, 0)`, p.ID, p.Label, members, p.CreatedBy)
	if err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	return nil
}

// DeleteProject tombstones by Id (Deleted=1, newer UpdatedAt). Returns
// ErrNotFound when no live row matches.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	if _, err := s.GetProject(ctx, id); err != nil {
		return err // ErrNotFound propagates
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, Deleted)
VALUES (?, '', [], 1)`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}
```

- [ ] **Step 7: Build + vet**

Run: `cd hub && go build ./... && go vet ./...`
Expected: PASS — `*Store` and `*Fake` both satisfy `storage.Store` (the
`var _ storage.Store = (*Fake)(nil)` assertion compiles).

- [ ] **Step 8: (integration, if ClickHouse available) backend lifecycle test**

`hub/internal/storage/clickhouse/projects_integration_test.go`:

```go
//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestProjectLifecycle(t *testing.T) {
	st := newTestStore(t) // existing harness in integration_test.go
	ctx := context.Background()

	if err := st.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging", CreatedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetProject(ctx, "staging")
	if err != nil || got.Label != "Staging" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	if err := st.DeleteProject(ctx, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetProject(ctx, "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted project resolves: %v", err)
	}
}
```

Run: `cd hub && go test -tags integration ./internal/storage/clickhouse/ -run TestProjectLifecycle -v`
Expected: PASS (skips without ClickHouse).

- [ ] **Step 9: Commit**

```bash
git add hub/internal/storage/store.go hub/internal/storage/clickhouse/projects.go hub/internal/storage/clickhouse/projects_integration_test.go hub/internal/storage/storagetest/fake.go hub/internal/storage/storagetest/fake_projects_test.go
git commit -m "feat(hub): storage — project store (list/get/save/delete)"
```

---

### Task 3: API — merge `db` source into the project list

**Files:**
- Modify: `hub/internal/api/projects.go`
- Test: `hub/internal/api/projects_test.go`

- [ ] **Step 1: Write the failing test**

Add to `hub/internal/api/projects_test.go` (create the file if absent; use the
existing handler-test harness — a `*API` over a `storagetest.Fake`, matching
`users_test.go`):

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestHandleProjectsMergesDBSource(t *testing.T) {
	f := &storagetest.Fake{}
	_ = f.SaveProject(context.Background(), storage.Project{ID: "staging", Label: "Staging EU"})
	a := &API{provider: func() storage.Store { return f }, cfg: Config{Projects: []string{"prod"}}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	if err := a.handleProjects(rec, req); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Projects []projectDTO `json:"projects"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	byID := map[string]projectDTO{}
	for _, p := range resp.Projects {
		byID[p.ID] = p
	}
	if d := byID["default"]; d.Source != "default" || d.Editable {
		t.Errorf("default = %+v, want source=default editable=false", d)
	}
	if p := byID["prod"]; p.Source != "config" || p.Editable {
		t.Errorf("prod = %+v, want source=config editable=false", p)
	}
	if s := byID["staging"]; s.Source != "db" || !s.Editable || s.Label != "Staging EU" {
		t.Errorf("staging = %+v, want source=db editable=true label=Staging EU", s)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run TestHandleProjectsMergesDBSource -v`
Expected: FAIL (`projectDTO` has no `Editable`/`Label`; db not merged).

- [ ] **Step 3: Extend `projectDTO` and the merge**

In `projects.go`, replace the `projectDTO` type and rewrite `handleProjects`:

```go
// projectDTO is one selectable project. Source records provenance: "default"
// (always present), "config" (AVURUOPS_PROJECTS), "db" (UI-managed), "data"
// (observed in telemetry), or "granted" (an RBAC scope with no other entry yet).
// Editable is true only for db projects — default/config are deployment-owned
// and read-only; data/granted are not real project records. Label and Members
// are carried for db projects.
type projectDTO struct {
	ID       string   `json:"id"`
	Label    string   `json:"label,omitempty"`
	Source   string   `json:"source"`
	Editable bool     `json:"editable"`
	Members  []string `json:"members,omitempty"`
}
```

Rewrite `handleProjects` to build a map keyed by id with fixed precedence
(default > config > db > data > granted), tolerating a store outage:

```go
// handleProjects returns {default} ∪ config ∪ db ∪ observed-in-data, filtered to
// the caller's scopes. Answers 200 with default+config even when ClickHouse is
// down — the switcher must always render.
func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) error {
	byID := map[string]projectDTO{
		storage.DefaultTenant: {ID: storage.DefaultTenant, Source: "default"},
	}
	for _, p := range a.cfg.Projects {
		if p != "" && p != storage.DefaultTenant {
			if _, ok := byID[p]; !ok {
				byID[p] = projectDTO{ID: p, Source: "config"}
			}
		}
	}
	for _, p := range a.dbProjects(r) {
		if _, ok := byID[p.ID]; !ok {
			byID[p.ID] = projectDTO{
				ID: p.ID, Label: p.Label, Source: "db", Editable: true, Members: p.Members,
			}
		}
	}
	for _, t := range a.observedTenants(r) {
		if _, ok := byID[t]; !ok {
			byID[t] = projectDTO{ID: t, Source: "data"}
		}
	}

	resp := projectsResponse{Projects: make([]projectDTO, 0, len(byID))}
	for _, p := range byID {
		resp.Projects = append(resp.Projects, p)
	}
	sort.Slice(resp.Projects, func(i, j int) bool { return resp.Projects[i].ID < resp.Projects[j].ID })
	resp.Projects = filterProjectsForIdentity(resp.Projects, identityFrom(r.Context()))
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// dbProjects returns UI-managed projects, or nil when the store is unreachable
// (degrade like observedTenants — the list must always render).
func (a *API) dbProjects(r *http.Request) []storage.Project {
	s := a.provider()
	if s == nil {
		return nil
	}
	ps, err := s.ListProjects(r.Context())
	if err != nil {
		return nil
	}
	return ps
}
```

`filterProjectsForIdentity`'s appended "granted" entries already set no
`Editable` (zero value false) — no change needed there.

- [ ] **Step 4: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run TestHandleProjectsMergesDBSource -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/api/projects.go hub/internal/api/projects_test.go
git commit -m "feat(hub): merge UI-managed (db) projects into the project list"
```

---

### Task 4: API — create / rename / delete handlers + routes

**Files:**
- Modify: `hub/internal/api/projects.go`
- Modify: `hub/internal/api/router.go`
- Test: `hub/internal/api/projects_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `projects_test.go`:

```go
func TestCreateProject(t *testing.T) {
	f := &storagetest.Fake{}
	a := &API{provider: func() storage.Store { return f }, cfg: Config{Projects: []string{"prod"}}}

	// Happy path.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"staging","label":"Staging"}`))
	if err := a.handleCreateProject(rec, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, err := f.GetProject(context.Background(), "staging"); err != nil {
		t.Fatalf("not persisted: %v", err)
	}

	// Reserved id "default" → 400.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"default"}`))); !isStatus(err, 400) {
		t.Fatalf("default id: %v", err)
	}
	// Shadowing a config id → 409.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"prod"}`))); !isStatus(err, 409) {
		t.Fatalf("config shadow: %v", err)
	}
	// Invalid slug → 400.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"Has Space"}`))); !isStatus(err, 400) {
		t.Fatalf("bad slug: %v", err)
	}
	// Duplicate db id → 409.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"staging"}`))); !isStatus(err, 409) {
		t.Fatalf("dup: %v", err)
	}
}

func TestRenameAndDeleteProject(t *testing.T) {
	f := &storagetest.Fake{}
	_ = f.SaveProject(context.Background(), storage.Project{ID: "staging", Label: "old"})
	a := &API{provider: func() storage.Store { return f }, cfg: Config{Projects: []string{"prod"}}}

	// Rename label.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/staging", strings.NewReader(`{"label":"new"}`))
	req.SetPathValue("id", "staging")
	if err := a.handleRenameProject(rec, req); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got, _ := f.GetProject(context.Background(), "staging"); got.Label != "new" {
		t.Fatalf("label = %q", got.Label)
	}
	// Renaming a config project → 409 (read-only).
	rc := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/prod", strings.NewReader(`{"label":"x"}`))
	rc.SetPathValue("id", "prod")
	if err := a.handleRenameProject(httptest.NewRecorder(), rc); !isStatus(err, 409) {
		t.Fatalf("rename config: %v", err)
	}
	// Delete.
	rd := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/staging", nil)
	rd.SetPathValue("id", "staging")
	if err := a.handleDeleteProject(httptest.NewRecorder(), rd); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.GetProject(context.Background(), "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("still present: %v", err)
	}
	// Deleting the default project → 409.
	rdd := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/default", nil)
	rdd.SetPathValue("id", "default")
	if err := a.handleDeleteProject(httptest.NewRecorder(), rdd); !isStatus(err, 409) {
		t.Fatalf("delete default: %v", err)
	}
}

// isStatus reports whether err is an *apiError with the given status.
func isStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == status
}
```

Add imports to the test file: `errors`, `strings`.

- [ ] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run 'TestCreateProject|TestRenameAndDeleteProject' -v`
Expected: FAIL (handlers undefined).

- [ ] **Step 3: Implement the handlers**

Append to `projects.go` (add imports `encoding/json`, `net/http`, `regexp`,
`slices`):

```go
// projectIDRe bounds a project id: a lowercase slug, so it is a safe tenant
// value and a clean URL/path segment. Mirrors the DNS-label spirit already used
// for k8s-shaped names elsewhere.
var projectIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type createProjectRequest struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type renameProjectRequest struct {
	Label string `json:"label"`
}

// reservedProject reports whether id is deployment-owned (the built-in default
// or a config-declared project) and therefore not editable/deletable via the UI.
func (a *API) reservedProject(id string) bool {
	return id == storage.DefaultTenant || slices.Contains(a.cfg.Projects, id)
}

// handleCreateProject creates a UI-managed project. A reserved id is 400
// (default) or 409 (config-shadow); a duplicate db id is 409; a bad slug is 400.
func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	var req createProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	if req.ID == storage.DefaultTenant {
		return badRequest("%q is reserved", storage.DefaultTenant)
	}
	if !projectIDRe.MatchString(req.ID) {
		return badRequest("invalid project id %q (lowercase letters, digits, hyphens; must start with a letter)", req.ID)
	}
	if slices.Contains(a.cfg.Projects, req.ID) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q is defined through config and cannot be created here", req.ID)}
	}
	if _, err := st.GetProject(r.Context(), req.ID); err == nil {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q already exists", req.ID)}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	p := storage.Project{ID: req.ID, Label: req.Label, CreatedBy: creatorOf(r)}
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, projectDTO{ID: p.ID, Label: p.Label, Source: "db", Editable: true})
	return nil
}

// handleRenameProject edits a db project's label. Reserved (default/config)
// projects are read-only → 409; an unknown id is 404.
func (a *API) handleRenameProject(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if a.reservedProject(id) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q is deployment-owned and cannot be modified here", id)}
	}
	var req renameProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	p, err := st.GetProject(r.Context(), id)
	if err != nil {
		return err // ErrNotFound -> 404
	}
	p.Label = req.Label
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, projectDTO{ID: p.ID, Label: p.Label, Source: "db", Editable: true, Members: p.Members})
	return nil
}

// handleDeleteProject tombstones a db project. Reserved projects → 409; unknown
// → 404. Telemetry is untouched (ages out by TTL; a still-active tenant
// re-appears as source=data — intended).
func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if a.reservedProject(id) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q is deployment-owned and cannot be deleted here", id)}
	}
	if err := st.DeleteProject(r.Context(), id); err != nil {
		return err // ErrNotFound -> 404
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// creatorOf returns the acting admin's id for the audit column, or "" when auth
// is disabled.
func creatorOf(r *http.Request) string {
	if id := identityFrom(r.Context()); id != nil {
		return id.UserID
	}
	return ""
}
```

- [ ] **Step 4: Register the routes**

In `router.go`, inside the existing core block (right after the
`GET /api/v1/projects` line), add the admin mutations:

```go
	mux.Handle("GET /api/v1/projects", a.secured(auth.RoleViewer, a.handleProjects))
	mux.Handle("POST /api/v1/projects", a.securedAdmin(a.handleCreateProject))
	mux.Handle("PATCH /api/v1/projects/{id}", a.securedAdmin(a.handleRenameProject))
	mux.Handle("DELETE /api/v1/projects/{id}", a.securedAdmin(a.handleDeleteProject))
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run 'TestCreateProject|TestRenameAndDeleteProject|TestHandleProjects' -v && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/api/projects.go hub/internal/api/projects_test.go hub/internal/api/router.go
git commit -m "feat(hub): admin project CRUD endpoints (create/rename/delete)"
```

---

### Task 5: UI — types + mutation hooks

**Files:**
- Modify: `ui/src/lib/api-types.ts`
- Modify: `ui/src/hooks/use-projects.ts`

- [ ] **Step 1: Extend the `Project` type**

In `api-types.ts`, replace the `Project` interface:

```ts
export interface Project {
  id: string;
  label?: string;
  source: "default" | "config" | "db" | "data" | "granted";
  editable?: boolean;
  members?: string[];
}
```

- [ ] **Step 2: Add mutation hooks**

In `use-projects.ts`, add (keeping the existing `useProjects` query):

```ts
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiPost, apiPatch, apiDelete } from "@/lib/api";
import type { Project } from "@/lib/api-types";

function useInvalidateProjects() {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: queryKeys.projects });
}

export function useCreateProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: (input: { id: string; label: string }) =>
      apiPost<Project>("/api/v1/projects", input),
    onSuccess: invalidate,
  });
}

export function useRenameProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    // The hub rename route is PATCH; apiPatch is added to the api client in
    // Step 3 of this task.
    mutationFn: ({ id, label }: { id: string; label: string }) =>
      apiPatch<Project>(`/api/v1/projects/${encodeURIComponent(id)}`, { label }),
    onSuccess: invalidate,
  });
}

export function useDeleteProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: (id: string) => apiDelete(`/api/v1/projects/${encodeURIComponent(id)}`),
    onSuccess: invalidate,
  });
}
```

- [ ] **Step 3: Add `apiPatch` to the api client**

The hub uses PATCH for rename; `api.ts` has no PATCH helper. Add one mirroring
`apiPut` in `ui/src/lib/api.ts` (copy the `apiPut` body, change `method: "PUT"`
to `method: "PATCH"` and the name to `apiPatch`):

```ts
// apiPatch sends a JSON body via PATCH (partial updates, e.g. project rename).
export async function apiPatch<T>(
  path: string,
  body: unknown,
  opts?: { project?: string },
): Promise<T> {
  const url = `${apiBase()}${path}`;
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (opts?.project && opts.project !== "default") {
    headers["X-Avuru-Tenant"] = opts.project;
  }
  const res = await fetch(url, { method: "PATCH", headers, body: JSON.stringify(body) });
  return handleResponse<T>(res);
}
```

(Match the exact helper names used by `apiPut` in that file — `apiBase()` and the
shared response handler. Read `apiPut` first and copy its structure verbatim.)
Import `apiPatch` in `use-projects.ts`.

- [ ] **Step 4: Typecheck + lint**

Run: `cd ui && npm run typecheck && npm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/api-types.ts ui/src/lib/api.ts ui/src/hooks/use-projects.ts
git commit -m "feat(ui): project types + create/rename/delete mutation hooks"
```

---

### Task 6: UI — General tab CRUD + switcher label

**Files:**
- Modify: `ui/src/components/settings/general-tab.tsx`
- Modify: `ui/src/components/layout/project-switcher.tsx`

- [ ] **Step 1: Switcher shows the label**

In `project-switcher.tsx`, the active-project line renders `{project}` (the id).
Resolve a label from the loaded list and prefer it:

```tsx
  const active = projects.find((p) => p.id === project);
  const activeLabel = active?.label || project;
```

Replace the two `{project}` display spots (the footer button and the `title`)
with `{activeLabel}`, and in the option row render `{p.label || p.id}` instead of
`{p.id}` (selection still keys on `p.id`).

- [ ] **Step 2: General tab — editable label, create, delete**

Rewrite `general-tab.tsx`'s Project card so a `db` project's label is editable
and add a create form + danger-zone delete. Keep the existing read-only banner for
non-editable projects and the Retention card unchanged. Full replacement of the
`Project` card block:

```tsx
"use client";

import { useState } from "react";
import { Info, Plus, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { ApiError } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import {
  useProjects,
  useCreateProject,
  useRenameProject,
  useDeleteProject,
} from "@/hooks/use-projects";
import { useAuth } from "@/hooks/use-auth";
import { useSystemStatus } from "@/hooks/use-system-status";

export function GeneralTab() {
  const { project, setProject } = useProject();
  const { data: projects } = useProjects();
  const { isAdmin } = useAuth();
  const { data: status } = useSystemStatus();

  const active = projects?.projects.find((p) => p.id === project);
  const editable = !!active?.editable;
  const sourceLabel =
    active?.source === "data"
      ? "discovered from data"
      : active?.source === "config"
        ? "config-defined"
        : active?.source === "db"
          ? "project"
          : "built-in";

  const rename = useRenameProject();
  const remove = useDeleteProject();
  const [labelDraft, setLabelDraft] = useState(active?.label ?? "");
  const [creating, setCreating] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <div className="flex flex-col gap-4">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Project</CardTitle>
          <div className="flex items-center gap-2">
            {status && <span className="text-xs text-base-content/50">hub {status.version}</span>}
            {isAdmin && !creating && (
              <Button variant="secondary" size="sm" onClick={() => { setCreating(true); setErr(null); }}>
                <Plus className="h-3.5 w-3.5" /> New project
              </Button>
            )}
          </div>
        </CardHeader>
        <div className="flex flex-col gap-3 border-t border-neutral p-4">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm font-semibold">{active?.label || project}</span>
            <Badge tone="neutral">{sourceLabel}</Badge>
          </div>

          {editable && isAdmin ? (
            <div className="flex flex-col gap-2">
              <label className="flex flex-col gap-1 text-xs text-base-content/60">
                Display name
                <div className="flex items-center gap-2">
                  <input
                    className={inputClass}
                    value={labelDraft}
                    onChange={(e) => setLabelDraft(e.target.value)}
                    placeholder={project}
                  />
                  <Button
                    variant="primary"
                    size="sm"
                    disabled={rename.isPending}
                    onClick={async () => {
                      setErr(null);
                      try {
                        await rename.mutateAsync({ id: project, label: labelDraft });
                      } catch (e) {
                        setErr(e instanceof ApiError ? e.message : "request failed");
                      }
                    }}
                  >
                    Save
                  </Button>
                </div>
              </label>
            </div>
          ) : (
            <p className="flex items-start gap-2 rounded-lg border border-info/40 bg-info/10 p-3 text-xs text-base-content/80">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-info" aria-hidden />
              This project is defined through deployment configuration and cannot
              be modified here. Declare projects with the chart&apos;s{" "}
              <code className="font-mono">projects</code> value, or create one from
              this page.
            </p>
          )}

          {err && <p className="text-xs text-error">{err}</p>}
        </div>

        {editable && isAdmin && (
          <div className="flex items-center justify-between gap-2 border-t border-neutral bg-error/5 px-4 py-3">
            <span className="text-xs text-base-content/60">
              Delete this project. Telemetry ages out by retention; a still-active
              tenant re-appears automatically.
            </span>
            <Button
              variant="ghost"
              size="sm"
              disabled={remove.isPending}
              onClick={async () => {
                setErr(null);
                try {
                  await remove.mutateAsync(project);
                  setProject("default");
                } catch (e) {
                  setErr(e instanceof ApiError ? e.message : "request failed");
                }
              }}
            >
              <Trash2 className="h-3.5 w-3.5" /> Delete
            </Button>
          </div>
        )}

        {creating && (
          <CreateProjectForm
            onDone={(newId) => {
              setCreating(false);
              if (newId) setProject(newId);
            }}
          />
        )}
      </Card>

      {status && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Retention</CardTitle>
            <span className="text-xs text-base-content/50">per-signal TTL, instance-wide</span>
          </CardHeader>
          <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4">
            {status.signals.map((s) => (
              <div key={s.signal} className="bg-base-200 p-3">
                <p className="text-xs uppercase tracking-wider text-base-content/50">{s.signal}</p>
                <p className="text-sm font-semibold">{s.retentionDays} days</p>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}

// CreateProjectForm collects an id (immutable slug) + label and creates the
// project, then selects it.
function CreateProjectForm({ onDone }: { onDone: (newId?: string) => void }) {
  const create = useCreateProject();
  const [id, setId] = useState("");
  const [label, setLabel] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <form
      className="flex flex-col gap-2 border-t border-neutral px-4 py-3"
      onSubmit={async (e) => {
        e.preventDefault();
        setErr(null);
        try {
          await create.mutateAsync({ id, label });
          onDone(id);
        } catch (e2) {
          setErr(e2 instanceof ApiError ? e2.message : "request failed");
        }
      }}
    >
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Project id (immutable)
          <input
            className={inputClass}
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="staging"
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Display name
          <input
            className={inputClass}
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="Staging (EU)"
          />
        </label>
      </div>
      {err && <p className="text-xs text-error">{err}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={create.isPending}>
          Create
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => onDone()}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
```

- [ ] **Step 2b: Build**

Run: `cd ui && npm run typecheck && npm run lint && npm run build`
Expected: static export succeeds.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/settings/general-tab.tsx ui/src/components/layout/project-switcher.tsx
git commit -m "feat(ui): project create/rename/delete in Settings; switcher shows label"
```

---

### Task 7: e2e — project CRUD (auth-enabled stack)

**Files:**
- Modify: `ui/e2e/projects.spec.ts`

- [ ] **Step 1: Add an admin CRUD test**

The CRUD surface is admin-only, so this runs on the auth-enabled `make e2e-ui`
stack. Reuse the sign-in pattern from `auth.spec.ts` (admin `admin` /
`e2e-admin-pw`). Append to `projects.spec.ts`:

```ts
test.describe("project management (admin)", () => {
  const ADMIN = { email: "admin", password: "e2e-admin-pw" };

  async function signInAdmin(page) {
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN.email);
    await page.getByLabel("Password").fill(ADMIN.password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).not.toHaveURL(/\/login/);
  }

  test("create, rename, then delete a project", async ({ page }) => {
    await signInAdmin(page);
    await page.goto("/settings");

    await page.getByRole("button", { name: "New project" }).click();
    await page.getByLabel("Project id (immutable)").fill("e2e-proj");
    await page.getByLabel("Display name").fill("E2E Project");
    await page.getByRole("button", { name: "Create", exact: true }).click();

    // Switcher now shows the new project's label as active.
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("E2E Project");

    // Rename the label.
    await page.getByLabel("Display name").fill("E2E Renamed");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("E2E Renamed");

    // Delete → falls back to default.
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("default");
  });

  test("the default project shows the read-only banner", async ({ page }) => {
    await signInAdmin(page);
    await page.goto("/settings");
    await expect(
      page.getByText(/defined through deployment configuration/),
    ).toBeVisible();
  });
});
```

- [ ] **Step 2: Run against the auth-enabled stack (isolated project)**

Follow the isolated-run recipe (`-p avuru-obs-e2e`, `ports: !override`):

Run: `make e2e-ui` (or the project's e2e target) filtering to `projects.spec.ts`.
Expected: PASS — create/rename/delete round-trips; banner shows for default.

- [ ] **Step 3: Commit**

```bash
git add ui/e2e/projects.spec.ts
git commit -m "test(e2e): admin project create/rename/delete"
```

---

## PHASE 4 — DEMO PROJECT

### Task 8: Auth — demo viewer bootstrap

**Files:**
- Modify: `hub/internal/auth/service.go`
- Test: `hub/internal/auth/service_test.go`

- [ ] **Step 1: Write the failing test**

Add to `service_test.go` (reuse the existing fake-store test harness in that
file — a `*Service` over a `storagetest.Fake`):

```go
func TestEnsureDemoUser(t *testing.T) {
	f := &storagetest.Fake{}
	svc := NewService(func() storage.Store { return f }, time.Hour)
	ctx := context.Background()

	if err := svc.EnsureDemoUser(ctx, "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	// The demo user exists, viewer-scoped to "demo" only.
	u, err := f.GetAuthUserByEmail(ctx, "demo@avuru.obs")
	if err != nil {
		t.Fatalf("demo user missing: %v", err)
	}
	grants, _ := f.ListAuthGrants(ctx, u.ID)
	if len(grants) != 1 || grants[0].Scope != "demo" || grants[0].Role != string(RoleViewer) {
		t.Fatalf("grants = %+v, want one viewer@demo", grants)
	}
	// It logs in with the configured password.
	if _, _, err := svc.Login(ctx, "demo@avuru.obs", "demo-pw", "1.2.3.4"); err != nil {
		t.Fatalf("demo login: %v", err)
	}
	// Idempotent: a second call doesn't duplicate or error.
	if err := svc.EnsureDemoUser(ctx, "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd hub && go test ./internal/auth/ -run TestEnsureDemoUser -v`
Expected: FAIL (`EnsureDemoUser` undefined).

- [ ] **Step 3: Implement**

In `service.go`, add near `Bootstrap`:

```go
// demoViewerID is the FIXED id for the demo viewer (same rationale as
// bootstrapAdminID: a random id would let two replicas create divergent rows).
const demoViewerID = "demo-viewer"

// EnsureDemoUser idempotently creates/refreshes the read-only demo user
// (viewer @ "demo") from the configured credentials. Called at startup only
// when demo mode is enabled. Upsert-by-fixed-id keeps it safe under replicas and
// re-runnable on every boot (the chart owns the password).
func (s *Service) EnsureDemoUser(ctx context.Context, email, password string) error {
	st, err := s.st()
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	// Grant first (harmless orphan if we crash before the user write).
	if err := st.ReplaceAuthGrants(ctx, demoViewerID, []storage.AuthGrant{
		{UserID: demoViewerID, Scope: "demo", Role: string(RoleViewer)},
	}); err != nil {
		return fmt.Errorf("granting demo viewer: %w", err)
	}
	u := storage.AuthUser{ID: demoViewerID, Email: email, Name: "Demo (read-only)",
		PasswordHash: hash, Origin: "local"}
	if err := st.SaveAuthUser(ctx, u); err != nil {
		return fmt.Errorf("creating demo user: %w", err)
	}
	slog.Info("demo mode: ensured demo viewer", "email", email)
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd hub && go test ./internal/auth/ -run TestEnsureDemoUser -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/auth/service.go hub/internal/auth/service_test.go
git commit -m "feat(hub): demo viewer bootstrap (EnsureDemoUser)"
```

---

### Task 9: API — `/auth/demo` login + `demoEnabled` config

**Files:**
- Modify: `hub/internal/api/router.go` (Config field + route)
- Modify: `hub/internal/api/auth_handlers.go`
- Test: `hub/internal/api/auth_handlers_test.go`

- [ ] **Step 1: Add Config fields**

In `router.go`, add to the `Config` struct (near `AnonymousIdentity`):

```go
	// Demo mode: when DemoEnabled, POST /api/v1/auth/demo signs in as the
	// read-only demo viewer using DemoEmail/DemoPassword server-side (the shared
	// password never reaches the browser), and /auth/config advertises it.
	DemoEnabled  bool
	DemoEmail    string
	DemoPassword string
```

- [ ] **Step 2: Write the failing test**

Add to `auth_handlers_test.go` (reuse the existing helper that builds a `*API`
with a real `*auth.Service` over a `storagetest.Fake` — see the login tests in
that file; seed the demo user via `svc.EnsureDemoUser`):

```go
func TestDemoLogin(t *testing.T) {
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if err := svc.EnsureDemoUser(context.Background(), "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	a := &API{provider: func() storage.Store { return f }, cfg: Config{
		Auth: svc, DemoEnabled: true, DemoEmail: "demo@avuru.obs", DemoPassword: "demo-pw",
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/demo", nil)
	if err := a.handleDemoLogin(rec, req); err != nil {
		t.Fatalf("demo login: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// A session cookie was set and the identity is the read-only demo viewer.
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("no session cookie set")
	}
	var me meResponse
	json.NewDecoder(rec.Body).Decode(&me)
	if len(me.Grants) != 1 || me.Grants[0].Scope != "demo" {
		t.Fatalf("grants = %+v, want viewer@demo", me.Grants)
	}
}

func TestAuthConfigAdvertisesDemo(t *testing.T) {
	a := &API{cfg: Config{Auth: &auth.Service{}, DemoEnabled: true}}
	rec := httptest.NewRecorder()
	_ = a.handleAuthConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil))
	if !strings.Contains(rec.Body.String(), `"demoEnabled":true`) {
		t.Fatalf("config missing demoEnabled: %s", rec.Body)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd hub && go test ./internal/api/ -run 'TestDemoLogin|TestAuthConfigAdvertisesDemo' -v`
Expected: FAIL (handler + field undefined).

- [ ] **Step 4: Implement**

In `auth_handlers.go`, add `DemoEnabled` to the config response and the handler:

```go
type authConfigResponse struct {
	Enabled     bool     `json:"enabled"`
	Methods     []string `json:"methods"`
	ForceSSO    bool     `json:"forceSSO"`
	DemoEnabled bool     `json:"demoEnabled"`
}
```

Set it in `handleAuthConfig` (before `writeJSON`):

```go
	resp.DemoEnabled = a.cfg.DemoEnabled
```

Add the handler (mirrors `handleLogin`, but the credentials come from server
config, so the browser sends no body):

```go
// handleDemoLogin signs the caller in as the read-only demo viewer using the
// server-held demo credentials — the shared password never reaches the browser.
// Registered only when DemoEnabled. Reuses the login rate limiter (keyed on the
// demo email + client IP).
func (a *API) handleDemoLogin(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	if err := checkOrigin(r); err != nil {
		return err
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	token, id, err := a.cfg.Auth.Login(r.Context(), a.cfg.DemoEmail, a.cfg.DemoPassword, ip)
	switch {
	case errors.Is(err, auth.ErrStoreUnavailable):
		return errStoreUnavailable
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		return &apiError{status: http.StatusTooManyRequests, message: "too many attempts, retry in a minute"}
	case err != nil:
		// Any other failure (e.g. the demo user isn't bootstrapped yet) is a
		// server-side misconfiguration, not a client error.
		return err
	}
	setSessionCookie(w, r, token, int(a.cfg.Auth.SessionTTL()/time.Second))
	writeJSON(w, http.StatusOK, meFrom(id))
	return nil
}
```

- [ ] **Step 5: Register the route**

In `router.go`, inside the `if cfg.Auth != nil {` block, add:

```go
		if cfg.DemoEnabled {
			mux.Handle("POST /api/v1/auth/demo", handle(a.handleDemoLogin))
		}
```

- [ ] **Step 6: Run to verify it passes**

Run: `cd hub && go test ./internal/api/ -run 'TestDemoLogin|TestAuthConfigAdvertisesDemo' -v && go vet ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add hub/internal/api/router.go hub/internal/api/auth_handlers.go hub/internal/api/auth_handlers_test.go
git commit -m "feat(hub): /auth/demo one-click viewer login + demoEnabled config"
```

---

### Task 10: main.go — wire demo env

**Files:**
- Modify: `hub/cmd/hub/main.go`

- [ ] **Step 1: Resolve demo config once, wire it into bootstrap + Config**

In `run()`, after the `authSvc, anonID := authService(provider)` block, resolve
the demo settings so the SAME generated password feeds both the bootstrap and the
handler:

```go
	authSvc, anonID := authService(provider)
	demoEnabled := envOr("AVURUOPS_DEMO_ENABLED", "false") == "true" && authSvc != nil
	demoEmail := envOr("AVURUOPS_DEMO_EMAIL", "demo@avuru.obs")
	demoPassword := os.Getenv("AVURUOPS_DEMO_PASSWORD")
	if demoEnabled {
		if demoPassword == "" {
			demoPassword = auth.NewID() // held in-process; never disclosed
		}
		go ensureDemoUser(ctx, authSvc, provider, demoEmail, demoPassword)
	}
	if authSvc != nil {
		go bootstrapAdmin(ctx, authSvc, provider)
	}
```

(Note: the existing `if authSvc != nil { go bootstrapAdmin(...) }` moves below the
demo block; keep a single copy.)

Add the demo fields to the `api.Config{…}` literal:

```go
		Auth:                  authSvc,
		AnonymousIdentity:     anonID,
		DemoEnabled:           demoEnabled,
		DemoEmail:             demoEmail,
		DemoPassword:          demoPassword,
```

- [ ] **Step 2: Add the `ensureDemoUser` background helper**

Near `bootstrapAdmin`, add (same wait-for-store + retry shape, simpler):

```go
// ensureDemoUser waits for the store, then idempotently ensures the demo viewer
// exists. Retries every 5s until it succeeds (same degraded-not-crashed
// philosophy as bootstrapAdmin); the table may not be migrated yet on first boot.
func ensureDemoUser(ctx context.Context, svc *auth.Service, provider api.StoreProvider, email, password string) {
	for provider() == nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	for {
		if err := svc.EnsureDemoUser(ctx, email, password); err == nil {
			return
		} else {
			slog.Warn("demo user bootstrap failed, retrying", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
```

- [ ] **Step 3: Build + vet + test**

Run: `cd hub && go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add hub/cmd/hub/main.go
git commit -m "feat(hub): wire demo mode env (enable, email, generated password)"
```

---

### Task 11: UI — "Try the demo" login CTA

**Files:**
- Modify: `ui/src/lib/api-types.ts` (`AuthConfig.demoEnabled`)
- Modify: `ui/app/login/page.tsx`

- [ ] **Step 1: Extend `AuthConfig`**

Find the `AuthConfig` interface in `api-types.ts` and add the field:

```ts
export interface AuthConfig {
  enabled: boolean;
  methods: ("local" | "oidc")[];
  forceSSO: boolean;
  demoEnabled?: boolean;
}
```

(Match the existing `AuthConfig` shape in that file — add only `demoEnabled`.)

- [ ] **Step 2: Add the CTA and its handler**

In `login/page.tsx`, add a demo-login action and a button. After the existing
`submit` function, add:

```tsx
  const tryDemo = async () => {
    setError(null);
    setBusy(true);
    try {
      await apiPost<Me>("/api/v1/auth/demo", {});
      window.location.assign(safeNext());
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setError("The demo is busy — wait a minute and retry");
      } else {
        setError("Couldn’t start the demo — is the hub reachable?");
      }
      setBusy(false);
    }
  };
```

Render the button below the sign-in form, shown only when the hub advertises the
demo. Just before the closing `</div>` of the form column (after the SSO block),
add:

```tsx
              {config?.demoEnabled && (
                <>
                  <div className="flex items-center gap-3 text-[10px] font-medium uppercase tracking-wide text-base-content/40">
                    <span className="h-px flex-1 bg-neutral" />
                    or
                    <span className="h-px flex-1 bg-neutral" />
                  </div>
                  <Button
                    type="button"
                    variant="secondary"
                    className="w-full"
                    onClick={() => void tryDemo()}
                    disabled={busy}
                  >
                    Try the demo (read-only)
                  </Button>
                </>
              )}
```

- [ ] **Step 3: Typecheck + lint + build**

Run: `cd ui && npm run typecheck && npm run lint && npm run build`
Expected: PASS — static export succeeds.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/api-types.ts ui/app/login/page.tsx
git commit -m "feat(ui): 'Try the demo' one-click read-only login"
```

---

### Task 12: e2e — demo login

**Files:**
- Modify: `ui/e2e/auth.spec.ts`

- [ ] **Step 1: Add a demo-mode test (opt-in gate)**

The default `make e2e-ui` stack doesn't enable demo mode, so gate this like the
OIDC tests. Append to `auth.spec.ts`:

```ts
test.describe("auth: demo", () => {
  test.skip(!process.env.DEMO_E2E, "DEMO_E2E not set — demo mode not enabled in the stack");

  test("'Try the demo' signs in as a read-only viewer scoped to demo", async ({ page }) => {
    await page.goto("/login");
    await page.getByRole("button", { name: /Try the demo/ }).click();
    // Lands in the app with a session (Sign out present), on the demo project.
    await expect(page).not.toHaveURL(/\/login/);
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("demo");
    // Read-only: the admin-only Users tab is absent in Settings.
    await page.goto("/settings");
    await expect(page.getByRole("tab", { name: "Users" })).toHaveCount(0);
  });
});
```

- [ ] **Step 2: Run with demo mode enabled**

Bring up the stack with `AVURUOPS_DEMO_ENABLED=true`,
`AVURUOPS_DEMO_PASSWORD=demo-e2e-pw`, a `demo` project seeded, and
`DEMO_E2E=1`; run `auth.spec.ts`.
Expected: PASS — demo login lands a viewer session scoped to `demo` with no Users tab.

- [ ] **Step 3: Commit**

```bash
git add ui/e2e/auth.spec.ts
git commit -m "test(e2e): 'Try the demo' read-only viewer login (opt-in)"
```

---

### Task 13: Deploy + docs — Astronomy Shop demo tenant + changelog

**Files:**
- Modify: `deploy/demo/astronomy/values-avuru.yaml`, `deploy/demo/astronomy/README.md`
- Modify: `deploy/helm/avuruops/values.yaml`, `deploy/helm/README.md`
- Modify: `CHANGELOG.md`, `ROADMAP.md`

- [ ] **Step 1: Tag the astronomy demo telemetry with `tenant: demo`**

In `deploy/demo/astronomy/values-avuru.yaml`, set the gateway tenant so all demo
telemetry lands under the `demo` tenant, and declare it a project:

```yaml
gateway:
  tenant: demo
projects:
  - default
  - demo
```

Update `deploy/demo/astronomy/README.md` to note the demo runs under the `demo`
project and is reached via "Try the demo".

- [ ] **Step 2: Add a `demo` chart preset**

In `deploy/helm/avuruops/values.yaml`, add a `demo` block (documented, default
off):

```yaml
# Demo mode: a read-only viewer ("Try the demo" on the login page) scoped to the
# `demo` project. Opt-in — a normal install ships no demo user or tenant. Pair
# with the Astronomy Shop overlay (deploy/demo/astronomy) for live data.
demo:
  enabled: false
  email: "demo@avuru.obs"
  # password: auto-generated when empty (chart Secret), never disclosed.
```

Templates (in the hub Deployment env): when `demo.enabled`, set
`AVURUOPS_DEMO_ENABLED=true`, `AVURUOPS_DEMO_EMAIL`, and
`AVURUOPS_DEMO_PASSWORD` from a generated Secret (mirror the admin-password
secret pattern). Document the three env vars in `deploy/helm/README.md`.

- [ ] **Step 3: (if a helm template test exists) verify render**

Run: `cd deploy/helm && ./template-test.sh`
Expected: the demo env appears only when `demo.enabled=true`; the password is a
Secret, never a ConfigMap.

- [ ] **Step 4: Changelog + roadmap**

Add under `CHANGELOG.md` `## [Unreleased] / ### Added`: UI project management
(create/rename/delete) and the read-only demo ("Try the demo" + Astronomy Shop).
Tick the roadmap items for project CRUD and the demo.

- [ ] **Step 5: docs-align (EN + FR)**

Run the `docs-align` skill: changelog, feature-status matrix, API reference for
the project CRUD + `/auth/demo` endpoints, and a "managing projects" + "the demo"
guide.

- [ ] **Step 6: Commit**

```bash
git add deploy/ CHANGELOG.md ROADMAP.md
git commit -m "feat(deploy): demo mode preset + Astronomy Shop demo tenant; docs"
```

---

## Final verification (Phase 1 + Phase 4)

- [ ] `cd hub && go build ./... && go vet ./... && go test -race ./...` — green.
- [ ] `cd hub && go test ./internal/storage/migrations/ -run TestByModuleCoversOrdered` — `0012` tagged.
- [ ] (integration, if ClickHouse available) `go test -tags integration ./internal/storage/clickhouse/ -run 'TestMigrate|TestProjectLifecycle'` — green.
- [ ] `cd ui && npm run typecheck && npm run lint && npm run build` — static export succeeds.
- [ ] `make e2e-ui` (isolated `-p avuru-obs-e2e`) — project CRUD spec green; auth specs still green (Users tab in-place fix intact).
- [ ] Demo path exercised with `DEMO_E2E=1` + `AVURUOPS_DEMO_ENABLED=true` — "Try the demo" → viewer session scoped to `demo`, no Users tab.
- [ ] `docs-align` run (EN + FR) committed.

## Notes carried into execution

- **Store outage:** `handleProjects` must still answer 200 (default+config) when
  `ListProjects` fails — `dbProjects` returns nil on error (Task 3).
- **Demo password:** generated once in `run()` and shared by value between
  `ensureDemoUser` and `api.Config` — it never leaves the process (Task 10).
- **Members column** is written empty and unused here; the member-projects phase
  (separate plan) fills it and adds `WHERE Tenant IN (…)` fan-out.
- **e2e stacks:** project CRUD needs the auth-enabled `make e2e-ui` stack (admin
  sign-in); the demo spec is opt-in behind `DEMO_E2E` like the OIDC specs.
