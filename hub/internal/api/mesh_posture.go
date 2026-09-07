package api

import (
	"fmt"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// declaredMTLS answers what the cluster SAYS about a workload. An empty mode
// means no policy was read for it — which is a different fact from a policy
// that says PERMISSIVE, and the fold keeps them apart.
type declaredMTLS interface {
	// EffectiveMTLS is the PeerAuthentication mode that governs the workload
	// and how wide the policy that set it is: mesh, namespace, workload, or
	// "" when none applies.
	EffectiveMTLS(namespace, workload string) (mode, scope string)
	// DataplaneMode is "ambient", "sidecar" or "" for a namespace out of the
	// mesh.
	DataplaneMode(namespace string) string
	// Declared is false when nothing was read at all — the config module is
	// off, or the cluster could not be read. Every declared field is then
	// absent rather than empty, and no verdict compares against a policy.
	Declared() bool
}

// snapshotDeclared adapts a configuration snapshot to declaredMTLS.
//
// On this branch the snapshot resolves policy per NAMESPACE only, so the scope
// is reported as "namespace" whenever a mode is set: a mesh-wide default and a
// namespace policy cannot be told apart here. A sibling change adds
// Snapshot.EffectiveMTLS with workload selectors and the real scope; when it
// lands, this adapter shrinks to that one call.
type snapshotDeclared struct {
	snap       meshconfig.Snapshot
	namespaces map[string]meshconfig.Namespace
}

func newSnapshotDeclared(snap meshconfig.Snapshot) snapshotDeclared {
	byName := make(map[string]meshconfig.Namespace, len(snap.Namespaces))
	for _, ns := range snap.Namespaces {
		byName[ns.Name] = ns
	}
	return snapshotDeclared{snap: snap, namespaces: byName}
}

func (d snapshotDeclared) EffectiveMTLS(namespace, _ string) (mode, scope string) {
	if m := d.namespaces[namespace].MTLSMode; m != "" {
		return m, "namespace"
	}
	return "", ""
}

func (d snapshotDeclared) DataplaneMode(namespace string) string {
	return d.namespaces[namespace].DataplaneMode
}

func (d snapshotDeclared) Declared() bool { return d.snap.State == meshconfig.StateOK }

// observed is one workload's traffic as its proxy reported it, reduced to the
// three answers the verdict needs. Units add requests and connections: a
// verdict asks "did ANY plaintext arrive", for which a connection counts as
// much as a request. The DTO keeps the two apart for the reader.
type observed struct {
	reporter                 string
	mtls, plaintext, unknown uint64
	requests, connections    uint64
	// The plaintext split by unit, kept for the finding's sentence: "12
	// requests and 3 connections" is actionable where "15" is not.
	plaintextRequests, plaintextConnections uint64
	callers                                 []storage.MeshCaller
}

func observe(w storage.MeshWorkloadSecurity) *observed {
	c := w.Counts
	return &observed{
		reporter:    w.Reporter,
		mtls:        c.MTLSRequests + c.MTLSConnections,
		plaintext:   c.PlaintextRequests + c.PlaintextConnections,
		unknown:     c.UnknownRequests + c.UnknownConnections,
		requests:    c.MTLSRequests + c.PlaintextRequests + c.UnknownRequests,
		connections: c.MTLSConnections + c.PlaintextConnections + c.UnknownConnections,
		callers:     w.PlaintextCallers,

		plaintextRequests:    c.PlaintextRequests,
		plaintextConnections: c.PlaintextConnections,
	}
}

// mtlsShare is the encrypted fraction of what the proxy could classify.
// Unknown is left out of both sides: the proxy declined to say, and counting
// it as either would invent a certainty. nil when nothing was classified —
// 0/0 is not 0%.
func (o *observed) mtlsShare() *float64 {
	total := o.mtls + o.plaintext
	if total == 0 {
		return nil
	}
	share := float64(o.mtls) / float64(total)
	return &share
}

// postureInput is everything the fold reads. A struct rather than positional
// arguments because a call site that swaps two booleans compiles.
type postureInput struct {
	namespace, workload string
	// declared is declaredMTLS.Declared(): false makes every verdict
	// observed-only, and no finding compares against a policy nobody read.
	declared     bool
	declaredMode string // STRICT | PERMISSIVE | DISABLE | ""
	dataplane    string // ambient | sidecar | ""
	// obs is nil when no proxy reported for this workload in the window.
	obs *observed
	// hasTraffic: the traces saw this workload receive requests in the
	// window. With an ambient namespace and no observation, that is the
	// difference between "idle" and "not carried".
	hasTraffic bool
}

// Verdicts. Strings on the wire, so the UI can switch on them.
const (
	postureStrictAndMTLS          = "strict-and-mtls"
	postureStrictButPlaintext     = "declared-strict-observed-plaintext"
	posturePermissiveAllMTLS      = "permissive-but-all-mtls"
	posturePermissivePlaintext    = "permissive-with-plaintext-callers"
	postureDisabled               = "disabled"
	postureUncarried              = "uncarried"
	postureObservedOnlyMTLS       = "observed-only-mtls"
	postureObservedOnlyPlain      = "observed-only-plaintext"
	postureIdle                   = "idle"
	postureUnknown                = "unknown"
	meshPlaintextCallersInFinding = 5
)

// posture is the single fold of declared against observed. Pure, so the whole
// verdict table is one unit test; every consumer reads this and never
// re-derives a verdict of its own.
func posture(in postureInput) (verdict string, finding *meshconfig.Finding) {
	ref := in.namespace + "/" + in.workload
	if in.obs == nil {
		if in.declared && in.dataplane == "ambient" && in.hasTraffic {
			return postureUncarried, &meshconfig.Finding{
				Code: meshconfig.CodeTrafficUncarried, Severity: meshconfig.SeverityError, Ref: ref,
				Message: fmt.Sprintf("%s is labelled for ambient and %s has traffic, but no proxy reported carrying it — the pod is not enrolled",
					in.namespace, in.workload),
				Hint: "pods started before the namespace was labelled stay uncaptured until they restart — restart them, and check that the pod is not labelled istio.io/dataplane-mode=none",
			}
		}
		return postureUnknown, nil
	}
	o := in.obs
	if o.mtls+o.plaintext+o.unknown == 0 {
		return postureIdle, nil
	}
	if !in.declared {
		switch {
		case o.plaintext > 0:
			return postureObservedOnlyPlain, nil
		case o.mtls > 0:
			return postureObservedOnlyMTLS, nil
		}
		return postureUnknown, nil
	}
	switch strings.ToUpper(in.declaredMode) {
	case "DISABLE":
		return postureDisabled, nil
	case "STRICT":
		switch {
		case o.plaintext > 0:
			return postureStrictButPlaintext, &meshconfig.Finding{
				Code: meshconfig.CodeMTLSNotEnforced, Severity: meshconfig.SeverityError, Ref: ref,
				Message: fmt.Sprintf("%s reached %s under a STRICT policy — the policy is not applied to this workload",
					plaintextPhrase(in.obs), ref),
				Hint: "check whether the workload is enrolled (its pods captured or injected), whether the policy's selector matches it, and whether a DestinationRule disables TLS for this host",
			}
		case o.mtls > 0:
			return postureStrictAndMTLS, nil
		}
		return postureUnknown, nil
	}
	// PERMISSIVE, or no policy at all: the mesh default governs and it accepts
	// plaintext, so the two are one posture.
	switch {
	case o.plaintext > 0:
		return posturePermissivePlaintext, &meshconfig.Finding{
			Code: meshconfig.CodePlaintextCallers, Severity: meshconfig.SeverityWarning, Ref: ref,
			Message: fmt.Sprintf("%s reached %s from %s — a STRICT policy would refuse them",
				plaintextPhrase(in.obs), ref, callersPhrase(o.callers)),
			Hint: "migrate these callers into the mesh, or exclude them on purpose, before tightening the policy: " + callersPhrase(o.callers),
		}
	case o.mtls > 0:
		return posturePermissiveAllMTLS, &meshconfig.Finding{
			Code: meshconfig.CodeMTLSReadyToTighten, Severity: meshconfig.SeverityInfo, Ref: ref,
			Message: fmt.Sprintf("every observed caller of %s used mutual TLS over the window — STRICT would refuse nothing that is currently talking", ref),
			Hint:    "a PeerAuthentication with mode STRICT on this workload or its namespace closes the plaintext path",
		}
	}
	return postureUnknown, nil
}

// plaintextPhrase says how much plaintext arrived, in the units it arrived in.
// "12 requests and 3 connections" rather than "15", because 15 of what is not
// a number anyone can act on.
func plaintextPhrase(o *observed) string {
	var parts []string
	if o.plaintextRequests > 0 {
		parts = append(parts, fmt.Sprintf("%d plaintext %s", o.plaintextRequests, plural(o.plaintextRequests, "request")))
	}
	if o.plaintextConnections > 0 {
		parts = append(parts, fmt.Sprintf("%d plaintext %s", o.plaintextConnections, plural(o.plaintextConnections, "connection")))
	}
	return strings.Join(parts, " and ")
}

func plural(n uint64, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// callersPhrase names up to meshPlaintextCallersInFinding callers, loudest
// first, and says how many more there are.
func callersPhrase(callers []storage.MeshCaller) string {
	if len(callers) == 0 {
		return "callers the proxy did not name"
	}
	names := make([]string, 0, meshPlaintextCallersInFinding)
	for i, c := range callers {
		if i == meshPlaintextCallersInFinding {
			names = append(names, fmt.Sprintf("and %d more", len(callers)-i))
			break
		}
		names = append(names, fmt.Sprintf("%s/%s (%d)", c.Namespace, c.Workload, c.Units))
	}
	return strings.Join(names, ", ")
}
