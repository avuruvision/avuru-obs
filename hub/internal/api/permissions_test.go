package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// adminMuxWithConfigFor is adminMux over a caller-supplied fake, with the
// Config tweaked before Register — for routes and fields that only exist when
// an install opts into them.
func adminMuxWithConfigFor(t *testing.T, f *storagetest.Fake, tweak func(*Config)) (*http.ServeMux, *http.Cookie) {
	t.Helper()
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "root-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Auth: svc}
	if tweak != nil {
		tweak(&cfg)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, cfg)
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}
}

// adminMuxRuntimeCollection is adminMux with the default-off collection
// runtime control switched on, so its routes actually register.
func adminMuxRuntimeCollection(t *testing.T) (*http.ServeMux, *http.Cookie) {
	t.Helper()
	return adminMuxWithConfigFor(t, &storagetest.Fake{}, func(cfg *Config) {
		cfg.CollectionRuntimeControlEnabled = true
	})
}

// viewerAnywhere is a wildcard-viewer anonymous identity — the cheapest way to
// hold exactly one role in a test.
func viewerAnywhere() *auth.Identity {
	return &auth.Identity{Name: "Anonymous", Anonymous: true,
		Grants: []auth.Grant{{Scope: "*", Role: auth.RoleViewer}}}
}

func permissions(t *testing.T, mux *http.ServeMux, c *http.Cookie) permissionsResponse {
	t.Helper()
	w := doBody(mux, http.MethodGet, "/api/v1/auth/permissions", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", w.Code, w.Body.String())
	}
	var resp permissionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func areaByLabel(resp permissionsResponse, label string) (permissionAreaDTO, bool) {
	for _, a := range resp.Areas {
		if a.Label == label {
			return a, true
		}
	}
	return permissionAreaDTO{}, false
}

// The matrix must report the floors the middleware really applies. These are
// spot checks across the three shapes: read-only signal, editor write, and
// admin-only configuration.
func TestPermissionsMatrixReportsRealGuards(t *testing.T) {
	mux, c, _ := adminMux(t)
	resp := permissions(t, mux, c)

	if len(resp.Roles) != 3 {
		t.Fatalf("roles = %+v, want admin/editor/viewer", resp.Roles)
	}
	if !resp.AuthEnabled {
		t.Fatal("authEnabled = false on a mux with an auth service")
	}

	cases := []struct{ label, read, write string }{
		{"Traces", "viewer", ""},          // read-only signal
		{"Energy & carbon", "viewer", ""}, // was public until the green routes were fixed
		{"Error tracking", "viewer", "editor"},
		{"Projects", "viewer", "admin"},
		{"Users", "admin", "admin"},
		{"System status", "admin", ""},
	}
	for _, tc := range cases {
		got, ok := areaByLabel(resp, tc.label)
		if !ok {
			t.Errorf("%q missing from the matrix", tc.label)
			continue
		}
		if got.Read != tc.read || got.Write != tc.write {
			t.Errorf("%s = read %q write %q, want read %q write %q",
				tc.label, got.Read, got.Write, tc.read, tc.write)
		}
	}
}

// Nothing the caller cannot be granted belongs in a permissions matrix: the
// login endpoints, /auth/me and /healthz are reachable without any role, and
// listing them would read as "viewer can log in" — a permission that does not
// exist.
func TestPermissionsMatrixOmitsUngrantableRoutes(t *testing.T) {
	mux, c, _ := adminMux(t)
	for _, a := range permissions(t, mux, c).Areas {
		if a.Area == "auth" || a.Area == "healthz" {
			t.Errorf("%q should not appear: it is not a granted permission", a.Area)
		}
	}
}

// The whole point of deriving instead of hand-writing: a route registered with
// a guard is in the matrix without anyone remembering to add it. Routes gated
// off (collection runtime control, default-off) are correctly absent, and turn
// up as soon as the install enables them.
func TestPermissionsMatrixFollowsRouteRegistration(t *testing.T) {
	mux, c, _ := adminMux(t)
	if _, ok := areaByLabel(permissions(t, mux, c), "Collection"); ok {
		t.Fatal("Collection listed although runtime control is off")
	}

	onMux, onCookie := adminMuxRuntimeCollection(t)
	got, ok := areaByLabel(permissions(t, onMux, onCookie), "Collection")
	if !ok {
		t.Fatal("Collection missing once runtime control is enabled")
	}
	if got.Read != "admin" || got.Write != "admin" {
		t.Fatalf("Collection = read %q write %q, want admin/admin", got.Read, got.Write)
	}
}

// An unlabeled area still appears, under its raw path segment. A missing label
// must degrade to "shown with an ugly name", never to "silently absent" — the
// second failure mode would understate what a role can reach.
func TestPermissionsMatrixLabelsFallBackToTheSegment(t *testing.T) {
	for _, a := range []string{"traces", "users", "green"} {
		if _, ok := areaLabels[a]; !ok {
			t.Errorf("areaLabels missing %q", a)
		}
	}
	if got, ok := areaLabels["definitely-not-an-area"]; ok {
		t.Fatalf("unexpected label %q", got)
	}
}

// An install running without authentication says so, rather than presenting a
// matrix that looks enforced when every caller is effectively an admin.
func TestPermissionsMatrixReportsAuthDisabled(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, Config{})
	resp := permissions(t, mux, nil)
	if resp.AuthEnabled {
		t.Fatal("authEnabled = true with no auth service configured")
	}
	if len(resp.Areas) == 0 {
		t.Fatal("no areas: the matrix should still describe what would apply")
	}
}
