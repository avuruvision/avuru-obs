//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

type firingAlert struct {
	Rule   string `json:"rule"`
	Target string `json:"target"`
	Status string `json:"status"`
}

type alertHistoryEntry struct {
	Rule   string `json:"rule"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type alertsResp struct {
	Firing  []firingAlert       `json:"firing"`
	History []alertHistoryEntry `json:"history"`
}

// TestAlertingFires proves the whole loop against the real stack: the alerting
// module is active, the background evaluator computes health from the seeded
// data, the config rule ("seed-checkout down") matches, and the transition is
// persisted — surfaced by GET /api/v1/alerts as a firing alert plus a "fired"
// history row. Delivery itself (webhook POST, retries, SSRF) is unit-tested; the
// seed rule targets an unroutable URL on purpose, and the loop persists state
// regardless of delivery.
func TestAlertingFires(t *testing.T) {
	target := "service:seed-checkout"
	poll(t, 60*time.Second, func() error {
		var resp alertsResp
		if err := getJSON("/api/v1/alerts", &resp); err != nil {
			return err
		}
		firing := false
		for _, a := range resp.Firing {
			if a.Target == target && a.Status == "firing" {
				firing = true
			}
		}
		if !firing {
			return fmt.Errorf("seed-checkout not firing yet; firing=%+v", resp.Firing)
		}
		for _, h := range resp.History {
			if h.Target == target && h.Kind == "fired" {
				return nil
			}
		}
		return fmt.Errorf("no fired history row yet; history=%+v", resp.History)
	})
}

// TestAlertRulesRedacted checks the read-only rules endpoint exposes the config
// without leaking any channel secret shape (the seed channel has none, but the
// endpoint must always answer and list the rule).
func TestAlertRulesRedacted(t *testing.T) {
	var resp struct {
		Rules []struct {
			Name    string `json:"name"`
			When    string `json:"when"`
			Channel string `json:"channel"`
		} `json:"rules"`
		Channels []struct {
			Name    string `json:"name"`
			HasAuth bool   `json:"hasAuth"`
		} `json:"channels"`
	}
	if err := getJSON("/api/v1/alerts/rules", &resp); err != nil {
		t.Fatalf("rules: %v", err)
	}
	if len(resp.Rules) != 1 || resp.Rules[0].Name != "checkout-down" || resp.Rules[0].When != "down" {
		t.Fatalf("rule not surfaced: %+v", resp.Rules)
	}
	if len(resp.Channels) != 1 || resp.Channels[0].Name != "sink" {
		t.Fatalf("channel not surfaced: %+v", resp.Channels)
	}
}
