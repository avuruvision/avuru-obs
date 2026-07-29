# Projects Completion — Phase 1 (Project CRUD) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give avuru-obs projects a persistent identity — admins create, rename, and delete projects from the UI; the built-in `default` and config-defined projects stay read-only.

**Architecture:** A new `otel.project` ClickHouse table (`ReplacingMergeTree(UpdatedAt)` + `FINAL` + tombstone `Deleted`, mirroring `auth_*`/`alert_channel`), fronted by four `storage.Store` methods with a ClickHouse impl and a `storagetest.Fake`. The existing read-only `GET /api/v1/projects` gains a `db` source and `label`/`editable`/`members` fields; three new `securedAdmin` handlers (create/rename/delete) do the writes. UI adds project-mutation hooks and an admin-gated project card on Settings → General, following the existing inline-form-in-Card CRUD convention.

**Tech Stack:** Go 1.26 (stdlib `net/http.ServeMux`, `clickhouse-go`), ClickHouse; Next.js 16 static export, React 19, TanStack Query v5, Tailwind/daisyUI; Playwright e2e.

**Source spec:** `docs/superpowers/specs/2026-07-28-project-management-design.md` (Phase 1 = §5–§7). This plan implements only Phase 1; Phases 2 (ingest keys) and 3 (member aggregates) are separate.

---

## Pre-execution setup

- [ ] **Isolated worktree.** Phase 1 is multi-hour and there are concurrent sessions on this repo. Before any edit, create an isolated worktree + compose project via the `superpowers:using-git-worktrees` skill (branch `feature/projects-phase1-crud` off `main`), and export a unique `COMPOSE_PROJECT_NAME` (e.g. `avuru-obs-projects`). Do NOT reuse the shared compose stack (see memory `isolated-e2e-compose-run`).
- [ ] Confirm base: `git fetch origin && git log --oneline -1 origin/main` → tip is `1795544` (or later). Branch from `main`, not from `feature/project-management`.

## Decisions resolved (spec §12 open questions)

1. **Create-project UX → inline-form-in-Card on the General tab**, NOT a portal dialog. Rationale: the repo has no `Dialog`/`Modal`/`Form`/`Input`/`Table` primitive; every admin CRUD screen (`users-panel.tsx`, `channels-panel.tsx`) uses an inline form inside a `Card`. `channels-panel.tsx` + `channel-form.tsx` (add-vs-edit, read-only rows keyed on a `source`/`initial` flag, react-query `useMutation`) is the structural template. Introducing a dialog would be a new convention — rejected per "follow established patterns".
2. **Update verb → `PUT /api/v1/projects/{id}`, NOT `PATCH`** (a deliberate deviation from the spec table). Every existing update endpoint uses PUT (`PUT /api/v1/users/{id}`, `PUT /api/v1/alerts/channels/{name}`) and `ui/src/lib/api.ts` has an `apiPut` helper but **no** `apiPatch`. Uniform verbs beat the draft spec's PATCH; semantics are identical (label-only update).
3. **Members column ships in Phase 1 but is inert** (empty `[]`, no member-editing UI) so Phase 3 needs no migration.

---

## File structure

**Backend (hub):**
- Create `hub/internal/storage/migrations/0012_projects.sql` — the DDL.
- Modify `hub/internal/storage/migrations/migrations.go` — register in `Ordered` + `ByModule`.
- Modify `hub/internal/storage/store.go` — add `Project` struct + 4 interface methods.
- Create `hub/internal/storage/clickhouse/project.go` — ClickHouse CRUD.
- Create `hub/internal/storage/clickhouse/project_integration_test.go` — real-ClickHouse roundtrip (`//go:build integration`).
- Modify `hub/internal/storage/storagetest/fake.go` — fake impl of the 4 methods.
- Modify `hub/internal/api/projects.go` — richer `projectDTO`, `db`-source merge, 3 handlers, helpers.
- Modify `hub/internal/api/router.go` — register POST/PUT/DELETE under `securedAdmin`.
- Create `hub/internal/api/projects_test.go` — handler tests (fake store).

**Frontend (ui):**
- Modify `ui/src/lib/api-types.ts` — extend `Project`, add request types.
- Modify `ui/src/hooks/use-projects.ts` — add create/rename/delete mutations.
- Create `ui/src/components/settings/project-settings-card.tsx` — the admin project card.
- Modify `ui/src/components/settings/general-tab.tsx` — mount the new card in place of the read-only banner.
- Modify `ui/src/components/layout/project-switcher.tsx` — render `label || id`.
- Modify `ui/e2e/projects.spec.ts` + `ui/e2e/settings.spec.ts` — e2e.

**Docs:** `design/README.md`, `CHANGELOG.md`, `ROADMAP.md`, plus the `docs-align` skill for the bilingual docs site.

---

## Task 1: DB migration `0012_projects.sql` + registry

**Files:**
- Create: `hub/internal/storage/migrations/0012_projects.sql`
- Modify: `hub/internal/storage/migrations/migrations.go:21-33` (`Ordered`), `:41-59` (`ByModule`)
- Test: `hub/internal/storage/migrations/migrations_test.go` (existing `TestByModuleCoversOrdered`)

- [ ] **Step 1: Create the migration file**

