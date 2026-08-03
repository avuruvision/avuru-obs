package collection

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"k8s.io/client-go/kubernetes/fake"
)

const testNamespace = "avuruobs"

// seedCluster builds the cluster state a chart install with runtime control on
// leaves behind: the base-values ConfigMap, all four sensor ConfigMaps
// (placeholders for the signals that are off) and the sensor DaemonSet with
// Helm's own checksum annotation already on the pod template.
func seedCluster(t *testing.T, dsAnnotations map[string]string) *fake.Clientset {
	t.Helper()
	return fake.NewSimpleClientset(
		configMap("avuruobs-collection-base-values", map[string]string{
			"values.json": `{"sensor":{},"modules":{},"collection":{"runtimeControl":{"enabled":true}}}`,
		}),
		configMap("avuruobs-sensor-obi", map[string]string{"obi-config.yml": "stale"}),
		configMap("avuruobs-sensor-agent", map[string]string{"config.yaml": "stale"}),
		configMap("avuruobs-sensor-profiler", map[string]string{"config.yaml": "# Placeholder: signal disabled"}),
		configMap("avuruobs-sensor-kepler", map[string]string{"config.yaml": "# Placeholder: signal disabled"}),
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "avuruobs-sensor", Namespace: testNamespace},
			Spec: appsv1.DaemonSetSpec{
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Annotations: dsAnnotations},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "obi"}, {Name: "otel-agent"}},
						Volumes:    []corev1.Volume{{Name: "obi-config"}},
					},
				},
			},
		},
	)
}

func configMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Data:       data,
	}
}

func testApplier(client *fake.Clientset, fullname string) *K8sApplier {
	return &K8sApplier{
		Client:      client,
		Namespace:   testNamespace,
		ReleaseName: "avuruobs",
		Fullname:    fullname,
	}
}

// countWrites installs reactors that tally mutating calls without consuming
// them, so a test can assert "this Apply wrote nothing" rather than inferring
// it from unchanged objects.
func countWrites(client *fake.Clientset) (updates, patches, creates *int) {
	var u, p, c int
	tally := func(n *int) k8stesting.ReactionFunc {
		return func(k8stesting.Action) (bool, runtime.Object, error) {
			*n++
			return false, nil, nil
		}
	}
	client.PrependReactor("update", "*", tally(&u))
	client.PrependReactor("patch", "*", tally(&p))
	client.PrependReactor("create", "*", tally(&c))
	return &u, &p, &c
}

func liveDaemonSet(t *testing.T, client *fake.Clientset) *appsv1.DaemonSet {
	t.Helper()
	ds, err := client.AppsV1().DaemonSets(testNamespace).Get(context.Background(), "avuruobs-sensor", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get DaemonSet: %v", err)
	}
	return ds
}

func liveConfigMap(t *testing.T, client *fake.Clientset, name string) *corev1.ConfigMap {
	t.Helper()
	cm, err := client.CoreV1().ConfigMaps(testNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap %s: %v", name, err)
	}
	return cm
}

func TestK8sApplier_UpdatesConfigMapsAndPatchesDaemonSet(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})

	if err := testApplier(client, "avuruobs").Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	obi := liveConfigMap(t, client, "avuruobs-sensor-obi")
	if obi.Data["obi-config.yml"] == "stale" {
		t.Fatal("obi ConfigMap not reconciled")
	}
	if !strings.Contains(obi.Data["obi-config.yml"], "otel_ebpf") &&
		!strings.Contains(obi.Data["obi-config.yml"], "discovery") {
		t.Fatalf("obi ConfigMap does not look like a rendered OBI config:\n%s", obi.Data["obi-config.yml"])
	}

	ds := liveDaemonSet(t, client)
	annotations := ds.Spec.Template.Annotations
	if annotations[OverlayChecksumAnnotation] == "" {
		t.Fatalf("overlay checksum annotation not set on the pod template: %v", annotations)
	}
	// Helm owns checksum/config; clobbering it would make the next
	// `helm upgrade` see drift it did not cause.
	if annotations["checksum/config"] != "helm-owned" {
		t.Fatalf("Helm's checksum/config annotation was overwritten: %v", annotations)
	}
	names := map[string]bool{}
	for _, c := range ds.Spec.Template.Spec.Containers {
		names[c.Name] = true
	}
	if !names["otel-agent"] {
		t.Fatalf("patched DaemonSet lost the otel-agent container: %v", names)
	}
}

// A pod template with no annotations at all: a JSON-patch `replace` on a
// missing path is an error, so the patch must not assume the map exists.
func TestK8sApplier_PatchesDaemonSetWithoutAnnotations(t *testing.T) {
	client := seedCluster(t, nil)

	if err := testApplier(client, "avuruobs").Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := liveDaemonSet(t, client).Spec.Template.Annotations[OverlayChecksumAnnotation]; got == "" {
		t.Fatal("overlay checksum annotation not set on an annotation-free pod template")
	}
}

func TestK8sApplier_Idempotent(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})
	applier := testApplier(client, "avuruobs")

	if err := applier.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	updates, patches, _ := countWrites(client)
	if err := applier.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if *updates != 0 || *patches != 0 {
		t.Fatalf("re-applying an unchanged overlay wrote: %d updates, %d patches", *updates, *patches)
	}
}

