# Users CRUD Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete user management per `design/2026-08-06-users-crud-password.md`: delete-after-disable, admin edit of name/roles/password in the Users panel, and self-service password change via a new Settings → Account tab.

**Architecture:** The hub API already carries most of the update surface (`PUT /api/v1/users/{id}`). This adds one migration (tombstone column on `auth_user`), one storage method (`DeleteAuthUser`), two routes (`DELETE /api/v1/users/{id}`, `POST /api/v1/auth/password`), an `Origin` field on the identity, and the UI that exposes all of it. TDD throughout; one commit per task; **no `Co-Authored-By` trailer on commits (AI_POLICY.md)**.

**Tech Stack:** Go 1.x (stdlib mux, ClickHouse via `s.conn`), Next.js static SPA (TanStack Query NOT used in users-panel — it uses direct `apiGet/apiPost` + `reload()`, keep that pattern), Playwright route-stub e2e.

**Worktree:** `/Users/egilberny/project/avuru-obs/.claude/worktrees/users-crud-password`, branch `feature/users-crud-password`. All commands below run from the worktree root unless stated.

---

### Task 1: Migration 0015 — `Deleted` column on `auth_user`

The original auth migration deliberately omitted the tombstone column (its header comment says so). Do **not** edit `0010_auth.sql` — applied migrations are immutable; the new file carries its own rationale.

**Files:**
- Create: `hub/internal/storage/migrations/0015_auth_user_deleted.sql`
- Modify: `hub/internal/storage/migrations/migrations.go` (both `Ordered` and `ByModule` — `TestByModuleCoversOrdered` enforces the pairing)

- [ ] **Step 1: Write the migration file**

```sql
-- Tombstone column for auth_user. 0010 deliberately omitted it ("users are
-- disabled, never hard-deleted"); AEP 2026-08-06-users-crud-password amends
-- that decision: delete is now allowed as an explicit second step after
-- disable. Reads filter Deleted = 0; DeleteAuthUser writes the tombstone.
ALTER TABLE otel.auth_user ADD COLUMN IF NOT EXISTS `Deleted` UInt8 DEFAULT 0;
```

- [ ] **Step 2: Add to `Ordered` only, run the coverage test to see it fail**

In `migrations.go`, append to `Ordered`:

```go
	"0014_collection_overlay.sql",
	"0015_auth_user_deleted.sql",
```

Run: `cd hub && go test ./internal/storage/migrations/ -run TestByModuleCoversOrdered -v`
Expected: FAIL — `0015_auth_user_deleted.sql` has no `ByModule` entry.

- [ ] **Step 3: Tag the module, re-run**

In the `ByModule` map (after the `0014` entry, matching its comment style):

```go
	// Tombstone column on auth_user — auth gates everything, so core.
	"0015_auth_user_deleted.sql": {modules.Core},
```

Run: `cd hub && go test ./internal/storage/migrations/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add hub/internal/storage/migrations/
git commit -m "feat(storage): migration 0015 — Deleted tombstone column on auth_user

AEP 2026-08-06 amends the auth AEP's disable-not-delete: delete becomes an
explicit second step after disable. 0010 stays untouched (applied migrations
are immutable); this file carries the amendment."
```

---

### Task 2: `DeleteAuthUser` + deleted-row filtering in storage

**Files:**
- Modify: `hub/internal/storage/store.go` (interface, ~line 667)
- Modify: `hub/internal/storage/clickhouse/auth.go` (three reads + new method)
- Modify: `hub/internal/storage/storagetest/fake.go` (new method, after `SaveAuthUser` ~line 390)
- Test: `hub/internal/storage/clickhouse/auth_integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `auth_integration_test.go`, matching the file's existing setup helpers (open the file first and reuse its store-constructor helper — the other tests in it show the pattern; do not invent a new one):

```go
// Delete tombstones: the user vanishes from every read path, and a later
// SaveAuthUser for the same Id (an SSO user signing in again) resurrects a
// fresh live row — the AEP's documented re-provisioning behavior.
func TestDeleteAuthUserTombstones(t *testing.T) {
	store := newTestStore(t) // reuse the file's existing helper name

	u := storage.AuthUser{ID: "del-1", Email: "del@x.io", Origin: "local", Disabled: true}
	if err := store.SaveAuthUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAuthUser(ctx, "del-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := store.GetAuthUser(ctx, "del-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetAuthUser after delete: %v, want ErrNotFound", err)
	}
	if _, err := store.GetAuthUserByEmail(ctx, "del@x.io"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetAuthUserByEmail after delete: %v, want ErrNotFound", err)
	}
	users, err := store.ListAuthUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range users {
		if got.ID == "del-1" {
			t.Fatal("deleted user still listed")
		}
	}

	// Same-Id re-save supersedes the tombstone (newer UpdatedAt wins).
	if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: "del-1", Email: "del@x.io", Origin: "oidc"}); err != nil {
		t.Fatal(err)
	}
	back, err := store.GetAuthUser(ctx, "del-1")
	if err != nil {
		t.Fatalf("re-saved user not readable: %v", err)
	}
	if back.Origin != "oidc" {
		t.Fatalf("re-saved user Origin = %q", back.Origin)
	}
}
```

Adjust `newTestStore`/`ctx` to whatever `auth_integration_test.go` actually names its helper and context — copy the neighboring test's first lines verbatim.

- [ ] **Step 2: Verify it fails to compile**

Run: `cd hub && go vet ./internal/storage/...`
Expected: `store.DeleteAuthUser undefined`

- [ ] **Step 3: Interface + ClickHouse + fake**

`store.go` — in the auth block of the `Store` interface (after `SaveAuthUser`), extend the doc comment and add:

```go
	// DeleteAuthUser tombstones a user; every read path then reports
	// ErrNotFound. A later SaveAuthUser for the same ID resurrects a fresh
	// live row (SSO re-provisioning).
	DeleteAuthUser(ctx context.Context, id string) error
