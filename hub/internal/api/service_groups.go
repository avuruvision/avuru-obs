package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Service health groups authored from the UI. Chart-declared groups keep
// living in the ConfigMap and stay read-only here; a name the config owns is
// refused rather than stored, because a stored row under that name would never
// take effect — the resolver lets the config win
// (design/2026-08-07-service-groups-crud.md).

const (
	maxServiceGroupNameLen = 100
	maxServiceGroupMembers = 200
)

type serviceGroupInput struct {
	Name       string   `json:"name"`
	Tier       string   `json:"tier"`
	Namespaces []string `json:"namespaces"`
	Services   []string `json:"services"`
}

// serviceGroupDTO is one group as the editor sees it. Source is where it is
// managed — "config" (ConfigMap, read-only) or "db" (authored here). Shadowed
// marks a stored group the config has since taken the name of: it is still in
// the table and still editable, but the config wins, so it no longer groups
// anything. Surfacing it beats letting the grouping change under an operator
// who never touched it.
type serviceGroupDTO struct {
	Name       string   `json:"name"`
	Tier       string   `json:"tier"`
	Namespaces []string `json:"namespaces"`
	Services   []string `json:"services"`
	Source     string   `json:"source"`
	Editable   bool     `json:"editable"`
	Shadowed   bool     `json:"shadowed,omitempty"`
}

type serviceGroupsResponse struct {
	Groups []serviceGroupDTO `json:"groups"`
}

func toServiceGroupDTO(g health.Group, source string, shadowed bool) serviceGroupDTO {
	return serviceGroupDTO{
		Name:       g.Name,
		Tier:       string(g.Tier),
		Namespaces: nonNilSlice(g.Selector.Namespaces),
		Services:   nonNilSlice(g.Selector.Services),
		Source:     source,
		Editable:   source == "db",
		Shadowed:   shadowed,
	}
}

