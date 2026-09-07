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
	Name  string `json:"name"`
	Node  string `json:"node,omitempty"`
	Phase string `json:"phase,omitempty"`
	// Revision is the pod-template-hash: which rollout this pod belongs to.
	Revision  string  `json:"revision,omitempty"`
	CreatedAt *string `json:"createdAt,omitempty"`
	Injected  bool    `json:"injected"`
	Captured  bool    `json:"captured"`
}

// meshHealthDTO is the page's one-word verdict on a workload, in the same
// vocabulary as every other health badge, with the reason that decided it.
type meshHealthDTO struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// meshConfigRefDTO is one route or rule that reaches the workload through
// one of its Services, with that object's own findings — so "this route is
// broken" is said beside the route.
type meshConfigRefDTO struct {
	Kind      string           `json:"kind"`
	Namespace string           `json:"namespace"`
	Name      string           `json:"name"`
	Service   string           `json:"service"`
	Host      string           `json:"host"`
	Findings  []meshFindingDTO `json:"findings,omitempty"`
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
	// Labels are the workload's, without the ReplicaSet's template hash;
	// Annotations the controller's, within the reader's bounds. Both detail-
	// only: a list of workloads is not the place to repeat them.
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	AnnotationsCut bool              `json:"annotationsCut,omitempty"`
	// Health is absent when pods could not be read — a verdict over pods
	// nobody counted would be a guess.
	Health *meshHealthDTO `json:"health,omitempty"`
	// Routes are the routes and rules that reach this workload through its
	// Services. Always a list: empty is an answer.
	Routes   []meshConfigRefDTO `json:"routes"`
	Findings []meshFindingDTO   `json:"findings"`
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
		Routes:        []meshConfigRefDTO{},
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
	for i := range row.Policies {
		p := &row.Policies[i]
		p.Findings = findingsFor(snap.Objects, p.Kind, p.Namespace, p.Name)
	}
	resp.Findings = append(resp.Findings, toFindingDTOs(wl.Findings)...)
	if f := a.observedWorkloads(r).decorate(&row, wl, snap); f != nil {
		resp.Findings = append(resp.Findings, toFindingDTOs([]meshconfig.Finding{*f})...)
	}
	resp.Workload = &row
	resp.Labels = workloadLabels(wl.Labels)
	resp.Annotations, resp.AnnotationsCut = wl.Annotations, wl.AnnotationsCut
	health := workloadHealth(wl, row)
	resp.Health = &health
	for _, rt := range wl.Routes {
		resp.Routes = append(resp.Routes, meshConfigRefDTO{
			Kind: rt.Kind, Namespace: rt.Namespace, Name: rt.Name, Service: rt.Service, Host: rt.Host,
			Findings: findingsFor(snap.Objects, rt.Kind, rt.Namespace, rt.Name),
		})
	}
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

// findingsFor returns one object's own findings by kind and name, so the
// detail view can say "this policy is broken" or "this route is broken"
// beside the reference rather than sending the reader to the config browser.
// Nil when the object is not in the snapshot — a reference the validator
// never saw carries no verdict.
func findingsFor(objects []meshconfig.Object, kind, namespace, name string) []meshFindingDTO {
	for _, o := range objects {
		if o.Kind == kind && o.Namespace == namespace && o.Name == name {
			return toFindingDTOs(o.Findings)
		}
	}
	return nil
}

// workloadLabels is the pods' labels minus the template hash, which names a
// ReplicaSet rather than the workload and changes on every rollout.
func workloadLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != podLabelTemplateHash {
			out[k] = v
		}
	}
	return out
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
		Name:      p.Name,
		Node:      p.NodeName,
		Phase:     p.Phase,
		Revision:  p.Labels[podLabelTemplateHash],
		CreatedAt: syncedAtString(p.CreatedAt),
		Injected:  slices.Contains(p.Containers, podContainerIstioProxy),
		Captured:  p.Annotations[podAnnotationAmbientRedirect] == "enabled",
	}
}
