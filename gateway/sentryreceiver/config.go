package sentryreceiver

import "go.opentelemetry.io/collector/config/confighttp"

// Config is the sentryreceiver configuration. The HTTP server settings come
// from confighttp (endpoint, TLS, CORS, compression) — CORS matters because
// browser SDKs POST cross-origin, and confighttp auto-decompresses gzip'd
// envelopes.
type Config struct {
	confighttp.ServerConfig `mapstructure:",squash"`

	// Projects maps a Sentry project id (the trailing path segment of a DSN,
	// e.g. "1" in .../api/1/envelope/) to its settings. A request for an
	// unknown project is accepted and its service name defaults to
	// "sentry-project-<id>" — parity with the topology trust the OTLP receiver
	// already grants (per-project DSN keys arrive with v0.2 auth).
	Projects map[string]ProjectConfig `mapstructure:"projects"`
}

// ProjectConfig configures one Sentry project.
type ProjectConfig struct {
	// ServiceName stamped on this project's events (resource service.name). If
	// empty, falls back to the event's server_name, then sentry-project-<id>.
	ServiceName string `mapstructure:"service_name"`
	// Keys optionally restricts which DSN public keys are accepted. Empty =
	// accept any key (topology trust).
	Keys []string `mapstructure:"keys"`
}

func (c *Config) Validate() error { return nil }