```

`clickhouse/auth.go` — add `AND Deleted = 0` to `GetAuthUser` and `GetAuthUserByEmail` WHERE clauses, and `WHERE Deleted = 0` to `ListAuthUsers` (before `ORDER BY Email`). Then:

```go
// DeleteAuthUser tombstones a user (Deleted=1). Reads filter Deleted = 0, so
// the row disappears from every lookup while ReplacingMergeTree(UpdatedAt)
// supersedes the live row — same pattern as DeleteProject. SaveAuthUser's
// column list omits Deleted, so any later upsert is live again by default.
func (s *Store) DeleteAuthUser(ctx context.Context, id string) error {
	err := s.conn.Exec(ctx, `
INSERT INTO auth_user (Id, Deleted) VALUES (?, 1)`, id)
	if err != nil {
		return fmt.Errorf("delete auth user: %w", err)
	}
	return nil
}
```

`storagetest/fake.go` — after `SaveAuthUser`:

```go
// DeleteAuthUser removes the user from both indexes — mirrors the tombstone
// from the caller's point of view (no read path can observe it afterwards).
func (f *Fake) DeleteAuthUser(_ context.Context, id string) error {
	if u, ok := f.Users[id]; ok {
		delete(f.UsersByEmail, u.Email)
	}
	delete(f.Users, id)
	return nil
}
```

- [ ] **Step 4: Run unit tests, then the integration test**

Run: `cd hub && go build ./... && go test ./internal/storage/... ./internal/api/... ./internal/auth/...`
Expected: PASS (compile fixed, nothing regressed)

Run: `cd hub && TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./internal/storage/clickhouse/ -run TestDeleteAuthUserTombstones -v`
Expected: PASS (needs Docker/colima up; `TESTCONTAINERS_RYUK_DISABLED=true` is required on this machine)

- [ ] **Step 5: Commit**

```bash
git add hub/internal/storage/
git commit -m "feat(storage): DeleteAuthUser tombstone + deleted-row filtering"
```

---

### Task 3: `DELETE /api/v1/users/{id}` — disabled-first hard delete

**Files:**
- Modify: `hub/internal/api/users.go`
- Modify: `hub/internal/api/router.go` (users block, ~line 186)
- Test: `hub/internal/api/users_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `users_test.go` (it already has `adminMux` + `doBody` helpers):

