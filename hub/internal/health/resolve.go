package health

import (
	"fmt"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// unlabeledNamespace is the auto-group bucket for services carrying no
// namespace label at all (some pure-SDK apps). Kept explicit so the UI can
// teach ("assign these to a tier in config").
const unlabeledNamespace = "(unlabeled)"

// assignment is the resolved grouping for one service.
type assignment struct {
	Service     string
	Group       string
	Environment string // declared deployment environment; "" = no dimension
	Tier        Tier
	Source      string // "config" (matched a group selector) or "auto" (by namespace)
	TierSource  string // "override" | "config" | "declared" | "default"
}

// namespaceOf resolves a service's grouping namespace: k8s.namespace.name,
// falling back to service.namespace, then the unlabeled bucket.
func namespaceOf(l storage.ServiceLabel) string {
	switch {
	case l.K8sNamespace != "":
		return l.K8sNamespace
	case l.ServiceNamespace != "":
		return l.ServiceNamespace
	default:
		return unlabeledNamespace
	}
}

// resolve assigns every service to a group. Config selectors win (first
// matching group, in registry order); unmatched services auto-group by their
// namespace — the hybrid auto+config model. Tier resolves independently through
// tierOverrides > config group > declared avuru.tier > defaultTier. The service
// set comes from stats (the RED population); labels supply the declarations.
//
// It returns warnings for declarations it could not honour. Warnings are never
// errors: application telemetry has no operator review gate, so bad input
// degrades one service's tier, never the whole board.
func resolve(cfg Config, stats []storage.ServiceStats, labels []storage.ServiceLabel) (map[string]assignment, []string) {
	labelByService := make(map[string]storage.ServiceLabel, len(labels))
	for _, l := range labels {
		labelByService[l.Service] = l
	}

	out := make(map[string]assignment, len(stats))
	var warnings []string
	for _, s := range stats {
		// A service with no label row yields the zero ServiceLabel, and
		// namespaceOf already maps that to the unlabeled bucket.
		l := labelByService[s.Name]
		ns := namespaceOf(l)

		a := assignment{Service: s.Name, Environment: l.Environment}
		if g, matched := matchGroup(cfg, s.Name, ns); matched {
			a.Group, a.Source = g.Name, "config"
			a.Tier, a.TierSource = g.Tier, "config"
		} else {
			a.Group, a.Source = ns, "auto"
			a.Tier, a.TierSource = cfg.DefaultTier, "default"
			if l.DeclaredTier != "" {
				if t, ok := parseTierSoft(l.DeclaredTier); ok {
					a.Tier, a.TierSource = t, "declared"
				} else {
					warnings = append(warnings, fmt.Sprintf(
						"service %q declared an invalid avuru.tier %q — using %s",
						s.Name, l.DeclaredTier, cfg.DefaultTier))
				}
			}
		}
		// A config group still loses to an explicit per-service override.
		if t, ok := cfg.TierOverrides[s.Name]; ok {
			a.Tier, a.TierSource = t, "override"
		}
		out[s.Name] = a
	}
	sort.Strings(warnings)
	return out, warnings
}

// Assignment is the exported view of one service's resolved grouping, for
// callers outside the rollup (the green module maps services→groups with it).
type Assignment struct {
	Service     string
	Group       string
	Environment string
	Tier        Tier
	Source      string // "config" (matched a group selector) or "auto" (by namespace)
	TierSource  string // "override" | "config" | "declared" | "default"
}

// Assign resolves every service to its group exactly as the service-health
// rollup does — config selectors win, unmatched services auto-group by
// namespace. It is a thin exported wrapper over the internal resolve (zero
// behavior change): the green module uses it so energy/carbon rolls up to the
// same groups the operator already sees in service health.
func Assign(cfg Config, stats []storage.ServiceStats, labels []storage.ServiceLabel) map[string]Assignment {
	in, _ := resolve(cfg, stats, labels)
	out := make(map[string]Assignment, len(in))
	for svc, a := range in {
		out[svc] = Assignment(a)
	}
	return out
}

// matchGroup returns the first config group whose selector matches the service
// by explicit name or by namespace.
func matchGroup(cfg Config, service, namespace string) (Group, bool) {
	for _, g := range cfg.Groups {
		for _, svc := range g.Selector.Services {
			if svc == service {
				return g, true
			}
		}
		for _, ns := range g.Selector.Namespaces {
			if ns == namespace {
				return g, true
			}
		}
	}
	return Group{}, false
}