func nonNilSlice(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// handleServiceGroups lists chart-declared groups (read-only) followed by the
// stored ones. It answers with the config-only list when the store is
// unreachable rather than failing: the read is what the Health screen links
// to, and half an answer is more useful than none.
func (a *API) handleServiceGroups(w http.ResponseWriter, r *http.Request) error {
	declared := a.cfg.Groups.Declared()
	declaredNames := make(map[string]bool, len(declared.Groups))
	resp := serviceGroupsResponse{Groups: make([]serviceGroupDTO, 0, len(declared.Groups))}
	for _, g := range declared.Groups {
		declaredNames[g.Name] = true
		resp.Groups = append(resp.Groups, toServiceGroupDTO(g, "config", false))
	}
	stored, err := a.cfg.Groups.Stored(r.Context())
	if err != nil {
		slog.Warn("service groups: stored groups unreadable, listing config only", "error", err)
	}
	for _, s := range stored {
		resp.Groups = append(resp.Groups, toServiceGroupDTO(health.StoredGroup(s), "db", declaredNames[s.Name]))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// decodeServiceGroupInput reads and normalizes the body: names and selector
// entries are trimmed, and empty entries dropped, so a stray blank line in the
// UI's textarea cannot become a selector that matches the empty namespace.
func decodeServiceGroupInput(w http.ResponseWriter, r *http.Request) (serviceGroupInput, error) {
	var in serviceGroupInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		return in, decodeJSONError(err)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Tier = strings.TrimSpace(in.Tier)
	in.Namespaces = cleanSelectorList(in.Namespaces)
	in.Services = cleanSelectorList(in.Services)
	return in, nil
}

func cleanSelectorList(v []string) []string {
	out := make([]string, 0, len(v))
	seen := make(map[string]bool, len(v))
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// validateServiceGroup runs the input through the same Validate the ConfigMap
// loader uses at boot — reusing it rather than re-implementing tier parsing at
// the edge, so the API can never store a group the loader would reject. The
// group is validated inside a config carrying the live defaultTier, since
// Validate checks that too.
func (a *API) validateServiceGroup(in serviceGroupInput) error {
	if in.Name == "" {
		return badRequest("name is required")
	}
	if len(in.Name) > maxServiceGroupNameLen {
		return badRequest("name must be %d characters or fewer", maxServiceGroupNameLen)
	}
	if len(in.Namespaces)+len(in.Services) > maxServiceGroupMembers {
		return badRequest("a selector may name at most %d namespaces and services combined", maxServiceGroupMembers)
	}
	probe := a.cfg.Groups.Declared()
	probe.Groups = []health.Group{{
		Name:     in.Name,
		Tier:     health.Tier(in.Tier),
		Selector: health.Selector{Namespaces: in.Namespaces, Services: in.Services},
	}}
	if err := probe.Validate(); err != nil {
		return badRequest("%s", err)
	}
	return nil
}

// declaredServiceGroup answers 409 when the name is chart-declared. It is a
// conflict rather than a permission problem — the caller IS an admin; the
// group is simply managed somewhere else — and it matches how
// deployment-managed projects already answer.
func (a *API) declaredServiceGroup(name string) error {
	for _, g := range a.cfg.Groups.Declared().Groups {
		if g.Name == name {
			return &apiError{status: http.StatusConflict,
				message: fmt.Sprintf("service group %q is declared in the chart config and cannot be edited here", name)}
		}
	}
	return nil
}

// storedServiceGroup returns the live stored group by name, or ErrNotFound.
func storedServiceGroup(stored []storage.ServiceGroup, name string) (storage.ServiceGroup, bool) {
	for _, s := range stored {
		if s.Name == name {
			return s, true
		}
	}
	return storage.ServiceGroup{}, false
}

func (a *API) handleCreateServiceGroup(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	in, err := decodeServiceGroupInput(w, r)
	if err != nil {
		return err
	}
	if err := a.validateServiceGroup(in); err != nil {
		return err
	}
	if err := a.declaredServiceGroup(in.Name); err != nil {
		return err
	}
	stored, err := a.cfg.Groups.Stored(r.Context())
	if err != nil {
		return err
	}
	if _, ok := storedServiceGroup(stored, in.Name); ok {
		return &apiError{status: http.StatusConflict,
			message: fmt.Sprintf("service group %q already exists", in.Name)}
	}
	g := storage.ServiceGroup{
		Name:       in.Name,
		Tier:       in.Tier,
		Namespaces: in.Namespaces,
		Services:   in.Services,
		CreatedBy:  identityUserID(r.Context()),
		CreatedAt:  time.Now().UTC(),
	}
	if err := a.saveServiceGroup(r.Context(), store, g); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toServiceGroupDTO(health.StoredGroup(g), "db", false))
	return nil
}

// handleUpdateServiceGroup edits a stored group. The name is the identity, so
// it is taken from the path and the body's name is ignored.
func (a *API) handleUpdateServiceGroup(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	in, err := decodeServiceGroupInput(w, r)
	if err != nil {
		return err
	}
	in.Name = name
	if err := a.validateServiceGroup(in); err != nil {
		return err
	}
	if err := a.declaredServiceGroup(name); err != nil {
		return err
	}
	stored, err := a.cfg.Groups.Stored(r.Context())
	if err != nil {
		return err
	}
	cur, ok := storedServiceGroup(stored, name)
	if !ok {
		return storage.ErrNotFound
	}
	g := storage.ServiceGroup{
		Name:       name,
		Tier:       in.Tier,
		Namespaces: in.Namespaces,
		Services:   in.Services,
		CreatedBy:  cur.CreatedBy,
		CreatedAt:  cur.CreatedAt,
	}
	if err := a.saveServiceGroup(r.Context(), store, g); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toServiceGroupDTO(health.StoredGroup(g), "db", false))
	return nil
}

func (a *API) handleDeleteServiceGroup(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := a.declaredServiceGroup(name); err != nil {
		return err
	}
	// The store answers ErrNotFound for an absent or already-deleted name,
	// which the error mapper turns into a 404.
	if err := store.DeleteServiceGroup(r.Context(), name); err != nil {
		return err
	}
	a.cfg.Groups.Invalidate()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// saveServiceGroup writes and drops the resolver's memo, so the admin's own
// edit is visible on the next read instead of up to a TTL later.
func (a *API) saveServiceGroup(ctx context.Context, store storage.Store, g storage.ServiceGroup) error {
	if err := store.SaveServiceGroup(ctx, g); err != nil {
		return err
	}
	a.cfg.Groups.Invalidate()
	return nil
}
