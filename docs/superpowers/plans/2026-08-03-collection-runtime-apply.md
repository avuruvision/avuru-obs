# Collection Runtime Control — Real Applier + Writable UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the collection overlay actually take effect on the cluster: a real `collection.Applier` that renders the sensor manifests from the chart's own templates (embedded in the hub binary) with base values ⊕ overlay, updates the four sensor ConfigMaps, and patches the sensor DaemonSet (containers, volumes, and a hub-owned `avuru.obs/overlay-checksum` annotation) — plus the Settings → Collection writable card. Completes design/2026-07-27-collection-control-plane.md.

**Architecture:** v0.3 shipped the overlay store (`0014`), closed-schema validation (`hub/internal/collection`), `GET/PUT/DELETE /api/v1/collection/overlay`, the `collection.runtimeControl.enabled` flag, and the `-collection-control` SA/Role/RoleBinding + `-collection-base-values` ConfigMap. This plan adds: (1) a committed, make-synced copy of the sensor chart templates under `hub/internal/collection/chart/` rendered via `helm.sh/helm/v3/pkg/engine`; (2) `K8sApplier` (client-go) replacing `NoopApplier` when running in-cluster with the flag on; (3) chart changes so disabled-signal ConfigMaps render as placeholders when runtime control is on (RBAC stays `create`-free — Kubernetes cannot scope `create` by resourceName); (4) `effective` config in GET; (5) the writable UI card; (6) kind e2e coverage.

**Tech Stack:** Go 1.26 (`hub/`), `k8s.io/client-go` + `helm.sh/helm/v3` (new deps), Helm chart (`deploy/helm/avuruobs`), Next.js 16 static export + TanStack Query 5 + daisyUI (`ui/`), Playwright, kind (`e2e/`, tag `e2ehelm`).

**Read first:** `design/2026-07-27-collection-control-plane.md` (AEP), `docs/superpowers/plans/2026-07-31-collection-overlay-storage-api.md` (what v0.3 shipped, incl. the Applier seam).

**Key current-state facts (verified 2026-08-03, HEAD c1b561a):**
- Overlay schema: `hub/internal/collection/overlay.go:31-38` — 5 `*bool` (`ObiEnabled`, `LogsEnabled`, `KubeletstatsEnabled`, `ProfilerEnabled`, `GreenEnabled`) + `ExcludeNamespaces *[]string`; `ParseOverlay`/`Encode`/`Empty`.
- Applier seam: `hub/internal/collection/applier.go:13-18` (`Apply(ctx, Overlay) error`), `NoopApplier` logs a warning. Held at `router.go:95`, called from `hub/internal/api/collection.go:89` (PUT) and `:116` (DELETE); apply failure → 502 with "overlay saved but applying it failed".
- Hub has **no k8s deps** (`hub/go.mod`), does **not** know its namespace, release name, or fullname (no downward API, no such env vars anywhere).
- Sensor ConfigMaps all render from ONE file `deploy/helm/avuruobs/templates/sensor-config.yaml` (458 lines): `-sensor-obi` (gate `sensor.enabled && sensor.obi.enabled`, line 22), `-sensor-agent` (gate `$agentActive` = agent.enabled && (collectLogs||collectInfraMetrics||collectGreen), line 116), `-sensor-profiler` (gate collectProfiles, line 369), `-sensor-kepler` (gate collectGreen, line 421). Data keys: `obi-config.yml` for obi, `config.yaml` for the other three.
- Signal toggles also gate DaemonSet **containers and volumes** (`sensor-daemonset.yaml`: obi :110, agent :165, profiler :220, kepler :259, tdp-estimator :317; volumes :370-418). Helm's own `checksum/config` annotation is at `sensor-daemonset.yaml:25-26` and must never be touched by the hub.
- The Role (`deploy/helm/avuruobs/templates/collection-rbac.yaml:58-79`) grants get/update/patch on the 5 named ConfigMaps and get/patch on the named DaemonSet. `template-test.sh:504-534` asserts no `create`/`delete`/wildcard.
- Base-values ConfigMap (`collection-rbac.yaml:37-56`): key `values.json`, curated dict `{sensor, modules, image:{registry,pullPolicy}, auth:{ingest:{mode,provisionSensorKey}}, nameOverride, fullnameOverride}`.
- Overlay→values mapping (from `_helpers.tpl` gates): `obiEnabled→sensor.obi.enabled`, `logsEnabled→sensor.agent.logs.enabled`, `kubeletstatsEnabled→sensor.agent.kubeletstats.enabled`, `profilerEnabled→sensor.profiler.enabled`, `greenEnabled→sensor.green.enabled`, `excludeNamespaces→sensor.collection.excludeNamespaces`. Effective = module gate AND sensor gate (`collectLogs` etc., `_helpers.tpl:238-257`).
- UI: `collection-settings.tsx` (126 lines) fully read-only; `CapabilitiesResponse` in `ui/src/lib/api-types.ts:17-20` **lacks** `collectionRuntimeControl`; no overlay client functions/hooks exist. `useCapabilities` at `ui/src/hooks/use-capabilities.ts`. Mutation-hook pattern to copy: `ui/src/hooks/use-alerts-data.ts` / `use-projects.ts`. e2e strings asserted in `ui/e2e/settings.spec.ts:19-30`.
- e2e kind harness: `deploy/helm/e2e-helm.sh` (helm install at :123, no runtimeControl flag today), Go tags `e2ehelm` in `e2e/` (own module, no deps; `helmGetJSON` + admin-cookie client in `e2e/helm_test.go`).
- CI: hub job runs `go build ./... && go test -race ./...` (no integration tag), `helm` job runs `make helm-check`, `e2e-helm` job runs the kind gate (25 min budget). Do-no-harm canary gate is at the END of e2e-helm.sh (:236-278) — the overlay e2e must run AFTER it (a sensor rollout re-attaches probes).

**Non-goals (unchanged from the AEP):** pod/node label opt-out from the UI, OpAMP, per-node overrides, free-form collector YAML. Also out: deleting orphaned ConfigMaps when a signal is toggled off (the stub stays, unmounted and inert).

---

### Task 1: Embedded sensor chart — make target + committed copy + drift guard

The applier renders the chart's OWN templates so sensor config logic has a single source of truth. `go:embed` cannot reach outside the `hub/` module, so a committed copy lives at `hub/internal/collection/chart/`, synced by `make sync-hub-chart`, drift-guarded by a unit test (CI runs hub tests from the repo checkout, so drift fails CI).

**Files:**
- Modify: `Makefile` (repo root)
- Create: `hub/internal/collection/chart/` (synced copies: `Chart.yaml`, `values.yaml`, `templates/_helpers.tpl`, `templates/sensor-config.yaml`, `templates/sensor-daemonset.yaml`)
- Create: `hub/internal/collection/chartsync_test.go`

- [ ] **Step 1: Add the make target**

In the repo-root `Makefile`, after the `version-set` target (ends around line 21), add:

```make
# Sync the sensor-relevant chart files into the hub so the collection applier
# can render them via go:embed (design/2026-07-27-collection-control-plane.md).
# A unit test (hub/internal/collection/chartsync_test.go) fails when these
# drift from deploy/helm/avuruobs — rerun this target after editing them.
.PHONY: sync-hub-chart
sync-hub-chart:
	mkdir -p hub/internal/collection/chart/templates
	cp deploy/helm/avuruobs/Chart.yaml hub/internal/collection/chart/Chart.yaml
	cp deploy/helm/avuruobs/values.yaml hub/internal/collection/chart/values.yaml
	cp deploy/helm/avuruobs/templates/_helpers.tpl hub/internal/collection/chart/templates/_helpers.tpl
	cp deploy/helm/avuruobs/templates/sensor-config.yaml hub/internal/collection/chart/templates/sensor-config.yaml
	cp deploy/helm/avuruobs/templates/sensor-daemonset.yaml hub/internal/collection/chart/templates/sensor-daemonset.yaml
```

- [ ] **Step 2: Run it**

Run: `make sync-hub-chart && ls hub/internal/collection/chart/templates`
Expected: the 3 template files + `Chart.yaml`/`values.yaml` in place.

- [ ] **Step 3: Write the drift-guard test**

