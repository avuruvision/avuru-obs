package oauth

// The OAuth error vocabulary. Two audiences read these: a client that must
// decide whether to retry, and a person who has to work out what went wrong —
// so every one carries a description written for the second.
type Error struct {
	Code        string
	Description string
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

const (
	ErrInvalidRequest         = "invalid_request"
	ErrInvalidClient          = "invalid_client"
	ErrInvalidGrant           = "invalid_grant"
	ErrUnauthorizedClient     = "unauthorized_client"
	ErrUnsupportedGrantType   = "unsupported_grant_type"
	ErrInvalidScope           = "invalid_scope"
	ErrAccessDenied           = "access_denied"
	ErrServerError            = "server_error"
	ErrInvalidClientMetadata  = "invalid_client_metadata"
	ErrInvalidRedirectURI     = "invalid_redirect_uri"
	ErrUnsupportedResponseTyp = "unsupported_response_type"
)

// Redirectable reports whether this error may be reported by redirecting to the
// client's redirect_uri.
//
// The distinction is the open-redirect defence, and it is about ORDER: a
// failure to validate the client or its redirect URI must be shown to the
// person, never bounced to a URI that has not been proven to belong to a
// registered client. Everything after that point may redirect.
func (e *Error) Redirectable() bool {
	switch e.Code {
	case ErrInvalidClient, ErrInvalidRedirectURI, ErrInvalidClientMetadata:
		return false
	default:
		return true
	}
}
