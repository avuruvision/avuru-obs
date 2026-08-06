package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// adminMux bootstraps the real admin user and returns its session cookie.
func adminMux(t *testing.T) (*http.ServeMux, *http.Cookie, *storagetest.Fake) {
	t.Helper()
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "root-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc})
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}, f
}

func doBody(mux *http.ServeMux, method, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestCreateListUpdateUser(t *testing.T) {
	mux, c, f := adminMux(t)

	// Create.
	w := doBody(mux, "POST", "/api/v1/users", c, `{
		"email":"dev@x.io","name":"Dev","password":"devpw",
		"grants":[{"scope":"payments","role":"editor"}]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Duplicate email is rejected — 409 Conflict (handleCreateAlertChannel's
	// precedent for admin-authored uniqueness conflicts), not 400.
	if w := doBody(mux, "POST", "/api/v1/users", c, `{"email":"dev@x.io","password":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("duplicate email: %d, want 409", w.Code)
	}

	// List includes both users with grants.
	w = authDo(mux, "GET", "/api/v1/users", c, nil)
	var list struct {
		Users []struct {
			Email  string `json:"email"`
			Grants []struct {
				Scope string `json:"scope"`
			} `json:"grants"`
		} `json:"users"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Users) != 2 {
		t.Fatalf("list: %+v", list)
	}

	// Update: disable + change grants.
	w = doBody(mux, "PUT", "/api/v1/users/"+created.ID, c,
		`{"disabled":true,"grants":[{"scope":"demo","role":"viewer"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body %s", w.Code, w.Body.String())
	}
	if u := f.Users[created.ID]; !u.Disabled {
		t.Fatal("user not disabled")
	}
	if g := f.Grants[created.ID]; len(g) != 1 || g[0].Scope != "demo" {
		t.Fatalf("grants after update: %+v", g)
	}
}

func TestInvalidRoleRejected(t *testing.T) {
	mux, c, _ := adminMux(t)
	w := doBody(mux, "POST", "/api/v1/users",
		c, `{"email":"z@x.io","password":"x","grants":[{"scope":"p","role":"superuser"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad role: %d, want 400", w.Code)
	}
}

func TestUsersRoutesAdminOnly(t *testing.T) {
	mux, c := authedMux(t) // editor-on-payments, not admin
	if w := authDo(mux, "GET", "/api/v1/users", c, nil); w.Code != http.StatusForbidden {
		t.Fatalf("editor listing users: %d, want 403", w.Code)
	}
	if w := doBody(mux, "POST", "/api/v1/users", c, `{"email":"x@x","password":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("editor creating user: %d, want 403", w.Code)
	}
}

func TestUpdateUnknownUserIs404(t *testing.T) {
	mux, c, _ := adminMux(t)
	if w := doBody(mux, "PUT", "/api/v1/users/nope", c, `{"disabled":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d, want 404", w.Code)
	}
}

// TestUpdateInvalidGrantDoesNotPersist proves handleUpdateUser validates the
// WHOLE request before writing anything: a password change bundled with an
// invalid grant role must not half-apply — the password rotation must not
// reach SaveAuthUser just because the grants failed validation afterward.
func TestUpdateInvalidGrantDoesNotPersist(t *testing.T) {
	mux, c, f := adminMux(t)

	w := doBody(mux, "POST", "/api/v1/users", c, `{"email":"grantfail@x.io","password":"pw"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	savedBefore := len(f.SavedUsers)

	w = doBody(mux, "PUT", "/api/v1/users/"+created.ID, c,
		`{"password":"newpw","grants":[{"scope":"p","role":"superuser"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid grant role on update: %d, want 400", w.Code)
	}
	if len(f.SavedUsers) != savedBefore {
		t.Fatalf("SavedUsers changed despite validation failure: before %d, after %d", savedBefore, len(f.SavedUsers))
	}
}

// TestSelfLockoutGuard proves an admin cannot disable their own account or
// strip their own global-admin grant through this API (both would be
// unrecoverable without DB surgery — no other admin left to undo it), while
// editing an unrelated field on their own record still works.
func TestSelfLockoutGuard(t *testing.T) {
	mux, c, _ := adminMux(t)

	me := authDo(mux, "GET", "/api/v1/auth/me", c, nil)
	var meResp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meResp); err != nil {
		t.Fatal(err)
	}
	adminID := meResp.User.ID

	if w := doBody(mux, "PUT", "/api/v1/users/"+adminID, c, `{"disabled":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("admin disabling self: %d, want 400", w.Code)
	}
	if w := doBody(mux, "PUT", "/api/v1/users/"+adminID, c,
		`{"grants":[{"scope":"payments","role":"viewer"}]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("admin de-adminning self via grants: %d, want 400", w.Code)
	}
	if w := doBody(mux, "PUT", "/api/v1/users/"+adminID, c, `{"name":"New Name"}`); w.Code != http.StatusOK {
		t.Fatalf("admin updating own name: %d, want 200 body %s", w.Code, w.Body.String())
	}
}

// TestPasswordRotationRevokesSessions proves a password reset via PUT ends
// the account's existing session immediately — the entire point of an
// incident password reset (see handleUpdateUser's password branch): a
// disable-then-re-enable cycle must not silently revive a stolen cookie.
func TestPasswordRotationRevokesSessions(t *testing.T) {
	mux, adminCookie, f := adminMux(t)

	w := doBody(mux, "POST", "/api/v1/users", adminCookie, `{"email":"rotate@x.io","password":"oldpw"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// The new user logs in and gets a session. A separate *auth.Service
	// instance is fine here — it shares the same underlying fake store, so
	// the session it creates is visible through mux.
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	token, _, err := svc.Login(context.Background(), "rotate@x.io", "oldpw", "test")
	if err != nil {
		t.Fatalf("login as new user: %v", err)
	}
	userCookie := &http.Cookie{Name: sessionCookieName, Value: token}
	if w := authDo(mux, "GET", "/api/v1/auth/me", userCookie, nil); w.Code != http.StatusOK {
		t.Fatalf("me before rotation: %d", w.Code)
	}

	if w := doBody(mux, "PUT", "/api/v1/users/"+created.ID, adminCookie, `{"password":"newpw"}`); w.Code != http.StatusOK {
		t.Fatalf("update password: %d body %s", w.Code, w.Body.String())
	}

	if w := authDo(mux, "GET", "/api/v1/auth/me", userCookie, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("me after rotation: %d, want 401 (old session must be revoked)", w.Code)
	}
}

// TestDuplicateGrantScopeRejected pins validGrants' duplicate-scope check:
// the fake and the real ClickHouse ReplaceAuthGrants would disagree on which
// of two same-scope grants wins, so the request is rejected outright.
func TestDuplicateGrantScopeRejected(t *testing.T) {
	mux, c, _ := adminMux(t)
	w := doBody(mux, "POST", "/api/v1/users", c,
		`{"email":"dupscope@x.io","password":"x","grants":[{"scope":"payments","role":"editor"},{"scope":"payments","role":"viewer"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate grant scope: %d, want 400", w.Code)
	}
}

// TestCreateUserPasswordTooLongIs400 pins bcrypt.ErrPasswordTooLong -> 400: a
// regression that let this fall through to the generic 500 must fail here.
func TestCreateUserPasswordTooLongIs400(t *testing.T) {
	mux, c, _ := adminMux(t)
	longPw := strings.Repeat("a", 73) // bcrypt caps at 72 bytes
	w := doBody(mux, "POST", "/api/v1/users", c,
		`{"email":"long@x.io","password":"`+longPw+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("73-byte password: %d, want 400", w.Code)
	}
}

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
