// Package green computes per-service energy (Wh) and carbon (gCO2e) from the
// Kepler metrics the sensor ships into the existing otel_metrics_* tables. It
// is pure logic like health/alerting: all SQL lives in the storage backend,
// all HTTP in the api package. See design/2026-07-22-green-carbon.md.
package green

// Config is the green-module configuration — carbon factors, metric-name
// overrides, and budgets — loaded from a mounted ConfigMap or Default() when
// absent. Only the wiring seam exists so far; the fields land with the
// read-time energy/carbon queries.
type Config struct{}

// Default is the zero-config configuration, mirroring an absent ConfigMap.
func Default() Config {
	return Config{}
}
