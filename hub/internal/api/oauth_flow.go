package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/oauth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The authorization-code flow.
//
// Consent is STATELESS: there is no pending-request table, no flow cookie and
// no in-memory map. The consent page carries the validated parameters and the
// POST re-runs the same validation — against the client registry, a live
// session and the CSRF origin check. An attacker supplying parameters gains
// nothing they could not have put in the original /authorize URL, and the
// design loses three pieces of state that could otherwise leak or expire.

// handleAuthorize is the browser entry point.
//
// It ends in a redirect every time: to /login when nobody is signed in, to the
// consent page when someone is, or to the client with an error. What it must
// never do is redirect to an unvalidated redirect_uri, which is why
// oauth.ParseAuthorize proves the client first and why Redirectable() gates
// the error path.
func (a *API) handleAuthorize(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	registered, err := a.registeredRedirects(r.Context(), q.Get("client_id"))
	if err != nil {
		return err
	}
	req, oerr := oauth.ParseAuthorize(q, registered, oauth.ResourceURI(a.cfg.PublicURL))
	if oerr != nil {
		if oerr.Redirectable() {
			http.Redirect(w, r, oauth.RedirectWithError(q.Get("redirect_uri"), q.Get("state"), oerr), http.StatusFound)
			return nil
		}
		// Not redirectable: the client or its redirect URI is unproven, so the
		// only safe audience for this failure is the person in front of it.
		return writeOAuthError(http.StatusBadRequest, oerr)
	}

	// A live session, or send them to sign in and come back. safeNext on the
	// login page accepts a same-origin path and returns here afterwards, so
	// this needs no UI change.
	id := a.sessionIdentity(r)
	if id == nil {
		next := oauth.PathAuthorize + "?" + req.Values().Encode()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
		return nil
	}
	if !holdsAnywhere(*id, auth.RoleViewer) {
		return writeOAuthError(http.StatusForbidden, &oauth.Error{
			Code:        oauth.ErrAccessDenied,
			Description: "this account cannot read any project, so there is nothing to share",
		})
	}

	http.Redirect(w, r, "/oauth/consent?"+req.Values().Encode(), http.StatusFound)
	return nil
}

// consentView is what the consent screen renders. Deliberately explicit about
// what is unverified: the client's own name is whatever it typed at
// registration, and the redirect host is the one fact a person can check.
type consentView struct {
	ClientID       string   `json:"clientId"`
	ClientName     string   `json:"clientName"`
	ClientVerified bool     `json:"clientVerified"`
	RedirectHost   string   `json:"redirectHost"`
	FirstUse       bool     `json:"firstUse"`
	Scopes         []string `json:"scopes"`
	Projects       []string `json:"projects"`
	DefaultProject string   `json:"defaultProject"`
	Resource       string   `json:"resource"`
}

// handleConsentView answers the consent page's questions. Session-authenticated
// like any other UI call.
func (a *API) handleConsentView(w http.ResponseWriter, r *http.Request) error {
	req, client, oerr := a.parseConsentRequest(r)
	if oerr != nil {
		return writeOAuthError(http.StatusBadRequest, oerr)
	}
	id := identityFrom(r.Context())
	host := ""
	if u, err := url.Parse(req.RedirectURI); err == nil {
		host = u.Host
	}
	projects := grantedProjects(id)
	first := true
	if st, err := a.store(); err == nil {
		if grants, err := st.ListOAuthGrants(r.Context(), id.UserID); err == nil {
			for _, g := range grants {
				if g.ClientID == client.ClientID {
					first = false
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, consentView{
		ClientID:       client.ClientID,
		ClientName:     client.Name,
		ClientVerified: false, // never true: nothing here is verified
		RedirectHost:   host,
		FirstUse:       first,
		Scopes:         strings.Fields(req.Scope),
		Projects:       projects,
		DefaultProject: defaultProject(projects),
		Resource:       req.Resource,
	})
	return nil
}

type consentDecision struct {
	Approve bool   `json:"approve"`
	Project string `json:"project"`
}

// handleConsentDecide mints the authorization code, or reports the refusal.
func (a *API) handleConsentDecide(w http.ResponseWriter, r *http.Request) error {
	req, client, oerr := a.parseConsentRequest(r)
	if oerr != nil {
		return writeOAuthError(http.StatusBadRequest, oerr)
	}
	var dec consentDecision
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&dec); err != nil {
		return decodeJSONError(err)
	}
	if !dec.Approve {
		writeJSON(w, http.StatusOK, map[string]string{
			"redirect": oauth.RedirectWithError(req.RedirectURI, req.State,
				&oauth.Error{Code: oauth.ErrAccessDenied, Description: "the request was declined"}),
		})
		return nil
	}

	id := identityFrom(r.Context())
	project := strings.TrimSpace(dec.Project)
	if project == "" {
		project = defaultProject(grantedProjects(id))
	}
	// The consenting user must actually be able to read what they are sharing.
	// Checked here rather than trusted from the page, because the page is a
	// client like any other.
	if !id.CanAccess(project, auth.RoleViewer) {
		return forbidden("no viewer access to project %q", project)
	}

	st, err := a.store()
	if err != nil {
		return err
	}
	now := time.Now()
	grant := storage.OAuthGrant{
		GrantID:   auth.NewID(),
		ClientID:  client.ClientID,
		UserID:    id.UserID,
		Scope:     req.Scope,
		Project:   project,
		Resource:  req.Resource,
		CreatedAt: now,
	}
	if err := st.CreateOAuthGrant(r.Context(), grant); err != nil {
		return err
	}
	rawCode, codeHash := auth.NewOAuthToken("avuruc_")
	if err := st.CreateOAuthAuthCode(r.Context(), storage.OAuthAuthCode{
		CodeHash:    codeHash,
		ClientID:    client.ClientID,
		UserID:      id.UserID,
		GrantID:     grant.GrantID,
		RedirectURI: req.RedirectURI,
		Resource:    req.Resource,
		Scope:       req.Scope,
		Project:     project,
		Challenge:   req.CodeChallenge,
		ExpiresAt:   now.Add(oauth.AuthCodeTTL),
		CreatedAt:   now,
	}); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"redirect": oauth.RedirectWithCode(req.RedirectURI, rawCode, req.State),
	})
	return nil
}

