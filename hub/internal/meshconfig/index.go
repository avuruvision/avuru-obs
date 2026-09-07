package meshconfig

import (
	"slices"
	"strconv"
	"strings"
)

// labelGatewayName is what the mesh stamps on the pods it deploys for a
// Gateway API Gateway — waypoints included — and how a Gateway is joined to
// the pods that serve it.
const labelGatewayName = "gateway.networking.k8s.io/gateway-name"

// index is the cross-object lookup every check needs. Built once: the checks
// are O(objects) and a nested scan would be O(objects squared) on a cluster
// where objects is the number the truncation cap exists to bound.
//
// Hosts are keyed by their canonical spelling — name.namespace.svc.cluster.local
// for a Service, the declared host for a ServiceEntry — so the four ways a
// VirtualService may spell one Service land on one key.
type index struct {
	services map[string]bool // "namespace/name"
	// serviceHosts maps every spelling a Service or ServiceEntry answers to
	// onto its canonical host; serviceByHost maps a canonical host back to
	// the Service, "namespace/name", for the workloads behind it.
	serviceHosts  map[string]string
	serviceByHost map[string]string
	gateways      map[string]bool // "namespace/name"
	waypoints     map[string]bool // "namespace/name", waypoint-class gateways only
	// routedGateways are gateways some route names as a parent.
	routedGateways map[string]bool
	namespaces     map[string]Namespace
	workloads      map[string]*Workload   // "namespace/name"
	workloadsByNS  map[string][]*Workload // every workload of a namespace
	servicesByKey  map[string]*Service    // "namespace/name"
	podsByNS       map[string][]Pod
	podsByWorkload map[string][]Pod // by workload "namespace/name"
	// serviceAccounts is "namespace/name" of every service account a Running
	// pod runs as — the set an authorization principal must be in to name
	// anyone.
	serviceAccounts map[string]bool
	// gatewayPods counts the Running pods labelled for each Gateway,
	// "namespace/name".
	gatewayPods map[string]int
	// drSubsetsByHost is every subset any DestinationRule defines for a
	// canonical host; drSubsets the same per rule, "namespace/name", and
	// drTrafficPolicy whether that rule sets a top-level trafficPolicy.
	drSubsetsByHost map[string]map[string]bool
	drSubsets       map[string]map[string]bool
	drTrafficPolicy map[string]bool
	// vsByHost lists the mesh-bound VirtualServices claiming each canonical
	// host, and drByHost the DestinationRules, both as "namespace/name" in
	// object order.
	vsByHost map[string][]string
	drByHost map[string][]string
	// podsUsable says the pod-dependent checks may run: pods were readable
	// and the list was not cut. When false, podsWhy says which, in the words
	// the response carries.
	podsUsable bool
	podsWhy    string
}

func newIndex(snap Snapshot) *index {
	idx := &index{
		services:        map[string]bool{},
		serviceHosts:    map[string]string{},
		serviceByHost:   map[string]string{},
		gateways:        map[string]bool{},
		waypoints:       map[string]bool{},
		routedGateways:  map[string]bool{},
		namespaces:      map[string]Namespace{},
		workloads:       map[string]*Workload{},
		workloadsByNS:   map[string][]*Workload{},
		servicesByKey:   map[string]*Service{},
		podsByNS:        map[string][]Pod{},
		podsByWorkload:  map[string][]Pod{},
		serviceAccounts: map[string]bool{},
		gatewayPods:     map[string]int{},
		drSubsetsByHost: map[string]map[string]bool{},
		drSubsets:       map[string]map[string]bool{},
		drTrafficPolicy: map[string]bool{},
		vsByHost:        map[string][]string{},
		drByHost:        map[string][]string{},
	}
	idx.podsUsable, idx.podsWhy = podsUsable(snap)
	for _, o := range snap.Objects {
		switch o.Kind {
		case KindService:
			idx.services[key(o.Namespace, o.Name)] = true
			// A Service answers to several spellings; all of them are valid in
			// a host field, so all of them must resolve — to one key.
			fqdn := o.Name + "." + o.Namespace + ".svc.cluster.local"
			// The bare name is not registered: it means a different Service
			// in every namespace, so hostKey qualifies it with the asker's.
			for _, spelling := range []string{
				o.Namespace + "/" + o.Name, o.Name + "." + o.Namespace, o.Name + "." + o.Namespace + ".svc", fqdn,
			} {
				idx.serviceHosts[spelling] = fqdn
			}
			idx.serviceByHost[fqdn] = key(o.Namespace, o.Name)
		case KindServiceEntry:
			for _, h := range stringSlice(o.Spec["hosts"]) {
				idx.serviceHosts[h] = h
			}
		case KindGateway:
			idx.gateways[key(o.Namespace, o.Name)] = true
			if IsWaypoint(o) {
				idx.waypoints[key(o.Namespace, o.Name)] = true
			}
		}
	}
	for _, ns := range snap.Namespaces {
		idx.namespaces[ns.Name] = ns
	}
	for i := range snap.Workloads {
		w := &snap.Workloads[i]
		idx.workloads[key(w.Namespace, w.Name)] = w
		idx.workloadsByNS[w.Namespace] = append(idx.workloadsByNS[w.Namespace], w)
	}
	for i := range snap.Services {
		s := &snap.Services[i]
		idx.servicesByKey[key(s.Namespace, s.Name)] = s
	}
	idx.indexPods(snap)
	// A second pass over objects, for what needs hosts resolved: which
	// gateways a route claims, and which rules claim each host.
	for _, o := range snap.Objects {
		switch o.Kind {
		case KindHTTPRoute, KindGRPCRoute:
			for _, p := range parentRefs(o) {
				idx.routedGateways[p] = true
			}
		case KindVirtualService:
			if !meshBound(o) {
				continue
			}
			for _, h := range stringSlice(o.Spec["hosts"]) {
				if k := idx.hostKey(h, o.Namespace); k != "" {
					idx.vsByHost[k] = append(idx.vsByHost[k], key(o.Namespace, o.Name))
				}
			}
		case KindDestinationRule:
			idx.indexDestinationRule(o)
		}
	}
	return idx
}

