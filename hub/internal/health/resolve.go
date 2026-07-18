package health

import "github.com/avuru/avuru-obs/hub/internal/storage"

// unlabeledNamespace is the auto-group bucket for services carrying no
// namespace label at all (some pure-SDK apps). Kept explicit so the UI can
// teach ("assign these to a tier in config").
const unlabeledNamespace = "(unlabeled)"

// assignment is the resolved grouping for one service.
type assignment struct {
	Service string
	Group   string
	Tier    Tier
	Source  string // "config" (matched a group selector) or "auto" (by namespace)
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
// namespace at the default tier — the hybrid auto+config model. The service
// set comes from stats (the RED population); labels supply namespaces.
func resolve(cfg Config, stats []storage.ServiceStats, labels []storage.ServiceLabel) map[string]assignment {
	nsByService := make(map[string]string, len(labels))
	for _, l := range labels {
		nsByService[l.Service] = namespaceOf(l)
	}

	out := make(map[string]assignment, len(stats))
	for _, s := range stats {
		ns := nsByService[s.Name]
		if ns == "" {
			ns = unlabeledNamespace
		}
		if g, ok := matchGroup(cfg, s.Name, ns); ok {
			out[s.Name] = assignment{Service: s.Name, Group: g.Name, Tier: g.Tier, Source: "config"}
			continue
		}
		out[s.Name] = assignment{Service: s.Name, Group: ns, Tier: cfg.DefaultTier, Source: "auto"}
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
