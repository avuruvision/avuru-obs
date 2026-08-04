package collection

import (
	"reflect"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// Chart defaults with an empty base: what a fresh install collects. Pinned
// here so a values.yaml change that flips a signal on or off shows up as a
// failing hub test, not as a surprise in the UI.
func TestEffectiveFromValues_Defaults(t *testing.T) {
	eff, err := EffectiveFromValues(map[string]any{}, Overlay{})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if !eff.Obi || !eff.Logs || !eff.Kubeletstats {
		t.Errorf("default signals off: %+v", eff)
	}
	if eff.Profiler || eff.Green {
		t.Errorf("opt-in signals on by default: %+v", eff)
	}
	want := []string{"kube-system", "kube-node-lease", "kube-public"}
	if !reflect.DeepEqual(eff.ExcludeNamespaces, want) {
		t.Errorf("ExcludeNamespaces = %v, want %v", eff.ExcludeNamespaces, want)
	}
}

// The overlay is what the UI writes, so where it speaks it wins over the
// install's values.
func TestEffectiveFromValues_OverlayWins(t *testing.T) {
	eff, err := EffectiveFromValues(map[string]any{}, Overlay{
		LogsEnabled:       boolPtr(false),
		ExcludeNamespaces: &[]string{"payments", "kube-system"},
	})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Logs {
		t.Error("overlay turned logs off but Effective still reports them on")
	}
	if !eff.Obi || !eff.Kubeletstats {
		t.Errorf("an overlay must not disturb the signals it does not mention: %+v", eff)
	}
	if want := []string{"payments", "kube-system"}; !reflect.DeepEqual(eff.ExcludeNamespaces, want) {
		t.Errorf("ExcludeNamespaces = %v, want %v", eff.ExcludeNamespaces, want)
	}
}

// The one-way rule from the design doc: an overlay drives the sensor-side knob
// only, so a module switched off at install time can never be widened back on
// at runtime. Effective has to report that honestly or the UI would show a
// signal as collecting when nothing collects it.
func TestEffectiveFromValues_ModuleGateWins(t *testing.T) {
	base := map[string]any{
		"modules": map[string]any{"profiling": map[string]any{"enabled": false}},
	}
	eff, err := EffectiveFromValues(base, Overlay{ProfilerEnabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Profiler {
		t.Error("overlay re-enabled a signal whose module is off at install time")
	}
}

// sensor.enabled is the outermost gate: with no sensor pod nothing is
// collected, whatever the modules and the overlay say.
func TestEffectiveFromValues_SensorOffDisablesEverything(t *testing.T) {
	base := map[string]any{"sensor": map[string]any{"enabled": false}}
	eff, err := EffectiveFromValues(base, Overlay{
		ObiEnabled:      boolPtr(true),
		LogsEnabled:     boolPtr(true),
		ProfilerEnabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Obi || eff.Logs || eff.Kubeletstats || eff.Profiler || eff.Green {
		t.Errorf("sensor disabled but signals report as collecting: %+v", eff)
	}
}

// Logs and pod metrics are collected BY the otel-agent container, which the
// chart renders only when sensor.agent.enabled. A legal install (no guard
// forbids it) that turns the agent off collects neither.
func TestEffectiveFromValues_AgentOffDisablesAgentSignals(t *testing.T) {
	base := map[string]any{
		"sensor": map[string]any{"agent": map[string]any{"enabled": false}},
	}
	eff, err := EffectiveFromValues(base, Overlay{LogsEnabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Logs || eff.Kubeletstats {
		t.Errorf("agent container off but its signals report as collecting: %+v", eff)
	}
	if !eff.Obi {
		t.Error("the agent gate leaked onto OBI, which is its own container")
	}
}

// Green needs both halves on; the module alone is not collection.
func TestEffectiveFromValues_GreenNeedsBothGates(t *testing.T) {
	base := map[string]any{
		"modules": map[string]any{"green": map[string]any{"enabled": true}},
	}
	eff, err := EffectiveFromValues(base, Overlay{})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Green {
		t.Error("green module on but the sensor half off — nothing collects energy")
	}
	on, err := EffectiveFromValues(base, Overlay{GreenEnabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if !on.Green {
		t.Error("both green gates on but Effective reports it off")
	}
}

// "Collect everything" is a real setting, and it must serialize as [] rather
// than JSON null — the UI renders this list directly.
func TestEffectiveFromValues_ExcludeNamespacesNeverNil(t *testing.T) {
	eff, err := EffectiveFromValues(map[string]any{}, Overlay{ExcludeNamespaces: &[]string{}})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.ExcludeNamespaces == nil {
		t.Fatal("an empty exclude list must be [], not nil")
	}
	if len(eff.ExcludeNamespaces) != 0 {
		t.Errorf("ExcludeNamespaces = %v, want empty", eff.ExcludeNamespaces)
	}
}

// The base values the applier reads are attacker-adjacent only in theory, but
// the namespace list still ends up in the UI, so a malformed overlay is
// refused here exactly as it is at the API boundary.
func TestEffectiveFromValues_RejectsInvalidNamespaces(t *testing.T) {
	_, err := EffectiveFromValues(map[string]any{}, Overlay{ExcludeNamespaces: &[]string{"Not A Namespace"}})
	if err == nil {
		t.Fatal("an invalid namespace name was accepted")
	}
}

// EffectiveFromValues must not write into the caller's values map: the applier
// holds base values it re-renders from.
func TestEffectiveFromValues_DoesNotMutateBase(t *testing.T) {
	base := map[string]any{"sensor": map[string]any{"obi": map[string]any{"enabled": true}}}
	if _, err := EffectiveFromValues(base, Overlay{ObiEnabled: boolPtr(false)}); err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	sensor, _ := base["sensor"].(map[string]any)
	obi, _ := sensor["obi"].(map[string]any)
	if len(sensor) != 1 || len(obi) != 1 || obi["enabled"] != true {
		t.Fatalf("base values were mutated: %+v", base)
	}
}
