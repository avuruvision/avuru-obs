package api

import (
	"context"
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

// do issues a request with an optional JSON body against the mux.
func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAlertChannelsCRUD(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := alerting.Config{Channels: []alerting.Channel{
		{Name: "sink", Type: "webhook", URL: "https://hooks.example.com/sink", Secret: "cfg-s3cr3t"},
	}}
	mux := alertsMux(fake, cfg)

	// Create.
	rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels", `{"name":"p1","url":"https://hooks.example.com/p1","secret":"ui-s3cr3t"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("create leaked secret: %s", rec.Body.String())
	}
	// Duplicate UI name -> 409.
	if rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels", `{"name":"p1","url":"https://hooks.example.com/other"}`); rec.Code != http.StatusConflict {
		t.Errorf("duplicate create: got %d, want 409", rec.Code)
	}
	// Bad URL -> 400.
	if rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels", `{"name":"bad","url":"ftp://x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad url: got %d, want 400", rec.Code)
	}

	// List: merged set, sources marked, secrets never serialized.
	rec = get(t, mux, "/api/v1/alerts/channels")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("list leaked secret: %s", rec.Body.String())
	}
	var listResp alertChannelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Channels) != 2 {
		t.Fatalf("want ui+config channels, got %+v", listResp.Channels)
	}
	bySource := map[string]alertChannelDTO{}
	for _, ch := range listResp.Channels {
		bySource[ch.Source] = ch
	}
	if bySource["ui"].Name != "p1" || !bySource["ui"].HasAuth {
		t.Errorf("ui channel wrong: %+v", bySource["ui"])
	}
	if bySource["config"].Name != "sink" || bySource["config"].Shadowed {
		t.Errorf("config channel wrong: %+v", bySource["config"])
	}

	// Update with empty secret keeps the stored one.
	rec = do(t, mux, http.MethodPut, "/api/v1/alerts/channels/p1", `{"url":"https://hooks.example.com/p1-v2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	if got := fake.Channels[0]; got.URL != "https://hooks.example.com/p1-v2" || got.Secret != "ui-s3cr3t" {
		t.Errorf("update should keep secret: %+v", got)
	}
	// Updating a config channel -> 404 (ConfigMap-managed).
	if rec := do(t, mux, http.MethodPut, "/api/v1/alerts/channels/sink", `{"url":"https://hooks.example.com/x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("update config channel: got %d, want 404", rec.Code)
	}

	// A same-name UI channel shadows the config one in the list.
	if rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels", `{"name":"sink","url":"https://hooks.example.com/ui-sink"}`); rec.Code != http.StatusCreated {
		t.Fatalf("shadowing create: %d", rec.Code)
	}
	rec = get(t, mux, "/api/v1/alerts/channels")
	listResp = alertChannelsResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	var sawShadowed bool
	for _, ch := range listResp.Channels {
		if ch.Source == "config" && ch.Name == "sink" && ch.Shadowed {
			sawShadowed = true
		}
	}
	if !sawShadowed {
		t.Errorf("config sink should be marked shadowed: %+v", listResp.Channels)
	}

	// Delete.
	if rec := do(t, mux, http.MethodDelete, "/api/v1/alerts/channels/p1", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: got %d, want 204", rec.Code)
	}
	if rec := do(t, mux, http.MethodDelete, "/api/v1/alerts/channels/p1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: got %d, want 404", rec.Code)
	}
}

// captureNotifier records Send calls for the /test endpoint.
type captureNotifier struct {
	sent []alerting.Notification
	ch   []alerting.Channel
	err  error
}

func (c *captureNotifier) Send(_ context.Context, ch alerting.Channel, n alerting.Notification) error {
	c.ch = append(c.ch, ch)
	c.sent = append(c.sent, n)
	return c.err
}

func TestAlertChannelTestSend(t *testing.T) {
	fake := &storagetest.Fake{Channels: []storage.AlertChannel{
		{Name: "p1", Type: "webhook", URL: "https://hooks.example.com/p1", Secret: "s"},
	}}
	notifier := &captureNotifier{}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		AlertsConfig: func() alerting.Config { return alerting.Default() },
		Notifier:     notifier,
	})

	rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels/p1/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("test send: %d %s", rec.Code, rec.Body.String())
	}
	if len(notifier.sent) != 1 || notifier.sent[0].Kind != "test" || notifier.ch[0].Secret != "s" {
		t.Errorf("test notification wrong: %+v via %+v", notifier.sent, notifier.ch)
	}
	if rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels/nope/test", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel: got %d, want 404", rec.Code)
	}

	notifier.err = errDeliver
	if rec := do(t, mux, http.MethodPost, "/api/v1/alerts/channels/p1/test", ""); rec.Code != http.StatusBadGateway {
		t.Errorf("delivery failure: got %d, want 502", rec.Code)
	}
}

var errDeliver = &apiErrorForTest{}

type apiErrorForTest struct{}

func (e *apiErrorForTest) Error() string { return "connection refused" }

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
