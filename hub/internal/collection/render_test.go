package collection

import (
	"strings"
	"testing"
)

func baseValuesFixture(t *testing.T) map[string]any {
	t.Helper()
	// Minimal install-realistic base: what the chart's base-values ConfigMap
	// holds for a DEFAULT install (see collection-rbac.yaml) — empty subtrees
	// mean "chart defaults", which the renderer coalesces from the embedded
	// values.yaml.
	return map[string]any{
		"sensor":  map[string]any{},
		"modules": map[string]any{},
		"collection": map[string]any{
			"runtimeControl": map[string]any{"enabled": true},
		},
	}
}

func TestRenderSensorManifests_Defaults(t *testing.T) {
	got, err := RenderSensorManifests(baseValuesFixture(t), Overlay{}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests: %v", err)
	}
	if got.DaemonSet == nil {
		t.Fatal("no DaemonSet rendered")
	}
	names := containerNames(got)
	if !names["obi"] || !names["otel-agent"] {
		t.Fatalf("default render missing obi/otel-agent containers: %v", names)
	}
	if names["profiler"] || names["kepler"] {
		t.Fatalf("default render should not run profiler/kepler: %v", names)
	}
	if len(got.ConfigMaps) != 4 {
		t.Fatalf("got %d ConfigMaps, want 4 (placeholders included)", len(got.ConfigMaps))
	}
	prof, ok := got.ConfigMap("avuruobs-sensor-profiler")
	if !ok {
		t.Fatal("profiler placeholder ConfigMap missing")
	}
	if !strings.Contains(prof.Data["config.yaml"], "Placeholder: signal disabled") {
		t.Fatalf("profiler ConfigMap should be a placeholder, got: %q", prof.Data["config.yaml"])
	}
}

func TestRenderSensorManifests_OverlayTogglesSignal(t *testing.T) {
	off := false
	got, err := RenderSensorManifests(baseValuesFixture(t), Overlay{LogsEnabled: &off}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests: %v", err)
	}
	agent, ok := got.ConfigMap("avuruobs-sensor-agent")
	if !ok {
		t.Fatal("agent ConfigMap missing")
	}
	// Matched on the receiver key and the pipeline, not on the bare word:
	// the always-rendered file_storage extension carries a "# filelog offset
	// checkpoints ..." comment, so a substring test for "filelog" can never
	// go green.
	if strings.Contains(agent.Data["config.yaml"], filelogReceiver) {
		t.Fatal("logs toggled off but the filelog receiver still rendered")
	}
	if strings.Contains(agent.Data["config.yaml"], filelogPipeline) {
		t.Fatal("logs toggled off but the logs pipeline still rendered")
	}
	if !containerNames(got)["otel-agent"] {
		t.Fatal("agent container should remain (kubeletstats still on)")
	}

	// Positive control: with logs on (chart default) both markers are present,
	// so the assertions above are testing the toggle and not a typo.
	on, err := RenderSensorManifests(baseValuesFixture(t), Overlay{}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests (control): %v", err)
	}
	onAgent, _ := on.ConfigMap("avuruobs-sensor-agent")
	if !strings.Contains(onAgent.Data["config.yaml"], filelogReceiver) ||
		!strings.Contains(onAgent.Data["config.yaml"], filelogPipeline) {
		t.Fatalf("control render should carry the filelog receiver + pipeline:\n%s", onAgent.Data["config.yaml"])
	}
}

// Literal shapes emitted by chart/templates/sensor-config.yaml once the
// block scalar's indentation is stripped by the YAML decode.
const (
	filelogReceiver = "\n  filelog:"
	filelogPipeline = "receivers: [filelog]"
)

func TestRenderSensorManifests_OverlayExcludeNamespaces(t *testing.T) {
	ns := []string{"payments"}
	got, err := RenderSensorManifests(baseValuesFixture(t), Overlay{ExcludeNamespaces: &ns}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests: %v", err)
	}
	obi, _ := got.ConfigMap("avuruobs-sensor-obi")
	if !strings.Contains(obi.Data["obi-config.yml"], `k8s_namespace: "payments"`) {
		t.Fatalf("obi exclude_instrument missing payments namespace:\n%s", obi.Data["obi-config.yml"])
	}
	agent, _ := got.ConfigMap("avuruobs-sensor-agent")
	if !strings.Contains(agent.Data["config.yaml"], "payments") {
		t.Fatal("agent filelog/filter should reference the excluded namespace")
	}
}

func TestRenderSensorManifests_OverlayEnablesProfiler(t *testing.T) {
	on := true
	base := baseValuesFixture(t)
	base["modules"] = map[string]any{"profiling": map[string]any{"enabled": true}}
	got, err := RenderSensorManifests(base, Overlay{ProfilerEnabled: &on}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests: %v", err)
	}
	if !containerNames(got)["profiler"] {
		t.Fatal("profiler toggled on but container not rendered")
	}
	prof, _ := got.ConfigMap("avuruobs-sensor-profiler")
	if strings.Contains(prof.Data["config.yaml"], "Placeholder") {
		t.Fatal("profiler ConfigMap still a placeholder after enabling")
	}
}

