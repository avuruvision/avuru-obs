package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"

	"github.com/oauth2-proxy/mockoidc"
)

func testAuthService() *auth.Service {
	return auth.NewService(func() storage.Store { return nil }, time.Hour)
}

// testNoStore stands in for a ClickHouse provider that never connects: these
// tests exercise config loading/reload, not the DB overlay (that is a later
// task's concern), and MappingCache.Refresh degrades to the config-only
// mapping when the store is nil.
func testNoStore() storage.Store { return nil }

// TestLoadOIDCConfigUnsetIsOff: no AVURUOBS_AUTH_OIDC_CONFIG → nil accessors
// (OIDC off), no polling, no error.
func TestLoadOIDCConfigUnsetIsOff(t *testing.T) {
	t.Setenv("AVURUOBS_AUTH_OIDC_CONFIG", "")
	p, s, err := loadOIDCConfig(context.Background(), testAuthService(), testNoStore)
	if err != nil {
		t.Fatalf("loadOIDCConfig: %v", err)
	}
	if p != nil || s != nil {
		t.Fatalf("expected nil accessors when unset, got p=%v s=%v", p != nil, s != nil)
	}
}

// TestLoadOIDCConfigAuthDisabledIsOff: a config is mounted but auth is disabled
// (authSvc nil) → OIDC is ignored (nil accessors), never a panic on the nil
// service. The config is never even read, so an unreachable issuer is fine.
func TestLoadOIDCConfigAuthDisabledIsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oidc.yaml")
	if err := os.WriteFile(path, []byte("issuer: https://unreachable.invalid\nclientId: y\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("AVURUOBS_AUTH_OIDC_CONFIG", path)
	p, s, err := loadOIDCConfig(context.Background(), nil, testNoStore)
	if err != nil {
		t.Fatalf("loadOIDCConfig: %v", err)
	}
	if p != nil || s != nil {
		t.Fatalf("expected nil accessors when auth disabled, got p=%v s=%v", p != nil, s != nil)
	}
}

// TestLoadOIDCConfigFromFile: a valid mounted file parses, discovers against
// the mock IdP, and the accessors serve the provider and its settings.
func TestLoadOIDCConfigFromFile(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc.Run: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	path := filepath.Join(t.TempDir(), "oidc.yaml")
	content := "issuer: " + m.Issuer() + "\n" +
		"clientId: " + m.ClientID + "\n" +
		"groupsClaim: groups\n" +
		"forceSSO: true\n" +
		"mapping:\n" +
		"  - {group: obs-admins, role: admin, projects: [\"*\"]}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("AVURUOBS_AUTH_OIDC_CONFIG", path)
	t.Setenv("AVURUOBS_AUTH_OIDC_CLIENT_SECRET", m.ClientSecret)
	t.Setenv("AVURUOBS_PUBLIC_URL", "https://obs.example.com")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // stops the watcher goroutine

	providerFn, settingsFn, err := loadOIDCConfig(ctx, testAuthService(), testNoStore)
	if err != nil {
		t.Fatalf("loadOIDCConfig: %v", err)
	}
	if providerFn == nil || settingsFn == nil {
		t.Fatal("expected non-nil accessors for a valid config")
	}
	if providerFn() == nil {
		t.Fatal("provider accessor returned nil after successful discovery")
	}
	s := settingsFn()
	if s == nil {
		t.Fatal("settings accessor returned nil")
	}
	if s.Issuer != m.Issuer() || !s.ForceSSO {
		t.Fatalf("settings: got issuer=%q forceSSO=%v", s.Issuer, s.ForceSSO)
	}
	if len(s.Mapping) != 1 || s.Mapping[0].Role != auth.RoleAdmin {
		t.Fatalf("mapping not parsed: %+v", s.Mapping)
	}
}

// TestLoadOIDCConfigInvalidFailsLoud: a present-but-invalid file (missing
// issuer) is a startup error (like the groups/green loaders), never a silent
// off.
func TestLoadOIDCConfigInvalidFailsLoud(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oidc.yaml")
	if err := os.WriteFile(path, []byte("clientId: only-client\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("AVURUOBS_AUTH_OIDC_CONFIG", path)
	if _, _, err := loadOIDCConfig(context.Background(), testAuthService(), testNoStore); err == nil {
		t.Fatal("expected error for missing issuer, got nil")
	}
}

// TestOIDCRedirectURL covers the three shapes: base with trailing slash, base
// without, and unset (bare relative callback path).
func TestOIDCRedirectURL(t *testing.T) {
	const want = "https://obs.example.com" + oidcCallbackPath

	t.Setenv("AVURUOBS_PUBLIC_URL", "https://obs.example.com/")
	if got := oidcRedirectURL(); got != want {
		t.Fatalf("trailing slash: got %q want %q", got, want)
	}
	t.Setenv("AVURUOBS_PUBLIC_URL", "https://obs.example.com")
	if got := oidcRedirectURL(); got != want {
		t.Fatalf("no slash: got %q want %q", got, want)
	}
	t.Setenv("AVURUOBS_PUBLIC_URL", "")
	if got := oidcRedirectURL(); got != oidcCallbackPath {
		t.Fatalf("unset: got %q want %q", got, oidcCallbackPath)
	}
}