```go
// Delete is a two-step: 409 while the user is live, 204 once disabled. The
// tombstone must clear grants and sessions, and self-delete is structurally
// impossible (you cannot be disabled while signed in).
func TestDeleteUserRequiresDisabledFirst(t *testing.T) {
	mux, c, f := adminMux(t)

	w := doBody(mux, "POST", "/api/v1/users", c, `{
		"email":"gone@x.io","name":"Gone","password":"gonepw",
		"grants":[{"scope":"payments","role":"editor"}]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Live user: refused with 409, nothing changes.
	if w := doBody(mux, "DELETE", "/api/v1/users/"+created.ID, c, ""); w.Code != http.StatusConflict {
		t.Fatalf("delete live user: %d, want 409; body %s", w.Code, w.Body.String())
	}

	// The user signs in — their session must die with the delete below.
	svcLogin := doBody(mux, "POST", "/api/v1/auth/login", nil, `{"email":"gone@x.io","password":"gonepw"}`)
	if svcLogin.Code != http.StatusOK {
		t.Fatalf("victim login: %d", svcLogin.Code)
	}

	// Disable, then delete.
	if w := doBody(mux, "PUT", "/api/v1/users/"+created.ID, c, `{"disabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("disable: %d", w.Code)
	}
	if w := doBody(mux, "DELETE", "/api/v1/users/"+created.ID, c, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete disabled user: %d, want 204; body %s", w.Code, w.Body.String())
	}

	// Gone from the list, grants cleared, sessions revoked.
	list := doBody(mux, "GET", "/api/v1/users", c, "")
	if strings.Contains(list.Body.String(), "gone@x.io") {
		t.Fatalf("deleted user still listed: %s", list.Body.String())
	}
	if got := f.Grants[created.ID]; len(got) != 0 {
		t.Fatalf("grants survived the delete: %+v", got)
	}
	for _, s := range f.Sessions {
		if s.UserID == created.ID {
			t.Fatal("a session survived the delete")
		}
	}

	// Unknown id -> 404 (repeat delete included: the row is gone).
	if w := doBody(mux, "DELETE", "/api/v1/users/"+created.ID, c, ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete deleted user: %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd hub && go test ./internal/api/ -run TestDeleteUserRequiresDisabledFirst -v`
Expected: FAIL — first DELETE answers 404 (route not registered)

- [ ] **Step 3: Handler + route**

`users.go`, after `handleUpdateUser`:

```go
// handleDeleteUser hard-deletes a DISABLED user; a live user answers 409 —
// disable is the reversible first step, delete the explicit cleanup
// (design/2026-08-06-users-crud-password.md, amending disable-not-delete).
// Self-delete is structurally impossible: the caller holds a live session and
// disabled users cannot (IdentityFromToken rejects them). Order matters:
// sessions → grants → user, so a crash mid-sequence leaves a disabled,
// grantless user — recoverable — never a half-deleted one that can sign in.
func (a *API) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	u, err := st.GetAuthUser(r.Context(), r.PathValue("id"))
	if err != nil {
		return err // storage.ErrNotFound -> 404 via handle()
	}
	if !u.Disabled {
		return &apiError{status: http.StatusConflict, message: "disable the user before deleting"}
	}
	if err := st.RevokeAuthSessionsForUser(r.Context(), u.ID); err != nil {
		return err
	}
	if err := st.ReplaceAuthGrants(r.Context(), u.ID, nil); err != nil {
		return err
	}
	if err := st.DeleteAuthUser(r.Context(), u.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
```

`router.go`, after the PUT registration:

```go
		mux.Handle("DELETE /api/v1/users/{id}", a.securedAdmin(a.handleDeleteUser))
```

- [ ] **Step 4: Run tests**

Run: `cd hub && go test ./internal/api/ -v -run TestDeleteUser`
Expected: PASS. Then the full package: `go test ./internal/api/` — PASS.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/api/
git commit -m "feat(hub): DELETE /api/v1/users/{id} — hard delete behind the disabled-first rule"
```

---

### Task 4: PUT origin guard — no passwords on SSO users

**Files:**
- Modify: `hub/internal/api/users.go` (`handleUpdateUser`, the password block ~line 176)
- Test: `hub/internal/api/users_test.go`

- [ ] **Step 1: Write the failing test**

```go
// An SSO user's credential lives at the IdP; storing a local hash for them
// is a silent no-op today and a confusing one — refuse it outright.
func TestUpdateUserRejectsPasswordForSSOUser(t *testing.T) {
	mux, c, f := adminMux(t)
	if err := f.SaveAuthUser(context.Background(), storage.AuthUser{
		ID: "oidc|sub1", Email: "sso@x.io", Origin: "oidc",
	}); err != nil {
		t.Fatal(err)
	}

	w := doBody(mux, "PUT", "/api/v1/users/oidc|sub1", c, `{"password":"newpw"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("password on SSO user: %d, want 400; body %s", w.Code, w.Body.String())
	}
	// Name edits on the same user still work.
	if w := doBody(mux, "PUT", "/api/v1/users/oidc|sub1", c, `{"name":"Renamed"}`); w.Code != http.StatusOK {
		t.Fatalf("name edit on SSO user: %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd hub && go test ./internal/api/ -run TestUpdateUserRejectsPasswordForSSOUser -v`
Expected: FAIL — the PUT answers 200

- [ ] **Step 3: Add the guard**

In `handleUpdateUser`, replace the password block's first line so the guard sits with the validation phase (before any write — the surrounding comment already demands "a 400 must mean nothing changed", so place it next to `checkSelfLockout`):

```go
	if req.Password != nil && *req.Password != "" && u.Origin != "local" {
		return badRequest("cannot set a password for an SSO user — it is managed by the identity provider")
	}
```

Insert immediately after the `checkSelfLockout` call.

- [ ] **Step 4: Run tests**

Run: `cd hub && go test ./internal/api/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add hub/internal/api/users.go hub/internal/api/users_test.go
git commit -m "fix(hub): reject password edits on SSO users — the IdP owns their credential"
```

---

### Task 5: auth service — `Identity.Origin` + `ChangePassword`

**Files:**
- Modify: `hub/internal/auth/auth.go` (Identity struct, ~line 54)
- Modify: `hub/internal/auth/service.go` (sentinels ~line 18, `identityFor` ~line 285, new method after `Login`)
- Test: `hub/internal/auth/service_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `service_test.go` (open it first; reuse its existing fake-store/service constructor exactly as neighboring tests do — the snippets below assume a helper that yields a `*Service` backed by a `storagetest.Fake`; adapt the constructor lines to the file's local idiom):

```go
// ChangePassword: the current password gates the rotation; success revokes
// every prior session and mints a fresh one.
func TestChangePasswordRotatesSessions(t *testing.T) {
	f := &storagetest.Fake{}
	svc := NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "old-pw"); err != nil {
		t.Fatal(err)
	}
	oldToken, id, err := svc.Login(context.Background(), "admin", "old-pw", "ip1")
	if err != nil {
		t.Fatal(err)
	}

	newToken, err := svc.ChangePassword(context.Background(), id.UserID, "old-pw", "new-pw", "ip1")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := svc.IdentityFromToken(context.Background(), oldToken); err == nil {
		t.Fatal("the pre-rotation session survived")
	}
	if _, err := svc.IdentityFromToken(context.Background(), newToken); err != nil {
		t.Fatalf("the fresh session does not work: %v", err)
	}
	if _, _, err := svc.Login(context.Background(), "admin", "new-pw", "ip1"); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	if _, _, err := svc.Login(context.Background(), "admin", "old-pw", "ip2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login with the old password: %v, want ErrInvalidCredentials", err)
	}
}

func TestChangePasswordGuards(t *testing.T) {
	f := &storagetest.Fake{}
	svc := NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "old-pw"); err != nil {
		t.Fatal(err)
	}

	// Wrong current password fails and counts toward the limiter.
	if _, err := svc.ChangePassword(context.Background(), "bootstrap-admin", "WRONG", "x", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current: %v, want ErrInvalidCredentials", err)
	}
	for i := 0; i < 4; i++ {
		_, _ = svc.ChangePassword(context.Background(), "bootstrap-admin", "WRONG", "x", "ip1")
	}
	if _, err := svc.ChangePassword(context.Background(), "bootstrap-admin", "old-pw", "x-good", "ip1"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("after 5 failures: %v, want ErrTooManyAttempts", err)
	}

	// SSO users have no local password.
	if err := f.SaveAuthUser(context.Background(), storage.AuthUser{ID: "oidc|s", Email: "s@x.io", Origin: "oidc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(context.Background(), "oidc|s", "a", "b", "ip9"); !errors.Is(err, ErrExternalPassword) {
		t.Fatalf("sso user: %v, want ErrExternalPassword", err)
	}

	// The shared demo account must not be re-keyed by a visitor.
	if err := svc.EnsureDemoUser(context.Background(), "demo@x.io", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(context.Background(), "demo-viewer", "demo-pw", "b", "ip9"); !errors.Is(err, ErrDemoUser) {
		t.Fatalf("demo user: %v, want ErrDemoUser", err)
	}
}

// Origin rides the identity so /auth/me can tell the SPA whether a password
// form even applies.
func TestIdentityCarriesOrigin(t *testing.T) {
	f := &storagetest.Fake{}
	svc := NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	_, id, err := svc.Login(context.Background(), "admin", "pw", "ip1")
	if err != nil {
		t.Fatal(err)
	}
	if id.Origin != "local" {
		t.Fatalf("Origin = %q, want local", id.Origin)
	}
}
```

- [ ] **Step 2: Verify compile failure**

Run: `cd hub && go vet ./internal/auth/`
Expected: `svc.ChangePassword undefined`, `ErrExternalPassword undefined`, `ErrDemoUser undefined`, `id.Origin undefined`

- [ ] **Step 3: Implement**

`auth.go` — add to `Identity` (after `Name`):

```go
	// Origin mirrors AuthUser.Origin ("local" | "oidc"); empty for the
	// anonymous identity. The SPA uses it to decide whether a password
	// form applies at all.
	Origin string `json:"origin"`
```

`service.go` — extend the sentinel block:

```go
	// ErrExternalPassword: the account's credential lives at the IdP.
	ErrExternalPassword = errors.New("password is managed by the identity provider")
	// ErrDemoUser: the shared demo account must not be re-keyed by a visitor.
	ErrDemoUser = errors.New("the demo account cannot change its password")
```

In `identityFor`, carry the origin:

```go
	id := Identity{UserID: u.ID, Email: u.Email, Name: u.Name, Origin: u.Origin}
```

New method after `Login`:

```go
// ChangePassword rotates userID's own password after verifying the current
// one. Success revokes EVERY existing session (an attacker holding a cookie
// is evicted) and mints a fresh session so the legitimate caller stays
// signed in. Failed current-password checks feed the same limiter as login —
// a stolen session must not become a password-guessing oracle.
func (s *Service) ChangePassword(ctx context.Context, userID, current, newPw, ip string) (string, error) {
	key := userID + "|" + ip
	if s.limiter.blocked(key, ip) {
		return "", ErrTooManyAttempts
	}
	st, err := s.st()
	if err != nil {
		return "", err
	}
	u, err := st.GetAuthUser(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	if u.Origin != "local" {
		return "", ErrExternalPassword
	}
	if u.ID == demoViewerID {
		return "", ErrDemoUser
	}
	if !CheckPassword(u.PasswordHash, current) {
		s.limiter.fail(key, ip)
		return "", ErrInvalidCredentials
	}
	hash, err := HashPassword(newPw)
	if err != nil {
		// bcrypt's >72-byte limit — a caller error, not a server fault.
		return "", fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
	}
	u.PasswordHash = hash
	if err := st.SaveAuthUser(ctx, u); err != nil {
		return "", fmt.Errorf("saving user: %w", err)
	}
	if err := st.RevokeAuthSessionsForUser(ctx, u.ID); err != nil {
		return "", fmt.Errorf("revoking sessions: %w", err)
	}
	return s.mintSession(ctx, st, u.ID)
}
```

- [ ] **Step 4: Run tests**

Run: `cd hub && go test ./internal/auth/ -v -run 'TestChangePassword|TestIdentityCarriesOrigin'`
Expected: PASS. Then `go test ./internal/...` — PASS (meFrom consumers unaffected; Identity gained a field).

- [ ] **Step 5: Commit**

```bash
git add hub/internal/auth/
git commit -m "feat(auth): ChangePassword — current-password gate, session rotation, origin on Identity"
```

---

### Task 6: `POST /api/v1/auth/password` + origin in `/auth/me`

**Files:**
- Modify: `hub/internal/api/auth_handlers.go` (meResponse ~line 80, meFrom ~line 164, new handler after `handleMe`)
- Modify: `hub/internal/api/router.go` (next to the logout/me registrations)
- Test: `hub/internal/api/auth_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `auth_handlers_test.go` (reuse its `authDo`-style helper — open the file and match its idiom; `adminMux` from `users_test.go` is in the same package and usable):

```go
// Self-service password change: wrong current -> 400 (NOT 401 — the SPA
// treats 401 as session-expired and redirects to login), success rotates the
// cookie, /auth/me carries origin so the SPA knows a password form applies.
func TestChangeOwnPassword(t *testing.T) {
	mux, c, _ := adminMux(t)

	// /auth/me now reports the origin.
	me := doBody(mux, "GET", "/api/v1/auth/me", c, "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"origin":"local"`) {
		t.Fatalf("me without origin: %d %s", me.Code, me.Body.String())
	}

	// Wrong current password.
	w := doBody(mux, "POST", "/api/v1/auth/password", c, `{"currentPassword":"WRONG","newPassword":"next-pw"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong current: %d, want 400; body %s", w.Code, w.Body.String())
	}

	// Success: 200, a fresh cookie, and the old session is dead.
	w = doBody(mux, "POST", "/api/v1/auth/password", c, `{"currentPassword":"root-pw","newPassword":"next-pw"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("change: %d body %s", w.Code, w.Body.String())
	}
	var fresh *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookieName && ck.Value != "" {
			fresh = ck
		}
	}
	if fresh == nil {
		t.Fatal("no fresh session cookie on the response")
	}
	if got := doBody(mux, "GET", "/api/v1/auth/me", c, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("old session after rotation: %d, want 401", got.Code)
	}
	if got := doBody(mux, "GET", "/api/v1/auth/me", fresh, ""); got.Code != http.StatusOK {
		t.Fatalf("fresh session: %d", got.Code)
	}
}

func TestChangeOwnPasswordRequiresBody(t *testing.T) {
	mux, c, _ := adminMux(t)
	w := doBody(mux, "POST", "/api/v1/auth/password", c, `{"currentPassword":"","newPassword":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty fields: %d, want 400", w.Code)
	}
	if w := doBody(mux, "POST", "/api/v1/auth/password", nil, `{"currentPassword":"a","newPassword":"b"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d, want 401", w.Code)
	}
}
```

Note: `doBody` sends no `Origin` header; if `checkOrigin` rejects that in tests, mirror however the login tests in this file satisfy it (same-origin requests with no Origin header pass `checkOrigin` — confirm against the existing login tests).

- [ ] **Step 2: Verify failure**

Run: `cd hub && go test ./internal/api/ -run TestChangeOwnPassword -v`
Expected: FAIL — 404 (route missing) and/or missing `"origin"` in `/auth/me`

- [ ] **Step 3: Implement**

`auth_handlers.go` — add `Origin` to the `meResponse` user struct:

```go
	User struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		Origin    string `json:"origin"`
		Anonymous bool   `json:"anonymous"`
	} `json:"user"`
```

In `meFrom`, copy it alongside the other user fields: `resp.User.Origin = id.Origin`.

New request type + handler after `handleMe`:

```go
type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// handleChangePassword is the self-service half of password management
// (design/2026-08-06-users-crud-password.md): any signed-in local user
// rotates their OWN password after proving they hold the current one. The
// admin half lives in handleUpdateUser. Wrong-current answers 400, not 401 —
// the SPA treats 401 as session-expired and would bounce the user to login
// mid-form. Success rotates the session cookie in place.
func (a *API) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	// Credentialed responses must never be cached (OWASP).
	w.Header().Set("Cache-Control", "no-store")
	if err := checkOrigin(r); err != nil {
		return err
	}
	id := identityFrom(r.Context())
	if id == nil || id.Anonymous {
		return unauthorized()
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return badRequest("current and new password required")
	}
	// RemoteAddr for the limiter, same reasoning as handleLogin.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	token, err := a.cfg.Auth.ChangePassword(r.Context(), id.UserID, req.CurrentPassword, req.NewPassword, ip)
	switch {
	case errors.Is(err, auth.ErrStoreUnavailable):
		return errStoreUnavailable
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		return &apiError{status: http.StatusTooManyRequests, message: "too many attempts, retry in a minute"}
	case errors.Is(err, auth.ErrExternalPassword):
		return badRequest("your password is managed by the identity provider")
	case errors.Is(err, auth.ErrDemoUser):
		return &apiError{status: http.StatusForbidden, message: "the shared demo account cannot change its password"}
	case errors.Is(err, auth.ErrInvalidCredentials):
		return badRequest("current password is incorrect")
	case err != nil:
		return err
	}
	setSessionCookie(w, r, token, int(a.cfg.Auth.SessionTTL()/time.Second))
	writeJSON(w, http.StatusOK, meFrom(*id))
	return nil
}
```

`router.go`, next to logout/me:

```go
		mux.Handle("POST /api/v1/auth/password", a.authenticated(a.handleChangePassword))
```

- [ ] **Step 4: Run tests**

Run: `cd hub && go test ./internal/api/ && go test ./internal/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add hub/internal/api/
git commit -m "feat(hub): POST /api/v1/auth/password — self-service rotation with cookie re-mint"
```

---

### Task 7: UI types

**Files:**
- Modify: `ui/src/lib/api-types.ts` (`Me` ~line 509, after `CreateUserRequest` ~line 533)

- [ ] **Step 1: Extend the types**

`Me` gains origin:

```ts
export interface Me {
  user: { id: string; email: string; name: string; origin: string; anonymous: boolean };
  grants: AuthGrant[];
}
```

After `CreateUserRequest`:

```ts
// PUT /api/v1/users/{id} — absent field = leave unchanged; grants replace
// the whole set. Mirrors updateUserRequest in hub/internal/api/users.go.
export interface UpdateUserRequest {
  name?: string;
  password?: string;
  disabled?: boolean;
  grants?: AuthGrant[];
}

// POST /api/v1/auth/password (self-service; answers the refreshed Me).
export interface ChangePasswordRequest {
  currentPassword: string;
  newPassword: string;
}
```

- [ ] **Step 2: Typecheck**

Run: `cd ui && npm run typecheck`
Expected: PASS (origin is additive; no consumer breaks)

- [ ] **Step 3: Commit**

```bash
git add ui/src/lib/api-types.ts
git commit -m "feat(ui): mirror origin on Me + update/change-password request types"
```

---

### Task 8: Users panel — edit, reset password, delete

**Files:**
- Create: `ui/src/components/settings/user-edit-form.tsx`
- Modify: `ui/src/components/settings/users-panel.tsx`

- [ ] **Step 1: Create the edit/reset forms**

`user-edit-form.tsx` — two inline forms in the add-user-form idiom (same `inputClass`, same button row). Full content:

```tsx
"use client";

import { useState } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import type { AdminUser, AuthGrant, UpdateUserRequest } from "@/lib/api-types";

const ROLES: AuthGrant["role"][] = ["viewer", "editor", "admin"];

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

// Inline edit for name + the WHOLE grant set (the hub's PUT replaces grants
// wholesale, so the form always submits the full list). Duplicate scopes are
// refused client-side — the hub 400s them anyway, this just says it sooner.
export function EditUserForm({
  user,
  onSubmit,
  onCancel,
}: {
  user: AdminUser;
  onSubmit: (req: UpdateUserRequest) => Promise<unknown>;
  onCancel: () => void;
}) {
  const [name, setName] = useState(user.name);
  const [grants, setGrants] = useState<AuthGrant[]>(user.grants);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const setGrant = (i: number, patch: Partial<AuthGrant>) =>
    setGrants(grants.map((g, j) => (j === i ? { ...g, ...patch } : g)));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const scopes = grants.map((g) => g.scope.trim());
    if (scopes.some((s) => s === "")) {
      setError("every grant needs a scope (* or a project)");
      return;
    }
    if (new Set(scopes).size !== scopes.length) {
      setError("duplicate grant scopes");
      return;
    }
    setError(null);
    setBusy(true);
    try {
      await onSubmit({ name, grants: grants.map((g) => ({ ...g, scope: g.scope.trim() })) });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-neutral bg-base-200/40 px-4 py-3">
      <span className="text-xs font-semibold text-base-content/60">
        Edit {user.email}
      </span>
      <label className="flex flex-col gap-1 text-xs text-base-content/60 sm:max-w-xs">
        Name
        <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <span className="text-xs text-base-content/60">Grants</span>
      {grants.map((g, i) => (
        <div key={i} className="flex items-center gap-2">
          <input
            className={inputClass + " max-w-48"}
            aria-label={`Grant ${i + 1} scope`}
            value={g.scope}
            onChange={(e) => setGrant(i, { scope: e.target.value })}
            placeholder="* or project"
          />
          <select
            className={inputClass + " max-w-32"}
            aria-label={`Grant ${i + 1} role`}
            value={g.role}
            onChange={(e) => setGrant(i, { role: e.target.value as AuthGrant["role"] })}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <button
            type="button"
            aria-label={`Remove grant ${i + 1}`}
            className="rounded p-1 text-base-content/50 hover:bg-base-300"
            onClick={() => setGrants(grants.filter((_, j) => j !== i))}
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      <div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setGrants([...grants, { scope: "", role: "viewer" }])}
        >
          Add grant
        </Button>
      </div>
      {error && <p className="text-xs text-error">{error}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          Save
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// Admin password reset — a single field; the hub revokes the user's sessions
// on rotation. Never offered for SSO users (the caller hides it).
export function ResetPasswordForm({
  user,
  onSubmit,
  onCancel,
}: {
  user: AdminUser;
  onSubmit: (req: UpdateUserRequest) => Promise<unknown>;
  onCancel: () => void;
}) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await onSubmit({ password });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-neutral bg-base-200/40 px-4 py-3">
      <span className="text-xs font-semibold text-base-content/60">
        Reset password for {user.email} — their existing sessions are signed out
      </span>
      <label className="flex flex-col gap-1 text-xs text-base-content/60 sm:max-w-xs">
        New password
        <input
          className={inputClass}
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
      </label>
      {error && <p className="text-xs text-error">{error}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          Reset password
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
```

- [ ] **Step 2: Wire the panel**

`users-panel.tsx` changes:

1. Imports: add `apiDelete` to the `@/lib/api` import; add `UpdateUserRequest` to the type import; add `import { EditUserForm, ResetPasswordForm } from "./user-edit-form";`.
2. New state next to `adding`:

```tsx
  const [editingId, setEditingId] = useState<string | null>(null);
  const [resettingId, setResettingId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
```

3. New actions next to `toggleDisabled` (which stays as-is):

```tsx
  const updateUser = async (id: string, req: UpdateUserRequest) => {
    // Let ApiError propagate so the inline form renders the hub's message
    // (self-lockout, bad grants, SSO-password guard).
    await apiPut(`/api/v1/users/${id}`, req);
    setEditingId(null);
    setResettingId(null);
    reload();
  };

  // Delete is only offered on disabled rows (the hub 409s otherwise) and
  // needs a second click — same confirm idiom as the collection reset.
  const deleteUser = async (u: AdminUser) => {
    if (confirmDeleteId !== u.id) {
      setConfirmDeleteId(u.id);
      return;
    }
    setConfirmDeleteId(null);
    setActionError(null);
    setBusyId(u.id);
    try {
      await apiDelete(`/api/v1/users/${u.id}`);
      reload();
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "request failed");
    } finally {
      setBusyId(null);
    }
  };
```

4. Replace the Actions `<td>` content with (Edit for everyone; Reset password only for `origin === "local"`; Delete only when disabled):

```tsx
                  <td className="px-4 py-2.5 text-right">
                    <span className="inline-flex gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setEditingId(editingId === u.id ? null : u.id);
                          setResettingId(null);
                          setConfirmDeleteId(null);
                        }}
                        disabled={busyId === u.id}
                      >
                        Edit
                      </Button>
                      {u.origin === "local" && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setResettingId(resettingId === u.id ? null : u.id);
                            setEditingId(null);
                            setConfirmDeleteId(null);
                          }}
                          disabled={busyId === u.id}
                        >
                          Reset password
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => void toggleDisabled(u)}
                        disabled={busyId === u.id}
                      >
                        {u.disabled ? "Enable" : "Disable"}
                      </Button>
                      {u.disabled && (
                        <Button
                          variant={confirmDeleteId === u.id ? "danger" : "ghost"}
                          size="sm"
                          onClick={() => void deleteUser(u)}
                          disabled={busyId === u.id}
                        >
                          {confirmDeleteId === u.id ? "Confirm delete" : "Delete"}
                        </Button>
                      )}
                    </span>
                  </td>
```

5. Render the inline forms: the table maps `<tr>` rows — an expanding form needs its own full-width row. Change the row render to a `React.Fragment` keyed by `u.id` containing the `<tr>` plus, conditionally:

```tsx
                {(editingId === u.id || resettingId === u.id) && (
                  <tr>
                    <td colSpan={5} className="p-0">
                      {editingId === u.id ? (
                        <EditUserForm
                          user={u}
                          onSubmit={(req) => updateUser(u.id, req)}
                          onCancel={() => setEditingId(null)}
                        />
                      ) : (
                        <ResetPasswordForm
                          user={u}
                          onSubmit={(req) => updateUser(u.id, req)}
                          onCancel={() => setResettingId(null)}
                        />
                      )}
                    </td>
                  </tr>
                )}
```

(Import `Fragment` from react or use `<React.Fragment key={u.id}>`.)

Also update the file-top comment (it still says "grant one role per project scope, and enable/disable" — now: create, edit name/grants, reset passwords, enable/disable, delete-after-disable).

- [ ] **Step 3: Typecheck + lint + build**

Run: `cd ui && npm run typecheck && npm run lint && npm run build`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/settings/user-edit-form.tsx ui/src/components/settings/users-panel.tsx
git commit -m "feat(ui): users panel — edit name/grants, admin password reset, delete-after-disable"
```

---

### Task 9: Settings → Account tab (self-service password change)

**Files:**
- Create: `ui/src/components/settings/account-tab.tsx`
- Modify: `ui/src/components/settings/settings-screen.tsx`

- [ ] **Step 1: Create the tab**

`account-tab.tsx`:

```tsx
"use client";

import { useState } from "react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { apiPost, ApiError } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import type { ChangePasswordRequest, Me } from "@/lib/api-types";

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

// Self-service account surface (design/2026-08-06-users-crud-password.md).
// Local users change their own password here — the hub verifies the current
// one, evicts every other session and re-mints this one, so no re-login.
// SSO users get a pointer to their IdP instead of a form that cannot work.
export function AccountTab() {
  const { me } = useAuth();
  if (!me) return null;

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Account</CardTitle>
        <span className="text-xs text-base-content/50">{me.user.email}</span>
      </CardHeader>
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 border-t border-neutral px-4 py-3 text-sm">
        <dt className="text-base-content/50">Email</dt>
        <dd className="font-mono">{me.user.email}</dd>
        <dt className="text-base-content/50">Name</dt>
        <dd>{me.user.name || "—"}</dd>
        <dt className="text-base-content/50">Sign-in</dt>
        <dd>{me.user.origin === "oidc" ? "single sign-on" : "password"}</dd>
      </dl>
      {me.user.origin === "oidc" ? (
        <p className="border-t border-neutral px-4 py-3 text-sm text-base-content/60">
          Your password is managed by your identity provider.
        </p>
      ) : (
        <ChangePasswordForm />
      )}
    </Card>
  );
}

function ChangePasswordForm() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      setError("new passwords do not match");
      return;
    }
    setError(null);
    setDone(false);
    setBusy(true);
    try {
      const req: ChangePasswordRequest = { currentPassword: current, newPassword: next };
      await apiPost<Me>("/api/v1/auth/password", req);
      setDone(true);
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-neutral px-4 py-3">
      <span className="text-xs font-semibold text-base-content/60">Change password</span>
      <span className="text-xs text-base-content/45">
        Your other sessions are signed out; this one stays.
      </span>
      <label className="flex flex-col gap-1 text-xs text-base-content/60 sm:max-w-xs">
        Current password
        <input
          className={inputClass}
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          required
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-base-content/60 sm:max-w-xs">
        New password
        <input
          className={inputClass}
          type="password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-base-content/60 sm:max-w-xs">
        Confirm new password
        <input
          className={inputClass}
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          required
        />
      </label>
      {error && <p className="text-xs text-error">{error}</p>}
      {done && <p className="text-xs text-success">Password changed.</p>}
      <div>
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          Change password
        </Button>
      </div>
    </form>
  );
}
```

- [ ] **Step 2: Register the tab**

`settings-screen.tsx`:

1. `import { AccountTab } from "./account-tab";`
2. `const TABS = ["general", "collection", "status", "account", "users"] as const;`
3. In `SettingsScreen`, take `me` from the existing `useAuth()` call: `const { me, isAdmin } = useAuth();` and `const signedIn = me !== null && !me.user.anonymous;`
4. Fallback filter gains the account gate:

```tsx
  const tab = (TABS.find(
    (t) =>
      t === requested &&
      (t !== "users" || isAdmin) &&
      (t !== "account" || signedIn),
  ) ?? "general") as Tab;
```

5. Tab items — account before users:

```tsx
    ...(signedIn ? [{ value: "account" as const, label: "Account" }] : []),
    ...(isAdmin ? [{ value: "users" as const, label: "Users" }] : []),
```

6. Render: `{tab === "account" && <AccountTab />}`

- [ ] **Step 3: Typecheck + lint + build**

Run: `cd ui && npm run typecheck && npm run lint && npm run build`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/settings/account-tab.tsx ui/src/components/settings/settings-screen.tsx
git commit -m "feat(ui): Settings → Account tab with self-service password change"
```

---

### Task 10: Playwright spec

**Files:**
- Create: `ui/e2e/users-crud.spec.ts`

- [ ] **Step 1: Write the spec**

Route-stub style (`ingest-keys.spec.ts` is the model). Full content:

```ts
import { test, expect, type Page } from "@playwright/test";

// Users CRUD + self-service password change (design/2026-08-06-users-crud-
// password.md). Self-contained via route interception, matching
// ingest-keys.spec.ts. Properties under test: edit PUTs the WHOLE grant set,
// delete is only offered after disable (and confirmed), SSO rows hide the
// password reset, and the Account tab's happy path posts current+new.

type User = {
  id: string;
  email: string;
  name: string;
  origin: string;
  disabled: boolean;
  grants: { scope: string; role: string }[];
};

async function stubMe(page: Page, opts?: { origin?: string; admin?: boolean }) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: {
          id: "u-self",
          email: "admin@x.io",
          name: "Admin",
          origin: opts?.origin ?? "local",
          anonymous: false,
        },
        grants: [{ scope: "*", role: opts?.admin === false ? "viewer" : "admin" }],
      },
    }),
  );
}