```go
// hub/internal/collection/chartsync_test.go
package collection

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedChartInSync fails when the committed copy under
// internal/collection/chart/ drifts from deploy/helm/avuruobs. The applier
// renders sensor manifests from the embedded copy, so a drift would make
// runtime toggles apply STALE config. Fix with: make sync-hub-chart.
func TestEmbeddedChartInSync(t *testing.T) {
	chartRoot := filepath.Join("..", "..", "..", "deploy", "helm", "avuruobs")
	if _, err := os.Stat(chartRoot); err != nil {
		t.Skipf("chart source not present at %s (building outside the monorepo?): %v", chartRoot, err)
	}
	pairs := []struct{ src, embedded string }{
		{filepath.Join(chartRoot, "Chart.yaml"), filepath.Join("chart", "Chart.yaml")},
		{filepath.Join(chartRoot, "values.yaml"), filepath.Join("chart", "values.yaml")},
		{filepath.Join(chartRoot, "templates", "_helpers.tpl"), filepath.Join("chart", "templates", "_helpers.tpl")},
		{filepath.Join(chartRoot, "templates", "sensor-config.yaml"), filepath.Join("chart", "templates", "sensor-config.yaml")},
		{filepath.Join(chartRoot, "templates", "sensor-daemonset.yaml"), filepath.Join("chart", "templates", "sensor-daemonset.yaml")},
	}
	for _, p := range pairs {
		src, err := os.ReadFile(p.src)
		if err != nil {
			t.Fatalf("read %s: %v", p.src, err)
		}
		emb, err := os.ReadFile(p.embedded)
		if err != nil {
			t.Fatalf("read %s (run `make sync-hub-chart`): %v", p.embedded, err)
		}
		if !bytes.Equal(src, emb) {
			t.Errorf("%s differs from %s — run `make sync-hub-chart` and commit the result", p.embedded, p.src)
		}
	}
}
```

Note: `hub/internal/collection` is 3 levels below `hub/`, and `hub/` is one below the repo root — so the relative path from the package dir is `../../../deploy/helm/avuruobs` (package dir `hub/internal/collection` → `..`×3 = repo root). The code above uses 3 `..` components; verify with `ls hub/internal/collection/../../../deploy/helm/avuruobs/Chart.yaml` before running and fix the join if the depth differs.

- [ ] **Step 4: Run the test**

Run: `cd hub && go test ./internal/collection/ -run TestEmbeddedChartInSync -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add Makefile hub/internal/collection/chart hub/internal/collection/chartsync_test.go
git commit -m "feat(hub): embed sensor chart templates for the collection applier"
```

---

### Task 2: Chart — placeholder ConfigMaps for disabled signals + base-values gains `collection`

Kubernetes RBAC cannot restrict `create` by resourceName, so the Role must stay create-free. Instead, when `collection.runtimeControl.enabled` is on, the chart renders ALL FOUR sensor ConfigMaps — real content when the signal's gate is true, a placeholder otherwise — so the applier only ever needs `update`/`patch`. Also: the base-values ConfigMap gains the `collection` subtree so the applier's render sees `runtimeControl.enabled=true` and reproduces the same placeholder behavior, and the hub Deployment learns its namespace/release/fullname.

**Files:**
- Modify: `deploy/helm/avuruobs/templates/sensor-config.yaml`
- Modify: `deploy/helm/avuruobs/templates/collection-rbac.yaml`
- Modify: `deploy/helm/avuruobs/templates/hub-deploy.yaml`
- Modify: `deploy/helm/template-test.sh`

- [ ] **Step 1: Add a runtime-control helper for the placeholder gate**

In `deploy/helm/avuruobs/templates/_helpers.tpl`, after the `avuruobs.collectGreen` helper (ends ~line 257), add (same `"true"`-or-`""` convention documented at `_helpers.tpl:233-237`):

```yaml
{{/*
Runtime collection control placeholder gate: when the hub may toggle signals
at runtime, every sensor ConfigMap must already exist (RBAC cannot grant
`create` scoped by resourceName), so disabled-signal ConfigMaps render as
placeholders the applier later fills.
*/}}
{{- define "avuruobs.collectionPlaceholders" -}}
{{- if and .Values.collection.runtimeControl.enabled .Values.sensor.enabled -}}
true
{{- end -}}
{{- end -}}
```

- [ ] **Step 2: Rework the four ConfigMap gates in `sensor-config.yaml`**

For each of the four ConfigMaps, change the wrapping condition so the resource renders when EITHER its real gate OR the placeholder gate is true, and the *content* stays gated on the real condition. Pattern (shown for obi — the real gate today is `{{- if and .Values.sensor.enabled .Values.sensor.obi.enabled }}` at line 22):

```yaml
{{- $obiActive := and .Values.sensor.enabled .Values.sensor.obi.enabled }}
{{- if or $obiActive (include "avuruobs.collectionPlaceholders" .) }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "avuruobs.fullname" . }}-sensor-obi
  labels:
    {{- include "avuruobs.labels" . | nindent 4 }}
    app.kubernetes.io/component: sensor
data:
  obi-config.yml: |
{{- if $obiActive }}
    ... (existing content, unchanged) ...
{{- else }}
    # Placeholder: signal disabled. Managed by runtime collection control —
    # the hub rewrites this ConfigMap when the signal is toggled on.
{{- end }}
{{- end }}
```

Apply the same shape to `-sensor-agent` (real gate = the existing `$agentActive` computed at :116), `-sensor-profiler` (real gate `and .Values.sensor.enabled (include "avuruobs.collectProfiles" .)` at :369), and `-sensor-kepler` (real gate `and .Values.sensor.enabled (include "avuruobs.collectGreen" .)` at :421). Keep every existing `fail` guard at the top of the file untouched. Keep the existing `metadata`/`labels` blocks exactly as they are — only the outer `if` and the data-content conditional change. The three non-obi ConfigMaps use data key `config.yaml`.

Careful with the existing document separators: each ConfigMap is its own YAML doc with a leading `---`; keep the separators outside the new conditionals exactly as the current file has them (render with the flag off must be byte-identical to today — Step 5 asserts that).

- [ ] **Step 3: base-values ConfigMap gains `collection`; hub Deployment gains identity env**

In `deploy/helm/avuruobs/templates/collection-rbac.yaml`, in the `$baseValues` dict (lines 45-54), add one entry:

```yaml
          "collection" .Values.collection
```

In `deploy/helm/avuruobs/templates/hub-deploy.yaml`, right after the existing `AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED` env (lines 70-71), add — gated, because only the applier needs them:

```yaml
            {{- if .Values.collection.runtimeControl.enabled }}
            - name: AVURUOBS_RELEASE_NAME
              value: {{ .Release.Name | quote }}
            - name: AVURUOBS_RELEASE_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: AVURUOBS_COLLECTION_FULLNAME
              value: {{ include "avuruobs.fullname" . | quote }}
            {{- end }}
```

- [ ] **Step 4: template-test.sh — new assertions**

Open `deploy/helm/template-test.sh`, find the collection runtime control section (`:469-568`), and append after its last block (before the final `echo "ALL TEMPLATE ASSERTIONS PASSED"`):

```bash
echo "== collection runtime control: placeholder ConfigMaps for disabled signals"
# Default install: profiler is off -> no profiler ConfigMap.
out="$(render)"
grep -q "name: test-avuruobs-sensor-profiler" <<<"$out" && fail "profiler ConfigMap rendered on a default install (profiler disabled, runtime control off)"
# Runtime control on: ALL four sensor ConfigMaps exist, disabled ones as placeholders.
out="$(render --set collection.runtimeControl.enabled=true --set auth.enabled=true)"
for cm in sensor-obi sensor-agent sensor-profiler sensor-kepler; do
  grep -q "name: test-avuruobs-$cm" <<<"$out" || fail "runtime control on: $cm ConfigMap missing (applier cannot create, only update)"
done
grep -q "Placeholder: signal disabled" <<<"$out" || fail "disabled-signal ConfigMap did not render placeholder content"
ok "placeholder ConfigMaps render when runtime control is on"

echo "== collection runtime control: hub identity env"
grep -q "AVURUOBS_RELEASE_NAMESPACE" <<<"$out" || fail "hub missing AVURUOBS_RELEASE_NAMESPACE downward-API env"
grep -q "AVURUOBS_COLLECTION_FULLNAME" <<<"$out" || fail "hub missing AVURUOBS_COLLECTION_FULLNAME env"
out="$(render)"
grep -q "AVURUOBS_RELEASE_NAMESPACE" <<<"$out" && fail "identity env rendered with runtime control off"
ok "hub identity env gated on the flag"

echo "== collection runtime control: base-values carries collection subtree"
out="$(render --set collection.runtimeControl.enabled=true --set auth.enabled=true)"
grep -q '"runtimeControl"' <<<"$out" || fail "base-values ConfigMap missing the collection subtree"
ok "base-values includes collection"
```

Note the existing section's `render` helper and `test-avuruobs` naming — read the helper first and reuse it exactly (the earlier blocks at `:469-534` show the convention, including `--set auth.enabled=true` needed alongside the flag because of the auth guard at `collection-rbac.yaml:12-14`).

- [ ] **Step 5: Verify default render is unchanged and checks pass**

```bash
# NEVER git stash here — the stash stack is shared across worktrees/sessions.
# Snapshot the pre-change chart from HEAD instead (Task 2 is uncommitted at
# this point, so HEAD still has the untouched chart):
SNAP=$(mktemp -d)
git archive HEAD -- deploy/helm/avuruobs | tar -x -C "$SNAP"
helm template test "$SNAP/deploy/helm/avuruobs" > "$SNAP/render-before.yaml"
helm template test deploy/helm/avuruobs > "$SNAP/render-after.yaml"
diff "$SNAP/render-before.yaml" "$SNAP/render-after.yaml"
```
Expected: NO diff (default install byte-identical — the wedge is law). Then run `make helm-check` — expected: ALL TEMPLATE ASSERTIONS PASSED.

