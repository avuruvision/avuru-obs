// Package meshconfig reads the Kubernetes and Istio objects that DEFINE a mesh,
// so the product can answer what telemetry structurally cannot.
//
// Everything else in avuru-obs learns about the estate from traffic, which is a
// strength — it is why the map needs no configuration — and has one hard edge:
// a workload with no traffic does not exist. A namespace labelled for ambient
// and not working looks exactly like a namespace nobody labelled. A route whose
// backend does not exist is silently dead. Those failures produce no traffic at
// all, so the tool that only watches traffic is blindest where the failure is.
//
// The package is deliberately shaped like hub/internal/topology: the reading is
// separated from the judging, and the judging (validate.go) is pure.
// See design/2026-09-07-mesh-configuration.md.
package meshconfig

import (
	"context"
	"time"
)

// State says why a snapshot is empty, because "no configuration" has four
// causes with four different fixes and one of them is "this is correct".
//
// Modelled on storage.MeshControlPlaneState, and for the same reason: an
// operator told only "unavailable" checks the wrong thing first.
type State string

const (
	// StateOK: the cluster was read.
	StateOK State = "ok"
	// StateUnconfigured: the hub is not running in a cluster at all — compose,
	// a laptop, a test. Nothing is wrong; there is simply nothing to read.
	StateUnconfigured State = "unconfigured"
	// StateForbidden: the module is on and the API server refused us. This is
	// the one an operator can fix, and the message must name the ClusterRole.
	StateForbidden State = "forbidden"
	// StateNoCRDs: the cluster answered and has no Istio or Gateway API types.
	// A real answer: this cluster runs no mesh we can read.
	StateNoCRDs State = "no-crds"
)

// Object is one Kubernetes object, reduced to what this product reads.
//
// Unstructured on purpose (see the AEP): typed Istio clients would pin us to a
// version matrix against whatever the operator runs, and every field below is
// one we extract ourselves and can keep extracting across API versions.
type Object struct {
	Kind      string
	Namespace string
	Name      string
	Labels    map[string]string
	// Spec is the object's spec, as decoded. Validation walks it; the API
	// serves it back as YAML for the detail view.
	Spec map[string]any
	// Findings are attached by the validator, not by the reader.
	Findings []Finding
}

// Namespace is one namespace's mesh membership, from labels rather than traffic
// — which is the entire point: this row exists even when nothing is talking.
type Namespace struct {
	Name   string
	Labels map[string]string
	// DataplaneMode is "ambient", "sidecar" or "" for out of mesh. Empty is a
	// real answer and must not be rendered as a mode.
	DataplaneMode string
	// Waypoint is the waypoint serving this namespace, from
	// istio.io/use-waypoint, and WaypointNamespace where it is elsewhere.
	Waypoint          string
	WaypointNamespace string
	// MTLSMode is the effective PeerAuthentication mode: STRICT, PERMISSIVE,
	// DISABLE, or "" when no policy applies and the mesh default governs.
	MTLSMode string
}

// Pod is one pod, projected to what enrolment needs and nothing else.
//
// A pod is where mesh membership actually happens: the sidecar is a container
// in it, and ambient capture is an annotation the node agent writes on it. It
// is also, by a wide margin, the most numerous and the largest object in a
// cluster — so the reader keeps a dozen fields per pod rather than the pod, and
// this struct is the whole of what it keeps.
type Pod struct {
	Namespace string
	Name      string
	Labels    map[string]string
	// Annotations holds only the four the mesh writes: ambient redirection,
	// sidecar injection and status, and the revision. Everything else a pod
	// carries was dropped before it was stored.
	Annotations map[string]string
	// OwnerKind and OwnerName are the first controller ownerReference, which is
	// how a pod is joined back to the workload that made it.
	OwnerKind      string
	OwnerName      string
	ServiceAccount string
	NodeName       string
	HostNetwork    bool
	// Containers and InitContainers are NAMES only — enough to see istio-proxy
	// or istio-init among them, which is what a sidecar looks like.
	Containers     []string
	InitContainers []string
	Phase          string
}

// Snapshot is the whole readable state at one moment, plus WHY it is as small
// as it is.
type Snapshot struct {
	State State
	// Reason is the operator-facing explanation, empty when State is OK. It
	// names the thing to change, never just the thing that is missing.
	Reason string
	// SyncedAt is when this snapshot was taken. Zero when nothing was read —
	// callers must not render a timestamp for a read that never happened.
	SyncedAt time.Time
	// Kinds that could not be read at all, by name, so one missing CRD costs
	// its own row rather than the whole screen.
	MissingKinds []string
	// Truncated says a cluster was too large to snapshot whole. A short list
	// that does not say so is a lie about the cluster.
	Truncated bool
	// PodsTruncated is the same statement about pods, which are capped on
	// their own: a cluster with many pods must not cost it its configuration.
	PodsTruncated bool

	Namespaces []Namespace
	Objects    []Object
	// Pods are kept apart from Objects: they are not configuration, they are
	// what configuration acts on, and they never count against its cap.
	Pods []Pod
}

// Reader returns the current view of the cluster's mesh configuration.
//
// One method, because callers should not be able to ask for half a snapshot and
// then join two moments together.
type Reader interface {
	Snapshot(ctx context.Context) Snapshot
}

// NoopReader is what runs everywhere the real one cannot: outside a cluster,
// with the module off, in every test that is not about this package.
//
// It reports StateUnconfigured rather than an empty OK, so a hub on a laptop
// says "not running in a cluster" instead of "your mesh has no configuration",
// which would be a confident lie.
type NoopReader struct{}

func (NoopReader) Snapshot(context.Context) Snapshot {
	return Snapshot{
		State:  StateUnconfigured,
		Reason: "the hub is not running in a Kubernetes cluster, so there is no mesh configuration to read",
	}
}

// Reason turns a state into the instruction that fixes it. Kept beside the
// states so a new one cannot be added without answering "and what do I do".
func Reason(s State, clusterRole string) string {
	switch s {
	case StateForbidden:
		return "the hub may not read mesh configuration — grant the " + clusterRole +
			" ClusterRole, which the chart renders with modules.meshConfig.enabled"
	case StateNoCRDs:
		return "this cluster has no Istio or Gateway API custom resources, so there is no mesh configuration to read"
	case StateUnconfigured:
		return "the hub is not running in a Kubernetes cluster, so there is no mesh configuration to read"
	default:
		return ""
	}
}