func TestRenderSensorManifests_ModuleGateWins(t *testing.T) {
	on := true
	base := baseValuesFixture(t)
	base["modules"] = map[string]any{"profiling": map[string]any{"enabled": false}}
	got, err := RenderSensorManifests(base, Overlay{ProfilerEnabled: &on}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests: %v", err)
	}
	if containerNames(got)["profiler"] {
		t.Fatal("sensor toggle must not override a chart-disabled module")
	}
}

// Green is the widest render: four ConfigMaps with real bodies, the kepler and
// tdp-estimator containers, and the largest multi-document output — the case
// most likely to trip the document splitter or the decode.
func TestRenderSensorManifests_GreenRendersEveryDocument(t *testing.T) {
	on := true
	base := baseValuesFixture(t)
	base["modules"] = map[string]any{"green": map[string]any{"enabled": true}}
	base["sensor"] = map[string]any{
		"green": map[string]any{"estimation": map[string]any{"enabled": true}},
	}
	got, err := RenderSensorManifests(base, Overlay{GreenEnabled: &on}, "avuruobs", "avuruobs")
	if err != nil {
		t.Fatalf("RenderSensorManifests: %v", err)
	}
	names := containerNames(got)
	for _, want := range []string{"obi", "otel-agent", "kepler", "tdp-estimator"} {
		if !names[want] {
			t.Fatalf("green render missing the %s container: %v", want, names)
		}
	}
	kepler, ok := got.ConfigMap("avuruobs-sensor-kepler")
	if !ok {
		t.Fatal("kepler ConfigMap missing")
	}
	if strings.Contains(kepler.Data["config.yaml"], "Placeholder") {
		t.Fatalf("kepler ConfigMap still a placeholder after enabling green:\n%s", kepler.Data["config.yaml"])
	}
	if !strings.Contains(kepler.Data["config.yaml"], "127.0.0.1:28282") {
		t.Fatalf("kepler config missing the loopback listen address:\n%s", kepler.Data["config.yaml"])
	}
}

// The chart's own `fail` guards are the install-time contract; a hub-side
// re-render must reproduce them verbatim rather than rendering something the
// operator could never have installed.
func TestRenderSensorManifests_ChartGuardSurfaces(t *testing.T) {
	base := baseValuesFixture(t)
	base["modules"] = map[string]any{
		"green":        map[string]any{"enabled": true},
		"infraMetrics": map[string]any{"enabled": false},
	}
	_, err := RenderSensorManifests(base, Overlay{}, "avuruobs", "avuruobs")
	if err == nil {
		t.Fatal("green without infra-metrics rendered instead of failing")
	}
	if !strings.Contains(err.Error(), "modules.green.enabled requires modules.infraMetrics.enabled") {
		t.Fatalf("chart guard message not surfaced: %v", err)
	}
}

// The base values come from a ConfigMap an operator can edit; a wrong type
// there must be an error, not a panic in the hub's request path.
func TestRenderSensorManifests_BadBaseValuesType(t *testing.T) {
	base := baseValuesFixture(t)
	base["sensor"] = "not-a-map"
	got, err := RenderSensorManifests(base, Overlay{}, "avuruobs", "avuruobs")
	if err == nil {
		t.Fatalf("string in place of the sensor subtree rendered clean: %+v", got)
	}
}

// Overlay{} built directly, bypassing ParseOverlay: proves the renderer's own
// namespace re-validation is live and not dead defensive code.
func TestRenderSensorManifests_InvalidOverlayNamespace(t *testing.T) {
	ns := []string{"Bad_NS!"}
	_, err := RenderSensorManifests(baseValuesFixture(t), Overlay{ExcludeNamespaces: &ns}, "avuruobs", "avuruobs")
	if err == nil {
		t.Fatal("invalid namespace name reached the renderer without an error")
	}
	if !strings.Contains(err.Error(), "excludeNamespaces") {
		t.Fatalf("error does not name the offending field: %v", err)
	}
}

// The applier caches base values and renders repeatedly from them; aliasing a
// slice or a nested map into the copy would let one render corrupt the next.
func TestDeepCopyDoesNotShareSlices(t *testing.T) {
	base := baseValuesFixture(t)
	base["sensor"] = map[string]any{
		"collection": map[string]any{
			"excludeNamespaces": []any{"a", "b"},
		},
		"extra": []any{map[string]any{"k": "original"}},
	}

	off := false
	// An overlay that touches neither path, so anything shared would be
	// shared by the copy itself and not by an overlay write.
	got, err := applyOverlayToValues(base, Overlay{ObiEnabled: &off})
	if err != nil {
		t.Fatalf("applyOverlayToValues: %v", err)
	}

	copied := got["sensor"].(map[string]any)
	copied["collection"].(map[string]any)["excludeNamespaces"].([]any)[0] = "mutated"
	copied["extra"].([]any)[0].(map[string]any)["k"] = "mutated"

	orig := base["sensor"].(map[string]any)
	if v := orig["collection"].(map[string]any)["excludeNamespaces"].([]any)[0]; v != "a" {
		t.Fatalf("base slice element aliased into the copy: got %v", v)
	}
	if v := orig["extra"].([]any)[0].(map[string]any)["k"]; v != "original" {
		t.Fatalf("map nested inside a base slice aliased into the copy: got %v", v)
	}
}

func containerNames(m *SensorManifests) map[string]bool {
	names := map[string]bool{}
	if m.DaemonSet == nil {
		return names
	}
	for _, c := range m.DaemonSet.Spec.Template.Spec.Containers {
		names[c.Name] = true
	}
	return names
}