// Mutable in-memory user list: PUT edits, DELETE removes (only when the
// stored row is disabled — mirrors the hub's 409 contract).
async function stubUsers(page: Page, initial: User[]) {
  const users = [...initial];
  const calls = { puts: [] as { id: string; body: Record<string, unknown> }[], deletes: [] as string[] };

  await page.route("**/api/v1/users/*", async (route) => {
    const id = decodeURIComponent(route.request().url().split("/").pop() as string);
    const i = users.findIndex((u) => u.id === id);
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as Record<string, unknown>;
      calls.puts.push({ id, body });
      if (i >= 0) {
        const u = users[i];
        if (typeof body.name === "string") u.name = body.name;
        if (typeof body.disabled === "boolean") u.disabled = body.disabled;
        if (Array.isArray(body.grants)) u.grants = body.grants as User["grants"];
      }
      return route.fulfill({ json: users[i] });
    }
    if (route.request().method() === "DELETE") {
      if (i >= 0 && !users[i].disabled) {
        return route.fulfill({
          status: 409,
          json: { error: { message: "disable the user before deleting" } },
        });
      }
      calls.deletes.push(id);
      if (i >= 0) users.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fallback();
  });
  await page.route("**/api/v1/users", (route) => route.fulfill({ json: { users } }));
  return calls;
}

