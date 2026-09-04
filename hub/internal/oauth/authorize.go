package oauth

import (
	"net/url"
	"strings"
)

// AuthorizeRequest is a validated /authorize query. Both the GET that shows the
// consent screen and the POST that acts on it parse into this, through
// ParseAuthorize, so the decision is taken against the same values that were
// shown — there is no second, looser reading of the request.
type AuthorizeRequest struct {
	ClientID      string
	RedirectURI   string
	State         string
	Scope         string
	Resource      string
	CodeChallenge string
}

// Values re-serialises the request. The consent page carries these forward, so
// no server-side pending-request record is needed: the POST re-validates from
// scratch against the client registry and a live session.
func (a AuthorizeRequest) Values() url.Values {
	v := url.Values{}
	v.Set("client_id", a.ClientID)
	v.Set("redirect_uri", a.RedirectURI)
	v.Set("response_type", "code")
	v.Set("code_challenge", a.CodeChallenge)
	v.Set("code_challenge_method", ChallengeMethodS256)
	v.Set("scope", a.Scope)
	v.Set("resource", a.Resource)
	if a.State != "" {
		v.Set("state", a.State)
	}
	return v
}

// ParseAuthorize validates an /authorize request against the client registry.
//
// ORDER IS THE POINT, and it is the open-redirect defence. The client and its
// redirect URI are proven FIRST; only after that may any error be reported by
// redirecting, because only then is the redirect URI known to belong to a
// registered client. Everything before that must be shown to the person.
//
// `registered` is the client's redirect list; pass nil when the client is
// unknown, which is itself a non-redirectable failure.
func ParseAuthorize(q url.Values, registered []string, canonicalResource string) (AuthorizeRequest, *Error) {
	clientID := strings.TrimSpace(q.Get("client_id"))
	if clientID == "" {
		return AuthorizeRequest{}, &Error{Code: ErrInvalidClient, Description: "client_id is required"}
	}
	if registered == nil {
		return AuthorizeRequest{}, &Error{Code: ErrInvalidClient, Description: "unknown client_id"}
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" {
		return AuthorizeRequest{}, &Error{Code: ErrInvalidRedirectURI, Description: "redirect_uri is required"}
	}
	if !MatchRedirectURI(registered, redirect) {
		// Never bounced to the supplied URI: that is exactly the redirect this
		// check exists to refuse.
		return AuthorizeRequest{}, &Error{
			Code:        ErrInvalidRedirectURI,
			Description: "redirect_uri does not exactly match a registered URI for this client",
		}
	}

	// Past this line, errors may be reported as a redirect.
	if rt := q.Get("response_type"); rt != "code" {
		return AuthorizeRequest{}, &Error{
			Code:        ErrUnsupportedResponseTyp,
			Description: "only response_type=code is supported",
		}
	}
	if m := q.Get("code_challenge_method"); m != ChallengeMethodS256 {
		// Including the empty string. PKCE is mandatory here, and `plain` is
		// refused rather than tolerated — accepting it would make advertising
		// only S256 a suggestion.
		return AuthorizeRequest{}, &Error{
			Code:        ErrInvalidRequest,
			Description: "code_challenge_method must be S256",
		}
	}
	challenge := q.Get("code_challenge")
	if !ValidChallenge(challenge) {
		return AuthorizeRequest{}, &Error{Code: ErrInvalidRequest, Description: "code_challenge is missing or malformed"}
	}

	scope := NormalizeScope(q.Get("scope"))
	if raw := strings.Fields(q.Get("scope")); len(raw) > 0 && scope == "" {
		return AuthorizeRequest{}, &Error{Code: ErrInvalidScope, Description: "no requested scope is issued by this server"}
	}
	if scope == "" {
		scope = ScopeMCPRead
	}
	if !HasScope(scope, ScopeMCPRead) {
		return AuthorizeRequest{}, &Error{Code: ErrInvalidScope, Description: "the " + ScopeMCPRead + " scope is required"}
	}

	// RFC 8707. Absent means this server's own resource — the only one it has —
	// rather than a failure, so a client that predates resource indicators
	// still works. Naming a DIFFERENT resource is refused: it is a request this
	// server cannot honour, and silently retargeting it would issue a token for
	// something the client did not ask for.
	resource := strings.TrimSpace(q.Get("resource"))
	if resource == "" {
		resource = canonicalResource
	}
	if resource != canonicalResource {
		return AuthorizeRequest{}, &Error{
			Code:        ErrInvalidRequest,
			Description: "resource does not match this server's protected resource",
		}
	}

	return AuthorizeRequest{
		ClientID:      clientID,
		RedirectURI:   redirect,
		State:         q.Get("state"),
		Scope:         scope,
		Resource:      resource,
		CodeChallenge: challenge,
	}, nil
}

// RedirectWithError builds the error redirect for a failure that is allowed to
// use one. state is echoed so the client can match the response to its request.
func RedirectWithError(redirectURI, state string, e *Error) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("error", e.Code)
	if e.Description != "" {
		q.Set("error_description", e.Description)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RedirectWithCode builds the success redirect.
func RedirectWithCode(redirectURI, code, state string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
