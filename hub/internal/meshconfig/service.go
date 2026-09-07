package meshconfig

import (
	"sort"
	"strings"
)

// ServicePort is one port a Service exposes, with the protocol it declares.
type ServicePort struct {
	Name        string
	Port        int64
	Protocol    string
	AppProtocol string
	// DeclaredProtocol is what the mesh will treat this port as: appProtocol
	// when set, else the port-name prefix when it is one the mesh knows
	// (http, grpc, tcp...), else "" for a port that declared nothing.
	DeclaredProtocol string
}

// Service is one Kubernetes Service, joined to the workloads its selector
// picks and the waypoint that fronts it.
type Service struct {
	Namespace, Name string
	Selector        map[string]string
	Ports           []ServicePort
	// Workloads are "namespace/name" of every workload whose pods this
	// selector matches. Empty for a Service with no selector, which is a real
	// answer: an ExternalName or a hand-managed endpoint set.
	Workloads []string
	// Waypoint is the waypoint fronting this service, and WaypointSource
	// whether the binding is the service's own ("service") or its
	// namespace's ("namespace").
	Waypoint          string
	WaypointNamespace string
	WaypointSource    string
	Findings          []Finding
}

// knownPortProtocols are the prefixes the mesh reads a protocol from in a
// port name. grpc-web is the one with a dash of its own and is tried first.
var knownPortProtocols = map[string]bool{
	"http": true, "http2": true, "https": true, "grpc": true, "grpc-web": true,
	"mongo": true, "redis": true, "mysql": true, "tcp": true, "tls": true, "udp": true,
}

// ServicesFrom joins every Service to the workloads it selects.
//
// Pure, and the same selector resolution the workload list uses, so a
// Service's workloads and a workload's services are one fact read from two
// sides rather than two facts that may disagree.
func ServicesFrom(objects []Object, pods []Pod, namespaces []Namespace) []Service {
	nsByName := make(map[string]Namespace, len(namespaces))
	for _, ns := range namespaces {
		nsByName[ns.Name] = ns
	}
	links := resolveServices(objects, pods, deploymentSet(objects))

	var out []Service
	for _, o := range objects {
		if o.Kind != KindService {
			continue
		}
		s := Service{
			Namespace: o.Namespace,
			Name:      o.Name,
			Selector:  nestedStringMap(o.Spec, "selector"),
			Ports:     servicePorts(o),
			Workloads: links.workloadsByService[key(o.Namespace, o.Name)],
		}
		s.Waypoint, s.WaypointNamespace, s.WaypointSource =
			waypointBinding(o.Labels, o.Namespace, nsByName[o.Namespace], SourceService)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// serviceLinks is the selector resolution, read from both sides.
type serviceLinks struct {
	workloadsByService map[string][]string // "ns/service" -> sorted "ns/workload"
	servicesByWorkload map[string][]string // "ns/workload" -> sorted "ns/service"
}

// resolveServices matches every Service selector against the pods of its
// namespace and names the workloads behind the matches. O(pods × services in
// their namespace): a selector never reaches across namespaces, so no pod is
// compared with a Service it could not belong to.
func resolveServices(objects []Object, pods []Pod, deployments map[string]bool) serviceLinks {
	podsByNS := map[string][]Pod{}
	for _, p := range pods {
		podsByNS[p.Namespace] = append(podsByNS[p.Namespace], p)
	}
	byService := map[string]map[string]bool{}
	byWorkload := map[string]map[string]bool{}
	for _, o := range objects {
		if o.Kind != KindService {
			continue
		}
		selector := nestedStringMap(o.Spec, "selector")
		if len(selector) == 0 {
			continue
		}
		svc := key(o.Namespace, o.Name)
		for _, p := range podsByNS[o.Namespace] {
			if !labelsMatch(selector, p.Labels) {
				continue
			}
			wk := workloadKeyFor(p, deployments).id()
			link(byService, svc, wk)
			link(byWorkload, wk, svc)
		}
	}
	return serviceLinks{workloadsByService: sortedSets(byService), servicesByWorkload: sortedSets(byWorkload)}
}

func link(m map[string]map[string]bool, from, to string) {
	if m[from] == nil {
		m[from] = map[string]bool{}
	}
	m[from][to] = true
}

func sortedSets(m map[string]map[string]bool) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, set := range m {
		list := make([]string, 0, len(set))
		for v := range set {
			list = append(list, v)
		}
		sort.Strings(list)
		out[k] = list
	}
	return out
}

// servicePorts reads spec.ports, each with the protocol the mesh will assume.
func servicePorts(o Object) []ServicePort {
	var out []ServicePort
	for _, item := range slice(o.Spec["ports"]) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p := ServicePort{Port: portNumber(m["port"])}
		p.Name, _ = m["name"].(string)
		p.Protocol, _ = m["protocol"].(string)
		p.AppProtocol, _ = m["appProtocol"].(string)
		p.DeclaredProtocol = declaredProtocol(p.Name, p.AppProtocol)
		out = append(out, p)
	}
	return out
}

// declaredProtocol is the protocol a port declares: appProtocol outright, else
// the name's prefix before its first dash when that prefix is a protocol the
// mesh knows. A name like "web" declares nothing, and nothing is what is
// returned — guessing "tcp" here would hide exactly the ports worth flagging.
func declaredProtocol(name, appProtocol string) string {
	if appProtocol != "" {
		return appProtocol
	}
	name = strings.ToLower(name)
	if name == "grpc-web" || strings.HasPrefix(name, "grpc-web-") {
		return "grpc-web"
	}
	prefix, _, _ := strings.Cut(name, "-")
	if knownPortProtocols[prefix] {
		return prefix
	}
	return ""
}

// portNumber reads a port however the decoder spelled it: int64 from the API
// server, float64 from a JSON round trip, int from a hand-built spec.
func portNumber(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