- [ ] **Step 6: Resync the embedded copy and commit**

```bash
make sync-hub-chart
cd hub && go test ./internal/collection/ -run TestEmbeddedChartInSync && cd ..
git add deploy/helm/avuruobs/templates/_helpers.tpl deploy/helm/avuruobs/templates/sensor-config.yaml \
        deploy/helm/avuruobs/templates/collection-rbac.yaml deploy/helm/avuruobs/templates/hub-deploy.yaml \
        deploy/helm/template-test.sh hub/internal/collection/chart
git commit -m "feat(chart): placeholder sensor ConfigMaps + hub identity env for runtime collection control"
```

---

### Task 3: Renderer — `RenderSensorManifests` (embedded chart + helm engine)

**Files:**
- Create: `hub/internal/collection/render.go`
- Test: `hub/internal/collection/render_test.go`
- Modify: `hub/go.mod` / `hub/go.sum` (new deps)

- [ ] **Step 1: Add the dependencies**

```bash
cd hub
go get helm.sh/helm/v3@latest k8s.io/api@latest k8s.io/apimachinery@latest k8s.io/client-go@latest sigs.k8s.io/yaml@latest
go mod tidy
```
Expected: resolves cleanly against Go 1.26. If `helm.sh/helm/v3@latest` drags an incompatible k8s.io set, pin the k8s.io modules to the minor helm itself requires (check with `go mod graph | grep 'helm.sh/helm/v3 k8s.io/client-go'`).

- [ ] **Step 2: Write the failing tests**

```go
// hub/internal/collection/render_test.go
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
	// Default install: obi on, logs+kubeletstats on, profiler off, green off.
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
	// Runtime control on => all four ConfigMaps present (placeholders for off signals).
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
	if strings.Contains(agent.Data["config.yaml"], "filelog") {
		t.Fatal("logs toggled off but filelog receiver still rendered")
	}
	// kubeletstats still on -> agent container still present.
	if !containerNames(got)["otel-agent"] {
		t.Fatal("agent container should remain (kubeletstats still on)")
	}
}

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
	// modules.profiling.enabled defaults to... check embedded values.yaml; if
	// the default is true, force it false here — the module gate must win.
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
```

- [ ] **Step 3: Run to confirm they fail**

Run: `cd hub && go test ./internal/collection/ -run TestRenderSensorManifests`
Expected: FAIL — `RenderSensorManifests`, `SensorManifests` undefined.

- [ ] **Step 4: Implement the renderer**

```go
// hub/internal/collection/render.go
package collection

import (
	"embed"
	"fmt"
	"io/fs"
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
//go:embed chart/Chart.yaml chart/values.yaml all:chart/templates
var chartFS embed.FS

// SensorManifests is the rendered, decoded output the applier reconciles the
// cluster to: the four sensor ConfigMaps (placeholders included — see the
// chart's collectionPlaceholders helper) and the sensor DaemonSet.
type SensorManifests struct {
	ConfigMaps []corev1.ConfigMap
	DaemonSet  *appsv1.DaemonSet
}

// ConfigMap returns the rendered ConfigMap with the given name.
func (m *SensorManifests) ConfigMap(name string) (corev1.ConfigMap, bool) {
	for _, cm := range m.ConfigMaps {
		if cm.Name == name {
			return cm, true
		}
	}
	return corev1.ConfigMap{}, false
}

// RenderSensorManifests renders the embedded sensor chart templates with the
// install's base values (from the -collection-base-values ConfigMap) merged
// under the chart defaults, with the overlay applied on top. The overlay maps
// onto the exact values the chart's own gates read, so a runtime toggle
// renders precisely what `helm upgrade` with those values would have.
func RenderSensorManifests(baseValues map[string]any, overlay Overlay, releaseName, namespace string) (*SensorManifests, error) {
	files, err := chartFiles()
	if err != nil {
		return nil, err
	}
	ch, err := loader.LoadFiles(files)
	if err != nil {
		return nil, fmt.Errorf("load embedded chart: %w", err)
	}

	vals := applyOverlayToValues(baseValues, overlay)
	renderVals, err := chartutil.ToRenderValues(ch, vals, chartutil.ReleaseOptions{
		Name:      releaseName,
		Namespace: namespace,
		IsInstall: true,
	}, chartutil.DefaultCapabilities.Copy())
	if err != nil {
		return nil, fmt.Errorf("prepare render values: %w", err)
	}

	rendered, err := engine.Engine{}.Render(ch, renderVals)
	if err != nil {
		return nil, fmt.Errorf("render sensor templates: %w", err)
	}

	out := &SensorManifests{}
	for name, content := range rendered {
		if !strings.HasSuffix(name, "sensor-config.yaml") && !strings.HasSuffix(name, "sensor-daemonset.yaml") {
			continue
		}
		for _, doc := range strings.Split(content, "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var probe struct {
				Kind string `json:"kind"`
			}
			if err := sigyaml.Unmarshal([]byte(doc), &probe); err != nil {
				return nil, fmt.Errorf("parse rendered doc from %s: %w", name, err)
			}
			switch probe.Kind {
			case "ConfigMap":
				var cm corev1.ConfigMap
				if err := sigyaml.Unmarshal([]byte(doc), &cm); err != nil {
					return nil, fmt.Errorf("decode rendered ConfigMap: %w", err)
				}
				out.ConfigMaps = append(out.ConfigMaps, cm)
			case "DaemonSet":
				var ds appsv1.DaemonSet
				if err := sigyaml.Unmarshal([]byte(doc), &ds); err != nil {
					return nil, fmt.Errorf("decode rendered DaemonSet: %w", err)
				}
				out.DaemonSet = &ds
			}
		}
	}
	return out, nil
}

func chartFiles() ([]*loader.BufferedFile, error) {
	var files []*loader.BufferedFile
	err := fs.WalkDir(chartFS, "chart", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := chartFS.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, &loader.BufferedFile{
			Name: strings.TrimPrefix(path, "chart/"),
			Data: data,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded chart: %w", err)
	}
	return files, nil
}

// applyOverlayToValues deep-copies baseValues and sets the chart values each
// overlay field maps to. The mapping mirrors the chart's own gates
// (_helpers.tpl collectLogs/collectInfraMetrics/collectProfiles/collectGreen):
// a module disabled at install stays disabled — the sensor-side toggle alone
// cannot widen collection beyond what the chart allows.
func applyOverlayToValues(base map[string]any, ov Overlay) map[string]any {
	vals := deepCopyMap(base)
	set := func(path []string, v any) {
		m := vals
		for _, k := range path[:len(path)-1] {
			next, ok := m[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[k] = next
			}
			m = next
		}
		m[path[len(path)-1]] = v
	}
	if ov.ObiEnabled != nil {
		set([]string{"sensor", "obi", "enabled"}, *ov.ObiEnabled)
	}
	if ov.LogsEnabled != nil {
		set([]string{"sensor", "agent", "logs", "enabled"}, *ov.LogsEnabled)
	}
	if ov.KubeletstatsEnabled != nil {
		set([]string{"sensor", "agent", "kubeletstats", "enabled"}, *ov.KubeletstatsEnabled)
	}
	if ov.ProfilerEnabled != nil {
		set([]string{"sensor", "profiler", "enabled"}, *ov.ProfilerEnabled)
	}
	if ov.GreenEnabled != nil {
		set([]string{"sensor", "green", "enabled"}, *ov.GreenEnabled)
	}
	if ov.ExcludeNamespaces != nil {
		nss := make([]any, len(*ov.ExcludeNamespaces))
		for i, ns := range *ov.ExcludeNamespaces {
			nss[i] = ns
		}
		set([]string{"sensor", "collection", "excludeNamespaces"}, nss)
	}
	return vals
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case map[string]any:
			out[k] = deepCopyMap(t)
		case []any:
			cp := make([]any, len(t))
			for i, e := range t {
				if em, ok := e.(map[string]any); ok {
					cp[i] = deepCopyMap(em)
				} else {
					cp[i] = e
				}
			}
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}
```

Implementation notes for the executor:
- `chartutil.ToRenderValues` coalesces the provided values with the chart's `values.yaml` defaults, so an empty `sensor:{}` in base values falls back to chart defaults — same semantics as `helm template` with a values file.
- If `engine.Engine{}.Render` errors on the `lookup` template function or capabilities, switch to `engine.New(nil)`-style construction per the helm version's API; the sensor templates use neither.
- Rendered doc splitting on `"\n---"`: the chart emits standard `---` separators at column 0; if a template legitimately contains a line starting with `---` inside literal config (check `sensor-config.yaml` — it does not today), switch to a YAML-aware splitter (`k8s.io/apimachinery/pkg/util/yaml.NewYAMLReader`).
- Before finalizing, grep the two sensor templates for values outside the base-values subtrees: `grep -oE '\.Values\.[a-zA-Z.]+' deploy/helm/avuruobs/templates/sensor-config.yaml deploy/helm/avuruobs/templates/sensor-daemonset.yaml | sort -u`. Every prefix must be one of `sensor`, `modules`, `image`, `auth.ingest`, `collection`, `nameOverride`, `fullnameOverride` — if anything else appears (e.g. `.Values.green.*`), add that subtree to the base-values dict in `collection-rbac.yaml` (Task 2) AND to this list, and note it in the commit message.