const DEV: User = {
  id: "u-dev",
  email: "dev@x.io",
  name: "Dev",
  origin: "local",
  disabled: false,
  grants: [{ scope: "payments", role: "editor" }],
};
const SSO: User = {
  id: "oidc|s1",
  email: "sso@x.io",
  name: "Sso",
  origin: "oidc",
  disabled: false,
  grants: [],
};

test.describe("users CRUD", () => {
  test("edit replaces the whole grant set in one PUT", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [DEV]);

    await page.goto("/settings?tab=users");
    await page.getByRole("button", { name: "Edit" }).click();
    await page.getByRole("button", { name: "Add grant" }).click();
    await page.getByLabel("Grant 2 scope").fill("checkout");
    await page.getByLabel("Grant 2 role").selectOption("admin");
    await page.getByRole("button", { name: "Save" }).click();

    await expect.poll(() => calls.puts.length).toBe(1);
    expect(calls.puts[0].body).toEqual({
      name: "Dev",
      grants: [
        { scope: "payments", role: "editor" },
        { scope: "checkout", role: "admin" },
      ],
    });
    await expect(page.getByText("admin@checkout")).toBeVisible();
  });

  test("duplicate grant scopes are refused before any request", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [DEV]);

    await page.goto("/settings?tab=users");
    await page.getByRole("button", { name: "Edit" }).click();
    await page.getByRole("button", { name: "Add grant" }).click();
    await page.getByLabel("Grant 2 scope").fill("payments");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("duplicate grant scopes")).toBeVisible();
    expect(calls.puts).toHaveLength(0);
  });

  test("SSO rows hide the password reset; local rows offer it", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [DEV, SSO]);

    await page.goto("/settings?tab=users");
    // One reset button (the local user), not two.
    await expect(page.getByRole("button", { name: "Reset password" })).toHaveCount(1);

    await page.getByRole("button", { name: "Reset password" }).click();
    await page.getByLabel("New password").fill("rotated-pw");
    await page.getByRole("button", { name: "Reset password" }).last().click();
    await expect.poll(() => calls.puts.length).toBe(1);
    expect(calls.puts[0]).toEqual({ id: "u-dev", body: { password: "rotated-pw" } });
  });

  test("delete appears only after disable and needs a confirm click", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [{ ...DEV }]);

    await page.goto("/settings?tab=users");
    await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);

    await page.getByRole("button", { name: "Disable" }).click();
    await expect(page.getByRole("button", { name: "Delete" })).toBeVisible();

    await page.getByRole("button", { name: "Delete" }).click();
    expect(calls.deletes).toHaveLength(0);
    await page.getByRole("button", { name: "Confirm delete" }).click();
    await expect.poll(() => calls.deletes.length).toBe(1);
    await expect(page.getByText("dev@x.io")).toHaveCount(0);
  });
});

