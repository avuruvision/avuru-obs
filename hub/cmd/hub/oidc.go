package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
)

// oidcReloadInterval is how often the mounted OIDC config file is re-stat'd for
// changes — the same cadence as the groups/alerting/green loaders.
const oidcReloadInterval = 15 * time.Second

// oidcCallbackPath is the hub route the IdP redirects back to; the external
// redirect URL is AVURUOPS_PUBLIC_URL joined to it.
const oidcCallbackPath = "/api/v1/auth/oidc/callback"

// oidcState bundles the discovered provider with its parsed config so both swap
// atomically behind one pointer — a reader never sees a new provider paired
// with the old forceSSO/mapping, or vice versa.
type oidcState struct {
	provider *auth.OIDCProvider
	settings *auth.OIDCConfig
}

// loadOIDCConfig loads the OIDC config from AVURUOPS_AUTH_OIDC_CONFIG (a file
// path) paired with the AVURUOPS_AUTH_OIDC_CLIENT_SECRET env, discovers the
// provider, and returns accessors for both (nil,nil when OIDC is off). An unset
// path — or auth being disabled — yields nil accessors (OIDC off). A
// present-but-invalid file (parse or discovery failure) fails loud at startup,
// mirroring the groups/alerting loaders; a later bad edit is logged and ignored
// (last good provider stays live). SSO group→grant mapping is installed on
// authSvc here and re-installed on every good reload.
func loadOIDCConfig(ctx context.Context, authSvc *auth.Service) (func() *auth.OIDCProvider, func() *auth.OIDCConfig, error) {
	path := os.Getenv("AVURUOPS_AUTH_OIDC_CONFIG")
	if path == "" {
		return nil, nil, nil
	}
	if authSvc == nil {
		// A config was mounted but auth is disabled: SSO has no session store to
		// mint into. Log the mismatch rather than silently ignoring it.
		slog.Warn("AVURUOPS_AUTH_OIDC_CONFIG is set but authentication is disabled — OIDC ignored")
		return nil, nil, nil
	}

	secret := os.Getenv("AVURUOPS_AUTH_OIDC_CLIENT_SECRET")
	redirectURL := oidcRedirectURL()

	p, cfg, modTime, err := readOIDCConfig(ctx, path, secret, redirectURL)
	if err != nil {
		return nil, nil, fmt.Errorf("AVURUOPS_AUTH_OIDC_CONFIG: %w", err)
	}
	authSvc.SetGroupMapper(cfg.MapGroups)
	slog.Info("oidc config loaded", "path", path, "issuer", cfg.Issuer, "forceSSO", cfg.ForceSSO, "mappings", len(cfg.Mapping))

	var current atomic.Pointer[oidcState]
	current.Store(&oidcState{provider: p, settings: cfg})
	go watchOIDCConfig(ctx, path, secret, redirectURL, modTime, &current, authSvc)

	return func() *auth.OIDCProvider { return current.Load().provider },
		func() *auth.OIDCConfig { return current.Load().settings },
		nil
}

// readOIDCConfig reads+parses the config file, pairs it with the secret, and
// performs OIDC discovery, returning the provider, config, and the file's mod
// time for change detection.
func readOIDCConfig(ctx context.Context, path, secret, redirectURL string) (*auth.OIDCProvider, *auth.OIDCConfig, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	cfg, err := auth.ParseOIDCConfig(data, secret)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	p, err := auth.NewOIDCProvider(ctx, cfg, redirectURL)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	return p, cfg, info.ModTime(), nil
}

// watchOIDCConfig polls the config file's mod time and, on change, re-parses +
// re-discovers, swapping in the new provider and re-installing the group mapper.
// Failures after startup (bad edit, transient IdP unreachable) are logged and
// skipped — the last good provider stays live.
func watchOIDCConfig(ctx context.Context, path, secret, redirectURL string, last time.Time, current *atomic.Pointer[oidcState], authSvc *auth.Service) {
	ticker := time.NewTicker(oidcReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				slog.Warn("oidc config stat failed, keeping current", "path", path, "error", err)
				continue
			}
			if !info.ModTime().After(last) {
				continue
			}
			p, cfg, modTime, err := readOIDCConfig(ctx, path, secret, redirectURL)
			if err != nil {
				slog.Warn("oidc config reload rejected, keeping current", "path", path, "error", err)
				last = info.ModTime() // don't retry the same bad content every tick
				continue
			}
			current.Store(&oidcState{provider: p, settings: cfg})
			authSvc.SetGroupMapper(cfg.MapGroups)
			last = modTime
			slog.Info("oidc config reloaded", "path", path, "issuer", cfg.Issuer, "forceSSO", cfg.ForceSSO, "mappings", len(cfg.Mapping))
		}
	}
}

// oidcRedirectURL builds the IdP redirect-back URL from AVURUOPS_PUBLIC_URL (the
// hub's external base, e.g. https://obs.example.com) joined to the callback
// path. When PUBLIC_URL is unset the URL degrades to the bare callback path and
// a warning is logged: most IdPs require an absolute redirect_uri, so the
// operator must set it for a real deployment.
func oidcRedirectURL() string {
	base := strings.TrimRight(os.Getenv("AVURUOPS_PUBLIC_URL"), "/")
	if base == "" {
		slog.Warn("AVURUOPS_PUBLIC_URL is unset — OIDC redirect URL is relative and likely rejected by the IdP; set it to the hub's external base URL (e.g. https://obs.example.com)",
			"redirectPath", oidcCallbackPath)
		return oidcCallbackPath
	}
	return base + oidcCallbackPath
}