- [ ] **Step 5: Run the tests**

Run: `cd hub && go test ./internal/collection/ -run TestRenderSensorManifests -v`
Expected: PASS (5 tests). Debug renders by dumping `rendered` keys if extraction finds nothing.

- [ ] **Step 6: Full package + build check, commit**

Run: `cd hub && go build ./... && go test ./internal/collection/...`
Expected: PASS.

```bash
git add hub/internal/collection/render.go hub/internal/collection/render_test.go hub/go.mod hub/go.sum
git commit -m "feat(hub): render sensor manifests from the embedded chart with overlay applied"
```

---

### Task 4: `K8sApplier` — ConfigMap update + DaemonSet patch, idempotent

**Files:**
- Create: `hub/internal/collection/k8sapplier.go`
- Test: `hub/internal/collection/k8sapplier_test.go`

- [ ] **Step 1: Write the failing tests (client-go fake clientset)**

```go
// hub/internal/collection/k8sapplier_test.go
package collection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testNS = "avuruobs"

func seedCluster(t *testing.T) *fake.Clientset {
	t.Helper()
	baseVals, err := json.Marshal(map[string]any{
		"sensor":     map[string]any{},
		"modules":    map[string]any{},
		"collection": map[string]any{"runtimeControl": map[string]any{"enabled": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	objs := []struct {
		name string
		data map[string]string
	}{
		{"avuruobs-collection-base-values", map[string]string{"values.json": string(baseVals)}},
		{"avuruobs-sensor-obi", map[string]string{"obi-config.yml": "stale"}},
		{"avuruobs-sensor-agent", map[string]string{"config.yaml": "stale"}},
		{"avuruobs-sensor-profiler", map[string]string{"config.yaml": "# Placeholder: signal disabled"}},
		{"avuruobs-sensor-kepler", map[string]string{"config.yaml": "# Placeholder: signal disabled"}},
	}
	var runtime []interface{ DeepCopyObject() interface{} }
	_ = runtime
	client := fake.NewSimpleClientset()
	for _, o := range objs {
		if _, err := client.CoreV1().ConfigMaps(testNS).Create(context.Background(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: o.name, Namespace: testNS},
			Data:       o.data,
		}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "avuruobs-sensor", Namespace: testNS},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"checksum/config": "helm-owned"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "obi"}, {Name: "otel-agent"}},
					Volumes:    []corev1.Volume{{Name: "obi-config"}},
				},
			},
		},
	}
	if _, err := client.AppsV1().DaemonSets(testNS).Create(context.Background(), ds, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return client
}

func newTestApplier(client *fake.Clientset) *K8sApplier {
	return &K8sApplier{Client: client, Namespace: testNS, ReleaseName: "avuruobs", Fullname: "avuruobs"}
}

func TestK8sApplier_UpdatesConfigMapsAndPatchesDaemonSet(t *testing.T) {
	client := seedCluster(t)
	a := newTestApplier(client)

	if err := a.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	obi, err := client.CoreV1().ConfigMaps(testNS).Get(context.Background(), "avuruobs-sensor-obi", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if obi.Data["obi-config.yml"] == "stale" {
		t.Fatal("obi ConfigMap not updated with rendered content")
	}
	ds, err := client.AppsV1().DaemonSets(testNS).Get(context.Background(), "avuruobs-sensor", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ds.Spec.Template.Annotations[OverlayChecksumAnnotation] == "" {
		t.Fatal("overlay-checksum annotation not set")
	}
	if ds.Spec.Template.Annotations["checksum/config"] != "helm-owned" {
		t.Fatal("helm's checksum/config annotation must be preserved")
	}
	found := false
	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == "otel-agent" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected otel-agent container in patched DaemonSet")
	}
}

func TestK8sApplier_Idempotent(t *testing.T) {
	client := seedCluster(t)
	a := newTestApplier(client)
	if err := a.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	var writes int
	client.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, interface{}, error) {
		writes++
		return false, nil, nil
	})
	client.PrependReactor("patch", "daemonsets", func(k8stesting.Action) (bool, interface{}, error) {
		writes++
		return false, nil, nil
	})
	if err := a.Apply(context.Background(), Overlay{}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if writes != 0 {
		t.Fatalf("identical overlay re-applied caused %d writes, want 0 (idempotency)", writes)
	}
}

func TestK8sApplier_RefusesUnexpectedNames(t *testing.T) {
	client := seedCluster(t)
	a := newTestApplier(client)
	a.Fullname = "other-release" // rendered names won't match this allowlist
	if err := a.Apply(context.Background(), Overlay{}); err == nil {
		t.Fatal("Apply must fail closed when rendered names don't match the release fullname")
	}
	if !strings.Contains(errString(a, t), "") {
		// error content asserted in the err above; this helper keeps lint quiet
		_ = a
	}
}

func errString(_ *K8sApplier, _ *testing.T) string { return "" }
```

Adjust `TestK8sApplier_RefusesUnexpectedNames` to assert on the returned error message (`"rendered object"` / `"not in the allowlist"`) — the helper stub above is a placeholder; write it as a direct `err := a.Apply(...)` + `strings.Contains(err.Error(), "allowlist")` check.

- [ ] **Step 2: Run to confirm failure**

Run: `cd hub && go test ./internal/collection/ -run TestK8sApplier`
Expected: FAIL — `K8sApplier` undefined.

- [ ] **Step 3: Implement**

```go
// hub/internal/collection/k8sapplier.go
package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// OverlayChecksumAnnotation is the hub-owned pod-template annotation that
// forces a sensor rollout when the applied overlay changes. Distinct from
// Helm's checksum/config, which stays untouched so `helm upgrade` keeps
// working normally (AEP: design/2026-07-27-collection-control-plane.md).
const OverlayChecksumAnnotation = "avuru.obs/overlay-checksum"

// K8sApplier reconciles the sensor ConfigMaps + DaemonSet to match the
// overlay. It renders the embedded chart with the install's base values
// (read from the -collection-base-values ConfigMap) and updates only the
// objects the chart-scoped Role allows.
type K8sApplier struct {
	Client      kubernetes.Interface
	Namespace   string
	ReleaseName string
	Fullname    string
}

func (a *K8sApplier) baseValuesName() string { return a.Fullname + "-collection-base-values" }
func (a *K8sApplier) daemonSetName() string  { return a.Fullname + "-sensor" }

func (a *K8sApplier) allowedConfigMaps() map[string]bool {
	return map[string]bool{
		a.Fullname + "-sensor-obi":      true,
		a.Fullname + "-sensor-agent":    true,
		a.Fullname + "-sensor-profiler": true,
		a.Fullname + "-sensor-kepler":   true,
	}
}

// Apply implements Applier.
func (a *K8sApplier) Apply(ctx context.Context, overlay Overlay) error {
	baseCM, err := a.Client.CoreV1().ConfigMaps(a.Namespace).Get(ctx, a.baseValuesName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read base values ConfigMap %s: %w", a.baseValuesName(), err)
	}
	var baseValues map[string]any
	if err := json.Unmarshal([]byte(baseCM.Data["values.json"]), &baseValues); err != nil {
		return fmt.Errorf("parse %s values.json: %w", a.baseValuesName(), err)
	}

	rendered, err := RenderSensorManifests(baseValues, overlay, a.ReleaseName, a.Namespace)
	if err != nil {
		return err
	}
	if rendered.DaemonSet == nil {
		return fmt.Errorf("render produced no sensor DaemonSet — refusing to apply")
	}

	// Fail closed on any rendered name outside the RBAC allowlist BEFORE
	// writing anything.
	allowed := a.allowedConfigMaps()
	for _, cm := range rendered.ConfigMaps {
		if !allowed[cm.Name] {
			return fmt.Errorf("rendered object %q is not in the applier allowlist — refusing to apply", cm.Name)
		}
	}
	if rendered.DaemonSet.Name != a.daemonSetName() {
		return fmt.Errorf("rendered DaemonSet %q is not in the applier allowlist — refusing to apply", rendered.DaemonSet.Name)
	}

	checksum := overlayChecksum(rendered)

	for _, cm := range rendered.ConfigMaps {
		if err := a.applyConfigMap(ctx, cm); err != nil {
			return err
		}
	}
	return a.patchDaemonSet(ctx, rendered, checksum)
}

func (a *K8sApplier) applyConfigMap(ctx context.Context, want corev1.ConfigMap) error {
	cms := a.Client.CoreV1().ConfigMaps(a.Namespace)
	live, err := cms.Get(ctx, want.Name, metav1.GetOptions{})
	if err != nil {
		// No create: the chart pre-renders every sensor ConfigMap (as a
		// placeholder when the signal is off) precisely so the Role never
		// needs a create grant (create cannot be resourceName-scoped).
		return fmt.Errorf("get ConfigMap %s (the chart should have created it): %w", want.Name, err)
	}
	if equalStringMaps(live.Data, want.Data) {
		return nil
	}
	updated := live.DeepCopy()
	updated.Data = want.Data
	if _, err := cms.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ConfigMap %s: %w", want.Name, err)
	}
	slog.Info("collection applier updated ConfigMap", "name", want.Name, "resourceVersion", live.ResourceVersion)
	return nil
}

func (a *K8sApplier) patchDaemonSet(ctx context.Context, rendered *SensorManifests, checksum string) error {
	dss := a.Client.AppsV1().DaemonSets(a.Namespace)
	live, err := dss.Get(ctx, a.daemonSetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get DaemonSet %s: %w", a.daemonSetName(), err)
	}
	if live.Spec.Template.Annotations[OverlayChecksumAnnotation] == checksum {
		return nil // identical content already applied — no rollout
	}

	annotations := map[string]string{}
	for k, v := range live.Spec.Template.Annotations {
		annotations[k] = v
	}
	annotations[OverlayChecksumAnnotation] = checksum

	patch := []map[string]any{
		{"op": "replace", "path": "/spec/template/spec/containers", "value": rendered.DaemonSet.Spec.Template.Spec.Containers},
		{"op": "replace", "path": "/spec/template/spec/volumes", "value": rendered.DaemonSet.Spec.Template.Spec.Volumes},
		{"op": "replace", "path": "/spec/template/metadata/annotations", "value": annotations},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal DaemonSet patch: %w", err)
	}
	if _, err := dss.Patch(ctx, a.daemonSetName(), types.JSONPatchType, body, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patch DaemonSet %s: %w", a.daemonSetName(), err)
	}
	slog.Info("collection applier patched sensor DaemonSet",
		"name", a.daemonSetName(), "overlayChecksum", checksum, "resourceVersion", live.ResourceVersion)
	return nil
}

// overlayChecksum hashes everything the applier manages: ConfigMap data and
// the DaemonSet's containers+volumes. Identical renders hash identically, so
// re-applying an unchanged overlay patches nothing (AEP idempotency goal).
func overlayChecksum(m *SensorManifests) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	for _, cm := range m.ConfigMaps {
		_ = enc.Encode(cm.Name)
		_ = enc.Encode(cm.Data)
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
		if b[k] != v {
			return false
		}
	}
	return true
}

var _ Applier = (*K8sApplier)(nil)
```