test.describe("account tab", () => {
  test("local user changes their password", async ({ page }) => {
    await stubMe(page);
    await stubUsers(page, []);
    const posts: Record<string, unknown>[] = [];
    await page.route("**/api/v1/auth/password", (route) => {
      posts.push(route.request().postDataJSON() as Record<string, unknown>);
      return route.fulfill({
        json: {
          user: { id: "u-self", email: "admin@x.io", name: "Admin", origin: "local", anonymous: false },
          grants: [{ scope: "*", role: "admin" }],
        },
      });
    });

    await page.goto("/settings?tab=account");
    await page.getByLabel("Current password").fill("old-pw");
    await page.getByLabel("New password", { exact: true }).fill("new-pw");
    await page.getByLabel("Confirm new password").fill("new-pw");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("Password changed.")).toBeVisible();
    expect(posts).toEqual([{ currentPassword: "old-pw", newPassword: "new-pw" }]);
  });

  test("mismatched confirmation never leaves the browser", async ({ page }) => {
    await stubMe(page);
    let hit = false;
    await page.route("**/api/v1/auth/password", (route) => {
      hit = true;
      return route.fulfill({ status: 500 });
    });
    await page.goto("/settings?tab=account");
    await page.getByLabel("Current password").fill("old-pw");
    await page.getByLabel("New password", { exact: true }).fill("a");
    await page.getByLabel("Confirm new password").fill("b");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("new passwords do not match")).toBeVisible();
    expect(hit).toBe(false);
  });

  test("SSO user sees the IdP note, no form", async ({ page }) => {
    await stubMe(page, { origin: "oidc", admin: false });
    await page.goto("/settings?tab=account");
    await expect(
      page.getByText("Your password is managed by your identity provider."),
    ).toBeVisible();
    await expect(page.getByLabel("Current password")).toHaveCount(0);
  });
});
```

- [ ] **Step 2: Run against a stub-only dev server**

Port 3000/3001 are taken on this machine — use 3005:

```bash
cd ui && (npx next dev -p 3005 > /tmp/next-dev-3005.log 2>&1 &) && sleep 8
AVURUOBS_BASE_URL=http://localhost:3005 npx playwright test e2e/users-crud.spec.ts
```

Expected: 7 passed. Fix locator/copy drift here, not by weakening assertions. Kill the server after: `lsof -ti :3005 | xargs kill`.

- [ ] **Step 3: Commit**

```bash
git add ui/e2e/users-crud.spec.ts
git commit -m "test(ui): users CRUD + account tab e2e (route-stubbed)"
```

---

### Task 11: Changelog, AEP roadmap, verification sweep

**Files:**
- Modify: `CHANGELOG.md` (Unreleased)
- Modify: `design/2026-08-06-users-crud-password.md` (roadmap ticks)

- [ ] **Step 1: Changelog**

Add an `### Added` section under `## [Unreleased]` (before the existing `### Fixed`):

