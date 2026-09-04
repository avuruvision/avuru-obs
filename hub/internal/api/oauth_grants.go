package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Connected applications: the consents a person has given, and the ability to
// take one back.
//
// Without this, consent is a thing you can grant and cannot revoke from the
// UI — and the whole disclosure on the consent screen ("this sends your traces
// and log bodies out of the installation") rests on being able to change your
// mind afterwards.

type grantDTO struct {
	ID         string    `json:"id"`
	ClientID   string    `json:"clientId"`
	ClientName string    `json:"clientName"`
	Project    string    `json:"project"`
	Scopes     string    `json:"scopes"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (a *API) handleListGrants(w http.ResponseWriter, r *http.Request) error {
	id := identityFrom(r.Context())
	st, err := a.store()
	if err != nil {
		return err
	}
	grants, err := st.ListOAuthGrants(r.Context(), id.UserID)
	if err != nil {
		return err
	}
	out := make([]grantDTO, 0, len(grants))
	for _, g := range grants {
		// The client's own name, still unverified — shown because it is what
		// the person saw when they consented, not because it is trustworthy.
		name := g.ClientID
		if c, err := st.GetOAuthClient(r.Context(), g.ClientID); err == nil {
			name = c.Name
		}
		out = append(out, grantDTO{
			ID: g.GrantID, ClientID: g.ClientID, ClientName: name,
			Project: g.Project, Scopes: g.Scope, CreatedAt: g.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": out})
	return nil
}

// handleRevokeGrant disconnects an application. Scoped to the caller's own
// consents — the store enforces it too, but saying so here keeps the intent
// visible at the route.
func (a *API) handleRevokeGrant(w http.ResponseWriter, r *http.Request) error {
	id := identityFrom(r.Context())
	grantID := r.PathValue("id")
	st, err := a.store()
	if err != nil {
		return err
	}
	// Tokens first: if the grant delete succeeded and this failed, live tokens
	// would outlive the consent that justified them.
	if err := st.RevokeOAuthTokensForGrant(r.Context(), grantID); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err := st.RevokeOAuthGrant(r.Context(), id.UserID, grantID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
