package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type userDTO struct {
	ID       string       `json:"id"`
	Email    string       `json:"email"`
	Name     string       `json:"name"`
	Origin   string       `json:"origin"`
	Disabled bool         `json:"disabled"`
	Grants   []auth.Grant `json:"grants"`
}

type createUserRequest struct {
	Email    string       `json:"email"`
	Name     string       `json:"name"`
	Password string       `json:"password"`
	Grants   []auth.Grant `json:"grants"`
}

type updateUserRequest struct {
	Name     *string       `json:"name"`
	Password *string       `json:"password"`
	Disabled *bool         `json:"disabled"`
	Grants   *[]auth.Grant `json:"grants"`
}

// validGrants rejects a role the auth package doesn't recognize, a blank
// scope — the same "no hardcoded strings, no silent drops" rule Identity
// itself follows when parsing stored grants (auth.go's ParseRole) — or two
// grants naming the same scope: the fake and the real ClickHouse
// ReplaceAuthGrants would disagree on which one wins (last Append in the
// batch vs. undefined map iteration order), so reject it up front instead of
// leaving the winner unspecified.
func validGrants(gs []auth.Grant) error {
	seen := make(map[string]bool, len(gs))
	for _, g := range gs {
		if _, ok := auth.ParseRole(string(g.Role)); !ok {
			return badRequest("invalid role %q", g.Role)
		}
		if g.Scope == "" {
			return badRequest("grant scope required")
		}
		if seen[g.Scope] {
			return badRequest("duplicate grant scope %q", g.Scope)
		}
		seen[g.Scope] = true
	}
	return nil
}

// handleListUsers returns every local/SSO user with their current grants.
// Credential-adjacent (email is PII, grants reveal access), so no-store.
func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	users, err := st.ListAuthUsers(r.Context())
	if err != nil {
		return err
	}
	out := struct {
		Users []userDTO `json:"users"`
	}{Users: []userDTO{}}
	for _, u := range users {
		grants, err := st.ListAuthGrants(r.Context(), u.ID)
		if err != nil {
			return err
		}
		out.Users = append(out.Users, toUserDTO(u, grants))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleCreateUser creates a local user with an initial grant set. A
// duplicate email is a 409 (Conflict) — matching handleCreateAlertChannel's
// precedent for admin-authored uniqueness conflicts on this API — not the
// generic 400 used for malformed input.
func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	var req createUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	if req.Email == "" || req.Password == "" {
		return badRequest("email and password required")
	}
	if err := validGrants(req.Grants); err != nil {
		return err
	}
	// Email uniqueness is enforced at the APPLICATION layer only — auth_user
	// has no unique index (0010_auth.sql: "tables are tiny"). This
	// check-then-insert is a TOCTOU race under concurrent creates for the
	// same email; accepted, same as ReplaceAuthGrants' documented
	// last-writer-wins race — admin-driven user creation is effectively
	// single-writer in practice.
	if _, err := st.GetAuthUserByEmail(r.Context(), req.Email); err == nil {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("a user with email %q already exists", req.Email)}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return badRequest("invalid password: %v", err)
	}
	u := storage.AuthUser{
		ID:           auth.NewID(),
		Email:        req.Email,
		Name:         req.Name,
		PasswordHash: hash,
		Origin:       "local",
	}
	if err := st.SaveAuthUser(r.Context(), u); err != nil {
		return err
	}
	grants := toStorageGrants(u.ID, req.Grants)
	if err := st.ReplaceAuthGrants(r.Context(), u.ID, grants); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toUserDTO(u, grants))
	return nil
}

// handleUpdateUser edits name/password/disabled and, when Grants is present,
// replaces the grant set wholesale. Disabling is the reversible first step —
// see handleDeleteUser for the explicit hard-delete that follows it
// (design/2026-08-06-users-crud-password.md, amending disable-not-delete).
func (a *API) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	u, err := st.GetAuthUser(r.Context(), r.PathValue("id"))
	if err != nil {
		return err // storage.ErrNotFound -> 404 via handle()
	}
	var req updateUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}

	// Validate the WHOLE request before any write: rejecting a bad grant
	// role after the password (or Disabled) field already persisted would
	// leave a half-applied update behind — a 400 must mean nothing changed.
	if req.Grants != nil {
		if err := validGrants(*req.Grants); err != nil {
			return err
		}
	}
	if err := checkSelfLockout(r, u, req); err != nil {
		return err
	}

	if req.Name != nil {
		u.Name = *req.Name
	}
	// An absent or empty Password means "leave unchanged" — deliberately
	// asymmetric with handleCreateUser, where an empty password is a 400: an
	// admin editing just the name/grants/disabled flag shouldn't be forced
	// to also supply a new password.
	if req.Password != nil && *req.Password != "" {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			return badRequest("invalid password: %v", err)
		}
		u.PasswordHash = hash
	}
	if req.Disabled != nil {
		u.Disabled = *req.Disabled
	}
	if err := st.SaveAuthUser(r.Context(), u); err != nil {
		return err
	}
	if req.Password != nil && *req.Password != "" {
		// Rotation ends existing sessions — that's the whole point of an
		// incident password reset: a disable-then-re-enable cycle must not
		// silently revive the attacker's cookie, and a same-password no-op
		// change shouldn't have to bump every session either, so this only
		// fires when the password actually changed.
		if err := st.RevokeAuthSessionsForUser(r.Context(), u.ID); err != nil {
			return err
		}
	}

	grants, err := st.ListAuthGrants(r.Context(), u.ID)
	if err != nil {
		return err
	}
	if req.Grants != nil {
		grants = toStorageGrants(u.ID, *req.Grants)
		if err := st.ReplaceAuthGrants(r.Context(), u.ID, grants); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, toUserDTO(u, grants))
	return nil
}

