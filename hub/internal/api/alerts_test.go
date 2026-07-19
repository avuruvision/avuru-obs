package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func alertsMux(fake *storagetest.Fake, cfg alerting.Config) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		AlertsConfig: func() alerting.Config { return cfg },
	})
	return mux
}

func TestAlertsFiringAndHistory(t *testing.T) {
	now := time.Now().UTC()
	fake := &storagetest.Fake{
		AlertStates: []storage.AlertState{
			{Tenant: "default", RuleName: "r", Target: "group:payments", Status: "firing", Since: now.Add(-10 * time.Minute)},
			{Tenant: "default", RuleName: "r", Target: "group:cart", Status: "pending", Since: now}, // not firing
		},
		AlertHistoryRows: []storage.AlertHistoryEntry{
			{RuleName: "r", Target: "group:payments", Kind: "fired", Status: "down", Reason: "boom", FiredAt: now.Add(-10 * time.Minute)},
		},
	}
	rec := get(t, alertsMux(fake, alerting.Default()), "/api/v1/alerts")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp alertsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Firing) != 1 || resp.Firing[0].Target != "group:payments" {
		t.Errorf("firing wrong (pending must be excluded): %+v", resp.Firing)
	}
	if len(resp.History) != 1 || resp.History[0].Kind != "fired" {
		t.Errorf("history wrong: %+v", resp.History)
	}
}

func TestAlertRulesRedactsSecret(t *testing.T) {
	cfg := alerting.Config{
		Channels: []alerting.Channel{{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/x", Secret: "s3cr3t"}},
		Rules:    []alerting.Rule{{Name: "r", When: "down", For: alerting.Duration(5 * time.Minute), Selector: alerting.Selector{Groups: []string{"payments"}}, Channel: "ops"}},
	}
	rec := get(t, alertsMux(&storagetest.Fake{}, cfg), "/api/v1/alerts/rules")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("secret leaked in /rules: %s", rec.Body.String())
	}
	var resp alertRulesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rules) != 1 || resp.Rules[0].ForSec != 300 || resp.Rules[0].Groups[0] != "payments" {
		t.Errorf("rule mapped wrong: %+v", resp.Rules)
	}
	if len(resp.Channels) != 1 || !resp.Channels[0].HasAuth || resp.Channels[0].URL == "" {
		t.Errorf("channel mapped wrong (want hasAuth + url, no secret): %+v", resp.Channels)
	}
}

func TestAlertsStoreUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return nil }, Config{})
	if rec := get(t, mux, "/api/v1/alerts"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("store down: got %d, want 503", rec.Code)
	}
	// /rules reads config, not the store — it stays 200 even during an outage.
	if rec := get(t, mux, "/api/v1/alerts/rules"); rec.Code != http.StatusOK {
		t.Errorf("/rules during outage: got %d, want 200", rec.Code)
	}
}

func TestAlertsTenantHeader(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := alertsMux(fake, alerting.Default())
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	req.Header.Set("X-Avuru-Tenant", "prod-eu")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if fake.LastAlertHistoryQuery.Tenant != "prod-eu" {
		t.Errorf("tenant header not honored: %q", fake.LastAlertHistoryQuery.Tenant)
	}
}
