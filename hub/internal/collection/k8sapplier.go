package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// OverlayChecksumAnnotation is the hub-owned pod-template annotation that
// forces a sensor rollout when the applied overlay changes. Distinct from
// Helm's checksum/config, which stays untouched so `helm upgrade` keeps
// working normally (design/2026-07-27-collection-control-plane.md).
const OverlayChecksumAnnotation = "avuru.obs/overlay-checksum"

// baseValuesKey is the single data key in the chart's base-values ConfigMap
// (collection-rbac.yaml).
const baseValuesKey = "values.json"

// K8sApplier reconciles the live sensor objects to whatever the chart renders
// for the current overlay. It only ever GETs, UPDATEs and PATCHes objects the
// chart already created: the control Role grants no create and no delete, and
// the chart renders placeholder ConfigMaps for disabled signals precisely so
// this applier never needs one (see collection-rbac.yaml).
type K8sApplier struct {
	Client    kubernetes.Interface
	Namespace string
	// ReleaseName is the Helm release the sensor belongs to; it drives the
	// names the re-render produces.
	ReleaseName string
	// Fullname is `avuruobs.fullname` for that release — the actual prefix on
	// the live object names, which nameOverride/fullnameOverride can make
	// differ from ReleaseName. It drives the allowlist, so a render that
	// disagrees with it is refused rather than applied to the wrong objects.
	Fullname string
}

var _ Applier = (*K8sApplier)(nil)

func (a *K8sApplier) baseValuesName() string { return a.Fullname + "-collection-base-values" }

func (a *K8sApplier) daemonSetName() string { return a.Fullname + "-sensor" }

// allowedConfigMaps mirrors the resourceNames on the chart's control Role. It
// is checked in-process before any write so a name mismatch surfaces as one
// clear error instead of a partial apply that dies on an RBAC denial halfway
// through.
func (a *K8sApplier) allowedConfigMaps() map[string]bool {
	allowed := make(map[string]bool, 4)
	for _, signal := range []string{"obi", "agent", "profiler", "kepler"} {
		allowed[a.Fullname+"-sensor-"+signal] = true
	}
	return allowed
}

// Apply re-renders the sensor manifests from the chart's published base values
// with overlay on top, then reconciles the live ConfigMaps and DaemonSet.
func (a *K8sApplier) Apply(ctx context.Context, overlay Overlay) error {
	base, err := a.loadBaseValues(ctx)
	if err != nil {
		return err
	}

	manifests, err := RenderSensorManifests(base, overlay, a.ReleaseName, a.Namespace)
	if err != nil {
		return fmt.Errorf("render sensor manifests for release %q: %w", a.ReleaseName, err)
	}
	if manifests.DaemonSet == nil {
		// Every signal off renders no sensor pod at all. The applier can only
		// patch an existing DaemonSet, so it has nothing to reconcile against
		// and must not silently leave the live one running the old config.
		return fmt.Errorf("refusing to apply: the overlay renders no sensor DaemonSet (every signal disabled); disable the sensor via `helm upgrade` instead")
	}
	if err := a.verifyNames(manifests); err != nil {
		return err
	}

	checksum := overlayChecksum(manifests)
	for i := range manifests.ConfigMaps {
		if err := a.applyConfigMap(ctx, &manifests.ConfigMaps[i]); err != nil {
			return err
		}
	}
	return a.patchDaemonSet(ctx, manifests, checksum)
}

// loadBaseValues reads the curated, non-secret values subset the chart
// publishes for exactly this purpose. Rendering from anything else (the hub's
// own guesses, the chart defaults) would revert operator settings.
func (a *K8sApplier) loadBaseValues(ctx context.Context) (map[string]any, error) {
	cm, err := a.Client.CoreV1().ConfigMaps(a.Namespace).Get(ctx, a.baseValuesName(), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get base-values ConfigMap %s/%s: %w", a.Namespace, a.baseValuesName(), err)
	}
	raw, ok := cm.Data[baseValuesKey]
	if !ok {
		return nil, fmt.Errorf("base-values ConfigMap %s/%s has no %q key", a.Namespace, a.baseValuesName(), baseValuesKey)
	}
	var base map[string]any
	if err := json.Unmarshal([]byte(raw), &base); err != nil {
		return nil, fmt.Errorf("parse %s from ConfigMap %s/%s: %w", baseValuesKey, a.Namespace, a.baseValuesName(), err)
	}
	return base, nil
}

// verifyNames fails closed before the first write. Rendered names come from
// ReleaseName plus the chart's naming helpers; the allowlist comes from
// Fullname. They disagree when the hub's identity env is wrong for the
// release, and applying anyway would write sensor config onto whatever
// happens to carry those names.
func (a *K8sApplier) verifyNames(m *SensorManifests) error {
	allowed := a.allowedConfigMaps()
	for i := range m.ConfigMaps {
		if name := m.ConfigMaps[i].Name; !allowed[name] {
			return fmt.Errorf("refusing to apply: rendered ConfigMap %q is not in the applier's allowlist for release fullname %q — check AVURUOBS_COLLECTION_FULLNAME", name, a.Fullname)
		}
	}
	if got := m.DaemonSet.Name; got != a.daemonSetName() {
		return fmt.Errorf("refusing to apply: rendered DaemonSet %q is not in the applier's allowlist (want %q) — check AVURUOBS_COLLECTION_FULLNAME", got, a.daemonSetName())
	}
	return nil
}