// handleDeleteUser hard-deletes a DISABLED user; a live user answers 409 —
// disable is the reversible first step, delete the explicit cleanup
// (design/2026-08-06-users-crud-password.md, amending disable-not-delete).
//
// For origin=oidc this deletes only the LOCAL record. Disabled is the flag
// CompleteSSO checks, so deleting a disabled SSO user REMOVES their lockout:
// they return on their next IdP login with group-mapped grants only. Disable,
// not delete, is how you lock an SSO user out.
//
// Order matters: sessions → grants → user. A failure mid-sequence never
// leaves a half-deleted user who can still sign in, and every step is
// idempotent, so retrying the DELETE completes it. It is NOT an undo: once
// the grants are tombstoned the old set is unrecoverable from any read the
// UI can reach. Deleting the user first would be worse — orphaned grants
// resurrect on ids that recur (bootstrap-admin, demo-viewer, oidc|<sub>).
func (a *API) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	u, err := st.GetAuthUser(r.Context(), r.PathValue("id"))
	if err != nil {
		return err // storage.ErrNotFound -> 404 via handle()
	}
	if !u.Disabled {
		return &apiError{status: http.StatusConflict, message: "disable the user before deleting"}
	}
	// The demo account is server-managed: EnsureDemoUser recreates it (with
	// its chart-configured password) on every boot, so a "successful" delete
	// would silently undo itself on the next restart.
	if a.cfg.DemoEnabled && u.Email == a.cfg.DemoEmail {
		return &apiError{status: http.StatusConflict, message: "the demo account is managed by the server and is recreated on restart"}
	}
	if err := st.RevokeAuthSessionsForUser(r.Context(), u.ID); err != nil {
		return err
	}
	if err := st.ReplaceAuthGrants(r.Context(), u.ID, nil); err != nil {
		return err
	}
	if err := st.DeleteAuthUser(r.Context(), u.ID); err != nil {
		return err
	}
	// This endpoint destroys an identity and the AEP rules out an audit
	// pipeline for it — this log line is the only forensic record that will
	// ever exist of who deleted whom.
	slog.Info("deleted user", "id", u.ID, "email", u.Email, "origin", u.Origin, "actor", requestedBy(r))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// checkSelfLockout rejects an admin's PUT against their OWN account when it
// would disable them or drop their global-admin grant: both are effectively
// unrecoverable without direct DB surgery (no other admin left to undo it).
// Editing your own name (or any other field) is unaffected.
func checkSelfLockout(r *http.Request, u storage.AuthUser, req updateUserRequest) error {
	id := identityFrom(r.Context())
	if id == nil || id.UserID != u.ID {
		return nil
	}
	if req.Disabled != nil && *req.Disabled {
		return badRequest("cannot disable or de-admin your own account")
	}
	if req.Grants != nil && !hasGlobalAdmin(*req.Grants) {
		return badRequest("cannot disable or de-admin your own account")
	}
	return nil
}

func hasGlobalAdmin(gs []auth.Grant) bool {
	for _, g := range gs {
		if g.Scope == "*" && g.Role == auth.RoleAdmin {
			return true
		}
	}
	return false
}

func toUserDTO(u storage.AuthUser, grants []storage.AuthGrant) userDTO {
	d := userDTO{
		ID: u.ID, Email: u.Email, Name: u.Name, Origin: u.Origin,
		Disabled: u.Disabled, Grants: []auth.Grant{},
	}
	for _, g := range grants {
		if r, ok := auth.ParseRole(g.Role); ok {
			d.Grants = append(d.Grants, auth.Grant{Scope: g.Scope, Role: r})
		}
	}
	return d
}

func toStorageGrants(userID string, gs []auth.Grant) []storage.AuthGrant {
	out := make([]storage.AuthGrant, 0, len(gs))
	for _, g := range gs {
		out = append(out, storage.AuthGrant{UserID: userID, Scope: g.Scope, Role: string(g.Role)})
	}
	return out
}
