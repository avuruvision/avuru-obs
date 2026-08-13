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
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// oidcReloadInterval is how often the mounted OIDC config file is re-stat'd
// for changes — the same cadence as the groups/alerting/green loaders. It is
// also the cross-replica staleness bound for the mapping cache below: the
// same ticker unconditionally refreshes the cache on every tick, whether or
// not the file itself changed, since a write from another hub replica lands
// in ClickHouse without ever touching this file.
const oidcReloadInterval = 15 * time.Second

// oidcCallbackPath is the hub route the IdP redirects back to; the external
// redirect URL is AVURUOBS_PUBLIC_URL joined to it.
const oidcCallbackPath = "/api/v1/auth/oidc/callback"

// oidcState bundles the discovered provider with its parsed config so both swap
// atomically behind one pointer — a reader never sees a new provider paired
// with the old forceSSO/mapping, or vice versa.
type oidcState struct {
	provider *auth.OIDCProvider
	settings *auth.OIDCConfig
}

// loadOIDCConfig loads the OIDC config from AVURUOBS_AUTH_OIDC_CONFIG (a file
// path) paired with the AVURUOBS_AUTH_OIDC_CLIENT_SECRET env, discovers the
// provider, and returns accessors for both (nil,nil when OIDC is off). An unset
// path — or auth being disabled — yields nil accessors (OIDC off). A
// present-but-invalid file (parse or discovery failure) fails loud at startup,
// mirroring the groups/alerting loaders; a later bad edit is logged and ignored
// (last good provider stays live).
//
// SSO group→grant mapping is installed on authSvc here as an
// auth.MappingCache.Mapper() — not cfg.MapGroups directly — because the
// mapping an admin authors in the UI (the DB overlay) must be re-derivable on
// every authenticated request without a per-request ClickHouse read. See
// mapping.go / mappingcache.go for why that read has to be cached rather than
// done inline. store is the hub's ClickHouse provider; it is not necessarily
// connected yet when this runs at startup — MappingCache.Refresh degrades to
// the config-only mapping in that case rather than failing, and the periodic
// poll started below picks up the overlay once the store comes up.
//
// The cache itself is also returned so main.go can hand it to api.Config
// (OIDCMapping) — it is what the admin CRUD in internal/api/oidc_mapping.go
// lists from and refreshes after a write. nil exactly when OIDC is off,
// which is also what gates those routes' registration (router.go).
func loadOIDCConfig(ctx context.Context, authSvc *auth.Service, store func() storage.Store) (func() *auth.OIDCProvider, func() *auth.OIDCConfig, *auth.MappingCache, error) {
	path := os.Getenv("AVURUOBS_AUTH_OIDC_CONFIG")
	if path == "" {
		return nil, nil, nil, nil
	}
	if authSvc == nil {
		// A config was mounted but auth is disabled: SSO has no session store to
		// mint into. Log the mismatch rather than silently ignoring it.
		slog.Warn("AVURUOBS_AUTH_OIDC_CONFIG is set but authentication is disabled — OIDC ignored")
		return nil, nil, nil, nil
	}

	secret := os.Getenv("AVURUOBS_AUTH_OIDC_CLIENT_SECRET")
	redirectURL := oidcRedirectURL()

	p, cfg, modTime, err := readOIDCConfig(ctx, path, secret, redirectURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("AVURUOBS_AUTH_OIDC_CONFIG: %w", err)
	}

	mapping := auth.NewMappingCache(store)
	mapping.SetConfig(mappingConfigOf(cfg))
	// A failed Refresh here is not a startup failure — see the func comment.
	// Refresh already logs its own warning; the poll below will retry.
	_ = mapping.Refresh(ctx)
	// Installed ONCE: Mapper()'s closure reads the cache's live snapshot on
	// every call, so a later Refresh (hot-reload or poll) takes effect
	// without ever calling SetGroupMapper again.
	authSvc.SetGroupMapper(mapping.Mapper())

	slog.Info("oidc config loaded", "path", path, "issuer", cfg.Issuer, "forceSSO", cfg.ForceSSO, "mappings", len(cfg.Mapping))

	var current atomic.Pointer[oidcState]
	current.Store(&oidcState{provider: p, settings: cfg})
	go watchOIDCConfig(ctx, path, secret, redirectURL, modTime, &current, mapping)

	return func() *auth.OIDCProvider { return current.Load().provider },
		func() *auth.OIDCConfig { return current.Load().settings },
		mapping,
		nil
}