// TestK8sApplier_DriftRestoreSkipsRollout pins the documented half-measure in
// patchDaemonSet: an out-of-band edit to a sensor ConfigMap IS restored on the
// next apply, but does NOT trigger a rollout, because the checksum describes
// the render and the render did not change. The running collectors therefore
// keep their startup config until something else restarts them. Asserted
// rather than left implicit — it is the behavior an operator will hit after a
// `kubectl edit configmap`, and a future change that starts hashing live state
// (rolling every sensor pod on any no-op) should have to break this test.
func TestK8sApplier_DriftRestoreSkipsRollout(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})
	applier := testApplier(client, "avuruobs")

	if err := applier.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	reconciled := liveConfigMap(t, client, "avuruobs-sensor-obi").Data["obi-config.yml"]
	checksum := liveDaemonSet(t, client).Spec.Template.Annotations[OverlayChecksumAnnotation]
	if reconciled == "" || checksum == "" {
		t.Fatalf("first Apply did not reconcile: config %q, checksum %q", reconciled, checksum)
	}

	// Out-of-band drift: someone edits the collector config by hand.
	drifted := liveConfigMap(t, client, "avuruobs-sensor-obi").DeepCopy()
	drifted.Data["obi-config.yml"] = "# hand-edited\n"
	if _, err := client.CoreV1().ConfigMaps(testNamespace).Update(
		context.Background(), drifted, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("simulate drift: %v", err)
	}

	// Count DaemonSet patches only: the ConfigMap restore is an update on
	// configmaps and must not be conflated with a pod-template rollout.
	var dsPatches int
	client.PrependReactor("patch", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		dsPatches++
		return false, nil, nil
	})

	if err := applier.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if got := liveConfigMap(t, client, "avuruobs-sensor-obi").Data["obi-config.yml"]; got != reconciled {
		t.Errorf("drifted ConfigMap not restored:\n got %q\nwant %q", got, reconciled)
	}
	if got := liveDaemonSet(t, client).Spec.Template.Annotations[OverlayChecksumAnnotation]; got != checksum {
		t.Errorf("overlay checksum changed on a drift restore: got %q, want %q", got, checksum)
	}
	if dsPatches != 0 {
		t.Errorf("a drift restore rolled the sensor pods: %d DaemonSet patch(es)", dsPatches)
	}
}

func TestK8sApplier_RefusesUnexpectedNames(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})
	updates, patches, creates := countWrites(client)

	// Rendered names follow ReleaseName; the allowlist follows Fullname. A
	// wrong release name renders `other-release-avuruobs-sensor-*`, which the
	// control Role never named — refuse before the first write rather than
	// die halfway through on an RBAC denial.
	applier := testApplier(client, "avuruobs")
	applier.ReleaseName = "other-release"

	err := applier.Apply(context.Background(), Overlay{})
	if err == nil {
		t.Fatal("applier wrote objects outside its allowlist")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("error does not explain the allowlist refusal: %v", err)
	}
	if *updates != 0 || *patches != 0 || *creates != 0 {
		t.Fatalf("a refused apply still wrote: %d updates, %d patches, %d creates", *updates, *patches, *creates)
	}
}

// The other half of the identity guard: a wrong fullname cannot even find the
// base-values ConfigMap, so it must fail there rather than render from chart
// defaults and revert the operator's install.
func TestK8sApplier_RefusesUnknownFullname(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})
	updates, patches, creates := countWrites(client)

	err := testApplier(client, "other-release").Apply(context.Background(), Overlay{})
	if err == nil {
		t.Fatal("applier rendered without the release's base values")
	}
	if !strings.Contains(err.Error(), "other-release-collection-base-values") {
		t.Fatalf("error does not name the ConfigMap it could not read: %v", err)
	}
	if *updates != 0 || *patches != 0 || *creates != 0 {
		t.Fatalf("a refused apply still wrote: %d updates, %d patches, %d creates", *updates, *patches, *creates)
	}
}

func TestK8sApplier_MissingConfigMapFailsClosed(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})
	if err := client.CoreV1().ConfigMaps(testNamespace).Delete(
		context.Background(), "avuruobs-sensor-profiler", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete profiler ConfigMap: %v", err)
	}
	_, _, creates := countWrites(client)

	err := testApplier(client, "avuruobs").Apply(context.Background(), Overlay{})
	if err == nil {
		t.Fatal("apply succeeded with a sensor ConfigMap missing")
	}
	if !strings.Contains(err.Error(), "avuruobs-sensor-profiler") {
		t.Fatalf("error does not name the missing ConfigMap: %v", err)
	}
	if !strings.Contains(err.Error(), "chart") {
		t.Fatalf("error does not point at the chart as the owner that should have created it: %v", err)
	}
	// The Role has no create verb by design; attempting one would fail in a
	// real cluster with a confusing RBAC error instead of this one.
	if *creates != 0 {
		t.Fatalf("applier attempted %d create(s) for a missing ConfigMap", *creates)
	}
}

func TestK8sApplier_SignalToggleChangesContainers(t *testing.T) {
	client := seedCluster(t, map[string]string{"checksum/config": "helm-owned"})
	applier := testApplier(client, "avuruobs")

	if err := applier.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("baseline Apply: %v", err)
	}
	baseline := liveDaemonSet(t, client).Spec.Template.Annotations[OverlayChecksumAnnotation]

	off := false
	if err := applier.Apply(context.Background(), Overlay{ObiEnabled: &off}); err != nil {
		t.Fatalf("Apply with obi off: %v", err)
	}

	ds := liveDaemonSet(t, client)
	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == "obi" {
			t.Fatal("obi toggled off but its container is still in the DaemonSet")
		}
	}
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "obi-config" {
			t.Fatal("obi toggled off but its config volume is still mounted")
		}
	}
	if got := ds.Spec.Template.Annotations[OverlayChecksumAnnotation]; got == baseline {
		t.Fatal("overlay checksum unchanged after a signal toggle — no rollout would be triggered")
	}
	if ds.Spec.Template.Annotations["checksum/config"] != "helm-owned" {
		t.Fatal("Helm's checksum/config annotation was overwritten by the second apply")
	}
}
