package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// OAuth 2.1 authorization-server storage.
//
// Same shape as authtoken.go throughout: ReplacingMergeTree keyed on the row's
// identifier, FINAL on read, revocation as a tombstone, and chEpoch/toCH/fromCH
// at the edges because Go's zero time is year 1 and DateTime64 starts in 1900.
// These tables are bounded by client and consent count, not by telemetry, so
// FINAL is cheap for the same reason it is on auth_token.

func (s *Store) CreateOAuthClient(ctx context.Context, c storage.OAuthClient) error {
	err := s.conn.Exec(ctx, `
INSERT INTO oauth_client (ClientID, Name, RedirectURIs, GrantTypes, TokenAuthMethod,
    SecretHash, Scope, SoftwareID, ClientURI, LogoURI, RegisteredIP, LastUsedAt, Revoked, CreatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		c.ClientID, c.Name, c.RedirectURIs, c.GrantTypes, c.TokenAuthMethod,
		c.SecretHash, c.Scope, c.SoftwareID, c.ClientURI, c.LogoURI, c.RegisteredIP,
		toCH(c.LastUsedAt), c.CreatedAt)
	if err != nil {
		return fmt.Errorf("create oauth client: %w", err)
	}
	return nil
}

func scanClient(sc interface{ Scan(...any) error }) (storage.OAuthClient, error) {
	var c storage.OAuthClient
	err := sc.Scan(&c.ClientID, &c.Name, &c.RedirectURIs, &c.GrantTypes, &c.TokenAuthMethod,
		&c.SecretHash, &c.Scope, &c.SoftwareID, &c.ClientURI, &c.LogoURI, &c.RegisteredIP,
		&c.LastUsedAt, &c.CreatedAt)
	if err != nil {
		return storage.OAuthClient{}, err
	}
	c.LastUsedAt = fromCH(c.LastUsedAt)
	return c, nil
}

const clientCols = `ClientID, Name, RedirectURIs, GrantTypes, TokenAuthMethod,
    SecretHash, Scope, SoftwareID, ClientURI, LogoURI, RegisteredIP, LastUsedAt, CreatedAt`

func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (storage.OAuthClient, error) {
	c, err := scanClient(s.conn.QueryRow(ctx, `
SELECT `+clientCols+`
FROM oauth_client FINAL
WHERE ClientID = ? AND Revoked = 0`, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return storage.OAuthClient{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.OAuthClient{}, fmt.Errorf("get oauth client: %w", err)
	}
	return c, nil
}

func (s *Store) ListOAuthClients(ctx context.Context) ([]storage.OAuthClient, error) {
	rows, err := s.conn.Query(ctx, `
SELECT `+clientCols+`
FROM oauth_client FINAL
WHERE Revoked = 0
ORDER BY CreatedAt DESC, ClientID`)
	if err != nil {
		return nil, fmt.Errorf("list oauth clients: %w", err)
	}
	defer rows.Close()

	var out []storage.OAuthClient
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("scan oauth client: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RevokeOAuthClient tombstones a registration. Its grants are NOT cascaded
// here: a grant is a person's own consent record, and it carries its own
// Revoked flag that IdentityFromOAuthToken checks on every request — so a
// revoked client stops working immediately without rewriting anyone's history.
func (s *Store) RevokeOAuthClient(ctx context.Context, clientID string) error {
	c, err := s.GetOAuthClient(ctx, clientID)
	if err != nil {
		return err
	}
	err = s.conn.Exec(ctx, `
INSERT INTO oauth_client (ClientID, Name, RedirectURIs, GrantTypes, TokenAuthMethod,
    SecretHash, Scope, SoftwareID, ClientURI, LogoURI, RegisteredIP, LastUsedAt, Revoked, CreatedAt, UpdatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, now64(3))`,
		c.ClientID, c.Name, c.RedirectURIs, c.GrantTypes, c.TokenAuthMethod,
		c.SecretHash, c.Scope, c.SoftwareID, c.ClientURI, c.LogoURI, c.RegisteredIP,
		toCH(c.LastUsedAt), c.CreatedAt)
	if err != nil {
		return fmt.Errorf("revoke oauth client: %w", err)
	}
	return nil
}

func (s *Store) CreateOAuthGrant(ctx context.Context, g storage.OAuthGrant) error {
	err := s.conn.Exec(ctx, `
INSERT INTO oauth_grant (GrantID, ClientID, UserID, Scope, Project, Resource, Revoked, CreatedAt)
VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		g.GrantID, g.ClientID, g.UserID, g.Scope, g.Project, g.Resource, g.CreatedAt)
	if err != nil {
		return fmt.Errorf("create oauth grant: %w", err)
	}
	return nil
}

