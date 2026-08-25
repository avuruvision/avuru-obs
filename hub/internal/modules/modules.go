// Package modules defines the install-level module registry: which signal
// families a deployment runs. A module gates schema migrations, Hub API
// routes, and (via deploy config) ingestion pipelines and UI surface. See
// design/2026-07-15-module-framework.md.
package modules

import (
	"fmt"
	"strings"
)

// Name identifies a module.
type Name string

// The module registry. Core is the wedge (service map + traces + RED) and is
// never disableable.
const (
	Core          Name = "core"
	Logs          Name = "logs"
	InfraMetrics  Name = "infra-metrics"
	Profiling     Name = "profiling"
	ErrorTracking Name = "error-tracking"
	// ServiceHealth rolls per-service RED health (core trace data) up into group
	// health. It derives everything it displays, so gating it off removes the
	// API routes + UI surface and never any ingestion weight. It owns exactly
	// one table — service_group (0016), the UI-authored groups; chart-declared
	// groups stay in the ConfigMap. Nothing else reads that table, so an install
	// with the module off simply never creates it.
	ServiceHealth Name = "service-health"
	// Alerting watches service-health status and fires webhooks on transitions.
	// It owns the alert_state/alert_history tables (0008) and is the hub's only
	// module that makes an outbound call. Inert until rules are configured.
	Alerting Name = "alerting"
	// Mesh surfaces the service mesh itself: proxy RED (derived from the same
	// spans the map already stores, filtered by the topology classifier rather
	// than by a query) and control-plane health from an istiod scrape. It owns
	// no schema. Born OFF — most installs run no mesh, and a module that lights
	// up a nav entry for infrastructure you do not have is what the v0.8
	// navigation work set out to stop. The control-plane half additionally
	// requires infra-metrics, since the scrape lands in otel_metrics_*.
	// See design/2026-08-25-mesh-surfaces.md.
	Mesh Name = "mesh"
	// Green derives per-service energy (Wh) and carbon (gCO2e) from Kepler
	// metrics at query time. It owns no schema/migration (it reads the existing
	// otel_metrics_* tables) but requires infra-metrics: the pod→workload join
	// reads the kubeletstats attributes. See design/2026-07-22-green-carbon.md.
	Green Name = "green"
)

// All lists every known module in registry (display) order.
var All = []Name{Core, Logs, InfraMetrics, Profiling, ErrorTracking, ServiceHealth, Alerting, Mesh, Green}

// Set is a resolved active-module set; Core is always present.
type Set map[Name]bool

// Enabled reports whether m is active. A nil Set means "not resolved yet" and
// callers should default to AllSet before querying.
func (s Set) Enabled(m Name) bool { return s[m] }

// Names returns the active modules in registry order.
func (s Set) Names() []string {
	out := make([]string, 0, len(s))
	for _, m := range All {
		if s[m] {
			out = append(out, string(m))
		}
	}
	return out
}

// AllSet returns a Set with every known module enabled (the default).
func AllSet() Set {
	s := make(Set, len(All))
	for _, m := range All {
		s[m] = true
	}
	return s
}

// Parse resolves a comma-separated module list (the AVURUOBS_MODULES env
// value). Empty means "all modules" — the backward-compatible default. Core
// is always forced on. Unknown names are an error: a typo must fail the
// deploy loudly, not silently disable a module's schema.
func Parse(v string) (Set, error) {
	if strings.TrimSpace(v) == "" {
		return AllSet(), nil
	}
	known := make(map[Name]bool, len(All))
	for _, m := range All {
		known[m] = true
	}
	s := Set{Core: true}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m := Name(part)
		if !known[m] {
			return nil, fmt.Errorf("unknown module %q (known: %s)", part, strings.Join(AllSet().Names(), ", "))
		}
		s[m] = true
	}
	// Green's hard dependency (see the Green const doc) must fail the deploy loudly.
	if s[Green] && !s[InfraMetrics] {
		return nil, fmt.Errorf("module %q requires %q — add it to AVURUOBS_MODULES", Green, InfraMetrics)
	}
	return s, nil
}