```sql
-- 0012_projects.sql  (module: Core)
-- UI-managed projects. Id is the immutable tenant slug (= partition key on all
-- telemetry); Label is the editable display name; Members is the multi-cluster
-- aggregate set (empty until Phase 3). Delete is a tombstone. Same
-- ReplacingMergeTree + FINAL + tombstone pattern as auth_grant / alert_channel.
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

- [ ] **Step 2: Register in `migrations.go`** — append to `Ordered` (after `"0011_auth_oidc_groups.sql"`):

```go
	"0011_auth_oidc_groups.sql",
	"0012_projects.sql",
}
```

and add to `ByModule` (after the `0011` entry):

```go
	"0011_auth_oidc_groups.sql": {modules.Core},
	// UI-managed projects (create/rename/delete). Auth-adjacent core state.
	"0012_projects.sql": {modules.Core},
}
```

- [ ] **Step 3: Run the registry test** — it fails if the new file is untagged or the `ByModule` key is absent from `Ordered`.

Run: `cd hub && go test ./internal/storage/migrations/...`
Expected: PASS (`TestByModuleCoversOrdered`, `TestOrderedMatchesFiles` or equivalent).

- [ ] **Step 4: Commit**

```bash
git add hub/internal/storage/migrations/0012_projects.sql hub/internal/storage/migrations/migrations.go
git commit -m "feat(hub): add otel.project migration (0012)"
```

---

## Task 2: `storage.Project` DTO + `Store` interface + fake

**Files:**
- Modify: `hub/internal/storage/store.go` (add struct near the Auth DTOs ~line 456; add methods to the `Store` interface ~line 534, in the Auth block)
- Modify: `hub/internal/storage/storagetest/fake.go` (fields ~line 68; methods near the Auth fakes)
- Test: `hub/internal/storage/storagetest/fake_projects_test.go` (create)

- [ ] **Step 1: Write the failing fake test**

`hub/internal/storage/storagetest/fake_projects_test.go`:

```go
package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestFakeProjectRoundtrip(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()

	if err := f.SaveProject(ctx, storage.Project{ID: "team-a", Label: "Team A", Members: []string{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := f.GetProject(ctx, "team-a")
	if err != nil || got.Label != "Team A" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	list, err := f.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	if err := f.DeleteProject(ctx, "team-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.GetProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
	if err := f.DeleteProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound on second delete, got %v", err)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails to compile** (methods don't exist yet)

Run: `cd hub && go test ./internal/storage/storagetest/...`
Expected: FAIL — `f.SaveProject undefined` (build error).

- [ ] **Step 3: Add the `Project` struct + interface methods to `store.go`**

Struct (place near `AuthUser`, ~line 456):

```go
// Project is one UI-managed project. ID is immutable (the tenant slug used in
// data and the X-Avuru-Tenant header); Label is display-only; Members is the
// aggregate set (empty for a leaf project — populated in Phase 3).
type Project struct {
	ID        string
	Label     string
	Members   []string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

Interface methods (add inside the `Store` interface, in the Auth/core block):

```go
	// UI-managed projects (Phase 1). Reads are live-only (tombstones filtered).
	ListProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, id string) (Project, error) // ErrNotFound if absent/deleted
	SaveProject(ctx context.Context, p Project) error           // upsert by ID
	DeleteProject(ctx context.Context, id string) error         // tombstone; ErrNotFound if none live
```

(`time` is already imported in `store.go`.)

- [ ] **Step 4: Add the fake fields + methods to `fake.go`**

Fields (add after the Auth fakes block, ~line 68):

```go
	// Project fakes. Projects keyed by ID; only live projects are ever stored
	// (DeleteProject removes the entry, mirroring the tombstone-then-FINAL read).
	Projects        map[string]storage.Project
	SavedProjects   []storage.Project
	DeletedProjects []string
```

Methods (add near the Auth fakes):

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

// SaveProject mirrors the ReplacingMergeTree upsert-by-ID.
func (f *Fake) SaveProject(_ context.Context, p storage.Project) error {
	if f.Projects == nil {
		f.Projects = make(map[string]storage.Project)
	}
	f.Projects[p.ID] = p
	f.SavedProjects = append(f.SavedProjects, p)
	return nil
}

// DeleteProject mirrors the tombstone from the caller's point of view: only
// live projects are ever observable, so a deleted id simply disappears.
func (f *Fake) DeleteProject(_ context.Context, id string) error {
	f.DeletedProjects = append(f.DeletedProjects, id)
	if _, ok := f.Projects[id]; !ok {
		return storage.ErrNotFound
	}
	delete(f.Projects, id)
	return nil
}
```

- [ ] **Step 5: Run the fake test — now passing, and `var _ storage.Store = (*Fake)(nil)` compiles**

Run: `cd hub && go test ./internal/storage/storagetest/... && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/storage/store.go hub/internal/storage/storagetest/fake.go hub/internal/storage/storagetest/fake_projects_test.go
git commit -m "feat(hub): add Project storage DTO, Store methods, and fake"
```

---

## Task 3: ClickHouse `clickhouse/project.go` + integration test

**Files:**
- Create: `hub/internal/storage/clickhouse/project.go`
- Create: `hub/internal/storage/clickhouse/project_integration_test.go`

- [ ] **Step 1: Write the failing integration test**

`hub/internal/storage/clickhouse/project_integration_test.go`:

```go
//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestProjectRoundtrip(t *testing.T) {
	store := startClickHouse(t) // migrates otel.project via the real migrator
	ctx := context.Background()

	t.Run("save and get", func(t *testing.T) {
		if err := store.SaveProject(ctx, storage.Project{ID: "team-a", Label: "Team A", Members: []string{}, CreatedBy: "u1"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := store.GetProject(ctx, "team-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Label != "Team A" || got.CreatedBy != "u1" {
			t.Fatalf("unexpected: %+v", got)
		}
	})

	t.Run("rename keeps newest via FINAL", func(t *testing.T) {
		p, _ := store.GetProject(ctx, "team-a")
		p.Label = "Renamed"
		if err := store.SaveProject(ctx, p); err != nil {
			t.Fatalf("resave: %v", err)
		}
		got, _ := store.GetProject(ctx, "team-a")
		if got.Label != "Renamed" {
			t.Fatalf("want Renamed, got %q", got.Label)
		}
	})

	t.Run("list live only", func(t *testing.T) {
		list, err := store.ListProjects(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %+v err=%v", list, err)
		}
	})

	t.Run("delete tombstones", func(t *testing.T) {
		if err := store.DeleteProject(ctx, "team-a"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := store.GetProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		if err := store.DeleteProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("want ErrNotFound on missing delete, got %v", err)
		}
	})
}
```

- [ ] **Step 2: Run it to verify it fails** (methods undefined)

Run: `cd hub && go test -tags integration ./internal/storage/clickhouse/ -run TestProjectRoundtrip`
Expected: FAIL — `store.SaveProject undefined`.

- [ ] **Step 3: Implement `clickhouse/project.go`** (mirrors `auth.go` + `alerts.go`)

```go
package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListProjects returns the live (non-tombstoned) UI-managed projects, by id.
// FINAL collapses the ReplacingMergeTree to the newest row per Id; the table is
// bounded by project count so FINAL is cheap (same reasoning as alert_channel).
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

// GetProject returns one live project by id, or ErrNotFound (absent or deleted).
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

// SaveProject upserts a project by Id (new row; ReplacingMergeTree keeps the
// newest by UpdatedAt). CreatedBy/CreatedAt are written explicitly so a rename
// (re-save) preserves them; UpdatedAt defaults to now64(3). Saving revives a
// tombstoned id (Deleted=0).
func (s *Store) SaveProject(ctx context.Context, p storage.Project) error {
	members := p.Members
	if members == nil {
		members = []string{}
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, Deleted, CreatedBy, CreatedAt)
VALUES (?, ?, ?, 0, ?, ?)`, p.ID, p.Label, members, p.CreatedBy, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	return nil
}

// DeleteProject tombstones a project (Deleted=1) so FINAL supersedes the live
// row. ErrNotFound when no live project has the id.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	var n uint64
	if err := s.conn.QueryRow(ctx, `
SELECT count() FROM project FINAL WHERE Id = ? AND Deleted = 0`, id).Scan(&n); err != nil {
		return fmt.Errorf("check project: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, Deleted) VALUES (?, '', [], 1)`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the integration test to verify it passes**

Run: `cd hub && go test -tags integration ./internal/storage/clickhouse/ -run TestProjectRoundtrip`
Expected: PASS (spins a `clickhouse/clickhouse-server:26.3` testcontainer, runs the real migrator, exercises FINAL/tombstone semantics). Requires Docker.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/storage/clickhouse/project.go hub/internal/storage/clickhouse/project_integration_test.go
git commit -m "feat(hub): ClickHouse project CRUD (FINAL + tombstone delete)"
```

---

## Task 4: API — richer `projectDTO` + `db`-source merge

**Files:**
- Modify: `hub/internal/api/projects.go` (`projectDTO` :18-21, `handleProjects` :40-61)
- Test: `hub/internal/api/projects_test.go` (create)

- [ ] **Step 1: Write the failing merge test**

`hub/internal/api/projects_test.go` (uses the `adminMux`/`doBody` helpers from `users_test.go`/`auth_middleware_test.go`):

```go
package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestProjectsMergeIncludesDBProject(t *testing.T) {
	mux, cookie, f := adminMux(t)
	f.Projects = map[string]storage.Project{
		"team-a": {ID: "team-a", Label: "Team A", Members: []string{}},
	}

	w := doBody(mux, http.MethodGet, "/api/v1/projects", cookie, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Projects []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Source   string `json:"source"`
			Editable bool   `json:"editable"`
		} `json:"projects"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	var found bool
	for _, p := range resp.Projects {
		if p.ID == "team-a" {
			found = true
			if p.Source != "db" || !p.Editable || p.Label != "Team A" {
				t.Fatalf("team-a = %+v, want source=db editable=true label=Team A", p)
			}
		}
	}
	if !found {
		t.Fatalf("team-a missing from %+v", resp.Projects)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hub && go test ./internal/api/ -run TestProjectsMergeIncludesDBProject`
Expected: FAIL — response has no `label`/`editable`/source is not `db`.

- [ ] **Step 3: Extend `projectDTO`** (projects.go:18-21):

```go
type projectDTO struct {
	ID       string   `json:"id"`
	Label    string   `json:"label,omitempty"`
	Source   string   `json:"source"`
	Editable bool     `json:"editable"`
	Members  []string `json:"members,omitempty"`
}
```

- [ ] **Step 4: Rewrite `handleProjects`** to merge `db` at the right precedence (`default > config > db > data > granted`) and degrade to 200 when the store is down:

```go
// handleProjects returns {default} ∪ config ∪ db ∪ observed-in-data, filtered
// to the caller's scopes. db projects (source "db") carry a label and are
// editable; default/config are read-only. Answers 200 with default+config even
// when ClickHouse is down — the switcher must always render.
func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) error {
	dtos := map[string]*projectDTO{}
	var order []string
	add := func(id, source string) *projectDTO {
		if d, ok := dtos[id]; ok {
			return d
		}
		d := &projectDTO{ID: id, Source: source}
		dtos[id] = d
		order = append(order, id)
		return d
	}

	add(storage.DefaultTenant, "default")
	for _, p := range a.cfg.Projects {
		if p != "" && p != storage.DefaultTenant {
			add(p, "config")
		}
	}
	// db rows: new id -> source "db" (editable); a db row for an existing
	// default/config id only adds a label and stays read-only (config wins).
	for _, p := range a.dbProjects(r) {
		d := add(p.ID, "db")
		d.Label = p.Label
		d.Members = p.Members
		if d.Source == "db" {
			d.Editable = true
		}
	}
	for _, t := range a.observedTenants(r) {
		add(t, "data")
	}

	resp := projectsResponse{Projects: make([]projectDTO, 0, len(order))}
	for _, id := range order {
		resp.Projects = append(resp.Projects, *dtos[id])
	}
	sort.Slice(resp.Projects, func(i, j int) bool { return resp.Projects[i].ID < resp.Projects[j].ID })
	resp.Projects = filterProjectsForIdentity(resp.Projects, identityFrom(r.Context()))
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// dbProjects returns UI-managed projects, or nil on any store error — the
// handler still answers 200 with default+config so the switcher renders.
func (a *API) dbProjects(r *http.Request) []storage.Project {
	st, err := a.store()
	if err != nil {
		return nil
	}
	ps, err := st.ListProjects(r.Context())
	if err != nil {
		return nil
	}
	return ps
}
```

(`filterProjectsForIdentity` already copies `projectDTO` by value, so `label`/`editable`/`members` carry through; the appended `granted` DTO is non-editable by zero value. No change needed there.)

- [ ] **Step 5: Run the merge test — passing**

Run: `cd hub && go test ./internal/api/ -run TestProjectsMergeIncludesDBProject`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/api/projects.go hub/internal/api/projects_test.go
git commit -m "feat(hub): merge db-managed projects into GET /projects with label/editable"
```

---

## Task 5: Create handler + route + tests

**Files:**
- Modify: `hub/internal/api/projects.go` (add request type, handler, helpers)
- Modify: `hub/internal/api/router.go` (register route ~after line 88)
- Modify: `hub/internal/api/projects_test.go` (add tests)

- [ ] **Step 1: Write the failing create tests**

Append to `projects_test.go`:

```go
func decodeProject(t *testing.T, w *httptest.ResponseRecorder) projectDTO {
	t.Helper()
	var p projectDTO
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p
}

func TestCreateProject(t *testing.T) {
	mux, cookie, f := adminMux(t)

	w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie,
		map[string]string{"id": "team-a", "label": "Team A"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body)
	}
	p := decodeProject(t, w)
	if p.Source != "db" || !p.Editable || p.Label != "Team A" {
		t.Fatalf("dto = %+v", p)
	}
	if got := f.Projects["team-a"]; got.Label != "Team A" {
		t.Fatalf("fake missing project: %+v", f.Projects)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	mux, cookie, _ := adminMux(t)
	cases := []struct {
		name, id string
		want     int
	}{
		{"bad chars", "Team_A", http.StatusBadRequest},
		{"leading digit", "1team", http.StatusBadRequest},
		{"reserved default", "default", http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie,
				map[string]string{"id": tc.id, "label": "x"})
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestCreateProjectDuplicate(t *testing.T) {
	mux, cookie, _ := adminMux(t)
	doBody(mux, http.MethodPost, "/api/v1/projects", cookie, map[string]string{"id": "team-a", "label": "A"})
	w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, map[string]string{"id": "team-a", "label": "A2"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestCreateProjectConfigConflict(t *testing.T) {
	mux, cookie := adminMuxCfg(t, []string{"prod"})
	w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, map[string]string{"id": "prod", "label": "Prod"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}
```

Add the config-aware mux helper (top of `projects_test.go`), mirroring `adminMux` from `users_test.go` but with `Config.Projects` set:

```go
func adminMuxCfg(t *testing.T, projects []string) (*http.ServeMux, *http.Cookie) {
	t.Helper()
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	svc.Bootstrap(context.Background(), "root-pw")
	token, _, _ := svc.Login(context.Background(), "admin", "root-pw", "test")
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc, Projects: projects})
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}
}
```

(Imports for `projects_test.go`: add `context`, `net/http/httptest`, `time`, `github.com/avuru/avuru-obs/hub/internal/auth`, `github.com/avuru/avuru-obs/hub/internal/storage`, `github.com/avuru/avuru-obs/hub/internal/storage/storagetest`.)

- [ ] **Step 2: Run to verify failure**

Run: `cd hub && go test ./internal/api/ -run TestCreateProject`
Expected: FAIL — route unregistered (404) / handler missing.

- [ ] **Step 3: Add the request type, validation, handler, and helpers to `projects.go`**

At the top (after imports add `context`, `encoding/json`, `errors`, `fmt`, `regexp`, `strings`):

```go
// projectIDRe: a tenant slug — lowercase alnum + hyphen, must start with a
// letter, ≤63 chars (fits a DNS label / the X-Avuru-Tenant header).
var projectIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

const maxProjectLabelLen = 200

type createProjectRequest struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type updateProjectRequest struct {
	Label string `json:"label"`
}

func toProjectDTO(p storage.Project) projectDTO {
	return projectDTO{ID: p.ID, Label: p.Label, Source: "db", Editable: true, Members: p.Members}
}

func (a *API) isConfigProject(id string) bool {
	for _, p := range a.cfg.Projects {
		if p == id {
			return true
		}
	}
	return false
}

func identityUserID(ctx context.Context) string {
	if id := identityFrom(ctx); id != nil {
		return id.UserID
	}
	return ""
}
```

Handler:

```go
// handleCreateProject creates a UI-managed (db) project. A reserved id
// (default or a config project) or a duplicate live id is a 409, matching
// handleCreateUser's precedent for admin uniqueness conflicts.
func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	var req createProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	id := strings.TrimSpace(req.ID)
	label := strings.TrimSpace(req.Label)
	if !projectIDRe.MatchString(id) {
		return badRequest("id must match ^[a-z][a-z0-9-]{0,62}$")
	}
	if len(label) > maxProjectLabelLen {
		return badRequest("label must be %d characters or fewer", maxProjectLabelLen)
	}
	if id == storage.DefaultTenant || a.isConfigProject(id) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("%q is a reserved project", id)}
	}
	if _, err := st.GetProject(r.Context(), id); err == nil {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q already exists", id)}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	p := storage.Project{
		ID:        id,
		Label:     label,
		Members:   []string{},
		CreatedBy: identityUserID(r.Context()),
		CreatedAt: time.Now(),
	}
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toProjectDTO(p))
	return nil
}
```

- [ ] **Step 4: Register the route in `router.go`** (after the existing `GET /api/v1/projects` line ~88):

```go
	mux.Handle("GET /api/v1/projects", a.secured(auth.RoleViewer, a.handleProjects))
	mux.Handle("POST /api/v1/projects", a.securedAdmin(a.handleCreateProject))
```

(Register unconditionally, like the GET — `securedAdmin` bypasses to open behavior when `cfg.Auth == nil`, matching alert-channel routes.)

- [ ] **Step 5: Run create tests — passing**

Run: `cd hub && go test ./internal/api/ -run TestCreateProject`
Expected: PASS (all subtests).

- [ ] **Step 6: Commit**

```bash
git add hub/internal/api/projects.go hub/internal/api/router.go hub/internal/api/projects_test.go
git commit -m "feat(hub): POST /api/v1/projects (admin create, validation)"
```

---

## Task 6: Rename (PUT) handler + route + tests

**Files:** Modify `hub/internal/api/projects.go`, `router.go`, `projects_test.go`.

- [ ] **Step 1: Write the failing rename tests**

```go
func TestRenameProject(t *testing.T) {
	mux, cookie, f := adminMux(t)
	doBody(mux, http.MethodPost, "/api/v1/projects", cookie, map[string]string{"id": "team-a", "label": "A"})

	w := doBody(mux, http.MethodPut, "/api/v1/projects/team-a", cookie, map[string]string{"label": "Renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if f.Projects["team-a"].Label != "Renamed" {
		t.Fatalf("label = %q", f.Projects["team-a"].Label)
	}
}

func TestRenameProjectRejectsReadOnly(t *testing.T) {
	mux, cookie := adminMuxCfg(t, []string{"prod"})
	for _, id := range []string{"default", "prod", "ghost"} { // reserved, config, unknown-db
		w := doBody(mux, http.MethodPut, "/api/v1/projects/"+id, cookie, map[string]string{"label": "x"})
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409", id, w.Code)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** (`405`/`404`).

Run: `cd hub && go test ./internal/api/ -run TestRenameProject`
Expected: FAIL.

- [ ] **Step 3: Add the `editableProject` guard + `handleUpdateProject`**

```go
// editableProject returns the live db project for id, or a 409 apiError when
// the id is deployment-managed (default/config) or has no live db row. Both
// rename and delete gate on this — only source "db" projects are mutable.
func (a *API) editableProject(ctx context.Context, st storage.Store, id string) (storage.Project, error) {
	if id == storage.DefaultTenant || a.isConfigProject(id) {
		return storage.Project{}, &apiError{status: http.StatusConflict,
			message: fmt.Sprintf("%q is a deployment-managed project and cannot be modified", id)}
	}
	p, err := st.GetProject(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Project{}, &apiError{status: http.StatusConflict,
			message: fmt.Sprintf("project %q is not editable", id)}
	}
	if err != nil {
		return storage.Project{}, err
	}
	return p, nil
}

// handleUpdateProject renames a db project's label (id is immutable).
func (a *API) handleUpdateProject(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	var req updateProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	label := strings.TrimSpace(req.Label)
	if len(label) > maxProjectLabelLen {
		return badRequest("label must be %d characters or fewer", maxProjectLabelLen)
	}
	p, err := a.editableProject(r.Context(), st, r.PathValue("id"))
	if err != nil {
		return err
	}
	p.Label = label
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toProjectDTO(p))
	return nil
}
```

- [ ] **Step 4: Register route** (`router.go`, after the POST):

```go
	mux.Handle("PUT /api/v1/projects/{id}", a.securedAdmin(a.handleUpdateProject))
```

- [ ] **Step 5: Run rename tests — passing**

Run: `cd hub && go test ./internal/api/ -run TestRenameProject`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/api/projects.go hub/internal/api/router.go hub/internal/api/projects_test.go
git commit -m "feat(hub): PUT /api/v1/projects/{id} (admin rename label)"
```

---

## Task 7: Delete handler + route + tests

**Files:** Modify `hub/internal/api/projects.go`, `router.go`, `projects_test.go`.

- [ ] **Step 1: Write the failing delete tests**

```go
func TestDeleteProject(t *testing.T) {
	mux, cookie, f := adminMux(t)
	doBody(mux, http.MethodPost, "/api/v1/projects", cookie, map[string]string{"id": "team-a", "label": "A"})

	w := doBody(mux, http.MethodDelete, "/api/v1/projects/team-a", cookie, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if _, ok := f.Projects["team-a"]; ok {
		t.Fatalf("project not removed: %+v", f.Projects)
	}
}

func TestDeleteProjectRejectsReadOnly(t *testing.T) {
	mux, cookie := adminMuxCfg(t, []string{"prod"})
	for _, id := range []string{"default", "prod", "ghost"} {
		w := doBody(mux, http.MethodDelete, "/api/v1/projects/"+id, cookie, nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409", id, w.Code)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure.**

Run: `cd hub && go test ./internal/api/ -run TestDeleteProject`
Expected: FAIL.

- [ ] **Step 3: Add `handleDeleteProject`**

```go
// handleDeleteProject tombstones a db project (its telemetry ages out by TTL).
func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if _, err := a.editableProject(r.Context(), st, id); err != nil {
		return err
	}
	if err := st.DeleteProject(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
```

- [ ] **Step 4: Register route** (`router.go`):

```go
	mux.Handle("DELETE /api/v1/projects/{id}", a.securedAdmin(a.handleDeleteProject))
```

- [ ] **Step 5: Add the admin-only guard test** (defense in depth — editor is 403)

```go
func TestProjectMutationsAdminOnly(t *testing.T) {
	mux, cookie, _ := authedMux(t) // editor identity (auth_middleware_test.go)
	create := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, map[string]string{"id": "x", "label": "x"})
	if create.Code != http.StatusForbidden {
		t.Fatalf("POST status = %d, want 403", create.Code)
	}
	put := doBody(mux, http.MethodPut, "/api/v1/projects/x", cookie, map[string]string{"label": "y"})
	if put.Code != http.StatusForbidden {
		t.Fatalf("PUT status = %d, want 403", put.Code)
	}
	del := doBody(mux, http.MethodDelete, "/api/v1/projects/x", cookie, nil)
	if del.Code != http.StatusForbidden {
		t.Fatalf("DELETE status = %d, want 403", del.Code)
	}
}
```

(If `authedMux`'s return arity differs, match `auth_middleware_test.go:40`. It provides an editor-not-admin cookie.)

- [ ] **Step 6: Run the full api package + lint**

Run: `cd hub && go test ./internal/api/... && golangci-lint run ./internal/api/...`
Expected: PASS + clean lint. (Per memory `run-golangci-lint-before-push`, lint before pushing Go.)

- [ ] **Step 7: Commit**

```bash
git add hub/internal/api/projects.go hub/internal/api/router.go hub/internal/api/projects_test.go
git commit -m "feat(hub): DELETE /api/v1/projects/{id} (admin tombstone) + admin-only guard test"
```

---

## Task 8: UI types + mutation hooks

**Files:**
- Modify: `ui/src/lib/api-types.ts:273` (extend `Project`, add request types)
- Modify: `ui/src/hooks/use-projects.ts` (add mutations)

- [ ] **Step 1: Extend the types** (`api-types.ts`, keep in sync with the Go DTO):

```ts
export interface Project {
  id: string;
  label?: string;
  source: "default" | "config" | "db" | "data" | "granted";
  editable?: boolean;
  members?: string[];
}
export interface ProjectsResponse { projects: Project[]; }

export interface CreateProjectRequest { id: string; label: string; }
export interface UpdateProjectRequest { label: string; }
```

- [ ] **Step 2: Add create/rename/delete mutations** to `use-projects.ts` (mirror `use-alerts-data.ts`):

```ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiGet, apiPost, apiPut, apiDelete } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type {
  ProjectsResponse,
  Project,
  CreateProjectRequest,
  UpdateProjectRequest,
} from "@/lib/api-types";

export function useProjects() {
  return useQuery({
    queryKey: queryKeys.projects,
    queryFn: () => apiGet<ProjectsResponse>("/api/v1/projects"),
    staleTime: 30_000,
  });
}

function useInvalidateProjects() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: queryKeys.projects });
}

export function useCreateProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: (input: CreateProjectRequest) => apiPost<Project>("/api/v1/projects", input),
    onSuccess: invalidate,
  });
}

export function useRenameProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateProjectRequest }) =>
      apiPut<Project>(`/api/v1/projects/${encodeURIComponent(id)}`, input),
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

(Keep the existing `useProjects` if it already lives here — merge, don't duplicate. Preserve the current import style.)

- [ ] **Step 3: Typecheck**

Run: `cd ui && npm run typecheck` (or `npx tsc --noEmit`)
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/api-types.ts ui/src/hooks/use-projects.ts
git commit -m "feat(ui): project mutation hooks + extended Project type"
```

---

## Task 9: General-tab project card + switcher label

**Files:**
- Create: `ui/src/components/settings/project-settings-card.tsx`
- Modify: `ui/src/components/settings/general-tab.tsx` (mount the card in place of the read-only project banner; keep the Retention card)
- Modify: `ui/src/components/layout/project-switcher.tsx` (render `label || id`)

- [ ] **Step 1: Create the project card** (inline-form-in-Card, admin-gated; models `channels-panel.tsx`)

`ui/src/components/settings/project-settings-card.tsx`:

```tsx
"use client";

import { useState } from "react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useAuth } from "@/hooks/use-auth";
import { useProject } from "@/lib/project-context";
import { useProjects, useCreateProject, useRenameProject, useDeleteProject } from "@/hooks/use-projects";
import { ApiError } from "@/lib/api";
import type { Project } from "@/lib/api-types";

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

function sourceBadge(source: Project["source"]) {
  switch (source) {
    case "config": return <Badge tone="neutral">config-defined</Badge>;
    case "data": return <Badge tone="neutral">discovered from data</Badge>;
    case "db": return <Badge tone="primary">UI-managed</Badge>;
    default: return <Badge tone="neutral">built-in</Badge>;
  }
}

export function ProjectSettingsCard() {
  const { isAdmin } = useAuth();
  const { project, setProject } = useProject();
  const { data } = useProjects();
  const current = data?.projects.find((p) => p.id === project) ?? { id: project, source: "default" as const };

  const rename = useRenameProject();
  const del = useDeleteProject();
  const [label, setLabel] = useState(current.label ?? "");
  const [error, setError] = useState<string | null>(null);

  const editable = !!current.editable && isAdmin;

  async function save() {
    setError(null);
    try {
      await rename.mutateAsync({ id: current.id, input: { label: label.trim() } });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Failed to rename");
    }
  }

  async function remove() {
    setError(null);
    try {
      await del.mutateAsync(current.id);
      setProject("default");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Failed to delete");
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Project</CardTitle>
          {sourceBadge(current.source)}
        </CardHeader>
        <div className="flex flex-col gap-3 p-4 pt-0">
          <div className="text-sm text-neutral-content">
            Project id: <span className="font-mono">{current.id}</span> (immutable)
          </div>
          {editable ? (
            <label className="flex flex-col gap-1 text-sm">
              Project name
              <div className="flex gap-2">
                <input className={inputClass} value={label} onChange={(e) => setLabel(e.target.value)} maxLength={200} />
                <Button variant="primary" size="sm" onClick={save} disabled={rename.isPending}>Save</Button>
              </div>
            </label>
          ) : (
            <p className="text-sm text-neutral-content">
              This project is defined through deployment configuration and cannot be modified here.
            </p>
          )}
          {error && <p className="text-xs text-error">{error}</p>}
        </div>
      </Card>

      {isAdmin && <NewProjectCard />}

      {editable && (
        <Card>
          <CardHeader><CardTitle>Danger zone</CardTitle></CardHeader>
          <div className="flex items-center justify-between p-4 pt-0">
            <p className="text-sm text-neutral-content">
              Delete this project. Its telemetry ages out by retention; the id can be re-created.
            </p>
            <Button variant="danger" size="sm" onClick={remove} disabled={del.isPending}>Delete project</Button>
          </div>
        </Card>
      )}
    </div>
  );
}

function NewProjectCard() {
  const create = useCreateProject();
  const { setProject } = useProject();
  const [open, setOpen] = useState(false);
  const [id, setId] = useState("");
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setError(null);
    try {
      await create.mutateAsync({ id: id.trim(), label: label.trim() });
      setProject(id.trim());
      setOpen(false);
      setId("");
      setLabel("");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Failed to create");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>New project</CardTitle>
        {!open && <Button variant="secondary" size="sm" onClick={() => setOpen(true)}>New project</Button>}
      </CardHeader>
      {open && (
        <div className="flex flex-col gap-3 p-4 pt-0">
          <label className="flex flex-col gap-1 text-sm">
            Project id
            <input className={inputClass} value={id} onChange={(e) => setId(e.target.value)}
              placeholder="team-a" pattern="[a-z][a-z0-9-]{0,62}" />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Project name
            <input className={inputClass} value={label} onChange={(e) => setLabel(e.target.value)} maxLength={200} />
          </label>
          {error && <p className="text-xs text-error">{error}</p>}
          <div className="flex gap-2">
            <Button variant="primary" size="sm" onClick={submit} disabled={create.isPending}>New project</Button>
            <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>Cancel</Button>
          </div>
        </div>
      )}
    </Card>
  );
}
```

- [ ] **Step 2: Mount it in `general-tab.tsx`** — Read the current file, then replace the existing read-only project card/banner block with `<ProjectSettingsCard />`, keeping the Retention card untouched:

```tsx
import { ProjectSettingsCard } from "@/components/settings/project-settings-card";
// ...in the returned JSX, where the old project banner card was:
<ProjectSettingsCard />
// (leave the existing Retention <Card> as-is)
```

Remove the now-dead `useProjects`/source-badge logic that the old banner used, if it's no longer referenced.

- [ ] **Step 3: Switcher renders the label** (`project-switcher.tsx`) — where each option label is `p.id`, use `p.label || p.id`; selection still keys on `p.id`:

```tsx
{projects.map((p) => (
  <li key={p.id} role="option" aria-selected={p.id === project}>
    <button onClick={() => { setProject(p.id); setOpen(false); }}>
      {p.label || p.id}
    </button>
  </li>
))}
```

(Keep the existing markup/handlers; only the visible text changes. The e2e still selects options by `p.id` where seeded projects have no label, so those stay green.)

- [ ] **Step 4: Typecheck + lint + build**

Run: `cd ui && npm run typecheck && npm run lint && npm run build`
Expected: PASS (static export builds).

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/settings/project-settings-card.tsx ui/src/components/settings/general-tab.tsx ui/src/components/layout/project-switcher.tsx
git commit -m "feat(ui): admin project card (create/rename/delete) + switcher labels"
```

---

## Task 10: Playwright e2e

**Files:**
- Modify: `ui/e2e/projects.spec.ts` (admin CRUD flows via route stubs)
- Modify: `ui/e2e/settings.spec.ts` (update the now-conditional banner assertion)

- [ ] **Step 1: Add an admin project-CRUD spec** using route interception (so it does not depend on hub write support in the seed stack), modeled on `auth.spec.ts`'s `signIn` + `alerts.spec.ts`'s `page.route`:

```ts
import { test, expect } from "@playwright/test";

async function signIn(page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("admin");
  await page.getByLabel("Password").fill("e2e-admin-pw");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

test("admin creates a project and it appears in the switcher", async ({ page }) => {
  const projects = [{ id: "default", source: "default" }];
  await page.route("**/api/v1/projects", async (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON();
      projects.push({ id: body.id, label: body.label, source: "db", editable: true });
      return route.fulfill({ status: 201, json: { id: body.id, label: body.label, source: "db", editable: true } });
    }
    return route.fulfill({ json: { projects } });
  });

  await signIn(page);
  await page.goto("/settings?tab=general");
  await page.getByRole("button", { name: "New project", exact: true }).click(); // reveal
  await page.getByLabel("Project id").fill("team-a");
  await page.getByLabel("Project name").fill("Team A");
  await page.getByRole("button", { name: "New project", exact: true }).click(); // submit

  await page.getByRole("button", { name: "Switch project" }).click();
  await expect(page.getByRole("option", { name: "Team A" })).toBeVisible();
});

test("a config project shows the read-only banner", async ({ page }) => {
  await page.route("**/api/v1/projects", (route) =>
    route.fulfill({ json: { projects: [{ id: "default", source: "default" }, { id: "prod", source: "config" }] } }));
  await signIn(page);
  await page.goto("/settings?tab=general");
  // switch to the config project, then assert the banner
  await page.getByRole("button", { name: "Switch project" }).click();
  await page.getByRole("option", { name: "prod" }).click();
  await expect(page.getByText(/defined through deployment configuration/)).toBeVisible();
});
```

(If `page.route` must also stub `/api/v1/auth/me` for `isAdmin`, add a fulfill returning `{ user: {...}, grants: [{ scope: "*", role: "admin" }] }`. Check whether the seeded `admin` login already yields that from the live `/auth/me`; prefer the live response and only stub `/projects`.)

- [ ] **Step 2: Fix the existing settings spec** — the General tab no longer always shows the banner. Update `settings.spec.ts` so the banner assertion runs only for a default/config project (the seeded default project is read-only, so the banner still shows on first load — verify and adjust the selector if the copy moved into `ProjectSettingsCard`).

- [ ] **Step 3: Run e2e against an isolated stack** (per memory `isolated-e2e-compose-run` / `ui-dev-loop-ports` — use `-p avuru-obs-e2e` and a non-3000/3001 port):

Run: `cd ui && AVURUOPS_BASE_URL=http://localhost:3005 npx playwright test e2e/projects.spec.ts e2e/settings.spec.ts`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add ui/e2e/projects.spec.ts ui/e2e/settings.spec.ts
git commit -m "test(ui): e2e for admin project create/rename/delete + config read-only"
```

---

## Task 11: Docs, AEP acceptance, changelog

**Files:** `design/README.md`, `design/2026-07-27-projects-completion.md`, `CHANGELOG.md`, `ROADMAP.md`, docs site (via skill).

- [ ] **Step 1: Accept the AEP** — flip `design/2026-07-27-projects-completion.md` status to `Accepted` and update its row in `design/README.md`'s index (Draft → Accepted). Add a one-line note that Phase 1 (CRUD) is implemented; Phases 2–3 remain.

- [ ] **Step 2: CHANGELOG** — add an `[Unreleased]` entry under a value-oriented heading (per memory `docs-value-highlighting`):

```md
### Added
- **UI-managed projects (Phase 1).** Admins can create, rename, and delete
  projects directly in Settings → General; the built-in and config-defined
  projects stay read-only. Endpoints: `POST/PUT/DELETE /api/v1/projects`.
```

- [ ] **Step 3: ROADMAP** — mark "Projects completion" Phase 1 as in progress/shipped in the v0.3 section.

- [ ] **Step 4: Run the `docs-align` skill** (EN + FR) to update the bilingual docs site: changelog entry, feature-status matrix row, API reference for the three new endpoints, and a "managing projects" guide. (Per memory `docs-alignment-procedure` / `docs-value-highlighting`.)

- [ ] **Step 5: Commit**

```bash
git add design/ CHANGELOG.md ROADMAP.md
git commit -m "docs: accept projects-completion AEP; changelog + roadmap for Phase 1 CRUD"
```

---

## Final verification

- [ ] `cd hub && go build ./... && go test ./... && golangci-lint run` — unit + fake-based handler tests green, lint clean.
- [ ] `cd hub && go test -tags integration ./internal/storage/clickhouse/ -run TestProjectRoundtrip` — real-ClickHouse CRUD green (Docker required).
- [ ] `cd ui && npm run typecheck && npm run lint && npm run build` — static export builds.
- [ ] e2e green against an isolated compose stack (Task 10 Step 3).
- [ ] **Manual smoke** (real stack): as admin, Settings → General → New project `team-a`/"Team A" → appears in switcher as "Team A"; rename → label updates; delete → gone and switcher falls back to default. As a viewer (non-admin), no New-project/Delete controls and the label field is read-only. `default` shows the read-only banner. Kill ClickHouse → `GET /api/v1/projects` still 200 with default+config (switcher renders).
- [ ] Open the PR (needs `gh auth login` or `GH_TOKEN`; see memory `v0-2-0-release-status` — `gh` is currently unauthenticated on this machine).

---

## Self-review (done during authoring)

- **Spec coverage:** §5 data model → Task 1–2; §5 store methods → Task 2–3; §6 merge/precedence + `projectDTO` → Task 4; §7 endpoints (create/rename/delete + validation + read-only guard) → Tasks 5–7; §7 UI (label field, New project, danger zone, switcher label, hooks, types) → Tasks 8–9; §7 tests (store integration, handler, e2e) → Tasks 3/5–7/10; §10 store-outage-still-200 → Task 4 `dbProjects`; §10 docs → Task 11. **Deferred by design:** Members editing, ingest keys, aggregates (Phases 2–3).
- **Deviations flagged:** PUT instead of PATCH (Decision 2); inline-form-in-Card instead of dialog (Decision 1).
- **Type consistency:** `storage.Project{ID,Label,Members,CreatedBy,CreatedAt,UpdatedAt}` used identically across Tasks 2/3/4/5/6; `projectDTO{ID,Label,Source,Editable,Members}` identical across Task 4–7; `toProjectDTO`/`editableProject`/`isConfigProject`/`identityUserID` defined once (Tasks 4–6) and reused; UI `Project`/`CreateProjectRequest`/`UpdateProjectRequest` consistent across Tasks 8–9.
