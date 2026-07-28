//go:build e2e

// Auth helpers for the compose e2e suite: the hub has auth enabled by
// default (Task 8), so every legacy test now needs a session. TestMain
// (dropin_test.go) logs in as admin once and swaps apiClient; loginAs is
// also used directly by auth_test.go to mint the project-scoped viewer
// session for the isolation story.
package e2e

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

// adminPassword mirrors AVURUOPS_AUTH_ADMIN_PASSWORD as set by the
// Makefile's `e2e` target — a fixed, known password so the harness can log
// in deterministically (the normal dev path leaves it empty/generated).
const adminPassword = "e2e-admin-pw"

// loginAs authenticates against the hub and returns a client whose cookie jar
// carries the session.
func loginAs(email, password string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Post(hubURL+"/api/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login as %s: %s", email, resp.Status)
	}
	return client, nil
}
