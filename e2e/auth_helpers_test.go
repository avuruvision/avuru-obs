//go:build e2e || e2ehelm

// Auth helpers shared by both e2e suites: the hub has auth enabled by
// default (Task 8), so every legacy test now needs a session. In the compose
// suite TestMain (dropin_test.go) logs in as admin once and swaps apiClient,
// and auth_test.go uses loginAs directly to mint the project-scoped viewer
// session for the isolation story. The kind/helm suite (helm_test.go, build
// tag e2ehelm) reuses loginAs the same way — the chart is installed with
// --set auth.adminPassword=e2e-admin-pw, matching adminPassword below.
// loginAs targets the package-level hubURL, which each suite defines for its
// own port (compose: dropin_test.go; helm: helm_test.go).
package e2e

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

// adminPassword mirrors AVURUOPS_AUTH_ADMIN_PASSWORD as pinned by both
// harnesses — the Makefile's `e2e` target (compose) and e2e-helm.sh's
// `--set auth.adminPassword=e2e-admin-pw` (kind/helm) — a fixed, known
// password so the suites can log in deterministically (the normal dev path
// leaves it empty/generated).
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