func (s *Store) GetOAuthGrant(ctx context.Context, grantID string) (storage.OAuthGrant, error) {
	var g storage.OAuthGrant
	err := s.conn.QueryRow(ctx, `
SELECT GrantID, ClientID, UserID, Scope, Project, Resource, CreatedAt
FROM oauth_grant FINAL
WHERE GrantID = ? AND Revoked = 0`, grantID).
		Scan(&g.GrantID, &g.ClientID, &g.UserID, &g.Scope, &g.Project, &g.Resource, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.OAuthGrant{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.OAuthGrant{}, fmt.Errorf("get oauth grant: %w", err)
	}
	return g, nil
}

func (s *Store) ListOAuthGrants(ctx context.Context, userID string) ([]storage.OAuthGrant, error) {
	rows, err := s.conn.Query(ctx, `
SELECT GrantID, ClientID, UserID, Scope, Project, Resource, CreatedAt
FROM oauth_grant FINAL
WHERE UserID = ? AND Revoked = 0
ORDER BY CreatedAt DESC, GrantID`, userID)
	if err != nil {
		return nil, fmt.Errorf("list oauth grants: %w", err)
	}
	defer rows.Close()

	var out []storage.OAuthGrant
	for rows.Next() {
		var g storage.OAuthGrant
		if err := rows.Scan(&g.GrantID, &g.ClientID, &g.UserID, &g.Scope,
			&g.Project, &g.Resource, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan oauth grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeOAuthGrant is scoped by userID for the same reason RevokeAuthToken is:
// a person revokes their OWN consent, and an unscoped id would let one user
// revoke another's.
func (s *Store) RevokeOAuthGrant(ctx context.Context, userID, grantID string) error {
	g, err := s.GetOAuthGrant(ctx, grantID)
	if err != nil {
		return err
	}
	if g.UserID != userID {
		return storage.ErrNotFound
	}
	err = s.conn.Exec(ctx, `
INSERT INTO oauth_grant (GrantID, ClientID, UserID, Scope, Project, Resource, Revoked, CreatedAt, UpdatedAt)
VALUES (?, ?, ?, ?, ?, ?, 1, ?, now64(3))`,
		g.GrantID, g.ClientID, g.UserID, g.Scope, g.Project, g.Resource, g.CreatedAt)
	if err != nil {
		return fmt.Errorf("revoke oauth grant: %w", err)
	}
	return nil
}

func (s *Store) CreateOAuthAuthCode(ctx context.Context, c storage.OAuthAuthCode) error {
	err := s.conn.Exec(ctx, `
INSERT INTO oauth_auth_code (CodeHash, ClientID, UserID, GrantID, RedirectURI,
    Resource, Scope, Project, Challenge, ExpiresAt, Consumed, CreatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		c.CodeHash, c.ClientID, c.UserID, c.GrantID, c.RedirectURI,
		c.Resource, c.Scope, c.Project, c.Challenge, toCH(c.ExpiresAt), c.CreatedAt)
	if err != nil {
		return fmt.Errorf("create oauth auth code: %w", err)
	}
	return nil
}

// GetOAuthAuthCode returns a code whether or not it is expired or consumed.
// Both are decided one layer up — the same split GetAuthTokenByHash makes for
// expiry — because a replayed code must revoke its whole family, which needs
// the row rather than a not-found.
func (s *Store) GetOAuthAuthCode(ctx context.Context, codeHash string) (storage.OAuthAuthCode, error) {
	var c storage.OAuthAuthCode
	var consumed uint8
	err := s.conn.QueryRow(ctx, `
SELECT CodeHash, ClientID, UserID, GrantID, RedirectURI, Resource, Scope, Project,
       Challenge, ExpiresAt, Consumed, CreatedAt
FROM oauth_auth_code FINAL
WHERE CodeHash = ?`, codeHash).
		Scan(&c.CodeHash, &c.ClientID, &c.UserID, &c.GrantID, &c.RedirectURI,
			&c.Resource, &c.Scope, &c.Project, &c.Challenge, &c.ExpiresAt, &consumed, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.OAuthAuthCode{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.OAuthAuthCode{}, fmt.Errorf("get oauth auth code: %w", err)
	}
	c.ExpiresAt = fromCH(c.ExpiresAt)
	c.Consumed = consumed == 1
	return c, nil
}

// ConsumeOAuthAuthCode marks a code used, returning ErrNotFound if it already
// was. See the single-use note in the api package: without compare-and-swap
// this is one of three defences, not the only one.
func (s *Store) ConsumeOAuthAuthCode(ctx context.Context, codeHash string) error {
	c, err := s.GetOAuthAuthCode(ctx, codeHash)
	if err != nil {
		return err
	}
	if c.Consumed {
		return storage.ErrNotFound
	}
	err = s.conn.Exec(ctx, `
INSERT INTO oauth_auth_code (CodeHash, ClientID, UserID, GrantID, RedirectURI,
    Resource, Scope, Project, Challenge, ExpiresAt, Consumed, CreatedAt, UpdatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, now64(3))`,
		c.CodeHash, c.ClientID, c.UserID, c.GrantID, c.RedirectURI,
		c.Resource, c.Scope, c.Project, c.Challenge, toCH(c.ExpiresAt), c.CreatedAt)
	if err != nil {
		return fmt.Errorf("consume oauth auth code: %w", err)
	}
	return nil
}

func (s *Store) CreateOAuthToken(ctx context.Context, t storage.OAuthToken) error {
	err := s.conn.Exec(ctx, `
INSERT INTO oauth_token (TokenHash, Kind, GrantID, ClientID, UserID, Resource,
    Scope, Project, ExpiresAt, LastUsedAt, Revoked, CreatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		t.TokenHash, t.Kind, t.GrantID, t.ClientID, t.UserID, t.Resource,
		t.Scope, t.Project, toCH(t.ExpiresAt), toCH(t.LastUsedAt), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("create oauth token: %w", err)
	}
	return nil
}

// GetOAuthTokenByHash returns a live token, ErrNotFound when unknown or
// revoked. Expiry is decided one layer up, as it is for API tokens.
func (s *Store) GetOAuthTokenByHash(ctx context.Context, tokenHash string) (storage.OAuthToken, error) {
	var t storage.OAuthToken
	err := s.conn.QueryRow(ctx, `
SELECT TokenHash, Kind, GrantID, ClientID, UserID, Resource, Scope, Project,
       ExpiresAt, LastUsedAt, CreatedAt
FROM oauth_token FINAL
WHERE TokenHash = ? AND Revoked = 0`, tokenHash).
		Scan(&t.TokenHash, &t.Kind, &t.GrantID, &t.ClientID, &t.UserID, &t.Resource,
			&t.Scope, &t.Project, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.OAuthToken{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.OAuthToken{}, fmt.Errorf("get oauth token: %w", err)
	}
	t.ExpiresAt = fromCH(t.ExpiresAt)
	t.LastUsedAt = fromCH(t.LastUsedAt)
	return t, nil
}

func (s *Store) RevokeOAuthToken(ctx context.Context, tokenHash string) error {
	t, err := s.GetOAuthTokenByHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	return s.tombstoneToken(ctx, t)
}

// RevokeOAuthTokensForGrant kills the whole family: what a detected refresh
// replay and an explicit "disconnect this application" both need.
func (s *Store) RevokeOAuthTokensForGrant(ctx context.Context, grantID string) error {
	rows, err := s.conn.Query(ctx, `
SELECT TokenHash, Kind, GrantID, ClientID, UserID, Resource, Scope, Project,
       ExpiresAt, LastUsedAt, CreatedAt
FROM oauth_token FINAL
WHERE GrantID = ? AND Revoked = 0`, grantID)
	if err != nil {
		return fmt.Errorf("list oauth tokens for grant: %w", err)
	}
	var live []storage.OAuthToken
	for rows.Next() {
		var t storage.OAuthToken
		if err := rows.Scan(&t.TokenHash, &t.Kind, &t.GrantID, &t.ClientID, &t.UserID,
			&t.Resource, &t.Scope, &t.Project, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan oauth token: %w", err)
		}
		t.ExpiresAt = fromCH(t.ExpiresAt)
		t.LastUsedAt = fromCH(t.LastUsedAt)
		live = append(live, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range live {
		if err := s.tombstoneToken(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) tombstoneToken(ctx context.Context, t storage.OAuthToken) error {
	err := s.conn.Exec(ctx, `
INSERT INTO oauth_token (TokenHash, Kind, GrantID, ClientID, UserID, Resource,
    Scope, Project, ExpiresAt, LastUsedAt, Revoked, CreatedAt, UpdatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, now64(3))`,
		t.TokenHash, t.Kind, t.GrantID, t.ClientID, t.UserID, t.Resource,
		t.Scope, t.Project, toCH(t.ExpiresAt), toCH(t.LastUsedAt), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("revoke oauth token: %w", err)
	}
	return nil
}

// TouchOAuthToken records last use, debounced by the caller exactly as
// TouchAuthToken is — an access token is presented on every tool call, and
// writing a row each time would turn a read path into a write path.
func (s *Store) TouchOAuthToken(ctx context.Context, tokenHash string, at time.Time) error {
	t, err := s.GetOAuthTokenByHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	t.LastUsedAt = at
	err = s.conn.Exec(ctx, `
INSERT INTO oauth_token (TokenHash, Kind, GrantID, ClientID, UserID, Resource,
    Scope, Project, ExpiresAt, LastUsedAt, Revoked, CreatedAt, UpdatedAt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, now64(3))`,
		t.TokenHash, t.Kind, t.GrantID, t.ClientID, t.UserID, t.Resource,
		t.Scope, t.Project, toCH(t.ExpiresAt), toCH(t.LastUsedAt), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("touch oauth token: %w", err)
	}
	return nil
}
