//go:build e2ehelm

// Helm install smoke: asserts the chart serves a working OTLP backend.
// Driven by deploy/helm/e2e-helm.sh (kind up → helm install → seed → test).
// Assertions use ONLY the deterministic seeded fixture, via port-forwarded
// hub on localhost:8080.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// The port-forwarded hub, env-overridable (a dev machine may have :8080 taken).
// deploy/helm/e2e-helm.sh forwards svc/avuruops-hub → :8080 and exports the
// matching URL; loginAs (auth_helpers_test.go) targets hubURL.
var hubURL = func() string {
	if v := os.Getenv("AVURUOPS_E2E_HUB_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}()

const helmSeedTrace = "aaaa1111bbbb2222cccc3333dddd4444"

// helmClient carries the admin session. The chart enables auth by default, so
// every hub route except /healthz and /api/v1/auth/* now needs a cookie;
// TestMain logs in and replaces this before any test runs.
var helmClient = http.DefaultClient

// TestMain logs in as admin once for the whole e2ehelm binary. The chart is
// installed with --set auth.adminPassword=e2e-admin-pw (adminPassword), but
// the admin user is bootstrapped asynchronously (bootstrapAdmin waits for
// ClickHouse + the migrate hook's auth tables), so login is retried rather
// than attempted once.
func TestMain(m *testing.M) {
	client, err := waitForHelmAdminLogin(90 * time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not log in as admin against %s within 90s: %v\n", hubURL, err)
		os.Exit(1)
	}
	helmClient = client
	os.Exit(m.Run())
}

func waitForHelmAdminLogin(timeout time.Duration) (*http.Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		client, err := loginAs("admin", adminPassword)
		if err == nil {
			return client, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(2 * time.Second)
	}
}

func helmGetJSON(path string, out any) error {
	resp, err := helmClient.Get(hubURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func TestSeededViaHelm(t *testing.T) {
	// The chart wired the hub to ClickHouse (migration hook + gateway insert).
	var status struct {
		ClickHouse string `json:"clickhouse"`
	}
	if err := helmGetJSON("/api/v1/status", &status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.ClickHouse != "ok" {
		t.Fatalf("clickhouse status = %q, want ok", status.ClickHouse)
	}

	// Seeded trace: exactly 3 spans (ingestion is async — poll).
	var trace struct {
		Spans []struct {
			Service string `json:"service"`
		} `json:"spans"`
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := helmGetJSON("/api/v1/traces/"+helmSeedTrace, &trace)
		if err == nil && len(trace.Spans) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seeded trace not 3 spans within 60s (err=%v, got %d)", err, len(trace.Spans))
		}
		time.Sleep(2 * time.Second)
	}

	// Correlated logs: the fixture has 2 logs on the seeded trace.
	var logs struct {
		Logs []struct {
			Severity string `json:"severity"`
		} `json:"logs"`
	}
	if err := helmGetJSON("/api/v1/traces/"+helmSeedTrace+"/logs", &logs); err != nil {
		t.Fatalf("trace logs: %v", err)
	}
	if len(logs.Logs) != 2 {
		t.Fatalf("got %d correlated logs, want 2", len(logs.Logs))
	}
}
