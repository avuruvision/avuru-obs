//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

type healthMember struct {
	Service         string `json:"service"`
	BaseStatus      string `json:"baseStatus"`
	EffectiveStatus string `json:"effectiveStatus"`
	Reason          string `json:"reason"`
}

type healthGroup struct {
	Name    string         `json:"name"`
	Tier    string         `json:"tier"`
	Source  string         `json:"source"`
	Status  string         `json:"status"`
	Members []healthMember `json:"members"`
}

type healthGroupsResp struct {
	Overall string        `json:"overall"`
	Groups  []healthGroup `json:"groups"`
}

func healthQuery() string {
	now := time.Now().UTC()
	q := url.Values{}
	q.Set("start", now.Add(-2*time.Hour).Format(time.RFC3339))
	q.Set("end", now.Add(time.Hour).Format(time.RFC3339))
	return "/api/v1/health/groups?" + q.Encode()
}

// TestServiceHealthGroups proves the real query path end to end: the
// service-health module is active, /api/v1/health/groups runs its SQL
// (ListServices + ServiceLabels + ServiceEdges) against real ClickHouse, the
// mounted groups config is loaded, and per-service status derives correctly
// from the seeded RED. In the default tenant the fixtures give seed-checkout an
// errored entry span (→ down) and seed-payments a clean one (→ healthy);
// seed-payments is assigned tier T0 via the config group "payments".
// Grouping/tiers/propagation logic is covered exhaustively by the hub unit
// tests; here we validate the derivation and config loading over real data.
func TestServiceHealthGroups(t *testing.T) {
	poll(t, 40*time.Second, func() error {
		var resp healthGroupsResp
		if err := getJSON(healthQuery(), &resp); err != nil {
			return err
		}
		if resp.Overall == "" {
			return fmt.Errorf("no overall status yet")
		}

		members := map[string]healthMember{}
		groups := map[string]healthGroup{}
		for _, g := range resp.Groups {
			if g.Status == "" || g.Name == "" {
				return fmt.Errorf("group with empty name/status: %+v", g)
			}
			groups[g.Name] = g
			for _, m := range g.Members {
				members[m.Service] = m
			}
		}

		// seed-checkout's only entry span is errored → down.
		checkout, ok := members["seed-checkout"]
		if !ok {
			return fmt.Errorf("seed-checkout not grouped yet; have %d services", len(members))
		}
		if checkout.EffectiveStatus != "down" {
			return fmt.Errorf("seed-checkout status=%q, want down (its entry span errors): %s",
				checkout.EffectiveStatus, checkout.Reason)
		}

		// seed-payments is clean → healthy, and lands in the config group at T0.
		pay, ok := members["seed-payments"]
		if !ok {
			return fmt.Errorf("seed-payments not grouped yet")
		}
		if pay.EffectiveStatus != "healthy" {
			return fmt.Errorf("seed-payments status=%q, want healthy: %s",
				pay.EffectiveStatus, pay.Reason)
		}
		pg, ok := groups["payments"]
		if !ok {
			return fmt.Errorf("config group 'payments' not present (groups config not loaded?)")
		}
		if pg.Source != "config" || pg.Tier != "T0" {
			return fmt.Errorf("payments group source=%q tier=%q, want config/T0", pg.Source, pg.Tier)
		}

		// Overall folds in the down checkout group.
		if resp.Overall != "down" {
			return fmt.Errorf("overall=%q, want down", resp.Overall)
		}
		return nil
	})
}
