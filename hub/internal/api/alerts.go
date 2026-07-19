package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Wire DTOs for the alerting module (read-only). Statuses reuse the health
// vocabulary; channel secrets are never serialized.

type firingAlertDTO struct {
	Rule   string `json:"rule"`
	Target string `json:"target"`
	Status string `json:"status"`
	Since  string `json:"since"`
}

type alertHistoryDTO struct {
	Rule    string `json:"rule"`
	Target  string `json:"target"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	FiredAt string `json:"firedAt"`
}

type alertsResponse struct {
	Firing  []firingAlertDTO  `json:"firing"`
	History []alertHistoryDTO `json:"history"`
}

// handleAlerts returns the currently-firing alerts and recent fire/resolve
// history for the tenant.
func (a *API) handleAlerts(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tenant := tenant(r)
	states, err := store.LoadAlertStates(r.Context(), tenant)
	if err != nil {
		return err
	}
	hist, err := store.ListAlertHistory(r.Context(), storage.AlertHistoryQuery{Tenant: tenant})
	if err != nil {
		return err
	}

	resp := alertsResponse{Firing: []firingAlertDTO{}, History: []alertHistoryDTO{}}
	for _, s := range states {
		if s.Status != "firing" {
			continue
		}
		resp.Firing = append(resp.Firing, firingAlertDTO{
			Rule: s.RuleName, Target: s.Target, Status: "firing",
			Since: s.Since.UTC().Format(time.RFC3339),
		})
	}
	for _, h := range hist {
		resp.History = append(resp.History, alertHistoryDTO{
			Rule: h.RuleName, Target: h.Target, Kind: h.Kind, Status: h.Status,
			Reason: h.Reason, FiredAt: h.FiredAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// alertRuleDTO mirrors a configured rule for the UI. The channel URL is shown
// but its Secret is NEVER serialized.
type alertRuleDTO struct {
	Name     string   `json:"name"`
	When     string   `json:"when"`
	ForSec   int      `json:"forSec"`
	Channel  string   `json:"channel"`
	Groups   []string `json:"groups,omitempty"`
	Services []string `json:"services,omitempty"`
	Tiers    []string `json:"tiers,omitempty"`
}

type alertChannelDTO struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	HasAuth bool   `json:"hasAuth"` // whether a signing secret is set (secret itself never sent)
}

type alertRulesResponse struct {
	Rules    []alertRuleDTO    `json:"rules"`
	Channels []alertChannelDTO `json:"channels"`
}

// handleAlertRules returns the loaded rules/channels so the UI can show what is
// configured. Secrets are redacted.
func (a *API) handleAlertRules(w http.ResponseWriter, _ *http.Request) error {
	cfg := a.alertsConfig()
	resp := alertRulesResponse{Rules: []alertRuleDTO{}, Channels: []alertChannelDTO{}}
	for _, ru := range cfg.Rules {
		resp.Rules = append(resp.Rules, alertRuleDTO{
			Name: ru.Name, When: ru.When, ForSec: int(ru.For.Std().Seconds()), Channel: ru.Channel,
			Groups: ru.Selector.Groups, Services: ru.Selector.Services, Tiers: ru.Selector.Tiers,
		})
	}
	for _, ch := range cfg.Channels {
		resp.Channels = append(resp.Channels, alertChannelDTO{
			Name: ch.Name, Type: ch.Type, URL: ch.URL, HasAuth: ch.Secret != "",
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
