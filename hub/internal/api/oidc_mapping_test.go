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

// oidcMappingMux is adminMux plus a seeded auth.MappingCache — merged from the
// caller's chart-declared rules and whatever is in the fake store — so the
// tests exercise the same cache the write handlers refresh, not a fake of
// their own.
func oidcMappingMux(t *testing.T, f *storagetest.Fake, declared ...auth.GroupMap) (*http.ServeMux, *http.Cookie, *auth.MappingCache) {
	t.Helper()
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "root-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	cache := auth.NewMappingCache(func() storage.Store { return f })
	cache.SetConfig(auth.MappingConfig{Mapping: declared})
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc, OIDCMapping: cache})
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}, cache
}

func listOIDCMapping(t *testing.T, mux *http.ServeMux, c *http.Cookie) []oidcMappingDTO {
	t.Helper()
	w := doBody(mux, http.MethodGet, "/api/v1/auth/oidc/mapping", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d body %s", w.Code, w.Body.String())
	}
	var resp oidcMappingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Rules
}

// The routes only exist on an install that actually has OIDC configured —
// with the cache unwired (cfg.OIDCMapping == nil, exactly like an install
// with no AVURUOBS_AUTH_OIDC_CONFIG mounted), they must 404, not answer with
// some degraded "not configured" body.
func TestOIDCMappingRoutesNotRegisteredWithoutOIDC(t *testing.T) {
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "root-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Cookie{Name: sessionCookieName, Value: token}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc})

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/oidc/mapping"},
		{http.MethodPut, "/api/v1/auth/oidc/mapping/oncall"},
		{http.MethodDelete, "/api/v1/auth/oidc/mapping/oncall"},
		{http.MethodPost, "/api/v1/auth/oidc/mapping/reset"},
	} {
		if w := doBody(mux, tc.method, tc.path, c, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (OIDC not configured)", tc.method, tc.path, w.Code)
		}
	}
}

// The list merges the chart-declared half with the authored half, tagged
// exactly like auth.MergeMapping already pins: config read-only, db editable.
func TestOIDCMappingList(t *testing.T) {
	f := &storagetest.Fake{OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
		"oncall": {Group: "oncall", Role: "editor", Projects: []string{"default"}},
	}}
	mux, c, _ := oidcMappingMux(t, f, auth.GroupMap{Group: "platform", Role: auth.RoleAdmin, Projects: []string{"*"}})

	rules := listOIDCMapping(t, mux, c)
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want 2", rules)
	}
	byKey := map[string]oidcMappingDTO{}
	for _, r := range rules {
		byKey[r.Group+"|"+r.Source] = r
	}
	if r := byKey["platform|config"]; r.Editable || r.Shadowed || r.Role != "admin" {
		t.Errorf("config rule = %+v, want read-only admin", r)
	}
	if r := byKey["oncall|db"]; !r.Editable || r.Shadowed || r.Role != "editor" {
		t.Errorf("db rule = %+v, want editable editor", r)
	}
}

