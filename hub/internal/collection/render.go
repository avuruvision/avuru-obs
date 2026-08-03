package collection

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	sigyaml "sigs.k8s.io/yaml"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// The all: prefix is load-bearing — without it, go:embed silently EXCLUDES
// underscore-prefixed files, i.e. templates/_helpers.tpl, and every sensor
// template calls helpers defined there.
//
//go:embed chart/Chart.yaml chart/values.yaml all:chart/templates
var chartFS embed.FS

// chartRoot is the embed prefix stripped so helm sees a chart rooted at
// Chart.yaml (loader.LoadFiles takes chart-relative names).
const chartRoot = "chart"

// sensorTemplates are the rendered files this package cares about. Anything
// else the chart copy may grow is ignored rather than parsed — the applier
// only ever touches the sensor's own objects.
var sensorTemplates = []string{"sensor-config.yaml", "sensor-daemonset.yaml"}

// SensorManifests is the rendered sensor surface for one set of values: the
// per-signal ConfigMaps and the DaemonSet that mounts them. It is exactly the
// set of objects the applier's Role may touch (see the chart's
// collection-rbac.yaml) — nothing here is a resource the hub could create.
type SensorManifests struct {
	// ConfigMaps is sorted by name so a render is byte-stable: the applier
	// hashes it to decide whether anything actually changed.
	ConfigMaps []corev1.ConfigMap
	// DaemonSet is nil when the values render no sensor pod at all (every
	// signal off). Callers decide what that means — this package does not
	// treat it as an error.
	DaemonSet *appsv1.DaemonSet
}

// ConfigMap returns the rendered ConfigMap with the given (fully-qualified)
// name.
func (m *SensorManifests) ConfigMap(name string) (*corev1.ConfigMap, bool) {
	for i := range m.ConfigMaps {
		if m.ConfigMaps[i].Name == name {
			return &m.ConfigMaps[i], true
		}
	}
	return nil, false
}

// RenderSensorManifests re-renders the chart's sensor templates server-side
// with overlay applied on top of baseValues.
//
// baseValues is the curated, non-secret values subset the chart publishes in
// its <release>-collection-base-values ConfigMap; empty or absent subtrees are
// coalesced against the embedded chart's own values.yaml, exactly as
// `helm template` would. releaseName and namespace must match the live release
// or the rendered object names won't match the ones the applier may update.
func RenderSensorManifests(baseValues map[string]any, overlay Overlay, releaseName, namespace string) (*SensorManifests, error) {
	files, err := chartFiles()
	if err != nil {
		return nil, err
	}
	ch, err := loader.LoadFiles(files)
	if err != nil {
		return nil, fmt.Errorf("load embedded chart: %w", err)
	}

	vals, err := applyOverlayToValues(baseValues, overlay)
	if err != nil {
		return nil, err
	}

	renderVals, err := chartutil.ToRenderValues(ch, vals, chartutil.ReleaseOptions{
		Name:      releaseName,
		Namespace: namespace,
		IsInstall: true,
	}, chartutil.DefaultCapabilities.Copy())
	if err != nil {
		return nil, fmt.Errorf("compose render values: %w", err)
	}

	// Zero-value Engine: non-strict (byte-for-byte parity with what
	// `helm template` / `helm upgrade` produced at install time, which is the
	// whole point of re-rendering the same chart) and with no Kubernetes
	// client, so `lookup` is unavailable. The sensor templates use no lookup.
	rendered, err := engine.Engine{}.Render(ch, renderVals)
	if err != nil {
		// Chart `fail` guards land here (e.g. green without infra-metrics).
		// Surfacing the message verbatim is the point: it is the same
		// diagnostic an operator would get from `helm upgrade`.
		return nil, fmt.Errorf("render sensor templates: %w", err)
	}

	out := &SensorManifests{}
	for name, content := range rendered {
		if !isSensorTemplate(name) {
			continue
		}
		if err := out.collect(name, content); err != nil {
			return nil, err
		}
	}
	sort.Slice(out.ConfigMaps, func(i, j int) bool {
		return out.ConfigMaps[i].Name < out.ConfigMaps[j].Name
	})
	return out, nil
}

// isSensorTemplate matches on the base name so it is independent of the chart
// name helm prefixes onto rendered keys (`avuruobs/templates/...`).
func isSensorTemplate(rendered string) bool {
	base := path.Base(rendered)
	for _, t := range sensorTemplates {
		if base == t {
			return true
		}
	}
	return false
}