```markdown
### Added

- **Full user management from the UI.** Settings → Users now edits a user's
  name and role grants, resets passwords (with every session of the affected
  user signed out), and **deletes** users — an explicit second step available
  only after disabling, amending the original disable-only decision
  (design/2026-08-06-users-crud-password.md). A new Settings → Account tab
  lets any signed-in local user change their own password (current password
  required; other sessions are evicted, the active one stays). Password
  operations are refused for SSO users — their credential lives at the
  identity provider.
```

- [ ] **Step 2: Tick the AEP roadmap**

In `design/2026-08-06-users-crud-password.md`, mark every completed roadmap item `[x]` (leave `docs-align` unticked — it runs at merge per the docs-alignment procedure).

- [ ] **Step 3: Full verification sweep**

```bash
cd hub && go build ./... && go vet ./... && go test ./... && golangci-lint run
cd ../ui && npm run typecheck && npm run lint && npm run build
cd ../hub && TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./internal/storage/clickhouse/ -run TestDeleteAuthUser -v
```

Expected: everything green. `golangci-lint` is mandatory before push on this repo (build/vet alone is not enough).

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md design/2026-08-06-users-crud-password.md
git commit -m "docs: changelog + roadmap ticks for users CRUD completion"
```

---

## Out of scope for this plan (tracked, not forgotten)

- **docs-align (EN/FR)** — runs when this merges, per the docs-alignment procedure/skill.
- **kind e2e against a real hub** — the compose-suite `settings.spec.ts` continues to cover the read-only path; a live-hub pass of the new flows can ride the next compose-suite run (`make e2e-ui` destroys the shared stack — check `docker compose ls` first, run under `-p avuru-obs-e2e`).
- **PR + merge** — user-gated; branch is `feature/users-crud-password`, no `Co-Authored-By` trailer on any commit.
