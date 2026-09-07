package api

import (
	"net/http"
	"slices"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
)

// maxWorkloadPods bounds the pod list on a single workload: a DaemonSet on a
// thousand nodes is one row here, not a thousand, and PodsTotal says the rest
// exist.
const maxWorkloadPods = 50

// How enrolment is spelled on a pod. The same vocabulary meshconfig reads to
// decide a workload's Injected and Captured; repeated here so a pod row can
// say the same thing per pod without the package exporting its label table.
const (
	podContainerIstioProxy       = "istio-proxy"
	podAnnotationAmbientRedirect = "ambient.istio.io/redirection"
	podLabelTemplateHash         = "pod-template-hash"
	podPhaseRunning              = "Running"
)

// meshPodDTO is one pod of a workload, reduced to where it runs and whether
// the mesh has it.
type meshPodDTO struct {
	Name     string `json:"name"`
	Node     string `json:"node,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Injected bool   `json:"injected"`
	Captured bool   `json:"captured"`
}

type meshWorkloadResponse struct {
	State         string   `json:"state"`
	Reason        string   `json:"reason,omitempty"`
	SyncedAt      *string  `json:"syncedAt,omitempty"`
	MissingKinds  []string `json:"missingKinds,omitempty"`
	PodsTruncated bool     `json:"podsTruncated,omitempty"`
	ChecksSkipped string   `json:"checksSkipped,omitempty"`
	// Workload is absent when the cluster could not be read, or pods could
	// not: a zero-valued workload would read as one that runs nothing.
	Workload *meshWorkloadDTO `json:"workload,omitempty"`
	Findings []meshFindingDTO `json:"findings"`
	// Pods is bounded; PodsShown and PodsTotal say by how much.
	Pods      []meshPodDTO `json:"pods"`
	PodsShown int          `json:"podsShown"`
	PodsTotal int          `json:"podsTotal"`
}

// handleMeshWorkload returns one workload whole: its row, its own findings,
// the policies that cover it with THEIR findings, and its pods.
func (a *API) handleMeshWorkload(w http.ResponseWriter, r *http.Request) error {
	namespace, name := r.PathValue("namespace"), r.PathValue("name")
	snap := a.meshConfig().Snapshot(r.Context())
	resp := meshWorkloadResponse{
		State:         string(snap.State),
		Reason:        snap.Reason,
		SyncedAt:      syncedAtString(snap.SyncedAt),
		MissingKinds:  snap.MissingKinds,
		PodsTruncated: snap.PodsTruncated,
		Findings:      []meshFindingDTO{},
		Pods:          []meshPodDTO{},
	}
	if snap.State != meshconfig.StateOK {
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	resp.ChecksSkipped = checksSkipped(snap)
	if !podsReadable(snap) {
		// Not a 404: the workload may well exist. We could not look, and
		// ChecksSkipped says what grants the look.
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

	i := slices.IndexFunc(snap.Workloads, func(wl meshconfig.Workload) bool {
		return wl.Namespace == namespace && wl.Name == name
	})
	if i < 0 {
		return notFound("no workload %s/%s in the cluster snapshot", namespace, name)
	}
	wl := snap.Workloads[i]
	row := toWorkloadDTO(wl, a.workloadTraffic(r))
	attachPolicyFindings(row.Policies, snap.Objects)
	resp.Workload = &row
	resp.Findings = append(resp.Findings, toFindingDTOs(wl.Findings)...)
	for _, p := range snap.Pods {
		if !podBelongsTo(p, wl) {
			continue
		}
		resp.PodsTotal++
		if len(resp.Pods) < maxWorkloadPods {
			resp.Pods = append(resp.Pods, toPodDTO(p))
		}
	}
	resp.PodsShown = len(resp.Pods)
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// attachPolicyFindings copies each covering policy's own findings onto its
// reference, so the detail view can say "this policy is broken" beside the
// policy rather than sending the reader to the config browser.
func attachPolicyFindings(refs []meshPolicyRefDTO, objects []meshconfig.Object) {
	for i := range refs {
		ref := &refs[i]
		for _, o := range objects {
			if o.Kind == ref.Kind && o.Namespace == ref.Namespace && o.Name == ref.Name {
				ref.Findings = toFindingDTOs(o.Findings)
				break
			}
		}
	}
}

// podBelongsTo joins a pod back to a workload row by the row's own kind — the
// inverse of how the row was built: a Deployment row exists only when the
// ReplicaSet's template hash confirmed it, so a Deployment's pods are the
// ones whose ReplicaSet is <name>-<hash>; a ReplicaSet row keeps its full
// name; a bare Pod is its own workload; any other controller is named as
// itself.
func podBelongsTo(p meshconfig.Pod, wl meshconfig.Workload) bool {
	if p.Namespace != wl.Namespace {
		return false
	}
	switch wl.Kind {
	case meshconfig.KindPod:
		return p.OwnerKind == "" && p.Name == wl.Name
	case meshconfig.KindDeployment:
		hash := p.Labels[podLabelTemplateHash]
		return p.OwnerKind == meshconfig.KindReplicaSet && hash != "" && p.OwnerName == wl.Name+"-"+hash
	default:
		return p.OwnerKind == wl.Kind && p.OwnerName == wl.Name
	}
}

func toPodDTO(p meshconfig.Pod) meshPodDTO {
	return meshPodDTO{
		Name:     p.Name,
		Node:     p.NodeName,
		Phase:    p.Phase,
		Injected: slices.Contains(p.Containers, podContainerIstioProxy),
		Captured: p.Annotations[podAnnotationAmbientRedirect] == "enabled",
	}
}