Note on map iteration in `overlayChecksum`: `json.Encoder` sorts map keys, and `m.ConfigMaps` order comes from render order — sort `m.ConfigMaps` by name in `RenderSensorManifests` before returning (add `sort.Slice` there) so the hash is deterministic. Add that line to `render.go` while implementing this task.

- [ ] **Step 4: Run the tests**

Run: `cd hub && go test ./internal/collection/ -run TestK8sApplier -v`
Expected: PASS (3 tests). The idempotency test is the critical one — if it fails, check hash determinism (ConfigMap ordering).

- [ ] **Step 5: Commit**

```bash
git add hub/internal/collection/k8sapplier.go hub/internal/collection/k8sapplier_test.go hub/internal/collection/render.go
git commit -m "feat(hub): K8sApplier — reconcile sensor ConfigMaps + DaemonSet from the overlay"
```

---

### Task 5: Effective config in GET + wiring in `main.go`

**Files:**
- Modify: `hub/internal/collection/applier.go` (add `Effective` type + reporter interface)
- Create: `hub/internal/collection/effective.go`
- Test: `hub/internal/collection/effective_test.go`
- Modify: `hub/internal/api/collection.go` (GET gains `effective`)
- Modify: `hub/internal/api/collection_test.go`
- Modify: `hub/cmd/hub/main.go`

- [ ] **Step 1: Effective computation (pure) — failing tests first**

```go
// hub/internal/collection/effective_test.go
package collection

import "testing"

func TestEffectiveFromValues_Defaults(t *testing.T) {
	base := map[string]any{"sensor": map[string]any{}, "modules": map[string]any{}}
	eff, err := EffectiveFromValues(base, Overlay{})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	// Chart defaults: obi+logs+kubeletstats on; profiler+green off.
	if !eff.Obi || !eff.Logs || !eff.Kubeletstats {
		t.Fatalf("defaults: obi/logs/kubeletstats should be on, got %+v", eff)
	}
	if eff.Profiler || eff.Green {
		t.Fatalf("defaults: profiler/green should be off, got %+v", eff)
	}
	if len(eff.ExcludeNamespaces) == 0 {
		t.Fatalf("defaults: excludeNamespaces should carry the chart defaults, got %+v", eff)
	}
}

func TestEffectiveFromValues_OverlayWins(t *testing.T) {
	off := false
	ns := []string{"payments"}
	base := map[string]any{"sensor": map[string]any{}, "modules": map[string]any{}}
	eff, err := EffectiveFromValues(base, Overlay{LogsEnabled: &off, ExcludeNamespaces: &ns})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Logs {
		t.Fatal("overlay logsEnabled=false must win over base")
	}
	if len(eff.ExcludeNamespaces) != 1 || eff.ExcludeNamespaces[0] != "payments" {
		t.Fatalf("overlay namespaces must win, got %v", eff.ExcludeNamespaces)
	}
}

func TestEffectiveFromValues_ModuleGateWins(t *testing.T) {
	on := true
	base := map[string]any{
		"sensor":  map[string]any{},
		"modules": map[string]any{"profiling": map[string]any{"enabled": false}},
	}
	eff, err := EffectiveFromValues(base, Overlay{ProfilerEnabled: &on})
	if err != nil {
		t.Fatalf("EffectiveFromValues: %v", err)
	}
	if eff.Profiler {
		t.Fatal("a chart-disabled module must stay off regardless of the sensor toggle")
	}
}
```

- [ ] **Step 2: Implement `effective.go`**

```go
// hub/internal/collection/effective.go
package collection

import "fmt"

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

// EffectiveFromValues computes the effective config from the install's base
// values (chart defaults coalesced underneath) and the overlay.
func EffectiveFromValues(base map[string]any, ov Overlay) (Effective, error) {
	defs, err := chartDefaultValues()
	if err != nil {
		return Effective{}, err
	}
	merged := coalesce(deepCopyMap(base), defs)

	boolAt := func(path ...string) bool {
		v, ok := lookupPath(merged, path)
		b, isB := v.(bool)
		return ok && isB && b
	}
	pick := func(overlayVal *bool, sensorPath ...string) bool {
		if overlayVal != nil {
			return *overlayVal
		}
		return boolAt(sensorPath...)
	}

	eff := Effective{
		Obi:          boolAt("sensor", "enabled") && pick(ov.ObiEnabled, "sensor", "obi", "enabled"),
		Logs:         boolAt("sensor", "enabled") && boolAt("modules", "logs", "enabled") && pick(ov.LogsEnabled, "sensor", "agent", "logs", "enabled"),
		Kubeletstats: boolAt("sensor", "enabled") && boolAt("modules", "infraMetrics", "enabled") && pick(ov.KubeletstatsEnabled, "sensor", "agent", "kubeletstats", "enabled"),
		Profiler:     boolAt("sensor", "enabled") && boolAt("modules", "profiling", "enabled") && pick(ov.ProfilerEnabled, "sensor", "profiler", "enabled"),
		Green:        boolAt("sensor", "enabled") && boolAt("modules", "green", "enabled") && pick(ov.GreenEnabled, "sensor", "green", "enabled"),
	}

	if ov.ExcludeNamespaces != nil {
		eff.ExcludeNamespaces = append([]string{}, (*ov.ExcludeNamespaces)...)
	} else if v, ok := lookupPath(merged, []string{"sensor", "collection", "excludeNamespaces"}); ok {
		if list, isList := v.([]any); isList {
			for _, e := range list {
				if s, isS := e.(string); isS {
					eff.ExcludeNamespaces = append(eff.ExcludeNamespaces, s)
				}
			}
		}
	}
	if eff.ExcludeNamespaces == nil {
		eff.ExcludeNamespaces = []string{}
	}
	return eff, nil
}

func lookupPath(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// coalesce fills gaps in dst from src (chart defaults), recursively — the
// same semantics chartutil applies at render time.
func coalesce(dst, src map[string]any) map[string]any {
	for k, sv := range src {
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dm, dOK := dv.(map[string]any)
		sm, sOK := sv.(map[string]any)
		if dOK && sOK {
			dst[k] = coalesce(dm, sm)
		}
	}
	return dst
}

// chartDefaultValues parses the embedded chart's values.yaml once.
func chartDefaultValues() (map[string]any, error) {
	data, err := chartFS.ReadFile("chart/values.yaml")
	if err != nil {
		return nil, fmt.Errorf("read embedded values.yaml: %w", err)
	}
	return parseYAMLMap(data)
}
```

