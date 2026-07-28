//go:build e2e

// Auth + project isolation against the compose stack: the AEP's core story —
// a user granted only project B gets 403 and a filtered switcher for project A.
package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAuthProjectIsolation(t *testing.T) {
	admin, err := loginAs("admin", adminPassword)
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}

	// Anonymous is 401 (no anonymous mode in the e2e stack).
	resp, err := http.Get(hubURL + "/api/v1/services")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d, want 401", resp.StatusCode)
	}

	// Admin creates a staging-only viewer.
	resp, err = admin.Post(hubURL+"/api/v1/users", "application/json",
		strings.NewReader(`{"email":"viewer@e2e","password":"viewer-pw",
			"grants":[{"scope":"staging","role":"viewer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		// Conflict: viewer@e2e already exists from a prior run against a
		// persistent stack — fine, the password/grants below still hold
		// only if this is the same fixture; treat non-fresh stacks honestly.
		t.Fatalf("create viewer: %d body %s", resp.StatusCode, body)
	}

	viewer, err := loginAs("viewer@e2e", "viewer-pw")
	if err != nil {
		t.Fatalf("viewer login: %v", err)
	}

	// Granted project works; default project is 403.
	for _, tc := range []struct {
		tenant string
		want   int
	}{
		{"staging", http.StatusOK},
		{"", http.StatusForbidden},        // resolves to "default" — not granted
		{"default", http.StatusForbidden}, // explicit
	} {
		req, _ := http.NewRequest("GET",
			hubURL+"/api/v1/services?start="+queryStart()+"&end="+queryEnd(), nil)
		if tc.tenant != "" {
			req.Header.Set("X-Avuru-Tenant", tc.tenant)
		}
		r, err := viewer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Fatalf("viewer tenant %q: %d, want %d", tc.tenant, r.StatusCode, tc.want)
		}
	}

	// The projects switcher lists only the granted project.
	req, _ := http.NewRequest("GET", hubURL+"/api/v1/projects", nil)
	req.Header.Set("X-Avuru-Tenant", "staging")
	r, err := viewer.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "staging") || strings.Contains(s, `"default"`) {
		t.Fatalf("viewer projects list leaks: %s", s)
	}

	// Viewer cannot create users.
	resp, err = viewer.Post(hubURL+"/api/v1/users", "application/json",
		strings.NewReader(`{"email":"x@x","password":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer creating user: %d, want 403", resp.StatusCode)
	}
}