// indexPods joins pods to what the checks ask of them: their namespace, their
// workload, the service accounts in use and the Gateways they serve.
func (idx *index) indexPods(snap Snapshot) {
	deployments := deploymentSet(snap.Objects)
	for _, p := range snap.Pods {
		idx.podsByNS[p.Namespace] = append(idx.podsByNS[p.Namespace], p)
		wk := workloadKeyFor(p, deployments).id()
		idx.podsByWorkload[wk] = append(idx.podsByWorkload[wk], p)
		if p.Phase != phaseRunning {
			continue
		}
		sa := p.ServiceAccount
		if sa == "" {
			sa = "default"
		}
		idx.serviceAccounts[key(p.Namespace, sa)] = true
		if gw := p.Labels[labelGatewayName]; gw != "" {
			idx.gatewayPods[key(p.Namespace, gw)]++
		}
	}
}

func (idx *index) indexDestinationRule(o Object) {
	id := key(o.Namespace, o.Name)
	host, _ := o.Spec["host"].(string)
	k := idx.hostKey(host, o.Namespace)
	if k != "" {
		idx.drByHost[k] = append(idx.drByHost[k], id)
		if idx.drSubsetsByHost[k] == nil {
			idx.drSubsetsByHost[k] = map[string]bool{}
		}
	}
	idx.drSubsets[id] = map[string]bool{}
	for _, item := range slice(o.Spec["subsets"]) {
		s, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := s["name"].(string); name != "" {
			idx.drSubsets[id][name] = true
			if k != "" {
				idx.drSubsetsByHost[k][name] = true
			}
		}
	}
	_, idx.drTrafficPolicy[id] = o.Spec["trafficPolicy"].(map[string]any)
}

// hostKey resolves a host, as written in some object of namespace, to its
// canonical key — or "" when it names nothing this snapshot knows. A bare
// name is qualified with the object's own namespace, as the mesh does.
func (idx *index) hostKey(host, namespace string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.Contains(host, "*") {
		return ""
	}
	if !strings.Contains(host, ".") {
		if k := idx.serviceHosts[host+"."+namespace]; k != "" {
			return k
		}
	}
	return idx.serviceHosts[host]
}

// meshBound reports whether a VirtualService applies inside the mesh rather
// than on a gateway: no gateways named, or only the reserved "mesh".
func meshBound(o Object) bool {
	for _, g := range stringSlice(o.Spec["gateways"]) {
		if g != "mesh" {
			return false
		}
	}
	return true
}

// podGatedChecks are the checks that read pods and cannot run without all of
// them. Listed so the response can name what went silent.
var podGatedChecks = []Code{
	CodePolicyNoMatch, CodeAmbientNotEnrolled, CodeDataplaneConflict, CodeGatewayNoWorkload, CodePrincipalUnknown,
}

// podsUsable says whether the pod-dependent checks may run, and when not,
// why — in the sentence the response carries. Two causes, two fixes: pods
// the ClusterRole does not grant, and a pod list the cap cut. A check that
// cannot see every pod would call a policy unmatched when its pods are simply
// past the cap, so it must not run at all.
func podsUsable(snap Snapshot) (bool, string) {
	switch {
	case slices.Contains(snap.MissingKinds, KindPod):
		why := "pods were not readable"
		if r := snap.MissingReasons[KindPod]; r != "" {
			why += " (" + r + ")"
		}
		return false, why + " — grant pods get, list and watch in the mesh-config ClusterRole"
	case snap.PodsTruncated:
		return false, "the pod list was cut at " + strconv.Itoa(len(snap.Pods)) +
			" — a check that cannot see every pod would report as absent what is only past the cap"
	}
	return true, ""
}

func key(namespace, name string) string { return namespace + "/" + name }

// splitHost pulls name and namespace out of a cluster-shaped host.
func splitHost(host string) (name, namespace string, ok bool) {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func slice(v any) []any {
	s, _ := v.([]any)
	return s
}

func stringSlice(v any) []string {
	var out []string
	for _, item := range slice(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mapSlice(v any) []map[string]any {
	var out []map[string]any
	for _, item := range slice(v) {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func nestedMap(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		next, ok := m[p].(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}
