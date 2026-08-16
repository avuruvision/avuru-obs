package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Admin CRUD for the OIDC group→role mapping overlay. Same shape as
// service_groups.go: the chart-declared half stays in the OIDC ConfigMap and
// is read-only here, the DB half is authored from this API, and the merge —
// auth.MergeMapping, cached in auth.MappingCache — decides which one wins a
// name collision. The one real difference from service groups: a name the
// chart already declares is a legitimate PUT target here (it lands
// Shadowed), because MergeMapping already has a tested, deliberate answer for
// that collision, unlike service groups where a stored row under a declared
// name would never be reachable at all.
//
// Reads are admin-gated too, unlike service groups' viewer-readable list:
// this configures WHO gets WHAT role, which is more sensitive than a service
// grouping.

// oidcMappingDTO is one OIDC group→role rule as the Access screen sees it,
// mirroring serviceGroupDTO's vocabulary. Source is "config" (chart-declared,
// read-only) or "db" (authored here). Shadowed marks an authored rule whose
// group the config also declares: still stored and still editable, but the
// config wins, so it grants nothing (auth.MergeMapping). Invalid marks a
// stored row whose Role no longer parses (see invalidOIDCMappingRows) — Role
// is empty for such a row; only the raw InvalidRole it was saved with is
// carried, so an admin can see it and delete it.
type oidcMappingDTO struct {
	Group       string   `json:"group"`
	Role        string   `json:"role,omitempty"`
	Projects    []string `json:"projects,omitempty"`
	Source      string   `json:"source"`
	Editable    bool     `json:"editable"`
	Shadowed    bool     `json:"shadowed,omitempty"`
	Invalid     bool     `json:"invalid,omitempty"`
	InvalidRole string   `json:"invalidRole,omitempty"`
}

type oidcMappingResponse struct {
	Rules []oidcMappingDTO `json:"rules"`
}

type oidcMappingInput struct {
	Role     string   `json:"role"`
	Projects []string `json:"projects"`
}

func toOIDCMappingDTO(m auth.EffectiveMapping) oidcMappingDTO {
	return oidcMappingDTO{
		Group:    m.Group,
		Role:     string(m.Role),
		Projects: nonNilSlice(m.Projects),
		Source:   m.Source,
		Editable: m.Editable,
		Shadowed: m.Shadowed,
	}
}

// decodeOIDCMappingInput reads and normalizes the body, mirroring
// decodeServiceGroupInput: the role is trimmed and the project list cleaned
// through the same helper service groups use for namespaces/services, so a
// stray blank entry in the editor can't become a project nothing matches.
func decodeOIDCMappingInput(w http.ResponseWriter, r *http.Request) (oidcMappingInput, error) {
	var in oidcMappingInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		return in, decodeJSONError(err)
	}
	in.Role = strings.TrimSpace(in.Role)
	in.Projects = cleanSelectorList(in.Projects)
	return in, nil
}

