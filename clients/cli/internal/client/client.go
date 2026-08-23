// Package client is the thin HTTP layer over the Hub API.
//
// Deliberately untyped: every call returns the decoded JSON as-is. The CLI
// formats a handful of known columns, but anything the API grows before the CLI
// models it is still reachable through `-o json`, so the client never becomes
// the reason a field is invisible.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/clients/cli/internal/config"
)

type Client struct {
	base    string
	token   string
	project string
	http    *http.Client
}

// New builds a client. An empty project means "whatever the token owner's
// default is" — the header is only sent when one was asked for, so the CLI
// never overrides a server-side default it did not choose.
func New(c config.Config, timeout time.Duration, project string) *Client {
	return &Client{base: c.URL, token: c.Token, project: project, http: &http.Client{Timeout: timeout}}
}

// APIError carries the status so callers can tell "you are not allowed" from
// "the hub is down" — a CI job wants to fail loudly on the first and retry the
// second.
type APIError struct {
	Status int
	Body   string
	Path   string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("%s: unauthorized — the token is missing, expired or revoked", e.Path)
	case http.StatusForbidden:
		return fmt.Sprintf("%s: forbidden — the token's owner has no access to this project", e.Path)
	case http.StatusNotFound:
		return fmt.Sprintf("%s: not found — the module that serves it may be disabled on this install", e.Path)
	}
	body := strings.TrimSpace(e.Body)
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	if body == "" {
		return fmt.Sprintf("%s: HTTP %d", e.Path, e.Status)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Path, e.Status, body)
}

// Get fetches path with query params and decodes the response into out.
func (c *Client) Get(ctx context.Context, path string, params url.Values, out any) error {
	u := c.base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if c.project != "" {
		req.Header.Set("X-Avuru-Tenant", c.project)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Body: string(body), Path: path}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}