Add `parseYAMLMap` using `sigs.k8s.io/yaml` (`yaml.Unmarshal` into `map[string]any` — sigs yaml converts via JSON so nested maps come out as `map[string]any`, which is why we use it rather than `gopkg.in/yaml.v3`):

```go
func parseYAMLMap(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := sigyaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse values.yaml: %w", err)
	}
	return m, nil
}
```
(with `sigyaml "sigs.k8s.io/yaml"` in the import block).

- [ ] **Step 3: Run the effective tests**

Run: `cd hub && go test ./internal/collection/ -run TestEffectiveFromValues -v`
Expected: PASS (3 tests).

- [ ] **Step 4: Reporter seam + K8sApplier implementation + API wiring**

In `hub/internal/collection/applier.go`, after the `Applier` interface, add:

```go
// EffectiveReporter is implemented by appliers that can resolve the install's
// base values — the API includes the effective (base ⊕ overlay) config in
// GET /api/v1/collection/overlay when available. NoopApplier does not
// implement it (no cluster to read base values from), so the field is simply
// omitted on installs without a real applier.
type EffectiveReporter interface {
	Effective(ctx context.Context, overlay Overlay) (Effective, error)
}
```

In `hub/internal/collection/k8sapplier.go`, add:

```go
// Effective implements EffectiveReporter from the live base-values ConfigMap.
func (a *K8sApplier) Effective(ctx context.Context, overlay Overlay) (Effective, error) {
	baseCM, err := a.Client.CoreV1().ConfigMaps(a.Namespace).Get(ctx, a.baseValuesName(), metav1.GetOptions{})
	if err != nil {
		return Effective{}, fmt.Errorf("read base values ConfigMap %s: %w", a.baseValuesName(), err)
	}
	var baseValues map[string]any
	if err := json.Unmarshal([]byte(baseCM.Data["values.json"]), &baseValues); err != nil {
		return Effective{}, fmt.Errorf("parse %s values.json: %w", a.baseValuesName(), err)
	}
	return EffectiveFromValues(baseValues, overlay)
}
```

In `hub/internal/api/collection.go`:
- Add `Effective *collection.Effective \`json:"effective,omitempty"\`` to `collectionOverlayResponse` (after `Overlay`).
- Add a helper on `*API`:

```go
// effectiveOrNil resolves the effective config when the applier can report
// it; a resolution failure only logs — GET must keep working when the
// cluster is briefly unreachable.
func (a *API) effectiveOrNil(ctx context.Context, ov collection.Overlay) *collection.Effective {
	rep, ok := a.collectionApplier.(collection.EffectiveReporter)
	if !ok {
		return nil
	}
	eff, err := rep.Effective(ctx, ov)
	if err != nil {
		slog.Warn("resolving effective collection config failed", "err", err)
		return nil
	}
	return &eff
}
```
(add `"log/slog"` to the imports). In `handleGetCollectionOverlay`, set `Effective: a.effectiveOrNil(r.Context(), ov)` in both the not-found response (`ov` = zero `collection.Overlay{}`) and the found response. Leave PUT/DELETE responses without `effective` (the UI refetches GET after mutations).

- [ ] **Step 5: Handler test for `effective`**

In `hub/internal/api/collection_test.go`, add (mirroring the existing `collectionMux` helper at `:19-23` — read it first):

```go
type stubReporter struct{ collection.NoopApplier }

func (stubReporter) Effective(_ context.Context, ov collection.Overlay) (collection.Effective, error) {
	logs := ov.LogsEnabled == nil || *ov.LogsEnabled
	return collection.Effective{Obi: true, Logs: logs, ExcludeNamespaces: []string{"kube-system"}}, nil
}

func TestCollectionOverlay_GetIncludesEffective(t *testing.T) {
	// Build the mux exactly like collectionMux does, but with
	// Config.CollectionApplier = stubReporter{}.
	// Assert GET body contains `"effective"` and `"obi":true`.
}
```

Fill the body of the test following `collectionMux`'s actual construction (it passes a `Config` — add `CollectionApplier: stubReporter{}`), then `GET /api/v1/collection/overlay` and assert `strings.Contains(rec.Body.String(), "\"effective\"")` and `"\"kube-system\""`.

- [ ] **Step 6: `main.go` wiring**

In `hub/cmd/hub/main.go`, the config literal currently has `CollectionApplier: collection.NoopApplier{}` at line 253. Replace with `CollectionApplier: collectionApplier(collectionRuntimeControlEnabled)` and add near the other helper funcs at the bottom of the file:

```go
// collectionApplier picks the real cluster applier when runtime collection
// control is on AND the hub runs in a cluster with the chart-provided
// identity env; anything else falls back to the logging no-op so compose
// stacks and local runs keep working (design/2026-07-27-collection-control-plane.md).
func collectionApplier(enabled bool) collection.Applier {
	if !enabled {
		return collection.NoopApplier{}
	}
	ns := os.Getenv("AVURUOBS_RELEASE_NAMESPACE")
	release := os.Getenv("AVURUOBS_RELEASE_NAME")
	fullname := os.Getenv("AVURUOBS_COLLECTION_FULLNAME")
	if ns == "" || release == "" || fullname == "" {
		slog.Warn("collection runtime control enabled but release identity env missing — overlay will persist without applying",
			"namespace", ns, "release", release, "fullname", fullname)
		return collection.NoopApplier{}
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		slog.Warn("collection runtime control enabled but not running in-cluster — overlay will persist without applying", "err", err)
		return collection.NoopApplier{}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		slog.Warn("collection runtime control: building kubernetes client failed", "err", err)
		return collection.NoopApplier{}
	}
	slog.Info("collection runtime control: cluster applier active",
		"namespace", ns, "release", release, "fullname", fullname)
	return &collection.K8sApplier{Client: client, Namespace: ns, ReleaseName: release, Fullname: fullname}
}
```

Imports to add in `main.go`: `"k8s.io/client-go/kubernetes"`, `"k8s.io/client-go/rest"` (`"os"`, `"log/slog"` and the `collection` package are already imported — verify).

- [ ] **Step 7: Full hub validation**

Run: `cd hub && go build ./... && go test -race ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add hub/internal/collection/applier.go hub/internal/collection/effective.go hub/internal/collection/effective_test.go \
        hub/internal/collection/k8sapplier.go hub/internal/api/collection.go hub/internal/api/collection_test.go \
        hub/cmd/hub/main.go
git commit -m "feat(hub): effective collection config in GET + in-cluster applier wiring"
```

---

### Task 6: UI — writable Settings → Collection card

**Files:**
- Modify: `ui/src/lib/api-types.ts`
- Create: `ui/src/hooks/use-collection-overlay.ts`
- Modify: `ui/src/lib/query-keys.ts`
- Modify: `ui/src/hooks/use-capabilities.ts`
- Modify: `ui/src/components/settings/collection-settings.tsx`
- Create: `ui/e2e/collection-admin.spec.ts`

- [ ] **Step 1: Types**

In `ui/src/lib/api-types.ts`, extend `CapabilitiesResponse` (lines 17-20) and add the overlay types near it:

```ts
export interface CapabilitiesResponse {
  version: string;
  modules: ModuleName[];
  collectionRuntimeControl: boolean;
}

/** Runtime collection overlay — design/2026-07-27-collection-control-plane.md. */
export interface CollectionOverlay {
  obiEnabled?: boolean;
  logsEnabled?: boolean;
  kubeletstatsEnabled?: boolean;
  profilerEnabled?: boolean;
  greenEnabled?: boolean;
  excludeNamespaces?: string[];
}

export interface CollectionEffective {
  obi: boolean;
  logs: boolean;
  kubeletstats: boolean;
  profiler: boolean;
  green: boolean;
  excludeNamespaces: string[];
}

export interface CollectionOverlayResponse {
  overlay: CollectionOverlay;
  effective?: CollectionEffective;
  updatedAt?: string;
  updatedBy?: string;
}
```

- [ ] **Step 2: Query key + hooks**

In `ui/src/lib/query-keys.ts`, next to `capabilities` (line 15), add:

```ts
  collectionOverlay: ["collection", "overlay"] as const,
```

