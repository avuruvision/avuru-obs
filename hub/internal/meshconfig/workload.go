package meshconfig

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// PolicyRef names one policy that covers a workload, and at what scope it
// reached it: "workload" by selector, "namespace" scope-wide, "mesh" from the
// root namespace.
type PolicyRef struct {
	Kind, Namespace, Name, Scope string
}

// RouteRef names one route or rule that reaches a workload through one of
// its Services: Service is that Service, "namespace/name", and Host is how the
// object spelled it — a bare name, a short form or a FQDN — so the page shows
// what the operator wrote.
type RouteRef struct {
	Kind, Namespace, Name, Service, Host string
}

// Where a workload's creation time came from. The controller's own date is
// the record; when no controller object was read the oldest pod dates it,
// and the page says so.
const (
	CreatedFromController = "controller"
	CreatedFromPods       = "pods"
)

// Workload is one thing the cluster runs, seen from its pods.
//
// This is the row below the namespace, and the one that answers what a label
// cannot: the namespace says what was asked for, only the pod says whether a
// sidecar was injected or the node agent captured it. Both the declared and
// the effective mode are kept, because the gap between them is the finding.
type Workload struct {
	Namespace, Name string
	// Kind is Deployment, StatefulSet, DaemonSet, Job, Pod for a bare pod, or
	// ReplicaSet when the Deployment behind it could not be confirmed. Any
	// other controller is named as itself.
	Kind string
	// Labels are the pods' labels, from the first pod in name order. It is
	// what every selector in the mesh matches against.
	Labels         map[string]string
	ServiceAccount string
	Pods           int
	RunningPods    int
	// CreatedAt is the controller's creation time, or the oldest pod's when
	// no controller object was read; CreatedFrom says which. Annotations are
	// the controller's, within the reader's bounds (AnnotationsCut).
	CreatedAt      time.Time
	CreatedFrom    string
	Annotations    map[string]string
	AnnotationsCut bool
	// DeclaredMode is what was asked for: "ambient", "sidecar" or "" — the
	// pod's own istio.io/dataplane-mode first, then the namespace's.
	DeclaredMode string
	// Injected: a running pod carries an istio-proxy container. Captured: a
	// running pod carries the ambient redirection annotation. Facts about
	// pods, never about labels.
	Injected bool
	Captured bool
	// DataplaneMode is what happened: "ambient" when captured, "sidecar" when
	// injected, "" when neither. Compared with DeclaredMode by the checks.
	DataplaneMode string
	// Waypoint is the waypoint this workload is bound to, and WaypointSource
	// whether the binding is its own ("workload") or inherited ("namespace").
	Waypoint          string
	WaypointNamespace string
	WaypointSource    string
	DeclaredMTLS      DeclaredMTLS
	// Policies are every PeerAuthentication, AuthorizationPolicy, Sidecar,
	// Telemetry and WasmPlugin that covers this workload, with the scope it
	// reached it at. An empty list is an answer, not a finding.
	Policies []PolicyRef
	// Services are "namespace/name" of every Service whose selector picks
	// these pods.
	Services []string
	// Routes are the routes and rules that reach this workload through those
	// Services — attached by the validator, which holds the host index. An
	// empty list is an answer: nothing names this workload.
	Routes   []RouteRef
	Findings []Finding
}

// workloadPolicyKinds are the kinds that attach to workloads by selector or
// by scope. Routes and rules attach to hosts and are not here.
var workloadPolicyKinds = map[string]bool{
	KindPeerAuthentication:  true,
	KindAuthorizationPolicy: true,
	KindSidecar:             true,
	KindTelemetry:           true,
	KindWasmPlugin:          true,
}

