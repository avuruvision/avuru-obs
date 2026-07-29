// Package avuruingestauth is a custom OpenTelemetry Collector server-auth
// extension (extensionauth.Server) that authenticates OTLP ingest with
// per-project Avuru ingest keys. It reads the key off each request, validates
// it against the hub's internal endpoint (with a short positive/negative cache
// and a stale-grace window that survives hub blips), and — per its mode —
// ignores, logs, or rejects. In enforce mode a valid key's project is attached
// as client auth data so the tenantfromauth processor can stamp avuru.tenant
// from it, making the key the authoritative tenant. See
// design/2026-07-21-auth-oidc-rbac.md (auth Plan C). The hub is never in the
// telemetry byte-path — only this control-plane validation call touches it.
package avuruingestauth

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

// typeStr is the component type as used in the collector config.
var typeStr = component.MustNewType("avuruingestauth")

// NewFactory builds the avuruingestauth factory (referenced by the OCB manifest).
func NewFactory() extension.Factory {
	return extension.NewFactory(
		typeStr,
		createDefaultConfig,
		createExtension,
		component.StabilityLevelAlpha,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Mode:       ModeLog,
		CacheTTL:   30 * time.Second,
		StaleGrace: 5 * time.Minute,
		Timeout:    5 * time.Second,
	}
}

func createExtension(_ context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	return newExtension(cfg.(*Config), set)
}