// PUT upserts an authored rule and, per the plan, calls Refresh so the very
// next read on THIS replica reflects it — no waiting for the 15s poll.
func TestOIDCMappingPutUpsertRefreshesImmediately(t *testing.T) {
	f := &storagetest.Fake{}
	mux, c, cache := oidcMappingMux(t, f)

	w := doBody(mux, http.MethodPut, "/api/v1/auth/oidc/mapping/oncall", c,
		`{"role":"editor","projects":["default"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d body %s", w.Code, w.Body.String())
	}
	var got oidcMappingDTO
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Group != "oncall" || got.Role != "editor" || got.Source != "db" || !got.Editable {
		t.Fatalf("put response = %+v", got)
	}

	if f.OIDCGroupMappings["oncall"].Role != "editor" {
		t.Fatalf("stored row = %+v, want role editor", f.OIDCGroupMappings["oncall"])
	}
	// Proves the handler actually called Refresh, not just saved: the cache's
	// own snapshot — not a re-read through the API — already carries the new
	// grant before this test does anything else.
	grants := cache.Mapper()([]string{"oncall"})
	if len(grants) != 1 || grants[0].Scope != "default" || grants[0].Role != auth.RoleEditor {
		t.Fatalf("cache grants after PUT = %+v, want one editor/default grant", grants)
	}

	rules := listOIDCMapping(t, mux, c)
	if len(rules) != 1 || rules[0].Group != "oncall" {
		t.Fatalf("list after put = %+v", rules)
	}
}

// A group the chart also declares is a legitimate PUT target — it lands
// Shadowed, per auth.MergeMapping, not refused outright the way a
// chart-declared service-group name is.
func TestOIDCMappingPutShadowsDeclaredGroup(t *testing.T) {
	f := &storagetest.Fake{}
	mux, c, _ := oidcMappingMux(t, f, auth.GroupMap{Group: "platform", Role: auth.RoleAdmin, Projects: []string{"*"}})

	w := doBody(mux, http.MethodPut, "/api/v1/auth/oidc/mapping/platform", c,
		`{"role":"viewer","projects":["default"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d body %s", w.Code, w.Body.String())
	}
	var got oidcMappingDTO
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Shadowed {
		t.Fatalf("put response = %+v, want shadowed", got)
	}

	rules := listOIDCMapping(t, mux, c)
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want config row + shadowed db row", rules)
	}
}

// An invalid role is a 400 via auth.ParseRole, not a stored row with a role
// nothing can ever match.
func TestOIDCMappingPutInvalidRoleIs400(t *testing.T) {
	f := &storagetest.Fake{}
	mux, c, _ := oidcMappingMux(t, f)

	w := doBody(mux, http.MethodPut, "/api/v1/auth/oidc/mapping/oncall", c, `{"role":"wizard","projects":["default"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("put invalid role: %d body %s, want 400", w.Code, w.Body.String())
	}
	if _, ok := f.OIDCGroupMappings["oncall"]; ok {
		t.Fatal("invalid role must not be stored")
	}
}

// Deleting a chart-declared group is a 400 that says where it actually lives,
// not a silent no-op that leaves the admin watching "platform" reappear on
// the next read because the chart keeps contributing it regardless.
func TestOIDCMappingDeleteDeclaredGroupIs400(t *testing.T) {
	f := &storagetest.Fake{}
	mux, c, _ := oidcMappingMux(t, f, auth.GroupMap{Group: "platform", Role: auth.RoleAdmin, Projects: []string{"*"}})

	w := doBody(mux, http.MethodDelete, "/api/v1/auth/oidc/mapping/platform", c, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete declared: %d body %s, want 400", w.Code, w.Body.String())
	}
	var body errorBody
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Message == "" {
		t.Fatal("error message must say where the group actually lives")
	}

	rules := listOIDCMapping(t, mux, c)
	if len(rules) != 1 || rules[0].Source != "config" {
		t.Fatalf("rules after refused delete = %+v, want the config row untouched", rules)
	}
}

// An unknown, never-declared group is a plain 404 — distinct from the 400
// above, which is specifically about a name the chart owns.
func TestOIDCMappingDeleteUnknownIs404(t *testing.T) {
	f := &storagetest.Fake{}
	mux, c, _ := oidcMappingMux(t, f)

	w := doBody(mux, http.MethodDelete, "/api/v1/auth/oidc/mapping/ghost", c, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: %d, want 404", w.Code)
	}
}

// DELETE tombstones an authored rule and refreshes the cache immediately.
func TestOIDCMappingDeleteRemovesRowAndRefreshes(t *testing.T) {
	f := &storagetest.Fake{OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
		"oncall": {Group: "oncall", Role: "editor", Projects: []string{"default"}},
	}}
	mux, c, cache := oidcMappingMux(t, f)
	if len(cache.Snapshot()) != 1 {
		t.Fatalf("seed snapshot = %+v, want 1 row", cache.Snapshot())
	}

	w := doBody(mux, http.MethodDelete, "/api/v1/auth/oidc/mapping/oncall", c, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d body %s", w.Code, w.Body.String())
	}
	if _, ok := f.OIDCGroupMappings["oncall"]; ok {
		t.Fatal("row still stored after delete")
	}
	if len(cache.Snapshot()) != 0 {
		t.Fatalf("cache snapshot after delete = %+v, want empty", cache.Snapshot())
	}
}

// POST /reset clears every authored rule and leaves the chart-declared ones
// exactly as they were.
func TestOIDCMappingReset(t *testing.T) {
	f := &storagetest.Fake{OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
		"oncall":  {Group: "oncall", Role: "editor", Projects: []string{"default"}},
		"support": {Group: "support", Role: "viewer", Projects: []string{"default"}},
	}}
	mux, c, cache := oidcMappingMux(t, f, auth.GroupMap{Group: "platform", Role: auth.RoleAdmin, Projects: []string{"*"}})

	w := doBody(mux, http.MethodPost, "/api/v1/auth/oidc/mapping/reset", c, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("reset: %d body %s", w.Code, w.Body.String())
	}
	if !f.ResetOIDCGroupMappingsCalled {
		t.Fatal("store's ResetOIDCGroupMappings not called")
	}

	rules := listOIDCMapping(t, mux, c)
	if len(rules) != 1 || rules[0].Source != "config" {
		t.Fatalf("rules after reset = %+v, want only the config row", rules)
	}
	if len(cache.Snapshot()) != 1 {
		t.Fatalf("cache snapshot after reset = %+v, want only the config row", cache.Snapshot())
	}
}

// A DB row whose Role fails ParseRole is dropped by auth.MergeMapping (Task
// 2's deliberate decision — see mapping.go) and so is invisible to a list
// built only on the cache's Snapshot(). The list handler must ALSO read the
// raw store rows and surface such a row flagged invalid, or it could never be
// seen or deleted and would sit in the table forever. PUT itself refuses an
// invalid role, so this can only arrive by a direct DB edit or a role retired
// in a later version — rare, but not a reason to leave it unreachable.
func TestOIDCMappingListSurfacesInvalidRoleRow(t *testing.T) {
	f := &storagetest.Fake{OIDCGroupMappings: map[string]storage.OIDCGroupMapping{
		"legacy": {Group: "legacy", Role: "wizard", Projects: []string{"default"}},
	}}
	mux, c, cache := oidcMappingMux(t, f)

	// MergeMapping really did drop it — confirms the premise of the test.
	if got := cache.Snapshot(); len(got) != 0 {
		t.Fatalf("merged snapshot = %+v, want the invalid row dropped", got)
	}

	rules := listOIDCMapping(t, mux, c)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want the invalid row surfaced", rules)
	}
	r := rules[0]
	if r.Group != "legacy" || !r.Invalid || r.InvalidRole != "wizard" || !r.Editable {
		t.Fatalf("invalid row = %+v, want group legacy, invalid, invalidRole wizard, editable", r)
	}

	// And it can be deleted like any other authored row — the whole point of
	// surfacing it.
	if w := doBody(mux, http.MethodDelete, "/api/v1/auth/oidc/mapping/legacy", c, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete invalid row: %d body %s", w.Code, w.Body.String())
	}
	if len(listOIDCMapping(t, mux, c)) != 0 {
		t.Fatal("invalid row still listed after delete")
	}
}

// Every route here is admin-gated: this configures WHO gets WHAT role, which
// is more sensitive than the service-health groupings a viewer may read.
func TestOIDCMappingWritesAndReadsAreAdminOnly(t *testing.T) {
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	cache := auth.NewMappingCache(func() storage.Store { return f })
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth:              svc,
		AnonymousIdentity: viewerAnywhere(),
		OIDCMapping:       cache,
	})

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/auth/oidc/mapping", ""},
		{http.MethodPut, "/api/v1/auth/oidc/mapping/oncall", `{"role":"editor","projects":["default"]}`},
		{http.MethodDelete, "/api/v1/auth/oidc/mapping/oncall", ""},
		{http.MethodPost, "/api/v1/auth/oidc/mapping/reset", ""},
	} {
		if w := doBody(mux, tc.method, tc.path, nil, tc.body); w.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d, want 403 (body %s)", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
