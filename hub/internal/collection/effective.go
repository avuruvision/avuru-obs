package collection

import (
	"fmt"
	"sync"

	sigyaml "sigs.k8s.io/yaml"
)

// Effective is the resolved base ⊕ overlay collection state — what the
// sensor actually collects after the overlay is applied. Mirrors the chart's
// gate logic: each signal is (module enabled) AND (sensor-side enabled),
// with the overlay overriding only the sensor side.
type Effective struct {
	Obi               bool     `json:"obi"`
	Logs              bool     `json:"logs"`
	Kubeletstats      bool     `json:"kubeletstats"`
	Profiler          bool     `json:"profiler"`
	Green             bool     `json:"green"`
	ExcludeNamespaces []string `json:"excludeNamespaces"`
}

// EffectiveFromValues resolves base (the install's published values subset)
// against the embedded chart's defaults, applies overlay, and reports the
// resulting collection state.
//
// The gates are transcribed from the chart's avuruobs.collect* helpers plus
// the DaemonSet's own render conditions, and every one of them is ANDed with
// sensor.enabled — no sensor pod, no collection, whatever else is set. Two
// gates are not in the collect* helpers and belong here anyway: logs and
// kubeletstats are produced BY the otel-agent container, which renders only
// when sensor.agent.enabled, and an install may legally turn that off (green
// cannot: a chart guard forbids green without the agent, so its gate is the
// helper's verbatim).
func EffectiveFromValues(base map[string]any, ov Overlay) (Effective, error) {
	defaults, err := chartDefaults()
	if err != nil {
		return Effective{}, err
	}
	// Deep copy first: base belongs to the caller (the applier caches it) and
	// coalescing writes the chart defaults into the gaps.
	vals := deepCopyMap(base)
	coalesce(vals, defaults)

	sensor := boolAt(vals, "sensor", "enabled")
	agent := sensor && boolAt(vals, "sensor", "agent", "enabled")

	eff := Effective{
		Obi: sensor && pick(ov.ObiEnabled, vals, "sensor", "obi", "enabled"),
		Logs: agent && boolAt(vals, "modules", "logs", "enabled") &&
			pick(ov.LogsEnabled, vals, "sensor", "agent", "logs", "enabled"),
		Kubeletstats: agent && boolAt(vals, "modules", "infraMetrics", "enabled") &&
			pick(ov.KubeletstatsEnabled, vals, "sensor", "agent", "kubeletstats", "enabled"),
		Profiler: sensor && boolAt(vals, "modules", "profiling", "enabled") &&
			pick(ov.ProfilerEnabled, vals, "sensor", "profiler", "enabled"),
		Green: sensor && boolAt(vals, "modules", "green", "enabled") &&
			pick(ov.GreenEnabled, vals, "sensor", "green", "enabled"),
	}

	if ov.ExcludeNamespaces != nil {
		// Same reasoning as applyOverlayToValues: an Overlay built in-process
		// never passed ParseOverlay, and this list is rendered into the UI.
		if err := validateNamespaces(ov.ExcludeNamespaces); err != nil {
			return Effective{}, err
		}
		eff.ExcludeNamespaces = append([]string{}, *ov.ExcludeNamespaces...)
	} else {
		eff.ExcludeNamespaces = stringsAt(vals, "sensor", "collection", "excludeNamespaces")
	}
	// Never nil: "collect everything" is a real setting and must serialize as
	// [] rather than JSON null, which the UI would have to special-case.
	if eff.ExcludeNamespaces == nil {
		eff.ExcludeNamespaces = []string{}
	}
	return eff, nil
}

// chartDefaults parses the embedded chart's values.yaml once. It is the same
// file the renderer coalesces against, so "effective" and "what a re-render
// would produce" cannot drift apart.
var chartDefaults = sync.OnceValues(func() (map[string]any, error) {
	raw, err := chartFS.ReadFile(chartRoot + "/values.yaml")
	if err != nil {
		return nil, fmt.Errorf("read embedded chart values.yaml: %w", err)
	}
	var vals map[string]any
	if err := sigyaml.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("parse embedded chart values.yaml: %w", err)
	}
	return vals, nil
})

// coalesce fills dst's gaps from src, recursing into subtrees present in both.
// This is chartutil.CoalesceValues' semantics for the part that matters here
// (a key the operator never set falls back to the chart default), written out
// because chartutil's variant mutates the chart it is handed. A key present in
// dst wins even when its value is nil — an explicit null deletes a default in
// Helm, and `if` on the result is false either way.
func coalesce(dst, src map[string]any) {
	for k, sv := range src {
		dv, ok := dst[k]
		if !ok {
			dst[k] = deepCopyValue(sv)
			continue
		}
		dm, dIsMap := dv.(map[string]any)
		sm, sIsMap := sv.(map[string]any)
		if dIsMap && sIsMap {
			coalesce(dm, sm)
		}
	}
}

// pick resolves one signal's sensor-side knob: the overlay when it speaks,
// otherwise the coalesced values.
func pick(override *bool, vals map[string]any, path ...string) bool {
	if override != nil {
		return *override
	}
	return boolAt(vals, path...)
}

// boolAt reads a bool at path. Anything missing, null or non-bool reads as
// false — the same answer the chart's `if` would give for a null, and the
// safe direction for a value that should not be there at all ("reported as
// collecting" is the answer that must never be wrong by accident).
func boolAt(vals map[string]any, path ...string) bool {
	cur := any(vals)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[key]
		if !ok {
			return false
		}
	}
	b, _ := cur.(bool)
	return b
}

// stringsAt reads a string list at path, skipping entries that are not
// strings. nil when the path is absent or not a list.
func stringsAt(vals map[string]any, path ...string) []string {
	cur := any(vals)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[key]
		if !ok {
			return nil
		}
	}
	list, ok := cur.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
