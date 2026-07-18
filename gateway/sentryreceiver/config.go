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
//
// There is no per-project key restriction yet: any DSN key is accepted, the
// same topology trust the OTLP receiver grants (see Projects above). Per-project
// DSN keys arrive with v0.2 auth — a `keys` field will be added when it is
// actually enforced, rather than shipped inert as if it were access control.
type ProjectConfig struct {
	// ServiceName stamped on this project's events (resource service.name). If
	// empty, falls back to the event's server_name, then sentry-project-<id>.
	ServiceName string `mapstructure:"service_name"`
}

func (c *Config) Validate() error { return nil }
