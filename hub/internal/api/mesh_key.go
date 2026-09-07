package api

import (
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// nsWorkloadKey is the join key between the two halves of the mesh view.
// Telemetry names a service the way the sensor derived it; configuration and
// the proxies name a workload by (namespace, workload). Every join in this
// package goes through this key so two screens cannot disagree about one
// workload (design/2026-09-08-mesh-declared-vs-observed.md, "The join").
type nsWorkloadKey struct{ ns, workload string }

// workloadKey resolves a sensor-derived service name to the workload name the
// mesh uses for it. The sensor suffixes a name with ".namespace" when it has
// to disambiguate (global-waypoint.istio-waypoint), and the proxies never do —
// so the suffix is stripped when it is exactly this namespace. Any other dot
// is part of the name and stays: a workload called "api.v2" in namespace
// "shop" is "api.v2", and guessing otherwise would join it to nothing.
//
// The namespace is returned alongside so a caller building a key does it in
// one expression, and never pairs a stripped name with a different namespace.
func workloadKey(serviceName, namespace string) (ns, workload string) {
	if namespace == "" {
		return "", serviceName
	}
	suffix := "." + namespace
	if strings.HasSuffix(serviceName, suffix) && len(serviceName) > len(suffix) {
		return namespace, strings.TrimSuffix(serviceName, suffix)
	}
	return namespace, serviceName
}

// servicesByWorkload indexes the traced services by their workload key, so a
// security row keyed the mesh's way can name the service the UI links to.
//
// Services with no known namespace are left out rather than keyed under "":
// a proxy always names the namespace it reported for, so a nameless service
// could only ever join by accident.
func servicesByWorkload(services []storage.ServiceStats, namespaces map[string]string) map[nsWorkloadKey]string {
	out := make(map[nsWorkloadKey]string, len(services))
	for _, s := range services {
		ns := namespaces[s.Name]
		if ns == "" {
			continue
		}
		ns, wl := workloadKey(s.Name, ns)
		out[nsWorkloadKey{ns, wl}] = s.Name
	}
	return out
}
