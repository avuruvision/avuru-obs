package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// This is the test the whole single-resolver design exists for. The alerting
// evaluator does not go through the API — main hands it the group provider
// directly — so merging UI-authored groups anywhere inside an API handler
// would make a T0 group created in the browser show as critical on /health
// and page on nothing. Both consumers are driven here from ONE resolver,
// exactly as main wires them (design/2026-08-07-service-groups-crud.md).
func TestUIAuthoredGroupReachesBothHealthAndAlerting(t *testing.T) {
	fake := &storagetest.Fake{
		// 50% errors over a healthy sample count -> down.
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 100, ErrorCount: 50, P95: 100 * time.Millisecond}},
		Labels:   []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
		// Authored in the UI, not in the chart: no groups config is loaded here.
		ServiceGroups: map[string]storage.ServiceGroup{
			"payments": {Name: "payments", Tier: "T0", Services: []string{"checkout"}},
		},
	}
	provider := func() storage.Store { return fake }
	groups := health.NewResolver(health.Default, func() health.GroupStore { return fake })
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	// 1. The API path: what an operator sees on the Service Health screen.
	mux := http.NewServeMux()
	api.Register(mux, provider, api.Config{Groups: groups})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/groups", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /health/groups = %d body %s", w.Code, w.Body.String())
	}
	var apiResp struct {
		Groups []struct {
			Name string `json:"name"`
			Tier string `json:"tier"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(w.Body).Decode(&apiResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(apiResp.Groups) != 1 || apiResp.Groups[0].Name != "payments" || apiResp.Groups[0].Tier != "T0" {
		t.Fatalf("API groups = %+v, want one T0 payments group", apiResp.Groups)
	}

	// 2. The evaluator path: the same rollup, reached without the API.
	report, err := computeHealth(ctx, fake, groups.Config(ctx), "default", now, 5*time.Minute)
	if err != nil {
		t.Fatalf("computeHealth: %v", err)
	}
	if len(report.Groups) != 1 || report.Groups[0].Name != "payments" || string(report.Groups[0].Tier) != "T0" {
		t.Fatalf("evaluator groups = %+v, want the same T0 payments group", report.Groups)
	}

	// 3. And it actually pages: a rule selecting the UI-authored group fires.
	acfg := alerting.Config{
		EvalIntervalSec: 30, WindowMinutes: 5,
		Channels: []alerting.Channel{{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/ops"}},
		Rules: []alerting.Rule{{
			Name: "payments-down", When: alerting.WhenDown,
			Selector: alerting.Selector{Groups: []string{"payments"}}, Channel: "ops",
		}},
	}
	notifier := &captureNotifier{}
	// Two ticks: ok -> pending, pending -> firing + notify (as in the
	// evaluateOnce test), replaying saved state between them.
	for range 2 {
		if err := evaluateOnce(ctx, provider, groups.Config(ctx), acfg, notifier, []string{"default"}, nil, nil, now); err != nil {
			t.Fatalf("evaluateOnce: %v", err)
		}
		fake.AlertStates = nil
		for _, batch := range fake.SavedAlertStates {
			fake.AlertStates = append(fake.AlertStates, batch...)
		}
	}
	if len(notifier.sent) == 0 {
		t.Fatal("no notification: a group authored in the UI did not reach alerting")
	}
	for _, n := range notifier.sent {
		if n.Rule != "payments-down" {
			t.Fatalf("unexpected notification %+v", n)
		}
	}
}