// mappingConfigOf lifts the config-declared half of the mapping out of the
// full auth.OIDCConfig (issuer, discovery, forceSSO, ...) into the narrower
// shape auth.MappingCache holds, so the cache stays ignorant of everything
// about OIDC config that isn't part of the group→role mapping.
func mappingConfigOf(cfg *auth.OIDCConfig) auth.MappingConfig {
	return auth.MappingConfig{
		Mapping:         cfg.Mapping,
		DefaultRole:     cfg.DefaultRole,
		DefaultProjects: cfg.DefaultProjects,
	}
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

// watchOIDCConfig ticks every oidcReloadInterval. Each tick does two jobs on
// the one ticker, since they share the cadence and the hub's other hot-reload
// watchers (groups, alerting, green) already establish this ctx.Done()-based
// lifecycle rather than a bare background goroutine:
//
//  1. Re-stat the config file; on a change, re-parse + re-discover, swap in
//     the new provider/settings, and replace the mapping cache's config half
//     (mapping.SetConfig). Failures here are logged and skipped — the last
//     good provider/settings stay live.
//  2. Unconditionally call mapping.Refresh — regardless of whether the file
//     changed this tick. This is the cross-replica poll: an admin's write on
//     ANOTHER hub replica lands in ClickHouse without ever touching this
//     file, so refresh-on-file-change alone would never see it. Refresh
//     itself keeps the previous snapshot and logs a warning on a store
//     error, so there is nothing further to do with its return value here.
func watchOIDCConfig(ctx context.Context, path, secret, redirectURL string, last time.Time, current *atomic.Pointer[oidcState], mapping *auth.MappingCache) {
	ticker := time.NewTicker(oidcReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			switch {
			case err != nil:
				slog.Warn("oidc config stat failed, keeping current", "path", path, "error", err)
			case info.ModTime().After(last):
				p, cfg, modTime, err := readOIDCConfig(ctx, path, secret, redirectURL)
				if err != nil {
					slog.Warn("oidc config reload rejected, keeping current", "path", path, "error", err)
					last = info.ModTime() // don't retry the same bad content every tick
				} else {
					current.Store(&oidcState{provider: p, settings: cfg})
					mapping.SetConfig(mappingConfigOf(cfg))
					last = modTime
					slog.Info("oidc config reloaded", "path", path, "issuer", cfg.Issuer, "forceSSO", cfg.ForceSSO, "mappings", len(cfg.Mapping))
				}
			}
			_ = mapping.Refresh(ctx)
		}
	}
}

// oidcRedirectURL builds the IdP redirect-back URL from AVURUOBS_PUBLIC_URL (the
// hub's external base, e.g. https://obs.example.com) joined to the callback
// path. When PUBLIC_URL is unset the URL degrades to the bare callback path and
// a warning is logged: most IdPs require an absolute redirect_uri, so the
// operator must set it for a real deployment.
func oidcRedirectURL() string {
	base := strings.TrimRight(os.Getenv("AVURUOBS_PUBLIC_URL"), "/")
	if base == "" {
		slog.Warn("AVURUOBS_PUBLIC_URL is unset — OIDC redirect URL is relative and likely rejected by the IdP; set it to the hub's external base URL (e.g. https://obs.example.com)",
			"redirectPath", oidcCallbackPath)
		return oidcCallbackPath
	}
	return base + oidcCallbackPath
}