// collect decodes one rendered template file, which may hold several YAML
// documents (sensor-config.yaml renders up to four ConfigMaps).
func (m *SensorManifests) collect(name, content string) error {
	for _, doc := range splitYAMLDocs(content) {
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := sigyaml.Unmarshal([]byte(doc), &probe); err != nil {
			return fmt.Errorf("parse rendered %s: %w", name, err)
		}
		switch probe.Kind {
		case "ConfigMap":
			var cm corev1.ConfigMap
			if err := sigyaml.Unmarshal([]byte(doc), &cm); err != nil {
				return fmt.Errorf("decode ConfigMap from %s: %w", name, err)
			}
			m.ConfigMaps = append(m.ConfigMaps, cm)
		case "DaemonSet":
			var ds appsv1.DaemonSet
			if err := sigyaml.Unmarshal([]byte(doc), &ds); err != nil {
				return fmt.Errorf("decode DaemonSet from %s: %w", name, err)
			}
			m.DaemonSet = &ds
		case "":
			// A comment-only or whitespace-only document.
		default:
			return fmt.Errorf("unexpected kind %q in rendered %s", probe.Kind, name)
		}
	}
	return nil
}

// splitYAMLDocs splits on document separators that sit alone at column 0,
// which is what the sensor templates emit. Matching the whole line (rather
// than a `\n---` prefix) keeps a `- --flag=value` container arg from ever
// being read as a separator. Documents with no content are dropped.
func splitYAMLDocs(content string) []string {
	var docs []string
	var cur []string
	flush := func() {
		doc := strings.Join(cur, "\n")
		if strings.TrimSpace(doc) != "" {
			docs = append(docs, doc)
		}
		cur = nil
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimRight(line, " \t\r") == "---" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return docs
}

// chartFiles reads the embedded chart into the in-memory form helm's loader
// wants, stripping the `chart/` embed prefix so names are chart-relative
// (Chart.yaml, values.yaml, templates/...).
func chartFiles() ([]*loader.BufferedFile, error) {
	var files []*loader.BufferedFile
	err := fs.WalkDir(chartFS, chartRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := chartFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, chartRoot), "/")
		files = append(files, &loader.BufferedFile{Name: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded chart: %w", err)
	}
	// Deterministic order: LoadFiles is order-insensitive, but a stable list
	// keeps failures reproducible.
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// applyOverlayToValues deep-copies baseValues and writes the overlay's set
// fields onto the value keys the chart's own gates read. The mapping is
// deliberately one-way and narrow: each toggle drives the SENSOR-side knob
// only, never the module switch above it, so a module disabled at install
// time cannot be widened at runtime (avuruobs.collectLogs and friends AND the
// two together — see _helpers.tpl). A nil pointer means "not overridden" and
// leaves the base value — and therefore the chart default — untouched.
func applyOverlayToValues(baseValues map[string]any, overlay Overlay) (map[string]any, error) {
	vals := deepCopyMap(baseValues)

	if overlay.ObiEnabled != nil {
		setNested(vals, *overlay.ObiEnabled, "sensor", "obi", "enabled")
	}
	if overlay.LogsEnabled != nil {
		setNested(vals, *overlay.LogsEnabled, "sensor", "agent", "logs", "enabled")
	}
	if overlay.KubeletstatsEnabled != nil {
		setNested(vals, *overlay.KubeletstatsEnabled, "sensor", "agent", "kubeletstats", "enabled")
	}
	if overlay.ProfilerEnabled != nil {
		setNested(vals, *overlay.ProfilerEnabled, "sensor", "profiler", "enabled")
	}
	if overlay.GreenEnabled != nil {
		setNested(vals, *overlay.GreenEnabled, "sensor", "green", "enabled")
	}
	if overlay.ExcludeNamespaces != nil {
		// Namespace names are validated at the API boundary (ParseOverlay);
		// they are rendered into collector config, so nothing unvalidated may
		// reach here.
		if err := validateNamespaces(overlay.ExcludeNamespaces); err != nil {
			return nil, err
		}
		list := make([]any, 0, len(*overlay.ExcludeNamespaces))
		for _, ns := range *overlay.ExcludeNamespaces {
			list = append(list, ns)
		}
		setNested(vals, list, "sensor", "collection", "excludeNamespaces")
	}
	return vals, nil
}

// setNested writes value at path, creating intermediate maps as needed. A
// non-map value sitting where a map is needed is replaced: the overlay's
// schema is the authority on the shape of these keys.
func setNested(m map[string]any, value any, path ...string) {
	cur := m
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}

// deepCopyMap copies the maps and slices a values tree is made of, so
// applying an overlay never mutates the caller's base values (which the
// applier caches and reuses across renders). Leaf scalars are immutable and
// shared by value.
func deepCopyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyValue(v)
	}
	return dst
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}