// WorkloadsFrom groups pods into the workloads that made them, and resolves
// for each what the mesh declared and what it did.
//
// Pure. Pods are grouped by owner without a ReplicaSet watch (see
// workloadKeyFor), and the result is sorted by namespace then name so it can
// be paged and searched.
func WorkloadsFrom(pods []Pod, namespaces []Namespace, objects []Object, rootNamespace string) []Workload {
	nsByName := make(map[string]Namespace, len(namespaces))
	for _, ns := range namespaces {
		nsByName[ns.Name] = ns
	}
	deployments := deploymentSet(objects)
	controllers := map[string]Object{}
	policiesByNS := map[string][]Object{}
	var peerAuths []Object
	for _, o := range objects {
		if workloadKinds[o.Kind] {
			controllers[o.Kind+"/"+key(o.Namespace, o.Name)] = o
		}
		if !workloadPolicyKinds[o.Kind] {
			continue
		}
		policiesByNS[o.Namespace] = append(policiesByNS[o.Namespace], o)
		if o.Kind == KindPeerAuthentication {
			peerAuths = append(peerAuths, o)
		}
	}
	links := resolveServices(objects, pods, deployments)

	// Name order, so "the first pod" means the same pod on every request.
	sorted := slices.Clone(pods)
	sortPods(sorted)
	groups := map[workloadKey]*Workload{}
	var order []workloadKey
	for _, p := range sorted {
		k := workloadKeyFor(p, deployments)
		w := groups[k]
		if w == nil {
			ns := nsByName[k.Namespace]
			w = &Workload{
				Namespace:      k.Namespace,
				Name:           k.Name,
				Kind:           k.Kind,
				Labels:         p.Labels,
				ServiceAccount: p.ServiceAccount,
				DeclaredMode:   declaredMode(p, ns),
			}
			w.Waypoint, w.WaypointNamespace, w.WaypointSource = waypointBinding(p.Labels, p.Namespace, ns, SourceWorkload)
			if c, ok := controllers[k.Kind+"/"+k.id()]; ok {
				w.CreatedAt, w.CreatedFrom = c.CreatedAt, CreatedFromController
				w.Annotations, w.AnnotationsCut = c.Annotations, c.AnnotationsCut
			}
			groups[k] = w
			order = append(order, k)
		}
		w.Pods++
		if w.CreatedFrom != CreatedFromController && !p.CreatedAt.IsZero() &&
			(w.CreatedAt.IsZero() || p.CreatedAt.Before(w.CreatedAt)) {
			w.CreatedAt, w.CreatedFrom = p.CreatedAt, CreatedFromPods
		}
		// Only a running pod says anything about enrolment: a Pending pod has
		// no sidecar yet and a Succeeded one no longer counts.
		if p.Phase != phaseRunning {
			continue
		}
		w.RunningPods++
		if slices.Contains(p.Containers, containerIstioProxy) {
			w.Injected = true
		}
		if p.Annotations[annotationAmbientRedirection] == valueInjectEnabled {
			w.Captured = true
		}
	}

	out := make([]Workload, 0, len(order))
	for _, k := range order {
		w := groups[k]
		switch {
		case w.Captured:
			w.DataplaneMode = DataplaneAmbient
		case w.Injected:
			w.DataplaneMode = DataplaneSidecar
		}
		w.DeclaredMTLS = DeclaredMTLSFor(w.Labels, w.Namespace, peerAuths, rootNamespace)
		w.Policies = attachedPolicies(w.Labels, w.Namespace, policiesByNS, rootNamespace)
		w.Services = links.servicesByWorkload[k.id()]
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// declaredMode is what a pod was asked to be, under the mesh's precedence: the
// pod's own dataplane-mode label first, then its namespace's — except that a
// pod opting out of injection turns a sidecar namespace into nothing.
func declaredMode(p Pod, ns Namespace) string {
	switch strings.ToLower(p.Labels[labelDataplaneMode]) {
	case valueAmbient:
		return DataplaneAmbient
	case valueDataplaneNone:
		return ""
	}
	if ns.DataplaneMode == DataplaneSidecar &&
		(p.Labels[labelSidecarInject] == "false" || p.Annotations[annotationSidecarInject] == "false") {
		return ""
	}
	return ns.DataplaneMode
}

// waypointBinding resolves istio.io/use-waypoint on an object, falling back
// to its namespace's binding. A literal "none" is an opt-out, and it is the
// object's own answer: it stops the namespace's binding from reaching down.
func waypointBinding(labels map[string]string, namespace string, ns Namespace, ownSource string) (waypoint, waypointNS, source string) {
	if wp := labels[labelUseWaypoint]; wp != "" {
		if wp == valueWaypointNone {
			return "", "", ownSource
		}
		waypointNS = labels[labelWaypointNS]
		if waypointNS == "" {
			waypointNS = namespace
		}
		return wp, waypointNS, ownSource
	}
	if ns.Waypoint != "" {
		return ns.Waypoint, ns.WaypointNamespace, SourceNamespace
	}
	return "", "", ""
}

// attachedPolicies lists every policy that covers a workload, and at what
// scope: a selector it matches, its namespace, or the mesh's root namespace.
func attachedPolicies(labels map[string]string, namespace string, byNS map[string][]Object, rootNamespace string) []PolicyRef {
	var out []PolicyRef
	consider := func(pol Object, scopeWide string) {
		if hasTargetRef(pol) {
			return
		}
		sel := selectorLabels(pol)
		switch {
		case len(sel) > 0:
			if pol.Namespace == namespace && labelsMatch(sel, labels) {
				out = append(out, PolicyRef{pol.Kind, pol.Namespace, pol.Name, SourceWorkload})
			}
		default:
			out = append(out, PolicyRef{pol.Kind, pol.Namespace, pol.Name, scopeWide})
		}
	}
	// A scope-wide policy in the root namespace is the mesh's, even for the
	// root namespace's own workloads — the same reading DeclaredMTLSFor makes.
	scopeWide := SourceNamespace
	if namespace == rootNamespace {
		scopeWide = SourceMesh
	}
	for _, pol := range byNS[namespace] {
		consider(pol, scopeWide)
	}
	if namespace != rootNamespace {
		for _, pol := range byNS[rootNamespace] {
			consider(pol, SourceMesh)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	return out
}

// countWorkloads fills each namespace's workload and enrolment counts from the
// workload list. The gap between the two numbers is the one no label shows.
func countWorkloads(namespaces []Namespace, workloads []Workload) {
	byName := make(map[string]int, len(namespaces))
	for i := range namespaces {
		byName[namespaces[i].Name] = i
	}
	for _, w := range workloads {
		i, ok := byName[w.Namespace]
		if !ok {
			continue
		}
		namespaces[i].Workloads++
		if w.Captured || w.Injected {
			namespaces[i].Enrolled++
		}
	}
}
