package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func checksConfig() Config {
	cfg := func() health.Config {
		return health.Config{
			DefaultTier: health.TierT2,
			Groups: []health.Group{{
				Name: "core", Tier: health.TierT0,
				Selector: health.Selector{Services: []string{"api"}},
				Checks: []health.Check{
					{ID: "core-login", URL: "https://app.example.com/health"},
					{ID: "core-search", URL: "https://app.example.com/search", Interval: "5m"},
				},
			}},
		}
	}
	return Config{Groups: health.NewResolver(cfg, nil)}
}

func TestChecksListsDeclaredChecks(t *testing.T) {
	rec := meshGet(t, &storagetest.Fake{}, checksConfig(), "/api/v1/checks")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp checksResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("got %d checks, want 2: %+v", len(resp.Checks), resp.Checks)
	}
	byID := map[string]checkDTO{}
	for _, c := range resp.Checks {
		byID[c.ID] = c
	}
	// The default is reported explicitly, not left blank: an operator reading
	// this needs to know how often it actually runs, not what they omitted.
	if byID["core-login"].Interval != "1m0s" {
		t.Errorf("default interval = %q, want the resolved default", byID["core-login"].Interval)
	}
	if byID["core-search"].Interval != "5m0s" {
		t.Errorf("configured interval = %q", byID["core-search"].Interval)
	}
	if byID["core-login"].Group != "core" || byID["core-login"].Tier != "T0" {
		t.Errorf("check not attributed to its group: %+v", byID["core-login"])
	}
}

func TestCheckResultsReturnsHistory(t *testing.T) {
	fake := &storagetest.Fake{CheckResultsByID: map[string][]storage.CheckResult{
		"core-login": {
			{CheckID: "core-login", At: time.Now(), OK: false, Status: 503, LatencyMs: 12, Error: "boom", TraceID: "abc"},
			{CheckID: "core-login", At: time.Now().Add(-time.Minute), OK: true, Status: 200, LatencyMs: 9},
		},
	}}
	rec := meshGet(t, fake, checksConfig(), "/api/v1/checks/core-login/results")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp checkResultsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 || resp.Results[0].OK {
		t.Fatalf("want two results newest-first with the failure leading: %+v", resp.Results)
	}
	// The trace id is the click-through from a failed check to the request
	// that failed; losing it here would cost the feature its point.
	if resp.Results[0].TraceID != "abc" {
		t.Errorf("trace id missing from the failed result: %+v", resp.Results[0])
	}
}

// A typo in a check id must read as "no such check", not as "it has not run
// yet" — those send the operator to two different places.
func TestCheckResultsUnknownIDIs404(t *testing.T) {
	rec := meshGet(t, &storagetest.Fake{}, checksConfig(), "/api/v1/checks/typo/results")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d for an undeclared check, want 404", rec.Code)
	}
}

// Checks belong to service-health; without it there is nothing for them to
// answer for and the routes should not exist.
func TestCheckRoutesGatedOnServiceHealth(t *testing.T) {
	cfg := checksConfig()
	active := modules.AllSet()
	delete(active, modules.ServiceHealth)
	cfg.Modules = active
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return &storagetest.Fake{} }, cfg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/checks", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/v1/checks = %d with service-health off, want 404", rec.Code)
	}
}
