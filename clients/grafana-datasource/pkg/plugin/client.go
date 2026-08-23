// Package plugin is the Avuru Obs Grafana data source, backend half.
//
// A backend plugin rather than a browser-side one, for two reasons that are the
// whole design: the API token is stored in Grafana's encrypted secure settings
// and never reaches a browser, and queries leave the Grafana server rather than
// the user's machine, so a hub that is only reachable inside the cluster still
// works.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hubClient is the thin HTTP layer over the Hub API.
//
// Untyped for the same reason the CLI is: the plugin shapes a handful of known
// columns into frames, and anything the API grows before the plugin models it
// should not become invisible.
type hubClient struct {
	base  string
	token string
	http  *http.Client
}

func newHubClient(base, token string, timeout time.Duration) *hubClient {
	return &hubClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: timeout},
	}
}

func (c *hubClient) get(ctx context.Context, path string, params url.Values, project string, out any) error {
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
	if project != "" {
		req.Header.Set("X-Avuru-Tenant", project)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling the hub: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &apiError{status: resp.StatusCode, path: path, body: string(body)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// apiError keeps the status so the health check can say something a person can
// act on. "Unauthorized" and "the hub is unreachable" call for different fixes,
// and a data source that reports both as "error" wastes the operator's time.
type apiError struct {
	status int
	path   string
	body   string
}

func (e *apiError) Error() string {
	switch e.status {
	case http.StatusUnauthorized:
		return "the API token is missing, expired or revoked"
	case http.StatusForbidden:
		return "the token's owner has no access to that project"
	case http.StatusNotFound:
		return fmt.Sprintf("%s is not served by this hub — the module behind it may be disabled", e.path)
	}
	body := strings.TrimSpace(e.body)
	if len(body) > 160 {
		body = body[:160] + "…"
	}
	if body == "" {
		return fmt.Sprintf("hub returned HTTP %d for %s", e.status, e.path)
	}
	return fmt.Sprintf("hub returned HTTP %d for %s: %s", e.status, e.path, body)
}