// parseConsentRequest re-validates the authorize parameters carried by the
// consent page. This is what makes the flow stateless — and safe: the client
// and its redirect URI are proven against the registry again, not trusted
// because they arrived from a page we rendered.
func (a *API) parseConsentRequest(r *http.Request) (oauth.AuthorizeRequest, storage.OAuthClient, *oauth.Error) {
	q := r.URL.Query()
	registered, err := a.registeredRedirects(r.Context(), q.Get("client_id"))
	if err != nil {
		return oauth.AuthorizeRequest{}, storage.OAuthClient{},
			&oauth.Error{Code: oauth.ErrServerError, Description: "could not read the client registry"}
	}
	req, oerr := oauth.ParseAuthorize(q, registered, oauth.ResourceURI(a.cfg.PublicURL))
	if oerr != nil {
		return oauth.AuthorizeRequest{}, storage.OAuthClient{}, oerr
	}
	st, err := a.store()
	if err != nil {
		return oauth.AuthorizeRequest{}, storage.OAuthClient{},
			&oauth.Error{Code: oauth.ErrServerError, Description: "store unavailable"}
	}
	client, err := st.GetOAuthClient(r.Context(), req.ClientID)
	if err != nil {
		return oauth.AuthorizeRequest{}, storage.OAuthClient{},
			&oauth.Error{Code: oauth.ErrInvalidClient, Description: "unknown client"}
	}
	return req, client, nil
}

func (a *API) registeredRedirects(ctx context.Context, clientID string) ([]string, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, nil
	}
	st, err := a.store()
	if err != nil {
		return nil, err
	}
	c, err := st.GetOAuthClient(ctx, clientID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.RedirectURIs, nil
}

// sessionIdentity resolves the browser session only — never a bearer token.
// /authorize is a page a person visits; a token presented here would be a
// client trying to consent on its owner's behalf.
func (a *API) sessionIdentity(r *http.Request) *auth.Identity {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	id, err := a.cfg.Auth.IdentityFromToken(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	return &id
}

func grantedProjects(id *auth.Identity) []string {
	if id == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, g := range id.Grants {
		if g.Scope == "" || seen[g.Scope] {
			continue
		}
		seen[g.Scope] = true
		out = append(out, g.Scope)
	}
	return out
}

func defaultProject(projects []string) string {
	for _, p := range projects {
		if p == storage.DefaultTenant {
			return p
		}
	}
	if len(projects) > 0 {
		return projects[0]
	}
	return storage.DefaultTenant
}

// codeClaims guards single use in this process.
//
// ClickHouse has no compare-and-swap, so two concurrent redemptions can both
// read Consumed=0. Three defences run together: this claim map, the stored
// Consumed flag, and revoking the whole token family when a replay is detected.
// Under more than one replica the map is per-process and the DB flag plus the
// family revocation are what remain — stated plainly rather than implied,
// because true single-use under HA needs leader election this product does not
// have. The same trade collectionMu and the alerting evaluator already make.
var codeClaims = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

func claimCode(hash string) bool {
	codeClaims.Lock()
	defer codeClaims.Unlock()
	now := time.Now()
	for h, t := range codeClaims.seen {
		if now.Sub(t) > 2*oauth.AuthCodeTTL {
			delete(codeClaims.seen, h)
		}
	}
	if _, taken := codeClaims.seen[hash]; taken {
		return false
	}
	codeClaims.seen[hash] = now
	return true
}
