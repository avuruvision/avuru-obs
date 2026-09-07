package meshconfig

import "sort"

// maxWorkloadRoutes bounds the references on one workload. A wildcard host
// resolves to nothing, so a fan-out past this is a generator's doing, and the
// page is unreadable past it anyway.
const maxWorkloadRoutes = 200

// attachRoutes lists, on every workload, the routes and rules that reach it
// through its Services: Gateway API routes by backendRef or by a Service
// parent, VirtualServices by host or destination, DestinationRules by host.
// Hosts resolve through the index the checks use, so a bare name means the
// object's own namespace and a FQDN means exactly that Service.
//
// Deliberately the inverse of Policies: a policy selects a workload by its
// labels, a route reaches it by its Service, and a page that only listed the
// first would call a routed workload unconfigured.
func attachRoutes(snap Snapshot, idx *index) {
	seen := map[string]map[RouteRef]bool{}
	add := func(o Object, host, namespace string) {
		for _, w := range idx.workloadsBehind(host, namespace) {
			svc := idx.serviceByHost[idx.hostKey(host, namespace)]
			ref := RouteRef{Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, Service: svc, Host: host}
			id := key(w.Namespace, w.Name)
			if seen[id] == nil {
				seen[id] = map[RouteRef]bool{}
			}
			if seen[id][ref] || len(w.Routes) >= maxWorkloadRoutes {
				continue
			}
			seen[id][ref] = true
			w.Routes = append(w.Routes, ref)
		}
	}
	for _, o := range snap.Objects {
		switch o.Kind {
		case KindHTTPRoute, KindGRPCRoute:
			for _, p := range mapSlice(o.Spec["parentRefs"]) {
				if kind, _ := p["kind"].(string); kind == KindService {
					add(o, refHost(p, o.Namespace), o.Namespace)
				}
			}
			for _, r := range mapSlice(o.Spec["rules"]) {
				for _, b := range mapSlice(r["backendRefs"]) {
					if kind, _ := b["kind"].(string); kind != "" && kind != KindService {
						continue
					}
					add(o, refHost(b, o.Namespace), o.Namespace)
				}
			}
		case KindVirtualService:
			for _, h := range stringSlice(o.Spec["hosts"]) {
				add(o, h, o.Namespace)
			}
			for _, proto := range []string{"http", "tcp", "tls"} {
				for _, rule := range mapSlice(o.Spec[proto]) {
					for _, r := range mapSlice(rule["route"]) {
						if dest, ok := r["destination"].(map[string]any); ok {
							if h, _ := dest["host"].(string); h != "" {
								add(o, h, o.Namespace)
							}
						}
					}
				}
			}
		case KindDestinationRule:
			if h, _ := o.Spec["host"].(string); h != "" {
				add(o, h, o.Namespace)
			}
		}
	}
	for _, w := range idx.workloads {
		sort.Slice(w.Routes, func(i, j int) bool {
			a, b := w.Routes[i], w.Routes[j]
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			if a.Namespace != b.Namespace {
				return a.Namespace < b.Namespace
			}
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			return a.Service < b.Service
		})
	}
}

// refHost spells a Gateway API object reference as a host the index can
// resolve: the bare name when the reference stays in the route's namespace,
// name.namespace when it crosses one.
func refHost(ref map[string]any, namespace string) string {
	name, _ := ref["name"].(string)
	if name == "" {
		return ""
	}
	if ns, _ := ref["namespace"].(string); ns != "" && ns != namespace {
		return name + "." + ns
	}
	return name
}