// handleOIDCMapping lists the merged mapping the cache is enforcing right now
// (Source/Editable/Shadowed already resolved by auth.MergeMapping), plus any
// row the merge silently dropped for an unparseable role.
func (a *API) handleOIDCMapping(w http.ResponseWriter, r *http.Request) error {
	snap := a.cfg.OIDCMapping.Snapshot()
	resp := oidcMappingResponse{Rules: make([]oidcMappingDTO, 0, len(snap))}
	for _, m := range snap {
		resp.Rules = append(resp.Rules, toOIDCMappingDTO(m))
	}
	resp.Rules = append(resp.Rules, a.invalidOIDCMappingRows(r.Context())...)
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// invalidOIDCMappingRows surfaces a DB row auth.MergeMapping drops because
// its Role fails ParseRole. That is the right call for grant derivation — an
// unparseable role must not silently resolve to something (mapping.go) — but
// it means the row is otherwise invisible: it grants nothing, by design, but
// a rule that cannot be SEEN is a rule that cannot be deleted, and it would
// sit in the table forever. PUT below refuses to create one, so this can only
// arrive by a direct DB edit or a role retired in a later version — rare, but
// "rare" is not "impossible to clean up".
//
// Reads the raw store rows separately from Snapshot() (which only ever holds
// what MergeMapping kept) and degrades to nothing — not a failed request —
// when the store is unreachable, the same as handleServiceGroups: the cache's
// merged snapshot above is still a complete, correct answer for everything
// but this one edge case.
func (a *API) invalidOIDCMappingRows(ctx context.Context) []oidcMappingDTO {
	store, err := a.store()
	if err != nil {
		return nil
	}
	rows, err := store.ListOIDCGroupMappings(ctx)
	if err != nil {
		slog.Warn("oidc mapping: raw rows unreadable, an invalid-role row (if any) may be hidden this read", "error", err)
		return nil
	}
	var out []oidcMappingDTO
	for _, row := range rows {
		if _, ok := auth.ParseRole(row.Role); ok {
			continue
		}
		out = append(out, oidcMappingDTO{
			Group:       row.Group,
			Projects:    nonNilSlice(row.Projects),
			Source:      "db",
			Editable:    true,
			Invalid:     true,
			InvalidRole: row.Role,
		})
	}
	return out
}

// declaredOIDCGroup reports whether group is chart-declared right now, i.e.
// auth.MergeMapping contributed a "config" row for it. Reading this off the
// cache's own snapshot — rather than a.cfg.OIDCSettings directly — keeps one
// source of truth: it is exactly the same declaration the merge and the list
// handler above already agree on.
func (a *API) declaredOIDCGroup(group string) bool {
	for _, m := range a.cfg.OIDCMapping.Snapshot() {
		if m.Source == "config" && m.Group == group {
			return true
		}
	}
	return false
}

// handleUpsertOIDCMapping saves (or overwrites) one authored rule by group,
// taken from the path — the body's job is only role and projects. A group
// the chart also declares is accepted, not refused: unlike service groups, an
// authored rule here is allowed to collide with a config-declared name,
// because auth.MergeMapping already has a defined, tested answer for that
// collision (config wins, the row is marked Shadowed) — refusing the write
// here would just be re-implementing, worse, a check the merge already makes
// correctly.
func (a *API) handleUpsertOIDCMapping(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	group := r.PathValue("group")
	in, err := decodeOIDCMappingInput(w, r)
	if err != nil {
		return err
	}
	role, ok := auth.ParseRole(in.Role)
	if !ok {
		return badRequest("unknown role %q", in.Role)
	}
	m := storage.OIDCGroupMapping{
		Group:     group,
		Role:      string(role),
		Projects:  in.Projects,
		CreatedBy: identityUserID(r.Context()),
	}
	if err := store.SaveOIDCGroupMapping(r.Context(), m); err != nil {
		return err
	}
	a.refreshOIDCMapping(r.Context())
	writeJSON(w, http.StatusOK, oidcMappingDTO{
		Group:    group,
		Role:     string(role),
		Projects: nonNilSlice(in.Projects),
		Source:   "db",
		Editable: true,
		Shadowed: a.declaredOIDCGroup(group),
	})
	return nil
}

// handleDeleteOIDCMapping tombstones an AUTHORED rule. What it refuses is a
// chart-declared rule with no authored row behind it: the chart keeps
// contributing a "config" row for that name on every future read regardless of
// what happens here, so a delete that appeared to succeed would just leave the
// admin watching the group reappear — the honest answer is to say where it
// actually lives instead of pretending the delete did something.
//
// The order matters. Refusing on the declared name ALONE would strand the one
// row an admin most wants gone: PUT deliberately accepts a group the chart
// also declares, storing it Shadowed so the UI can explain why it stopped
// applying — and if delete then refused that name, the admin's own rule could
// only be cleared by resetting every rule they ever wrote. So the authored row
// is deleted whenever one exists, shadowed or not, and the 400 is reserved for
// the case where there is genuinely nothing here to delete.
func (a *API) handleDeleteOIDCMapping(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	group := r.PathValue("group")
	// The store answers ErrNotFound for an absent or already-deleted group,
	// which the error mapper turns into a 404 — except when the chart declares
	// the name, where "where it really lives" is the more useful answer than
	// "not found".
	if err := store.DeleteOIDCGroupMapping(r.Context(), group); err != nil {
		if errors.Is(err, storage.ErrNotFound) && a.declaredOIDCGroup(group) {
			return badRequest("OIDC group %q is declared in the chart's auth.oidc.mapping and cannot be deleted here", group)
		}
		return err
	}
	a.refreshOIDCMapping(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleResetOIDCMapping clears every authored rule, returning the install to
// exactly what the chart declares — the "start over" hammer paired with the
// individual delete's refusal to touch a chart-declared name.
func (a *API) handleResetOIDCMapping(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	if err := store.ResetOIDCGroupMappings(r.Context()); err != nil {
		return err
	}
	a.refreshOIDCMapping(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// refreshOIDCMapping re-derives the merged snapshot right after a write, so
// THIS replica reflects the admin's own edit immediately instead of waiting
// up to 15s for the next poll (cmd/hub/oidc.go); other replicas still pick it
// up on their own poll. A refresh failure does not fail the write — the row
// is already durably saved in ClickHouse — but is worth its own log line
// here, distinct from Refresh's internal one, since this one names the write
// that triggered it.
func (a *API) refreshOIDCMapping(ctx context.Context) {
	if err := a.cfg.OIDCMapping.Refresh(ctx); err != nil {
		slog.Warn("oidc mapping: post-write refresh failed, this replica will catch up on the next poll", "error", err)
	}
}