Create `ui/src/hooks/use-collection-overlay.ts` (mirror `use-alerts-data.ts`'s shape):

```ts
"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPut } from "@/lib/api";
import type { CollectionOverlay, CollectionOverlayResponse } from "@/lib/api-types";
import { queryKeys } from "@/lib/query-keys";
import { useCapabilities } from "@/hooks/use-capabilities";

/**
 * True only when the hub reports the runtime-control capability. Defaults to
 * FALSE while capabilities load — the overlay routes 404 when the flag is
 * off, so optimistic gating would flash errors (opposite default from
 * useModuleEnabled, deliberately).
 */
export function useCollectionRuntimeControl(): boolean {
  const { data } = useCapabilities();
  return data?.collectionRuntimeControl === true;
}

export function useCollectionOverlay(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.collectionOverlay,
    queryFn: () => apiGet<CollectionOverlayResponse>("/api/v1/collection/overlay"),
    staleTime: 15_000,
    enabled,
  });
}

function useInvalidateOverlay() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.collectionOverlay });
  };
}

export function useSaveCollectionOverlay() {
  const invalidate = useInvalidateOverlay();
  return useMutation({
    mutationFn: (overlay: CollectionOverlay) =>
      apiPut<CollectionOverlayResponse>("/api/v1/collection/overlay", overlay),
    onSuccess: invalidate,
  });
}

export function useResetCollectionOverlay() {
  const invalidate = useInvalidateOverlay();
  return useMutation({
    mutationFn: () => apiDelete("/api/v1/collection/overlay"),
    onSuccess: invalidate,
  });
}
```

Check `ui/src/hooks/use-capabilities.ts`: `useCapabilities` must be exported (it is — line 11). `apiDelete` treats 200-with-body fine? Read `ui/src/lib/api.ts:109-127` — the hub returns 200 + JSON body on DELETE (not 204); if `apiDelete` only handles 204→void, it still works for 200 by parsing/ignoring — confirm and adjust (worst case use `apiPut`-style fetch or add a response-tolerant variant).

- [ ] **Step 3: The writable card**

Rework `ui/src/components/settings/collection-settings.tsx`. Keep the existing "Sensors" inventory card and the read-only "Deactivating collection" card EXACTLY as-is when runtime control is off (the e2e spec `settings.spec.ts:19-30` asserts its strings). When on, insert a new writable card between them:

```tsx
// Additions at the top of the file:
import { useCollectionOverlay, useCollectionRuntimeControl, useResetCollectionOverlay, useSaveCollectionOverlay } from "@/hooks/use-collection-overlay";
import type { CollectionOverlay } from "@/lib/api-types";
import { useEffect, useState } from "react";

const TOGGLE_FIELDS = [
  { key: "obiEnabled", effectiveKey: "obi", label: "Traces (eBPF auto-instrumentation)" },
  { key: "logsEnabled", effectiveKey: "logs", label: "Logs" },
  { key: "kubeletstatsEnabled", effectiveKey: "kubeletstats", label: "Infrastructure metrics" },
  { key: "profilerEnabled", effectiveKey: "profiler", label: "Continuous profiling" },
  { key: "greenEnabled", effectiveKey: "green", label: "Energy / carbon" },
] as const;

function RuntimeControlCard() {
  const { data, isLoading, error } = useCollectionOverlay(true);
  const save = useSaveCollectionOverlay();
  const reset = useResetCollectionOverlay();
  const [draft, setDraft] = useState<CollectionOverlay | null>(null);
  const [namespaces, setNamespaces] = useState<string>("");
  const [formError, setFormError] = useState<string | null>(null);

  useEffect(() => {
    if (!data) return;
    // Seed the form from EFFECTIVE state (what the sensor actually does),
    // falling back to the stored overlay when effective is unavailable.
    const eff = data.effective;
    setDraft({
      obiEnabled: data.overlay.obiEnabled ?? eff?.obi,
      logsEnabled: data.overlay.logsEnabled ?? eff?.logs,
      kubeletstatsEnabled: data.overlay.kubeletstatsEnabled ?? eff?.kubeletstats,
      profilerEnabled: data.overlay.profilerEnabled ?? eff?.profiler,
      greenEnabled: data.overlay.greenEnabled ?? eff?.green,
    });
    setNamespaces((data.overlay.excludeNamespaces ?? eff?.excludeNamespaces ?? []).join("\n"));
  }, [data]);

  const submit = () => {
    setFormError(null);
    const nsList = namespaces.split("\n").map((s) => s.trim()).filter(Boolean);
    const overlay: CollectionOverlay = { ...draft, excludeNamespaces: nsList };
    save.mutate(overlay, { onError: (e) => setFormError(errMessage(e, "Saving the overlay failed")) });
  };

  // Render: one daisyUI toggle row per TOGGLE_FIELDS entry bound to draft,
  // a textarea (one namespace per line) bound to namespaces, Save +
  // "Reset to chart defaults" buttons, updatedBy/updatedAt caption when set,
  // and formError as <p className="text-xs text-error">.
}
```

Follow the file's existing Card/CardHeader components and the `inputClass` pattern from `project-settings-card.tsx:19-20` (redeclare locally, matching the codebase's convention). `errMessage` — import from wherever `project-settings-card.tsx` imports it (check its imports; it's the shared helper described in `agent_docs/ui_patterns.md`). The reset button calls `reset.mutate` with a `window.confirm("Reset collection to chart defaults?")` guard, consistent with `DangerZone`'s confirm in `project-settings-card.tsx:190-224`.

In the main `CollectionSettings` component, call `const runtimeControl = useCollectionRuntimeControl();` and render `{runtimeControl && <RuntimeControlCard />}` between the two existing cards. Also update the stale header comment at lines 29-32 (drop the "arrive with the OpAMP control plane (v0.2)" sentence — say runtime toggles are available when the chart enables `collection.runtimeControl`) and the matching subtitle string at line 96 — BUT check `ui/e2e/settings.spec.ts:19-22` first: it asserts the literal strings "Deactivating collection" and "sensor.collection.excludeNamespaces" — keep those two intact; the sentence about OpAMP/v0.2 at line 96 is safe to replace with "Runtime switches appear here when the chart enables collection.runtimeControl."

- [ ] **Step 4: Playwright spec (route interception — copy `projects-admin.spec.ts`'s style)**

```ts
// ui/e2e/collection-admin.spec.ts
import { expect, test } from "@playwright/test";

// Route-intercepted: no hub writes needed. Mirrors projects-admin.spec.ts.
test.describe("collection runtime control", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({
        json: { version: "test", modules: ["traces", "logs", "infra-metrics"], collectionRuntimeControl: true },
      }),
    );
    let overlay: Record<string, unknown> = {};
    await page.route("**/api/v1/collection/overlay", async (route) => {
      const method = route.request().method();
      if (method === "GET") {
        await route.fulfill({
          json: {
            overlay,
            effective: { obi: true, logs: overlay["logsEnabled"] !== false, kubeletstats: true, profiler: false, green: false, excludeNamespaces: ["kube-system"] },
          },
        });
      } else if (method === "PUT") {
        overlay = route.request().postDataJSON() as Record<string, unknown>;
        await route.fulfill({ json: { overlay } });
      } else {
        overlay = {};
        await route.fulfill({ json: { overlay } });
      }
    });
  });

  test("toggles a signal off and saves", async ({ page }) => {
    await page.goto("/settings?tab=collection");
    const logsToggle = page.getByRole("checkbox", { name: "Logs" });
    await expect(logsToggle).toBeChecked();
    await logsToggle.uncheck();
    await page.getByRole("button", { name: "Save" }).click();
    await expect(logsToggle).not.toBeChecked();
  });

  test("reset returns to chart defaults", async ({ page }) => {
    page.on("dialog", (d) => void d.accept());
    await page.goto("/settings?tab=collection");
    await page.getByRole("button", { name: "Reset to chart defaults" }).click();
    await expect(page.getByRole("checkbox", { name: "Logs" })).toBeChecked();
  });

  test("card absent without the capability", async ({ page }) => {
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({ json: { version: "test", modules: ["traces"], collectionRuntimeControl: false } }),
    );
    await page.goto("/settings?tab=collection");
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0);
    await expect(page.getByText("Deactivating collection")).toBeVisible();
  });
});
```

Bind each toggle's accessible name via a `<label>` wrapping or `aria-label` on the checkbox — whichever the card implementation used; adjust the locators to match. If `projects-admin.spec.ts` stubs `**/api/v1/auth/me` for admin (it does, `:11-20`), copy that `stubAdmin` block into the `beforeEach` too.

- [ ] **Step 5: Validate UI**

Run: `cd ui && npm run lint && npm run typecheck && npm run build`
Expected: all PASS (static export must succeed).

Playwright against the dev loop (ports 3000/3001 are taken on this machine — use 3005):
```bash
cd ui && (npx next dev -p 3005 &) && sleep 6 && AVURUOBS_BASE_URL=http://localhost:3005 npx playwright test e2e/collection-admin.spec.ts e2e/settings.spec.ts; kill %1
```
Expected: new spec passes AND the existing `settings.spec.ts` still passes (its literal-string assertions were preserved).

- [ ] **Step 6: Commit**

```bash
git add ui/src/lib/api-types.ts ui/src/lib/query-keys.ts ui/src/hooks/use-collection-overlay.ts \
        ui/src/hooks/use-capabilities.ts ui/src/components/settings/collection-settings.tsx \
        ui/e2e/collection-admin.spec.ts
git commit -m "feat(ui): writable Settings → Collection card behind the runtime-control capability"
```

---

### Task 7: kind e2e — overlay applies on a real cluster

**Files:**
- Modify: `deploy/helm/e2e-helm.sh`
- Create: `e2e/collection_test.go`

- [ ] **Step 1: Enable the flag in the kind install**

In `deploy/helm/e2e-helm.sh`, add to the `helm install` `--set` list (after the auth line at ~`:127`):

```bash
  --set collection.runtimeControl.enabled=true \
```

- [ ] **Step 2: Go-side API test**

```go
// e2e/collection_test.go
//go:build e2ehelm

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCollectionOverlayApplies exercises the runtime collection control
// plane end to end: PUT an overlay through the hub API and assert the API
// reflects it (cluster-side assertions — ConfigMap content, DaemonSet
// annotation — live in e2e-helm.sh where kubectl is available).
func TestCollectionOverlayApplies(t *testing.T) {
	c := adminClient(t) // reuse the cookie-carrying helper from helm_test.go — check its actual name first

	// GET before: no overlay saved.
	var before struct {
		Overlay   map[string]any `json:"overlay"`
		Effective *struct {
			Logs bool `json:"logs"`
		} `json:"effective"`
	}
	helmGetJSONAuth(t, c, "/api/v1/collection/overlay", &before)
	if before.Effective == nil || !before.Effective.Logs {
		t.Fatalf("expected effective.logs=true before overlay, got %+v", before.Effective)
	}

	// PUT: logs off + exclude an extra namespace.
	body, _ := json.Marshal(map[string]any{
		"logsEnabled":       false,
		"excludeNamespaces": []string{"kube-system", "kube-node-lease", "kube-public", "wedge-demo"},
	})
	req, err := http.NewRequest(http.MethodPut, hubURL+"/api/v1/collection/overlay", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT overlay = %d, want 200", resp.StatusCode)
	}

	// GET after: overlay + effective reflect the change (poll — the applier
	// runs synchronously but give the API a deadline anyway).
	deadline := time.Now().Add(30 * time.Second)
	for {
		var after struct {
			Effective *struct {
				Logs bool `json:"logs"`
			} `json:"effective"`
		}
		helmGetJSONAuth(t, c, "/api/v1/collection/overlay", &after)
		if after.Effective != nil && !after.Effective.Logs {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("effective.logs still true %s after PUT", 30*time.Second)
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Println("collection overlay accepted and effective config updated")
}
```

Before writing, read `e2e/helm_test.go` and `e2e/auth_helpers_test.go` for the REAL helper names (`helmGetJSON`, the admin-login client, `hubURL`) and use those — the names above (`adminClient`, `helmGetJSONAuth`) are placeholders to be replaced with whatever exists; if no authenticated-GET helper exists, build the request explicitly with the login cookie like other `e2ehelm` tests do.

- [ ] **Step 3: Cluster-side assertions in the script**

In `deploy/helm/e2e-helm.sh`, AFTER the do-no-harm gate (`:236-278` — the overlay rollout must not disturb the canary soak), add:

```bash
# --- Runtime collection control: overlay actually lands on the cluster ---
echo "=== runtime collection control: apply overlay via hub API and verify cluster state"
go test -tags=e2ehelm -count=1 -run TestCollectionOverlayApplies -v ./... || fail "collection overlay API test failed"

CHECKSUM=$(kubectl -n "$NS" get ds avuruobs-sensor -o jsonpath='{.spec.template.metadata.annotations.avuru\.obs/overlay-checksum}')
[ -n "$CHECKSUM" ] || fail "sensor DaemonSet missing avuru.obs/overlay-checksum after overlay PUT"

kubectl -n "$NS" get cm avuruobs-sensor-agent -o yaml | grep -q "filelog" \
  && fail "agent ConfigMap still contains filelog receiver after logsEnabled=false"

kubectl -n "$NS" get cm avuruobs-sensor-obi -o yaml | grep -q "wedge-demo" \
  || fail "obi ConfigMap missing the overlay-added excluded namespace"

kubectl -n "$NS" rollout status ds/avuruobs-sensor --timeout=180s || fail "sensor rollout after overlay did not complete"
echo "runtime collection control OK (checksum=$CHECKSUM)"
```

Match the script's real variable names and error style first: it may use `$NS`/`$NAMESPACE` and a `fail()` helper or plain `exit 1` — read `deploy/helm/e2e-helm.sh` around lines 100-240 and mirror it exactly (the Go-test invocation must also match how `:148` and `:180` invoke the earlier tests — same working dir and env).

- [ ] **Step 4: Run the full kind gate locally**

Run: `make e2e-helm`
Expected: the wedge assertions, seeded tests, green checks, do-no-harm gate, AND the new collection section all pass. Budget ~20 min. If kind/docker resources are a problem on this machine, at minimum run `bash -n deploy/helm/e2e-helm.sh` (syntax), `cd e2e && go vet -tags=e2ehelm ./...`, and leave the full run to CI — say so explicitly in the task report.

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/e2e-helm.sh e2e/collection_test.go
git commit -m "test(e2e): runtime collection overlay applies on the kind cluster"
```

---

### Task 8: Docs + changelog + AEP checkboxes + final verification

**Files:**
- Modify: `deploy/helm/README.md`
- Modify: `CHANGELOG.md`
- Modify: `design/2026-07-27-collection-control-plane.md`
- Modify: `THIRD-PARTY-NOTICES.md` (regenerated)

- [ ] **Step 1: Helm README section**

In `deploy/helm/README.md`, find the collection/sensor configuration section and add a "Runtime collection control" subsection documenting: what `collection.runtimeControl.enabled=true` turns on (Settings → Collection becomes writable for the five signal toggles + excluded namespaces), the RBAC it grants (namespaced Role, named ConfigMaps + DaemonSet only, no create/delete), that toggles roll the sensor DaemonSet, precedence (chart values are the base; the overlay is a UI-owned layer; "Reset to chart defaults" clears it; a module disabled at install cannot be enabled from the UI), and that pod/node label opt-out remains a `kubectl label` operation. Keep the existing style of the file (read the OIDC or ingest-keys sections for tone).

- [ ] **Step 2: Changelog**

Under `## [Unreleased]` in `CHANGELOG.md` (line 12), add:

```markdown
### Added

- **Runtime collection control — the applier.** Flipping a collection switch in
  Settings → Collection now takes effect on the cluster: the hub renders the
  sensor configuration from the chart's own templates with your saved overlay
  applied, updates the sensor ConfigMaps, and rolls the sensor DaemonSet via a
  hub-owned checksum annotation — no `helm upgrade`, no kubectl. Identical
  saves are detected and cause no restart. The Settings → Collection page gains
  writable switches for the five signal families and the excluded-namespaces
  list, plus "Reset to chart defaults"; the API's `GET /api/v1/collection/overlay`
  now reports the effective (base ⊕ overlay) configuration. Everything stays
  behind the default-off `collection.runtimeControl.enabled` flag with the same
  least-privilege, named-resources RBAC shipped in v0.3.0.
  ([AEP](design/2026-07-27-collection-control-plane.md))
```

- [ ] **Step 3: AEP roadmap checkboxes**

In `design/2026-07-27-collection-control-plane.md`, mark the roadmap items now done (applier; flag/chart items already checked or checkable; Settings → Collection writable; unit + kind e2e). Leave the OpAMP follow-up section untouched. Do NOT check "docs-align (EN/FR)" — the docs site runs once for the whole v0.4 release.

- [ ] **Step 4: Notices (new Go deps)**

Run: `make notices`
Expected: `THIRD-PARTY-NOTICES.md` regenerates including the new helm/k8s dependencies. Commit whatever changed.

- [ ] **Step 5: Competitor-name sweep (user-facing prose changed in this plan)**

```bash
grep -rin "coroot\|skywalking\|kiali\|datadog\|signoz\|uptrace\|dynatrace\|new relic" \
  CHANGELOG.md README.md ROADMAP.md deploy/helm/README.md docs/
```
Expected: no hits in the text this plan added (pre-existing hits in `docs/superpowers/` planning docs are internal and fine — the sweep targets shipped prose; if the grep flags the new README/CHANGELOG text, rewrite it). Also re-read the new prose for comparison phrasing without a name.

- [ ] **Step 6: Full validation**

```bash
make check                      # hub build+test, gateway/sensor modules, ui lint+build
cd hub && golangci-lint run     # not covered by make check
make helm-check
```
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add deploy/helm/README.md CHANGELOG.md design/2026-07-27-collection-control-plane.md THIRD-PARTY-NOTICES.md
git commit -m "docs: runtime collection control — applier + writable UI shipped"
```

---

## Execution notes

- Branch: `feature/collection-runtime-apply` (worktree `.claude/worktrees/collection-runtime-apply`), based on `main` @ `c1b561a`. Push with `git push -u origin feature/collection-runtime-apply`; PR → `main`. **Signed commits, no AI trailers** (AGENTS.md).
- The riskiest task is 3 (helm engine + embedded chart) — if the helm dependency turns into a version-resolution swamp with Go 1.26, STOP and reconsider (fallback: vendor just `pkg/engine`'s template-func setup, ~200 lines) rather than forcing incompatible pins.
- Task order matters: 1 → 2 → 3 → 4 → 5 (hub complete) → 6 (UI) → 7 (e2e) → 8 (docs). Tasks 6 and 7 are independent of each other.
- After every task: run that component's validation before committing (AGENTS.md table).
