package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// stubHub answers the endpoints the CLI reads, and records what it was asked.
func stubHub(t *testing.T, body map[string]any) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(r.Context()))
		if r.Header.Get("Authorization") != "Bearer avurut_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		payload, ok := body[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func configure(t *testing.T, url string) {
	t.Helper()
	t.Setenv("AVURUOBS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("AVURUOBS_URL", url)
	t.Setenv("AVURUOBS_TOKEN", "avurut_test")
}

var services = map[string]any{
	"/api/v1/services": map[string]any{"services": []map[string]any{
		{"name": "checkout", "ratePerSec": 12.5, "errorRate": 0.12, "p50Ms": 40.0, "p95Ms": 420.0, "spanCount": 9000},
		{"name": "payments", "ratePerSec": 3.0, "errorRate": 0.0, "p50Ms": 10.0, "p95Ms": 60.0, "spanCount": 400},
	}},
}

// The three exit codes are the CLI's contract with a pipeline: a gate that
// tripped has to be distinguishable from a gate that could not run.
func TestExitCodes(t *testing.T) {
	srv, _ := stubHub(t, services)
	configure(t, srv.URL)

	if got := run([]string{"services"}); got != exitOK {
		t.Errorf("plain listing exited %d, want %d", got, exitOK)
	}
	if got := run([]string{"services", "--fail-on", "errorRate>0.05"}); got != exitPredicate {
		t.Errorf("matching predicate exited %d, want %d", got, exitPredicate)
	}
	if got := run([]string{"services", "--fail-on", "errorRate>0.9"}); got != exitOK {
		t.Errorf("non-matching predicate exited %d, want %d", got, exitOK)
	}
	// A misspelled field must fail the command, not quietly pass the gate.
	if got := run([]string{"services", "--fail-on", "erorRate>0.05"}); got != exitError {
		t.Errorf("unknown field exited %d, want %d", got, exitError)
	}
	if got := run([]string{"services", "--fail-on", "errorRate"}); got != exitError {
		t.Errorf("malformed predicate exited %d, want %d", got, exitError)
	}
}

func TestUnauthorizedIsAnErrorNotAPassingGate(t *testing.T) {
	srv, _ := stubHub(t, services)
	configure(t, srv.URL)
	t.Setenv("AVURUOBS_TOKEN", "avurut_wrong")

	// The dangerous failure: the request 401s, no rows come back, and a
	// --fail-on gate reads "nothing over threshold" as success.
	if got := run([]string{"services", "--fail-on", "errorRate>0.05"}); got != exitError {
		t.Errorf("bad token exited %d, want %d", got, exitError)
	}
}

func TestMissingCredentialsAreExplained(t *testing.T) {
	t.Setenv("AVURUOBS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("AVURUOBS_URL", "")
	t.Setenv("AVURUOBS_TOKEN", "")
	if got := run([]string{"services"}); got != exitError {
		t.Errorf("unconfigured CLI exited %d, want %d", got, exitError)
	}
}

func TestProjectIsSentAsTheTenantHeader(t *testing.T) {
	srv, seen := stubHub(t, services)
	configure(t, srv.URL)

	if got := run([]string{"services", "--project", "staging"}); got != exitOK {
		t.Fatalf("exited %d", got)
	}
	if len(*seen) == 0 {
		t.Fatal("no request reached the hub")
	}
	if h := (*seen)[len(*seen)-1].Header.Get("X-Avuru-Tenant"); h != "staging" {
		t.Errorf("tenant header = %q, want staging", h)
	}
}

// Without --project the CLI must send no tenant header at all, so the token
// owner's server-side default stands.
func TestNoProjectMeansNoHeader(t *testing.T) {
	srv, seen := stubHub(t, services)
	configure(t, srv.URL)

	if got := run([]string{"services"}); got != exitOK {
		t.Fatalf("exited %d", got)
	}
	if h := (*seen)[len(*seen)-1].Header.Get("X-Avuru-Tenant"); h != "" {
		t.Errorf("tenant header = %q, want it absent", h)
	}
}

func TestHealthPredicateOnStatus(t *testing.T) {
	srv, _ := stubHub(t, map[string]any{
		"/api/v1/health/groups": map[string]any{
			"overall": "degraded",
			"groups": []map[string]any{
				{"name": "payments", "environment": "prod", "tier": "T0", "status": "healthy", "ratePerSec": 1.0, "errorRate": 0.0, "p95Ms": 10.0},
				{"name": "payments", "environment": "staging", "tier": "T2", "status": "degraded", "ratePerSec": 0.5, "errorRate": 0.2, "p95Ms": 90.0},
			},
		},
	})
	configure(t, srv.URL)

	if got := run([]string{"health", "--fail-on", "status!=healthy"}); got != exitPredicate {
		t.Errorf("a degraded group should trip the gate; exited %d", got)
	}
	if got := run([]string{"health", "--fail-on", "tier==T3"}); got != exitOK {
		t.Errorf("no T3 group exists; exited %d", got)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if got := run([]string{"nonsense"}); got != exitError {
		t.Errorf("unknown command exited %d, want %d", got, exitError)
	}
	if got := run([]string{"version"}); got != exitOK {
		t.Errorf("version exited %d, want %d", got, exitOK)
	}
}
