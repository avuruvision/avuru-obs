package avuruingestauth

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/config/configopaque"
)

// Mode controls how the extension treats ingest requests.
const (
	// ModeOff disables all key checking — the extension is a pass-through.
	ModeOff = "off"
	// ModeLog validates keys and counts would-be denials, but never rejects a
	// request and never stamps the tenant. This is the drop-in default: an
	// existing unkeyed OTLP sender keeps landing exactly as before.
	ModeLog = "log"
	// ModeEnforce rejects requests without a valid key (HTTP 401 via confighttp)
	// and, on success, attaches the key's project as auth data so the
	// tenantfromauth processor can stamp avuru.tenant from it.
	ModeEnforce = "enforce"
)

// Config is the avuruingestauth server-auth extension configuration.
type Config struct {
	// HubValidateURL is the hub's internal validate endpoint
	// (e.g. http://hub:8080/internal/v1/ingest-keys/validate).
	HubValidateURL string `mapstructure:"hub_validate_url"`
	// InternalToken authenticates the gateway→hub call (chart-generated, shared
	// with the hub's AVURUOBS_INGEST_INTERNAL_TOKEN).
	InternalToken configopaque.String `mapstructure:"internal_token"`
	// Mode is off | log | enforce (default log).
	Mode string `mapstructure:"mode"`
	// CacheTTL bounds how long a positive OR negative verdict is trusted before
	// re-validating against the hub (default 30s).
	CacheTTL time.Duration `mapstructure:"cache_ttl"`
	// StaleGrace lets a cached verdict keep serving through a hub outage past
	// CacheTTL, up to this age (default 5m) — so a hub blip never drops traffic.
	StaleGrace time.Duration `mapstructure:"stale_grace"`
	// Timeout bounds each hub validation call (default 5s).
	Timeout time.Duration `mapstructure:"timeout"`
}

// Validate rejects an unknown mode and an enforce/log config with no hub URL —
// off needs nothing, but validating keys requires somewhere to validate them.
func (c *Config) Validate() error {
	switch c.Mode {
	case ModeOff, ModeLog, ModeEnforce:
	default:
		return fmt.Errorf("invalid mode %q (want off|log|enforce)", c.Mode)
	}
	if c.Mode != ModeOff && c.HubValidateURL == "" {
		return fmt.Errorf("hub_validate_url is required in %q mode", c.Mode)
	}
	return nil
}
