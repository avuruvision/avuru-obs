package main

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// captureNotifier records deliveries for evaluateOnce tests.
type captureNotifier struct {
	sent []alerting.Notification
	chs  []alerting.Channel
}

func (c *captureNotifier) Send(_ context.Context, ch alerting.Channel, n alerting.Notification) error {
	c.chs = append(c.chs, ch)
	c.sent = append(c.sent, n)
	return nil
}

// TestEvaluateOnceStampsTenantAndResolvesUIChannels drives one evaluator tick:
// a down service fires a for:0 rule; the delivered notification must carry the
// tenant, and the rule's channel must resolve from the UI store, shadowing the
// same-name config channel.
func TestEvaluateOnceStampsTenantAndResolvesUIChannels(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "payments", SpanCount: 100, ErrorCount: 50, P95: 100 * time.Millisecond}, // 50% errors -> down
		},
		Labels:  []storage.ServiceLabel{{Service: "payments", K8sNamespace: "prod-ns"}},
		Tenants: []string{"staging"},
		Channels: []storage.AlertChannel{
			{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/ui", Secret: "ui-secret"},
		},
	}
	gcfg := health.Default()
	acfg := alerting.Config{
		EvalIntervalSec: 30, WindowMinutes: 5,
		Channels: []alerting.Channel{{Name: "ops", Type: "webhook", URL: "https://hooks.example.com/config"}},
		Rules: []alerting.Rule{{
			Name: "payments-down", When: alerting.WhenDown,
			Selector: alerting.Selector{Services: []string{"payments"}}, Channel: "ops",
		}},
	}
	notifier := &captureNotifier{}

	// Two ticks: ok -> pending (tick 1), pending -> firing + notify (tick 2).
	for i := 0; i < 2; i++ {
		if err := evaluateOnce(context.Background(), func() storage.Store { return fake }, gcfg, acfg, notifier, nil); err != nil {
			t.Fatalf("evaluateOnce: %v", err)
		}
		// Feed all saved state back chronologically; toEvalState is last-wins
		// per key, mirroring the ReplacingMergeTree.
		fake.AlertStates = nil
		for _, batch := range fake.SavedAlertStates {
			fake.AlertStates = append(fake.AlertStates, batch...)
		}
	}

	if len(notifier.sent) == 0 {
		t.Fatal("expected a delivered notification")
	}
	// tenantsToEvaluate = default ∪ observed(staging): both evaluate the same
	// data (the fake ignores the tenant), so each fires with its own stamp.
	tenants := map[string]bool{}
	for _, n := range notifier.sent {
		if n.Tenant == "" {
			t.Errorf("notification missing tenant: %+v", n)
		}
		tenants[n.Tenant] = true
		if n.Rule != "payments-down" || n.Kind != alerting.KindFired {
			t.Errorf("unexpected notification: %+v", n)
		}
	}
	if !tenants["default"] || !tenants["staging"] {
		t.Errorf("want notifications for default and staging tenants, got %v", tenants)
	}
	// The UI-stored channel shadows the config channel of the same name.
	for _, ch := range notifier.chs {
		if ch.URL != "https://hooks.example.com/ui" || ch.Secret != "ui-secret" {
			t.Errorf("channel should resolve from UI store, got %+v", ch)
		}
	}
}