// applyConfigMap writes want's data onto the live object, leaving every other
// field (labels, annotations, ownership) as Helm left it.
func (a *K8sApplier) applyConfigMap(ctx context.Context, want *corev1.ConfigMap) error {
	api := a.Client.CoreV1().ConfigMaps(a.Namespace)
	live, err := api.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// No create, by design: the control Role has no create verb (RBAC
		// cannot scope create by resourceName), and the chart renders a
		// placeholder for every disabled signal so this object always exists
		// on an install with runtime control on. Absent means the install is
		// wrong, and creating it here would paper over that with an object
		// Helm does not own.
		return fmt.Errorf("sensor ConfigMap %s/%s does not exist and the applier may not create it — the chart should have created it; re-run `helm upgrade` with collection.runtimeControl.enabled=true", a.Namespace, want.Name)
	}
	if err != nil {
		return fmt.Errorf("get sensor ConfigMap %s/%s: %w", a.Namespace, want.Name, err)
	}
	if equalStringMaps(live.Data, want.Data) {
		return nil
	}
	updated := live.DeepCopy()
	updated.Data = want.Data
	out, err := api.Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update sensor ConfigMap %s/%s: %w", a.Namespace, want.Name, err)
	}
	// Names and resource versions only: the bodies are collector config, which
	// belongs at Debug if anywhere.
	slog.Info("collection applier updated sensor ConfigMap",
		"namespace", a.Namespace, "name", out.Name, "resourceVersion", out.ResourceVersion)
	return nil
}

// patchDaemonSet reconciles the containers and volumes a signal toggle adds or
// removes, plus the overlay checksum that triggers the rollout. A narrow JSON
// patch rather than a full update: everything else on the object — status,
// updateStrategy, nodeSelector, Helm's own pod-template annotations — stays
// exactly as the last `helm upgrade` left it.
//
// Two known limits of replacing the containers array wholesale: a sidecar a
// mutating webhook injected into the live pod template is dropped on the next
// apply (it comes back when the webhook re-injects on the rollout), and
// initContainers are left alone — the kernel-preflight script's green-only
// RAPL warning therefore stays stale until the next `helm upgrade`. Neither
// affects what the sensor collects.
func (a *K8sApplier) patchDaemonSet(ctx context.Context, m *SensorManifests, checksum string) error {
	api := a.Client.AppsV1().DaemonSets(a.Namespace)
	live, err := api.Get(ctx, a.daemonSetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("sensor DaemonSet %s/%s does not exist and the applier may not create it — the chart should have created it; re-run `helm upgrade` with sensor.enabled=true", a.Namespace, a.daemonSetName())
	}
	if err != nil {
		return fmt.Errorf("get sensor DaemonSet %s/%s: %w", a.Namespace, a.daemonSetName(), err)
	}
	if live.Spec.Template.Annotations[OverlayChecksumAnnotation] == checksum {
		// Identical render: patching anyway would bump the pod template and
		// roll every sensor pod in the cluster for no change.
		//
		// The tradeoff this buys, stated plainly: the checksum describes the
		// RENDER, not the live objects, so it stays equal even when someone
		// edited a sensor ConfigMap out of band. The ConfigMap loop above has
		// already restored that drift, but WITHOUT a rollout — the running
		// collectors keep the config they started with until something else
		// restarts them or the next real overlay change moves the checksum.
		// Accepted deliberately: the alternative is hashing live state, which
		// would roll every sensor pod in the cluster on any no-op re-apply
		// (defaulting, a mutating webhook, a field manager rewrite).
		return nil
	}

	// Merge rather than replace: Helm's checksum/config and anything else on
	// the pod template must survive an overlay apply untouched.
	annotations := make(map[string]string, len(live.Spec.Template.Annotations)+1)
	for k, v := range live.Spec.Template.Annotations {
		annotations[k] = v
	}
	annotations[OverlayChecksumAnnotation] = checksum

	// `add` on an object member is defined as create-or-replace (RFC 6902
	// §4.1), so one op covers both a live pod template that already has
	// annotations and one that has none — `replace` would fail on the latter.
	patch, err := json.Marshal([]map[string]any{
		{"op": "add", "path": "/spec/template/spec/containers", "value": m.DaemonSet.Spec.Template.Spec.Containers},
		{"op": "add", "path": "/spec/template/spec/volumes", "value": m.DaemonSet.Spec.Template.Spec.Volumes},
		{"op": "add", "path": "/spec/template/metadata/annotations", "value": annotations},
	})
	if err != nil {
		return fmt.Errorf("build DaemonSet patch for %s/%s: %w", a.Namespace, a.daemonSetName(), err)
	}
	out, err := api.Patch(ctx, a.daemonSetName(), types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patch sensor DaemonSet %s/%s: %w", a.Namespace, a.daemonSetName(), err)
	}
	slog.Info("collection applier patched sensor DaemonSet",
		"namespace", a.Namespace, "name", out.Name,
		"overlayChecksum", checksum, "resourceVersion", out.ResourceVersion)
	return nil
}

// overlayChecksum hashes the parts of a render the applier actually writes.
// Two identical renders hash identically, which is what makes a repeated
// Apply a no-op instead of a cluster-wide sensor rollout. ConfigMaps arrive
// sorted by name from the renderer, so the stream is order-stable.
func overlayChecksum(m *SensorManifests) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	for i := range m.ConfigMaps {
		// Errors here can only come from the hash writer, which never fails.
		_ = enc.Encode(m.ConfigMaps[i].Name)
		_ = enc.Encode(m.ConfigMaps[i].Data)
	}
	_ = enc.Encode(m.DaemonSet.Spec.Template.Spec.Containers)
	_ = enc.Encode(m.DaemonSet.Spec.Template.Spec.Volumes)
	return hex.EncodeToString(h.Sum(nil))
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
