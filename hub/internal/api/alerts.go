package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
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
	// Source is where the channel is managed: "config" (ConfigMap, read-only
	// via the API) or "ui" (stored, editable). Empty on the legacy /rules list.
	Source string `json:"source,omitempty"`
	// Shadowed marks a config channel hidden by a same-name UI channel (the
	// UI store wins at delivery time).
	Shadowed bool `json:"shadowed,omitempty"`
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

// --- UI-managed channels (create/edit/delete delivery endpoints from the UI).
// Channels are global (not per-tenant): they are delivery endpoints shared by
// all projects; the notification payload carries the tenant. File-config
// channels remain ConfigMap-managed and read-only here; a UI channel with the
// same name shadows the config one at delivery time.

type alertChannelInput struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

type alertChannelsResponse struct {
	Channels []alertChannelDTO `json:"channels"`
}

func toChannelDTO(ch alerting.Channel, source string, shadowed bool) alertChannelDTO {
	return alertChannelDTO{
		Name: ch.Name, Type: ch.Type, URL: ch.URL, HasAuth: ch.Secret != "",
		Source: source, Shadowed: shadowed,
	}
}

func storedToChannel(ch storage.AlertChannel) alerting.Channel {
	return alerting.Channel{Name: ch.Name, Type: ch.Type, URL: ch.URL, Secret: ch.Secret}
}

// handleListAlertChannels returns the merged channel set: UI-stored channels
// (editable) plus file-config channels (read-only), config entries flagged
// Shadowed when a same-name UI channel supersedes them.
func (a *API) handleListAlertChannels(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	stored, err := store.ListAlertChannels(r.Context())
	if err != nil {
		return err
	}
	uiNames := map[string]bool{}
	resp := alertChannelsResponse{Channels: []alertChannelDTO{}}
	for _, ch := range stored {
		uiNames[ch.Name] = true
		resp.Channels = append(resp.Channels, toChannelDTO(storedToChannel(ch), "ui", false))
	}
	for _, ch := range a.alertsConfig().Channels {
		resp.Channels = append(resp.Channels, toChannelDTO(ch, "config", uiNames[ch.Name]))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func decodeChannelInput(w http.ResponseWriter, r *http.Request) (alertChannelInput, error) {
	var in alertChannelInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		return in, badRequest("invalid body")
	}
	if in.Type == "" {
		in.Type = "webhook"
	}
	return in, nil
}

// handleCreateAlertChannel creates a UI-managed channel. 409 when a UI channel
// with the name already exists (creating over a config channel is allowed —
// it shadows it).
func (a *API) handleCreateAlertChannel(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	in, err := decodeChannelInput(w, r)
	if err != nil {
		return err
	}
	ch := alerting.Channel{Name: in.Name, Type: in.Type, URL: in.URL, Secret: in.Secret}
	if err := alerting.ValidateChannel(ch); err != nil {
		return badRequest("%s", err)
	}
	existing, err := store.ListAlertChannels(r.Context())
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.Name == ch.Name {
			return &apiError{status: http.StatusConflict, message: "channel " + ch.Name + " already exists"}
		}
	}
	if err := store.SaveAlertChannel(r.Context(), storage.AlertChannel{
		Name: ch.Name, Type: ch.Type, URL: ch.URL, Secret: ch.Secret,
	}); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toChannelDTO(ch, "ui", false))
	return nil
}

// handleUpdateAlertChannel edits a UI-managed channel. An empty/omitted secret
// keeps the stored one ("leave blank to keep"); config channels 404 here (they
// are ConfigMap-managed).
func (a *API) handleUpdateAlertChannel(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	in, err := decodeChannelInput(w, r)
	if err != nil {
		return err
	}
	existing, err := store.ListAlertChannels(r.Context())
	if err != nil {
		return err
	}
	var cur *storage.AlertChannel
	for i := range existing {
		if existing[i].Name == name {
			cur = &existing[i]
			break
		}
	}
	if cur == nil {
		return storage.ErrNotFound
	}
	if in.Secret == "" {
		in.Secret = cur.Secret
	}
	ch := alerting.Channel{Name: name, Type: in.Type, URL: in.URL, Secret: in.Secret}
	if err := alerting.ValidateChannel(ch); err != nil {
		return badRequest("%s", err)
	}
	if err := store.SaveAlertChannel(r.Context(), storage.AlertChannel{
		Name: ch.Name, Type: ch.Type, URL: ch.URL, Secret: ch.Secret,
	}); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toChannelDTO(ch, "ui", false))
	return nil
}

// handleDeleteAlertChannel removes a UI-managed channel (config channels 404 —
// they don't live in the store).
func (a *API) handleDeleteAlertChannel(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	if err := store.DeleteAlertChannel(r.Context(), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleTestAlertChannel sends a synthetic notification through the shared
// notifier so the user can verify delivery. The SSRF policy is enforced
// unchanged at dial time — the UI cannot bypass it.
func (a *API) handleTestAlertChannel(w http.ResponseWriter, r *http.Request) error {
	if a.cfg.Notifier == nil {
		return &apiError{status: http.StatusServiceUnavailable, message: "notifier unavailable"}
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	stored, err := store.ListAlertChannels(r.Context())
	if err != nil {
		return err
	}
	var ch alerting.Channel
	found := false
	for _, s := range stored {
		if s.Name == name {
			ch, found = storedToChannel(s), true
			break
		}
	}
	if !found {
		ch, found = a.alertsConfig().ChannelByName(name)
	}
	if !found {
		return storage.ErrNotFound
	}
	note := alerting.Notification{
		Rule: "test", Target: "channel:" + name, Kind: "test",
		Status: "healthy", Reason: "test notification from the Avuru Obs UI",
		Tenant: tenant(r), At: time.Now().UTC(),
	}
	if err := a.cfg.Notifier.Send(r.Context(), ch, note); err != nil {
		return &apiError{status: http.StatusBadGateway, message: "delivery failed: " + err.Error()}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
