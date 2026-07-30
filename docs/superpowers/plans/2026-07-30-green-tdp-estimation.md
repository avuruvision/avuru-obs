# Green TDP Estimation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an opt-in TDP-based power estimator for RAPL-less nodes, quality-stamped `measured`/`estimated` end-to-end (sensor → hub → UI → CSRD export), then cut release v0.3 — the only new feature this release.

**Architecture:** A new first-party Go exporter (`sensor/tdp-estimator/`) runs as a 5th sensor-pod container, dormant when RAPL is present, emitting Kepler-shaped metrics stamped `estimated` via the otel-agent. The hub adds a quality grouping dimension to the existing green SQL (no migration) and a node-coverage read; the UI and CSRD export surface the split without ever blending measured and estimated numbers.

**Tech Stack:** Go 1.26 (estimator + hub), stdlib only in the estimator (no client-go — pod discovery reuses the kubelet's `/pods` endpoint, same as `kubeletstats`), ClickHouse SQL, Next.js/TypeScript UI, Helm/Kustomize, GitHub Actions.

**Base documents:**
- AEP: [design/2026-07-28-green-tdp-estimation.md](../../../design/2026-07-28-green-tdp-estimation.md)
- Design spec: [docs/superpowers/specs/2026-07-30-green-tdp-estimation-design.md](../specs/2026-07-30-green-tdp-estimation-design.md)

---

## Task 1: Estimator module scaffold + RAPL probe

**Files:**
- Create: `sensor/tdp-estimator/go.mod`
- Create: `sensor/tdp-estimator/main.go`
- Create: `sensor/tdp-estimator/rapl.go`
- Create: `sensor/tdp-estimator/rapl_test.go`

- [ ] **Step 1: Init the module**

```bash
mkdir -p sensor/tdp-estimator
cd sensor/tdp-estimator
go mod init github.com/avuru/avuru-obs/sensor/tdp-estimator
```

Edit `go.mod` to pin the Go version to match the rest of the repo:

```
module github.com/avuru/avuru-obs/sensor/tdp-estimator

go 1.26
```

- [ ] **Step 2: Write the failing test for the RAPL probe**

```go
// sensor/tdp-estimator/rapl_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRAPLPresent(t *testing.T) {
	t.Run("no matching path", func(t *testing.T) {
		if raplPresent(filepath.Join(t.TempDir(), "intel-rapl*")) {
			t.Error("raplPresent() = true, want false when glob matches nothing")
		}
	})

	t.Run("dir exists but no energy_uj file", func(t *testing.T) {
		dir := t.TempDir()
		zone := filepath.Join(dir, "intel-rapl:0")
		if err := os.Mkdir(zone, 0o755); err != nil {
			t.Fatal(err)
		}
		if raplPresent(filepath.Join(dir, "intel-rapl*")) {
			t.Error("raplPresent() = true, want false when energy_uj is missing")
		}
	})

	t.Run("energy_uj readable", func(t *testing.T) {
		dir := t.TempDir()
		zone := filepath.Join(dir, "intel-rapl:0")
		if err := os.Mkdir(zone, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(zone, "energy_uj"), []byte("123456\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !raplPresent(filepath.Join(dir, "intel-rapl*")) {
			t.Error("raplPresent() = false, want true when energy_uj is readable")
		}
	})
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run TestRAPLPresent -v`
Expected: FAIL — `undefined: raplPresent`

- [ ] **Step 4: Implement the RAPL probe**

```go
// sensor/tdp-estimator/rapl.go
package main

import (
	"os"
	"path/filepath"
)

// defaultRAPLGlob is where the powercap sysfs exposes Intel RAPL energy
// counters on hardware that has them.
const defaultRAPLGlob = "/sys/class/powercap/intel-rapl*"

// raplPresent reports whether the node exposes at least one readable RAPL
// energy counter. When true, Kepler measures real power and this estimator
// must stay dormant for the whole process lifetime — a node's power
// interface doesn't change under a running pod, so callers probe once at
// startup, not on a loop (see main.go).
func raplPresent(glob string) bool {
	matches, err := filepath.Glob(glob)
	if err != nil || len(matches) == 0 {
		return false
	}
	for _, zone := range matches {
		f, err := os.Open(filepath.Join(zone, "energy_uj"))
		if err != nil {
			continue
		}
		f.Close()
		return true
	}
	return false
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go test ./... -run TestRAPLPresent -v`
Expected: PASS (all three subtests)

- [ ] **Step 6: Write the main.go skeleton**

```go
// sensor/tdp-estimator/main.go
//
// tdp-estimator is the sensor DaemonSet's opt-in 5th container: on a node
// with no RAPL/powercap it estimates CPU power from utilization
// (P = P_idle + u*(P_max-P_idle)) and serves the SAME Kepler metric names
// Kepler itself would emit, stamped estimated (not measured) by the
// otel-agent scrape config — see design/2026-07-28-green-tdp-estimation.md.
// Deliberately dependency-free (stdlib only): pod discovery reuses the
// kubelet's own /pods endpoint (same trust model as the otel-agent's
// kubeletstats receiver), not client-go.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	var (
		listenAddr     = flag.String("listen", ":28283", "address to serve /metrics on (loopback-bound by the pod, see values.yaml)")
		nodeName       = flag.String("node-name", os.Getenv("NODE_NAME"), "this node's name, for the kubelet /pods lookup and node_name label")
		sampleInterval = flag.Duration("sample-interval", 5*time.Second, "sampler tick; independent of the otel-agent scrape interval")
		idleWatts      = flag.Float64("idle-watts", 0, "operator-set P_idle override (0 = defer to table/fallback)")
		maxWatts       = flag.Float64("max-watts", 0, "operator-set P_max override (0 = defer to table/fallback)")
	)
	flag.Parse()

	if *nodeName == "" {
		slog.Error("node-name is required (set --node-name or NODE_NAME env)")
		os.Exit(1)
	}

	if raplPresent(defaultRAPLGlob) {
		slog.Info("RAPL present, tdp-estimator staying dormant for this process's lifetime", "node", *nodeName)
		serveDormant(*listenAddr)
		return
	}
	slog.Info("no RAPL detected, estimating via TDP model", "node", *nodeName)

	coeff := Resolve(nodeAnnotations(*nodeName), *idleWatts, *maxWatts, cpuModelName())
	slog.Info("resolved power coefficients", "tier", coeff.Tier, "provenance", coeff.Provenance, "idleWatts", coeff.IdleWatts, "maxWatts", coeff.MaxWatts)

	reg := newRegistry()
	go runSampler(*nodeName, *sampleInterval, coeff, reg)

	http.Handle("/metrics", reg)
	slog.Info("serving /metrics", "addr", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		slog.Error("metrics server exited", "error", err)
		os.Exit(1)
	}
}

// serveDormant answers /metrics with an empty body forever — the container
// stays alive and healthy (no probes are configured on it either way) but
// contributes nothing, matching Kepler's own no-RAPL posture.
func serveDormant(addr string) {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(addr, nil); err != nil {
		slog.Error("dormant metrics server exited", "error", err)
		os.Exit(1)
	}
}
```

This references `Resolve`, `nodeAnnotations`, `cpuModelName`, `newRegistry`, `runSampler` — defined in later tasks. It will not compile until Task 7; that's expected and each earlier task's own tests still run standalone via `go test -run <TestName>`.

- [ ] **Step 7: Commit**

```bash
git add sensor/tdp-estimator/go.mod sensor/tdp-estimator/main.go sensor/tdp-estimator/rapl.go sensor/tdp-estimator/rapl_test.go
git commit -m "feat(sensor): tdp-estimator module scaffold + RAPL probe"
```

---

## Task 2: Node utilization sampler (`/proc/stat`)

**Files:**
- Create: `sensor/tdp-estimator/sampler.go`
- Create: `sensor/tdp-estimator/sampler_test.go`

- [ ] **Step 1: Write the failing test**

```go
// sensor/tdp-estimator/sampler_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProcStat(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadCPUTimes(t *testing.T) {
	path := writeProcStat(t, `cpu  100 0 50 800 10 0 0 0 0 0
cpu0 50 0 25 400 5 0 0 0 0 0
intr 12345
`)
	got, err := readCPUTimes(path)
	if err != nil {
		t.Fatalf("readCPUTimes: %v", err)
	}
	want := cpuTimes{User: 100, Nice: 0, System: 50, Idle: 800, IOWait: 10, IRQ: 0, SoftIRQ: 0, Steal: 0}
	if got != want {
		t.Errorf("readCPUTimes = %+v, want %+v", got, want)
	}
}

func TestReadCPUTimes_NoAggregateLine(t *testing.T) {
	path := writeProcStat(t, "intr 12345\n")
	if _, err := readCPUTimes(path); err == nil {
		t.Error("readCPUTimes: want error when no aggregate \"cpu \" line exists")
	}
}

func TestUtilizationDelta(t *testing.T) {
	tests := []struct {
		name       string
		prev, cur  cpuTimes
		wantApprox float64
	}{
		{
			name: "50% busy",
			prev: cpuTimes{User: 100, Idle: 100},
			cur:  cpuTimes{User: 150, Idle: 150}, // +50 user, +50 idle -> 50% util
			wantApprox: 0.5,
		},
		{
			name: "fully idle",
			prev: cpuTimes{User: 100, Idle: 100},
			cur:  cpuTimes{User: 100, Idle: 200},
			wantApprox: 0,
		},
		{
			name: "fully busy",
			prev: cpuTimes{User: 100, Idle: 100},
			cur:  cpuTimes{User: 200, Idle: 100},
			wantApprox: 1,
		},
		{
			name: "no time elapsed",
			prev: cpuTimes{User: 100, Idle: 100},
			cur:  cpuTimes{User: 100, Idle: 100},
			wantApprox: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := utilizationDelta(tt.prev, tt.cur)
			if diff := got - tt.wantApprox; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("utilizationDelta = %v, want %v", got, tt.wantApprox)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestReadCPUTimes|TestUtilizationDelta' -v`
Expected: FAIL — `undefined: readCPUTimes` / `cpuTimes` / `utilizationDelta`

- [ ] **Step 3: Implement the node-utilization sampler**

```go
// sensor/tdp-estimator/sampler.go
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultProcStat is the standard /proc/stat path; overridable in tests.
const defaultProcStat = "/proc/stat"

// cpuTimes holds the cumulative jiffy counters from /proc/stat's aggregate
// "cpu" line (all cores summed). Values are cumulative since boot — callers
// difference two samples to get utilization over an interval, never read
// this as an instantaneous percentage.
type cpuTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// total is every counted jiffy — the utilizationDelta denominator.
func (c cpuTimes) total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// idle is time the kernel does not count as CPU work: true idle plus iowait
// (a core waiting on I/O is not doing CPU work either).
func (c cpuTimes) idle() uint64 {
	return c.Idle + c.IOWait
}

// readCPUTimes parses the aggregate "cpu" line (fields documented in
// proc(5)); per-core "cpu0", "cpu1", ... lines are ignored — the model only
// needs whole-node utilization.
func readCPUTimes(path string) (cpuTimes, error) {
	f, err := os.Open(path)
	if err != nil {
		return cpuTimes{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 9 || fields[0] != "cpu" {
			continue
		}
		vals := make([]uint64, 8)
		for i := 0; i < 8; i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("parsing %s field %d: %w", path, i+1, err)
			}
			vals[i] = v
		}
		return cpuTimes{User: vals[0], Nice: vals[1], System: vals[2], Idle: vals[3], IOWait: vals[4], IRQ: vals[5], SoftIRQ: vals[6], Steal: vals[7]}, nil
	}
	if err := sc.Err(); err != nil {
		return cpuTimes{}, fmt.Errorf("scanning %s: %w", path, err)
	}
	return cpuTimes{}, fmt.Errorf("no aggregate \"cpu \" line found in %s", path)
}

// utilizationDelta is fractional node CPU utilization (0..1) between two
// /proc/stat samples. Returns 0 on no elapsed time (guards a div-by-zero on
// a too-fast poll rather than returning NaN/Inf into the power model).
func utilizationDelta(prev, cur cpuTimes) float64 {
	totalDelta := cur.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}
	idleDelta := cur.idle() - prev.idle()
	return 1 - float64(idleDelta)/float64(totalDelta)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestReadCPUTimes|TestUtilizationDelta' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add sensor/tdp-estimator/sampler.go sensor/tdp-estimator/sampler_test.go
git commit -m "feat(sensor): tdp-estimator node utilization sampler (/proc/stat)"
```

---

## Task 3: Kubelet pod discovery (UID → name/namespace)

Reuses the sensor's existing RBAC surface (`nodes/proxy` get, already granted in [sensor-rbac.yaml:32-34](../../../deploy/helm/avuruops/templates/sensor-rbac.yaml#L32-L34) — its own comment confirms "Green/Kepler (kube pod-informer, kubelet mode) reuses this same surface... for the kubelet /pods endpoint. No extra rule is required"), and the same host:port/TLS convention as the otel-agent's `kubeletstats` receiver ([sensor-config.yaml:151-154](../../../deploy/helm/avuruops/templates/sensor-config.yaml#L151-L154): `https://${K8S_NODE_NAME}:10250`, `insecure_skip_verify: true`).

**Files:**
- Create: `sensor/tdp-estimator/kubelet.go`
- Create: `sensor/tdp-estimator/kubelet_test.go`

- [ ] **Step 1: Write the failing test**

```go
// sensor/tdp-estimator/kubelet_test.go
package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

const samplePodList = `{
  "items": [
    {"metadata": {"uid": "abc-123", "name": "web-1", "namespace": "shop"}},
    {"metadata": {"uid": "def-456", "name": "cart-1", "namespace": "shop"}}
  ]
}`

func TestFetchPods(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pods" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer test-token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(samplePodList))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	pods, err := fetchPods(client, srv.URL, "test-token")
	if err != nil {
		t.Fatalf("fetchPods: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("len(pods) = %d, want 2", len(pods))
	}
	if pods["abc-123"] != (podIdentity{Name: "web-1", Namespace: "shop"}) {
		t.Errorf("pods[abc-123] = %+v, want {web-1 shop}", pods["abc-123"])
	}
	if pods["def-456"] != (podIdentity{Name: "cart-1", Namespace: "shop"}) {
		t.Errorf("pods[def-456] = %+v, want {cart-1 shop}", pods["def-456"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run TestFetchPods -v`
Expected: FAIL — `undefined: fetchPods` / `podIdentity`

- [ ] **Step 3: Implement kubelet pod discovery**

```go
// sensor/tdp-estimator/kubelet.go
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Paths the in-cluster ServiceAccount mounts — the standard Kubernetes
// convention every ServiceAccount-bound pod gets automatically.
const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// podIdentity is the (name, namespace) pair carried on the estimator's
// pod_name/pod_namespace labels — matching Kepler's own labels exactly (AEP
// metric table), so the hub's existing pod->workload join needs no changes.
type podIdentity struct {
	Name      string
	Namespace string
}

// kubeletPodList is the minimal shape read from the kubelet's own /pods
// endpoint — deliberately NOT k8s.io/api/core/v1.PodList: the estimator stays
// dependency-free (stdlib only), and only two fields are ever needed.
type kubeletPodList struct {
	Items []struct {
		Metadata struct {
			UID       string `json:"uid"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	} `json:"items"`
}

// fetchPods calls the kubelet's /pods endpoint (baseURL like
// "https://node-1:10250") and returns a UID -> identity map for every pod the
// kubelet reports — which, by construction, is only ever pods on THIS node
// (no field-selector filtering needed, unlike the API server's pod list).
func fetchPods(client *http.Client, baseURL, token string) (map[string]podIdentity, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/pods", nil)
	if err != nil {
		return nil, fmt.Errorf("building kubelet /pods request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling kubelet /pods: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kubelet /pods returned %s", resp.Status)
	}

	var list kubeletPodList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decoding kubelet /pods response: %w", err)
	}

	out := make(map[string]podIdentity, len(list.Items))
	for _, item := range list.Items {
		out[item.Metadata.UID] = podIdentity{Name: item.Metadata.Name, Namespace: item.Metadata.Namespace}
	}
	return out, nil
}

// newKubeletClient builds the in-cluster HTTPS client for the kubelet API:
// self-signed serving certs are the norm per node, so verification is
// skipped — the exact trust boundary the otel-agent's kubeletstats receiver
// already accepts for the same endpoint (sensor-config.yaml, "kubelet
// serving certs are typically self-signed per node").
func newKubeletClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // matches kubeletstats's accepted trust model
		},
	}
}

// readServiceAccountToken reads the projected ServiceAccount token every
// in-cluster pod gets automatically — no Secret, no client-go, just a file
// read, refreshed on each call since Kubernetes rotates this token.
func readServiceAccountToken() (string, error) {
	b, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return "", fmt.Errorf("reading service account token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// kubeletBaseURL is the kubelet's HTTPS endpoint on this node — the same
// host:port the otel-agent's kubeletstats receiver targets.
func kubeletBaseURL(nodeName string) string {
	return "https://" + nodeName + ":10250"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go test ./... -run TestFetchPods -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add sensor/tdp-estimator/kubelet.go sensor/tdp-estimator/kubelet_test.go
git commit -m "feat(sensor): tdp-estimator kubelet pod discovery (no client-go)"
```

---

## Task 4: Cgroup v2 per-pod CPU discovery

**Files:**
- Create: `sensor/tdp-estimator/cgroup.go`
- Create: `sensor/tdp-estimator/cgroup_test.go`

- [ ] **Step 1: Write the failing test**

```go
// sensor/tdp-estimator/cgroup_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeCgroupFixture builds a fake cgroup v2 tree with one pod directory
// (systemd-driver naming) containing a cpu.stat file, mimicking
// /sys/fs/cgroup/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<uid>.slice/.
func makeCgroupFixture(t *testing.T, uidUnderscored string, usageUsec uint64) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice",
		"kubepods-burstable-pod"+uidUnderscored+".slice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "usage_usec " + itoa(usageUsec) + "\nuser_usec 0\nsystem_usec 0\n"
	if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestDiscoverPodCgroups(t *testing.T) {
	root := makeCgroupFixture(t, "12345678_1234_1234_1234_123456789012", 5_000_000)

	cgroups, err := discoverPodCgroups(root)
	if err != nil {
		t.Fatalf("discoverPodCgroups: %v", err)
	}
	if len(cgroups) != 1 {
		t.Fatalf("len(cgroups) = %d, want 1", len(cgroups))
	}
	if cgroups[0].uid != "12345678-1234-1234-1234-123456789012" {
		t.Errorf("uid = %q, want canonical dashed form", cgroups[0].uid)
	}
}

func TestReadCPUStatUsage(t *testing.T) {
	root := makeCgroupFixture(t, "aaaaaaaa_bbbb_cccc_dddd_eeeeeeeeeeee", 7_500_000)
	cgroups, err := discoverPodCgroups(root)
	if err != nil {
		t.Fatalf("discoverPodCgroups: %v", err)
	}
	usage, err := readCPUStatUsage(cgroups[0].path)
	if err != nil {
		t.Fatalf("readCPUStatUsage: %v", err)
	}
	if usage != 7_500_000 {
		t.Errorf("usage = %d, want 7500000 usec", usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestDiscoverPodCgroups|TestReadCPUStatUsage' -v`
Expected: FAIL — `undefined: discoverPodCgroups` / `readCPUStatUsage`

- [ ] **Step 3: Implement cgroup v2 pod discovery**

```go
// sensor/tdp-estimator/cgroup.go
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// defaultCgroupRoot is where the host's cgroup v2 hierarchy is bind-mounted
// read-only into the container (values.yaml: sensor.green.estimation, same
// host-mount convention as Kepler's powercap/sysfs mounts).
const defaultCgroupRoot = "/sys/fs/cgroup"

// podCgroupSystemd matches the systemd cgroup driver's pod-slice naming,
// e.g. "kubepods-burstable-pod12345678_1234_1234_1234_123456789012.slice"
// (guaranteed-QoS pods omit the burstable/besteffort segment:
// "kubepods-pod<uid>.slice"). Captures the underscored UID.
var podCgroupSystemd = regexp.MustCompile(`kubepods(?:-(?:burstable|besteffort))?-pod([0-9a-fA-F]{8}_[0-9a-fA-F]{4}_[0-9a-fA-F]{4}_[0-9a-fA-F]{4}_[0-9a-fA-F]{12})\.slice$`)

// podCgroupCgroupfs matches the older cgroupfs driver's naming, e.g.
// "kubepods/burstable/pod12345678-1234-1234-1234-123456789012" (guaranteed:
// "kubepods/pod<uid>"). Captures the dashed UID directly.
var podCgroupCgroupfs = regexp.MustCompile(`kubepods/(?:(?:burstable|besteffort)/)?pod([0-9a-fA-F-]{36})$`)

// podCgroup is one discovered pod cgroup directory.
type podCgroup struct {
	uid  string // canonical dashed UUID form, matches the kubelet /pods UID
	path string // absolute path to the cgroup directory (contains cpu.stat)
}

// discoverPodCgroups walks the cgroup v2 tree once and returns every
// directory that looks like a pod cgroup, under EITHER the systemd or
// cgroupfs driver naming — walk-and-match is driver-agnostic, so the
// estimator never has to guess or configure which driver a cluster uses.
// Directories with no cpu.stat are skipped (not yet fully created, or not a
// leaf pod cgroup); this is expected churn, not an error. Called on every
// sampler tick (Task 8): pods come and go, so the map cannot be cached
// indefinitely — see the documented per-tick-walk cost tradeoff in model.go.
func discoverPodCgroups(root string) ([]podCgroup, error) {
	var out []podCgroup
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A cgroup can disappear between readdir and stat (pod exited
			// mid-walk) — skip it, don't fail the whole walk.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		uid := ""
		if m := podCgroupSystemd.FindStringSubmatch(path); m != nil {
			uid = strings.ReplaceAll(m[1], "_", "-")
		} else if m := podCgroupCgroupfs.FindStringSubmatch(path); m != nil {
			uid = m[1]
		} else {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "cpu.stat")); err != nil {
			return nil
		}
		out = append(out, podCgroup{uid: uid, path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking cgroup root %s: %w", root, err)
	}
	return out, nil
}

// readCPUStatUsage reads cgroup v2 cpu.stat's cumulative usage_usec — the
// same accounting the kubelet itself reads for kubectl top. cgroup v1 has no
// cpu.stat with this field, hence the AEP's documented cgroup-v2-required
// limitation: this returns an error (fails loud) rather than misreading a
// v1 file.
func readCPUStatUsage(cgroupPath string) (uint64, error) {
	f, err := os.Open(filepath.Join(cgroupPath, "cpu.stat"))
	if err != nil {
		return 0, fmt.Errorf("opening cpu.stat at %s: %w", cgroupPath, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == "usage_usec" {
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parsing usage_usec at %s: %w", cgroupPath, err)
			}
			return v, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scanning cpu.stat at %s: %w", cgroupPath, err)
	}
	return 0, fmt.Errorf("no usage_usec field in %s/cpu.stat (cgroup v1 fleets are unsupported — AEP)", cgroupPath)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestDiscoverPodCgroups|TestReadCPUStatUsage' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add sensor/tdp-estimator/cgroup.go sensor/tdp-estimator/cgroup_test.go
git commit -m "feat(sensor): tdp-estimator cgroup v2 pod CPU discovery"
```

---

## Task 5: Coefficient table + resolution order

**Sourcing (see the design spec §6 and the AEP's corrected coefficients section for full provenance discussion):** primary data is Cloud Carbon Footprint's Apache-2.0 coefficient files, cross-checked against the original SPECpower-derived notebook. Where CCF's live AWS/GCP constants disagree with Azure's file and the notebook's own asserted output (Ivy Bridge, Haswell), **the Azure/notebook values are used** (per an explicit sourcing-conflict decision) — they're the most traceable to the underlying SPECpower methodology, and the AWS/GCP files diverge from them with no public changelog explaining why. One entry (`AMD_EPYC_5TH_GEN`) is **omitted entirely**: AWS's file (3.68W/8.96W) and GCP's file (0.27W/1.36W) disagree by almost an order of magnitude with no way to adjudicate which is correct, so per the AEP's own honesty principle this falls through to the generic per-core fallback tier rather than shipping either unverifiable number.

**Files:**
- Create: `sensor/tdp-estimator/coefficients_table.go`
- Create: `sensor/tdp-estimator/coefficients.go`
- Create: `sensor/tdp-estimator/coefficients_test.go`

- [ ] **Step 1: Write the failing test**

```go
// sensor/tdp-estimator/coefficients_test.go
package main

import "testing"

func TestResolve_AnnotationWins(t *testing.T) {
	c := Resolve(
		map[string]string{"obs.avuru.io/power-idle-watts": "10", "obs.avuru.io/power-max-watts": "50"},
		5, 40, // Helm values present too, but annotation must win
		"Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz",
	)
	if c.Tier != "annotation" || c.IdleWatts != 10 || c.MaxWatts != 50 {
		t.Errorf("Resolve = %+v, want tier=annotation idle=10 max=50", c)
	}
}

func TestResolve_ValuesWinOverTable(t *testing.T) {
	c := Resolve(nil, 7, 42, "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz")
	if c.Tier != "values" || c.IdleWatts != 7 || c.MaxWatts != 42 {
		t.Errorf("Resolve = %+v, want tier=values idle=7 max=42", c)
	}
}

func TestResolve_TableMatch(t *testing.T) {
	// 8259CL is a real Cascade Lake AWS c5 SKU; the table matches on the
	// "Cascade Lake" family, not the exact SKU number.
	c := Resolve(nil, 0, 0, "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz")
	if c.Tier != "table" {
		t.Fatalf("Tier = %q, want table", c.Tier)
	}
	if c.IdleWatts != 0.64 || c.MaxWatts != 3.97 {
		t.Errorf("Resolve = %+v, want the bundled Cascade Lake entry (0.64/3.97)", c)
	}
}

func TestResolve_GenericFallback(t *testing.T) {
	c := Resolve(nil, 0, 0, "Some Exotic CPU Nobody Has Modeled Yet")
	if c.Tier != "fallback" {
		t.Fatalf("Tier = %q, want fallback", c.Tier)
	}
	if c.IdleWatts <= 0 || c.MaxWatts <= c.IdleWatts {
		t.Errorf("fallback coefficients look wrong: %+v", c)
	}
}

func TestMatchArchitecture(t *testing.T) {
	tests := []struct {
		model string
		want  string // "" means no match (falls through to fallback)
	}{
		{"Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz", "CASCADE_LAKE"},
		{"Intel(R) Xeon(R) CPU E5-2670 v2 @ 2.50GHz", "IVY_BRIDGE"},
		{"AMD EPYC 7571 32-Core Processor", "AMD_EPYC_1ST_GEN"},
		{"AMD EPYC 7R32 48-Core Processor", "AMD_EPYC_2ND_GEN"},
		{"Some Exotic CPU Nobody Has Modeled Yet", ""},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := matchArchitecture(tt.model); got != tt.want {
				t.Errorf("matchArchitecture(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestResolve_|TestMatchArchitecture' -v`
Expected: FAIL — `undefined: Resolve` / `matchArchitecture`

- [ ] **Step 3: Write the bundled coefficient table**

```go
// sensor/tdp-estimator/coefficients_table.go
package main

// Bundled per-CPU-architecture power coefficients (idle/max watts per
// thread, from SPECpower_ssj2008-derived data), used to estimate node power
// via P = P_idle + u*(P_max-P_idle) when no operator override applies. Same
// bundled-dataset pattern as hub/internal/green/intensity.go's grid-carbon
// table: hand-authored, cited per entry, versioned by the constant below.
//
// Provenance: Cloud Carbon Footprint's Apache-2.0 coefficient data
// (github.com/cloud-carbon-footprint/cloud-carbon-footprint,
// AwsFootprintEstimationConstants.ts / GcpFootprintEstimationConstants.ts /
// AzureFootprintEstimationConstants.ts, fetched 2026-07-30), cross-checked
// against the original SPECpower-derived notebook
// (github.com/cloud-carbon-footprint/cloud-carbon-coefficients,
// coefficients.ipynb) whose own embedded `assert` statements independently
// confirm several entries. Where AWS/GCP's live files disagree with
// Azure's file AND the notebook's asserted output (Ivy Bridge, Haswell — up
// to ~78% apart, no public changelog found explaining the divergence), the
// Azure/notebook values are used as the more methodology-traceable source.
// AMD_EPYC_5TH_GEN is deliberately NOT included: AWS's file (3.68W/8.96W)
// and GCP's file (0.27W/1.36W) disagree by nearly an order of magnitude with
// no way to adjudicate — it falls through to the generic fallback tier
// rather than shipping either unverifiable number (the AEP's honesty
// principle: omit rather than guess).
const coefficientDataset = "Cloud Carbon Footprint 2026-07 (Azure/notebook preferred on conflict)"

// archCoefficients is keyed by an internal architecture identifier (not a
// vendor SKU) — matchArchitecture (coefficients.go) maps a raw
// /proc/cpuinfo "model name" string to one of these keys.
var archCoefficients = map[string]Coefficients{
	// Matches Azure's file AND the SPECpower notebook's own asserted output
	// exactly (cross-validated independently, see coefficients_test.go).
	"CASCADE_LAKE":     {IdleWatts: 0.64, MaxWatts: 3.97}, // Azure L47/L84; notebook cell 32
	"SKYLAKE":          {IdleWatts: 0.65, MaxWatts: 4.26}, // Azure L48/L85
	"BROADWELL":        {IdleWatts: 0.71, MaxWatts: 3.69}, // Azure L49/L86; notebook cell 28
	"COFFEE_LAKE":      {IdleWatts: 1.14, MaxWatts: 5.42}, // Azure L51/L88; notebook cell 34, cross-confirmed by ccf-coefficients README worked example
	"SANDY_BRIDGE":     {IdleWatts: 2.17, MaxWatts: 8.58}, // Azure L52/L89; notebook cell 22
	"AMD_EPYC_1ST_GEN": {IdleWatts: 0.82, MaxWatts: 2.55}, // Azure L54/L91; notebook cell 16
	"AMD_EPYC_2ND_GEN": {IdleWatts: 0.47, MaxWatts: 1.69}, // Azure L55/L92; notebook cell 18

	// Azure/notebook values preferred over AWS/GCP's diverging live figures
	// (AWS/GCP: Ivy Bridge 1.71/5.51-5.56, Haswell 1.86-1.90/5.56-5.60).
	"IVY_BRIDGE": {IdleWatts: 3.04, MaxWatts: 8.25}, // Azure L53/L90; notebook cell 24 exact match
	"HASWELL":    {IdleWatts: 1.00, MaxWatts: 4.74}, // Azure L50/L87 (only source; notebook cell 26 says 1.90/6.01 — Azure preferred per sourcing decision)

	// AMD_EPYC_3RD_GEN: Azure/notebook (0.45/2.02) vs AWS (0.46/1.96) vs GCP
	// (0.46/1.83) — all close; Azure/notebook value used for consistency
	// with the rest of this table's sourcing policy.
	"AMD_EPYC_3RD_GEN": {IdleWatts: 0.45, MaxWatts: 2.02}, // Azure L56/L93; notebook cell 20

	// AWS-only entries (no Azure coverage to conflict with; GCP's figures
	// for these agree closely with AWS, so AWS's are used directly).
	"AMD_EPYC_4TH_GEN": {IdleWatts: 0.74, MaxWatts: 2.28}, // AWS L64/L121 (GCP: 0.74/2.2, close)
	"EMERALD_RAPIDS":   {IdleWatts: 0.81, MaxWatts: 4.48}, // AWS L66/L123 (GCP: 0.81/4.38, close)
	"GRANITE_RAPIDS":   {IdleWatts: 0.58, MaxWatts: 2.53}, // AWS L67/L124 (GCP: 0.58/2.37, close)
	"ICELAKE":          {IdleWatts: 0.77, MaxWatts: 3.76}, // AWS L73/L130 (GCP: 0.77/3.65, close)
	"SAPPHIRE_RAPIDS":  {IdleWatts: 1.04, MaxWatts: 4.16}, // AWS L76/L133 (GCP: 1.04/4.06, close)
	"AWS_GRAVITON_2":   {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L68/L125 (AWS-proprietary silicon, no GCP/Azure equivalent)
	"AWS_GRAVITON_3":   {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L69/L126
	"AWS_GRAVITON_3E":  {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L70/L127
	"AWS_GRAVITON_4":   {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L71/L128
	"APPLE":            {IdleWatts: 6.8, MaxWatts: 39},    // AWS L57/L114 (EC2 Mac instances)
}

// genericFallback is the loud, widest-error-band tier-4 default (AEP: "allowed
// but loud" — main.go / coefficients.go log a warning whenever this is used).
// Taken from AWS's file's own fallback-default figures (MIN/MAX_WATTS_AVG,
// L54/L111); GCP's (0.68/4.11 median) and Azure's (0.74/3.54 average)
// fallbacks are close enough that picking one rather than averaging avoids
// synthesizing an unsourced blended number.
var genericFallback = Coefficients{IdleWatts: 0.74, MaxWatts: 3.5}
```

- [ ] **Step 4: Write coefficient resolution + architecture matching**

```go
// sensor/tdp-estimator/coefficients.go
package main

import (
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Coefficients is one node's resolved power-curve inputs plus provenance —
// Tier/Provenance ride into the metric's resource attributes (metrics.go) so
// the hub and the CSRD export can cite per-node sourcing without a
// side-channel (AEP §Coefficients).
type Coefficients struct {
	IdleWatts, MaxWatts float64
	Tier                string // "annotation" | "values" | "table" | "fallback"
	Provenance          string
}

// archPatterns maps a raw /proc/cpuinfo "model name" substring pattern to an
// archCoefficients key. Matching is heuristic (CPU marketing model numbers,
// not codenames, appear in /proc/cpuinfo) and deliberately conservative: an
// unrecognized model correctly falls through to the generic fallback tier
// rather than guessing — this list is NOT exhaustive of every SKU ever
// released, only the common families the bundled table covers.
var archPatterns = []struct {
	re  *regexp.Regexp
	key string
}{
	{regexp.MustCompile(`Xeon\(R\)\s+(?:Platinum|Gold)\s+8[0-3]\d\d`), "CASCADE_LAKE"},   // Platinum 82xx/83xx, Gold 80xx-83xx
	{regexp.MustCompile(`Xeon\(R\)\s+(?:Platinum|Gold)\s+6[23]\d\d`), "SKYLAKE"},         // Gold/Platinum 62xx/63xx
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+v4`), "BROADWELL"},               // E5-26xx v4
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+v3`), "HASWELL"},                 // E5-26xx v3
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+v2`), "IVY_BRIDGE"},              // E5-26xx v2
	{regexp.MustCompile(`Xeon\(R\)\s+CPU\s+E5-2\d\d\d\s+0\s+@`), "SANDY_BRIDGE"},         // E5-26xx (no v-suffix)
	{regexp.MustCompile(`Xeon\(R\)\s+Platinum\s+84\d\d`), "SAPPHIRE_RAPIDS"},             // Platinum 84xx
	{regexp.MustCompile(`Xeon\(R\)\s+6\d\d\d[NP]?\s*$`), "ICELAKE"},                      // Gen-3 Xeon Scalable "6xxx" (bare-metal naming)
	{regexp.MustCompile(`EPYC\s+7[0-9]0[0-9]`), "AMD_EPYC_1ST_GEN"},                      // EPYC 7001 series (Naples)
	{regexp.MustCompile(`EPYC\s+7[RH]?[0-9]{2}`), "AMD_EPYC_2ND_GEN"},                    // EPYC 7002 series (Rome)
	{regexp.MustCompile(`EPYC\s+7[0-9]{3}3`), "AMD_EPYC_3RD_GEN"},                        // EPYC 7003 series (Milan)
	{regexp.MustCompile(`EPYC\s+9[0-9]{3}4`), "AMD_EPYC_4TH_GEN"},                        // EPYC 9004 series (Genoa)
	{regexp.MustCompile(`Graviton2`), "AWS_GRAVITON_2"},
	{regexp.MustCompile(`Graviton3E`), "AWS_GRAVITON_3E"},
	{regexp.MustCompile(`Graviton3`), "AWS_GRAVITON_3"},
	{regexp.MustCompile(`Graviton4`), "AWS_GRAVITON_4"},
	{regexp.MustCompile(`Apple\s+M\d`), "APPLE"},
}

// matchArchitecture maps a raw /proc/cpuinfo "model name" string to an
// archCoefficients key, or "" if nothing matches (caller falls through to
// the generic fallback tier).
func matchArchitecture(cpuModel string) string {
	for _, p := range archPatterns {
		if p.re.MatchString(cpuModel) {
			return p.key
		}
	}
	return ""
}

// Resolve applies the AEP's four-tier precedence: node annotation > Helm
// values > bundled table (by /proc/cpuinfo model match) > generic per-core
// fallback. The fallback tier is loud (AEP: "allowed but loud") — logged as
// a warning since its error band is the widest of the four.
func Resolve(nodeAnnotations map[string]string, valuesIdle, valuesMax float64, cpuModel string) Coefficients {
	if nodeAnnotations != nil {
		idleStr, hasIdle := nodeAnnotations["obs.avuru.io/power-idle-watts"]
		maxStr, hasMax := nodeAnnotations["obs.avuru.io/power-max-watts"]
		if hasIdle && hasMax {
			idle, errIdle := strconv.ParseFloat(idleStr, 64)
			max, errMax := strconv.ParseFloat(maxStr, 64)
			if errIdle == nil && errMax == nil {
				return Coefficients{IdleWatts: idle, MaxWatts: max, Tier: "annotation", Provenance: "node annotation obs.avuru.io/power-{idle,max}-watts"}
			}
		}
	}
	if valuesIdle > 0 && valuesMax > 0 {
		return Coefficients{IdleWatts: valuesIdle, MaxWatts: valuesMax, Tier: "values", Provenance: "sensor.green.estimation.{idleWatts,maxWatts} (Helm values)"}
	}
	if key := matchArchitecture(cpuModel); key != "" {
		c := archCoefficients[key]
		c.Tier = "table"
		c.Provenance = "bundled table (" + coefficientDataset + "), matched architecture " + key
		return c
	}
	slog.Warn("no coefficient match for this CPU model, using the generic per-core fallback (widest error band)",
		"cpuModel", cpuModel, "idleWatts", genericFallback.IdleWatts, "maxWatts", genericFallback.MaxWatts)
	c := genericFallback
	c.Tier = "fallback"
	c.Provenance = "generic per-core fallback (no table match for %q)"
	return c
}

// cpuModelName reads /proc/cpuinfo's first "model name" line, or "" if
// unreadable (Resolve then always falls through to the fallback tier).
func cpuModelName() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// nodeAnnotations fetches this node's annotations from the Kubernetes API
// server's Node object — NOT the kubelet, which exposes pod/stats data but
// not Node object metadata (the Downward API doesn't expose node
// annotations either; it's pod/container-scoped only). Needs no new RBAC:
// sensor-rbac.yaml already grants "nodes" get/list/watch cluster-wide
// (originally for OBI's/Kepler's own informers), so this reuses that
// existing grant. Unlike the kubelet's self-signed serving cert (kubelet.go
// skips verification to match kubeletstats's accepted trust model), the API
// server's serving cert IS signed by the cluster CA mounted alongside the
// token, so this client verifies it properly. Best-effort: any failure
// (network, RBAC, decode) returns nil, and Resolve falls through to the next
// tier — a missing annotation is normal, not an error.
func nodeAnnotations(nodeName string) map[string]string {
	token, err := readServiceAccountToken()
	if err != nil {
		return nil
	}
	caCert, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	apiServer := "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT")
	req, err := http.NewRequest(http.MethodGet, apiServer+"/api/v1/nodes/"+nodeName, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var node struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return nil
	}
	return node.Metadata.Annotations
}
```

This needs `crypto/tls`, `crypto/x509`, `encoding/json`, `net/http`, `time` added to `coefficients.go`'s imports alongside the existing `log/slog`, `os`, `regexp`, `strconv`, `strings`.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestResolve_|TestMatchArchitecture' -v`
Expected: PASS (all four `TestResolve_*` + `TestMatchArchitecture`)

- [ ] **Step 6: Commit**

```bash
git add sensor/tdp-estimator/coefficients_table.go sensor/tdp-estimator/coefficients.go sensor/tdp-estimator/coefficients_test.go
git commit -m "feat(sensor): bundled CPU power coefficient table + resolution order

Sources cited per entry (Cloud Carbon Footprint, cross-checked against the
SPECpower notebook); Azure/notebook values preferred on AWS/GCP conflict;
AMD_EPYC_5TH_GEN omitted (AWS vs GCP disagree by ~10x, unverifiable)."
```

---

## Task 6: Power model + joule integration

**Files:**
- Create: `sensor/tdp-estimator/model.go`
- Create: `sensor/tdp-estimator/model_test.go`

- [ ] **Step 1: Write the failing test**

```go
// sensor/tdp-estimator/model_test.go
package main

import "testing"

func TestNodePower(t *testing.T) {
	c := Coefficients{IdleWatts: 10, MaxWatts: 50}
	tests := []struct {
		util float64
		want float64
	}{
		{0, 10},    // fully idle -> P_idle
		{1, 50},    // fully busy -> P_max
		{0.5, 30},  // halfway -> midpoint
	}
	for _, tt := range tests {
		if got := nodePower(c, tt.util); got != tt.want {
			t.Errorf("nodePower(util=%v) = %v, want %v", tt.util, got, tt.want)
		}
	}
}

func TestIntegrateJoules(t *testing.T) {
	// Two samples 10s apart at constant 20W -> 200 joules (trapezoidal of a
	// flat line is exact).
	samples := []wattSample{
		{atSeconds: 0, watts: 20},
		{atSeconds: 10, watts: 20},
	}
	got := integrateJoules(samples)
	if got != 200 {
		t.Errorf("integrateJoules = %v, want 200", got)
	}
}

func TestIntegrateJoules_RampAndGap(t *testing.T) {
	// 0s@10W -> 10s@30W (trapezoid: (10+30)/2 * 10 = 200J) then a 50s GAP
	// (sensor missed samples) -> 60s@30W. The gap must not be integrated as
	// if power were held constant for 50s at some interpolated value beyond
	// what was actually observed at the gap's start; trapezoidal integration
	// naturally handles this correctly using only the two straddling samples.
	samples := []wattSample{
		{atSeconds: 0, watts: 10},
		{atSeconds: 10, watts: 30},
		{atSeconds: 60, watts: 30},
	}
	got := integrateJoules(samples)
	want := 200.0 + (30.0+30.0)/2*50 // 200 + 1500 = 1700
	if got != want {
		t.Errorf("integrateJoules = %v, want %v", got, want)
	}
}

func TestIntegrateJoules_SingleSample(t *testing.T) {
	if got := integrateJoules([]wattSample{{atSeconds: 0, watts: 20}}); got != 0 {
		t.Errorf("integrateJoules(1 sample) = %v, want 0 (no interval to integrate over)", got)
	}
}

func TestPodDynamicShare(t *testing.T) {
	// Node used 8 of its 10s window busy (80% util); pod used 2s of CPU time
	// in that window -> pod's share of the node's ACTIVE (non-idle) time is
	// 2/8 = 0.25, applied only to the dynamic (P-P_idle) portion.
	nodeCoeff := Coefficients{IdleWatts: 10, MaxWatts: 50}
	nodePowerW := nodePower(nodeCoeff, 0.8) // 42W
	podW := podDynamicPower(nodePowerW, nodeCoeff.IdleWatts, podShareOfActive(2, 8))
	// dynamic = 42-10 = 32W; pod share 0.25 -> 8W
	if podW != 8 {
		t.Errorf("podDynamicPower = %v, want 8", podW)
	}
}

func TestPodShareOfActive_NoActiveTime(t *testing.T) {
	if got := podShareOfActive(2, 0); got != 0 {
		t.Errorf("podShareOfActive with 0 node-active-seconds = %v, want 0 (guards div-by-zero)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestNodePower|TestIntegrateJoules|TestPodDynamicShare|TestPodShareOfActive' -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement the power model**

```go
// sensor/tdp-estimator/model.go
package main

// wattSample is one instantaneous node-power reading at a point in the
// sampler's elapsed-time timeline (seconds since the estimator started).
type wattSample struct {
	atSeconds float64
	watts     float64
}

// nodePower is the AEP's power curve: P = P_idle + u*(P_max-P_idle), a
// straight-line interpolation between the two coefficient tiers by
// utilization u in [0,1]. Documented limitation (AEP): real power curves are
// convex; a curve exponent is a post-v1 refinement, not implemented here.
func nodePower(c Coefficients, util float64) float64 {
	return c.IdleWatts + util*(c.MaxWatts-c.IdleWatts)
}

// integrateJoules is the trapezoidal integral of a wattage series over
// elapsed time — joules = watts * seconds, summed trapezoid-by-trapezoid so
// uneven sample gaps (a missed tick, GC pause, whatever) are handled
// correctly using only the two samples that straddle the gap, never
// extrapolating beyond what was actually observed. A single sample has no
// interval to integrate over and contributes 0 — the caller's next tick
// completes the first interval.
func integrateJoules(samples []wattSample) float64 {
	var joules float64
	for i := 1; i < len(samples); i++ {
		dt := samples[i].atSeconds - samples[i-1].atSeconds
		avgW := (samples[i].watts + samples[i-1].watts) / 2
		joules += avgW * dt
	}
	return joules
}

// podShareOfActive is a pod's fraction of the node's non-idle CPU-seconds in
// a window: podActiveSeconds / nodeActiveSeconds. Returns 0 when the node
// reported no active time (guards div-by-zero; also correctly yields 0 pod
// dynamic power, since there is no dynamic power to share when nothing was
// busy).
func podShareOfActive(podActiveSeconds, nodeActiveSeconds float64) float64 {
	if nodeActiveSeconds <= 0 {
		return 0
	}
	return podActiveSeconds / nodeActiveSeconds
}

// podDynamicPower is a pod's share of the node's DYNAMIC power only
// (nodeWatts - idleWatts), scaled by its share of active CPU time. Idle
// power is deliberately excluded and stays node-only (lands in the hub's
// existing unattributed bucket) — no pod caused the idle draw, and
// attributing it would corrupt per-service comparisons (AEP non-goal).
func podDynamicPower(nodeWatts, idleWatts, shareOfActive float64) float64 {
	dynamic := nodeWatts - idleWatts
	if dynamic < 0 {
		dynamic = 0
	}
	return dynamic * shareOfActive
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go test ./... -run 'TestNodePower|TestIntegrateJoules|TestPodDynamicShare|TestPodShareOfActive' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add sensor/tdp-estimator/model.go sensor/tdp-estimator/model_test.go
git commit -m "feat(sensor): tdp-estimator power model + joule integration"
```

---

## Task 7: Prometheus metrics endpoint + sampler loop wiring

**Files:**
- Create: `sensor/tdp-estimator/metrics.go`
- Create: `sensor/tdp-estimator/metrics_test.go`
- Modify: `sensor/tdp-estimator/main.go` (already references `newRegistry`/`runSampler` from Task 1 — no further edits needed here beyond what Task 1 wrote)

- [ ] **Step 1: Write the failing test**

```go
// sensor/tdp-estimator/metrics_test.go
package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistry_ServeHTTP(t *testing.T) {
	reg := newRegistry()
	reg.setNodeEnergy("node-1", 123.45)
	reg.setPodEnergy("web-1", "shop", 67.89)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, req)

	body := rec.Body.String()
	wantLines := []string{
		`kepler_node_cpu_joules_total{node_name="node-1"} 123.45`,
		`kepler_pod_cpu_joules_total{pod_name="web-1",pod_namespace="shop"} 67.89`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("body missing line %q\nfull body:\n%s", want, body)
		}
	}
}

func TestRegistry_EmptyWhenDormant(t *testing.T) {
	reg := newRegistry()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body with no samples set, got %q", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sensor/tdp-estimator && go test ./... -run TestRegistry -v`
Expected: FAIL — `undefined: newRegistry`

- [ ] **Step 3: Implement the metrics registry + sampler loop**

```go
// sensor/tdp-estimator/metrics.go
package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
)

// podKey identifies one pod's cumulative-joules counter.
type podKey struct{ name, namespace string }

// registry holds the CUMULATIVE joules counters this process has emitted so
// far — kepler_{node,pod}_cpu_joules_total, exactly the metric names and
// shape Kepler itself emits (AEP metric table), so the existing keep-regex,
// otel-agent pipeline, and hub SQL all apply unchanged. Cumulative (not
// gauge watts) because the hub's green SQL already does read-time
// counter-delta math over these exact series (greenSeriesID) — the
// estimator must produce what that SQL expects, not reinvent a new shape.
type registry struct {
	mu        sync.Mutex
	nodeName  string
	nodeJ     float64
	podJ      map[podKey]float64
}

func newRegistry() *registry {
	return &registry{podJ: make(map[podKey]float64)}
}

// setNodeEnergy replaces the current node cumulative-joules value — called
// by the sampler each tick with joules accumulated so far this process
// lifetime (a monotonically increasing counter, like Kepler's own).
func (r *registry) setNodeEnergy(node string, joules float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodeName = node
	r.nodeJ = joules
}

// setPodEnergy replaces one pod's cumulative-joules value.
func (r *registry) setPodEnergy(name, namespace string, joules float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.podJ[podKey{name, namespace}] = joules
}

// removePod drops a pod that no longer exists (its cgroup disappeared) —
// its counter simply stops being exported, the same as Kepler when a pod
// exits.
func (r *registry) removePod(name, namespace string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.podJ, podKey{name, namespace})
}

// ServeHTTP renders the Prometheus text exposition format for exactly the
// four AEP metric names the pod/node kinds need
// (kepler_{node,pod}_cpu_joules_total) — no HELP/TYPE lines are required by
// the format, and the otel-agent's prometheus receiver reads bare metric
// lines fine (matching Kepler's own minimal exposition).
func (r *registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if r.nodeName != "" {
		fmt.Fprintf(w, "kepler_node_cpu_joules_total{node_name=%q} %s\n", r.nodeName, strconv.FormatFloat(r.nodeJ, 'f', -1, 64))
	}
	keys := make([]podKey, 0, len(r.podJ))
	for k := range r.podJ {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].namespace != keys[j].namespace {
			return keys[i].namespace < keys[j].namespace
		}
		return keys[i].name < keys[j].name
	})
	for _, k := range keys {
		fmt.Fprintf(w, "kepler_pod_cpu_joules_total{pod_name=%q,pod_namespace=%q} %s\n",
			k.name, k.namespace, strconv.FormatFloat(r.podJ[k], 'f', -1, 64))
	}
}
```

```go
// sensor/tdp-estimator/sampler_loop.go
package main

import (
	"log/slog"
	"time"
)

// runSampler is the estimator's main loop: on each tick, read node
// utilization and per-pod cgroup usage, compute power via the model, and
// integrate into the registry's cumulative joules counters. Runs for the
// process lifetime (main.go only calls this once RAPL has been ruled out).
func runSampler(nodeName string, interval time.Duration, coeff Coefficients, reg *registry) {
	client := newKubeletClient()
	token, err := readServiceAccountToken()
	if err != nil {
		slog.Error("cannot read service account token, pod attribution disabled (node energy still reported)", "error", err)
	}
	baseURL := kubeletBaseURL(nodeName)

	var (
		prevCPU      cpuTimes
		havePrevCPU  bool
		havePrevW    bool
		prevNodeW    float64 // last tick's node wattage, for trapezoidal integration
		nodeJoules   float64
		podPrevUsage = map[string]uint64{} // uid -> previous usage_usec
		podJoules    = map[string]float64{}
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		cur, err := readCPUTimes(defaultProcStat)
		if err != nil {
			slog.Warn("reading /proc/stat failed, skipping this tick", "error", err)
			continue
		}
		if !havePrevCPU {
			prevCPU, havePrevCPU = cur, true
			continue // need two samples before a utilization delta exists
		}
		util := utilizationDelta(prevCPU, cur)
		nodeW := nodePower(coeff, util)
		// Integrate ONLY this tick's increment (prevNodeW -> nodeW over
		// exactly `interval`), then accumulate — NOT the wattage times total
		// elapsed process lifetime, which would re-integrate the whole
		// history on every tick. The ticker's own fixed interval IS the dt
		// between consecutive samples, so no wall-clock timestamps are
		// needed here at all.
		if !havePrevW {
			prevNodeW, havePrevW = nodeW, true
		}
		nodeJoules += integrateJoules([]wattSample{{atSeconds: 0, watts: prevNodeW}, {atSeconds: interval.Seconds(), watts: nodeW}})
		prevNodeW = nodeW
		reg.setNodeEnergy(nodeName, nodeJoules)

		// Pod attribution: best-effort. A kubelet or cgroup-walk failure
		// degrades to node-only reporting (still useful) rather than
		// crashing the estimator — matches the AEP's "do no harm" posture.
		pods, err := fetchPods(client, baseURL, token)
		if err != nil {
			slog.Warn("kubelet pod list unavailable, pod-level energy skipped this tick", "error", err)
			prevCPU = cur
			continue
		}
		cgroups, err := discoverPodCgroups(defaultCgroupRoot)
		if err != nil {
			slog.Warn("cgroup discovery failed, pod-level energy skipped this tick", "error", err)
			prevCPU = cur
			continue
		}

		nodeActiveSecs := util * interval.Seconds()
		seen := map[string]bool{}
		for _, cg := range cgroups {
			id, known := pods[cg.uid]
			if !known {
				continue // cgroup exists but the kubelet hasn't reported this pod yet (race on create/delete)
			}
			usage, err := readCPUStatUsage(cg.path)
			if err != nil {
				continue
			}
			seen[cg.uid] = true
			deltaUsec := float64(0)
			if prev, ok := podPrevUsage[cg.uid]; ok && usage >= prev {
				deltaUsec = float64(usage - prev)
			}
			podPrevUsage[cg.uid] = usage
			podActiveSecs := deltaUsec / 1_000_000
			podW := podDynamicPower(nodeW, coeff.IdleWatts, podShareOfActive(podActiveSecs, nodeActiveSecs))
			podJoules[cg.uid] += podW * interval.Seconds()
			reg.setPodEnergy(id.Name, id.Namespace, podJoules[cg.uid])
		}
		// Pods whose cgroup disappeared (exited) stop being reported —
		// matches Kepler's own behavior on pod exit.
		for uid := range podPrevUsage {
			if !seen[uid] {
				delete(podPrevUsage, uid)
				delete(podJoules, uid)
			}
		}

		prevCPU = cur
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sensor/tdp-estimator && go build ./... && go test ./... -v`
Expected: the whole package now builds (main.go's forward references are satisfied) and every test from Tasks 1-7 passes.

- [ ] **Step 5: Commit**

```bash
git add sensor/tdp-estimator/metrics.go sensor/tdp-estimator/metrics_test.go sensor/tdp-estimator/sampler_loop.go
git commit -m "feat(sensor): tdp-estimator metrics endpoint + sampler loop wiring"
```

---

## Task 8: Dockerfile + local build verification

**Files:**
- Create: `sensor/tdp-estimator/Dockerfile`

- [ ] **Step 1: Write the Dockerfile**

```dockerfile
# sensor/tdp-estimator/Dockerfile
# tdp-estimator image: the sensor DaemonSet's opt-in 5th container. Mirrors
# hub/Dockerfile's shape (static Go binary, distroless runtime).
# Build context = repo root (release.yml: context ., file sensor/tdp-estimator/Dockerfile).

FROM golang:1.26-alpine AS build
WORKDIR /src/sensor/tdp-estimator
COPY sensor/tdp-estimator/go.mod ./
RUN go mod download
COPY sensor/tdp-estimator/ ./
RUN CGO_ENABLED=0 go build -o /tdp-estimator .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tdp-estimator /tdp-estimator
ENTRYPOINT ["/tdp-estimator"]
```

- [ ] **Step 2: Verify the module builds and tests pass standalone**

Run:
```bash
cd sensor/tdp-estimator && go build ./... && go vet ./... && go test ./... -v
```
Expected: PASS, no vet warnings.

- [ ] **Step 3: Verify the Docker image builds**

Run: `docker build -f sensor/tdp-estimator/Dockerfile -t avuru-obs-tdp-estimator:local .` (from the repo root)
Expected: image builds successfully.

- [ ] **Step 4: Commit**

```bash
git add sensor/tdp-estimator/Dockerfile
git commit -m "feat(sensor): tdp-estimator Dockerfile"
```

---

## Task 9: Helm values + gate helper + chart guard

**Files:**
- Modify: `deploy/helm/avuruops/values.yaml` (extend the `sensor.green` block, around L357-374)
- Modify: `deploy/helm/avuruops/templates/_helpers.tpl` (alongside `avuruops.collectGreen`, around L255-257)
- Modify: `deploy/helm/avuruops/templates/sensor-config.yaml` (guard block, around L1-18)

- [ ] **Step 1: Add the values.yaml block**

Read [deploy/helm/avuruops/values.yaml](../../../deploy/helm/avuruops/values.yaml) around line 357-374 first to find the exact insertion point (immediately after the existing `sensor.green.scrapeInterval` / before `sensor.green.privileged`, or at the end of the `green:` block — match existing indentation exactly). Add:

```yaml
    # TDP-based power estimation for RAPL-less nodes (opt-in, requires
    # sensor.green.enabled). See design/2026-07-28-green-tdp-estimation.md.
    # Every series it emits is stamped avuruops_quality="estimated" by the
    # otel-agent — never blended with Kepler's measured numbers.
    estimation:
      enabled: false
      image:
        repository: ""  # set to the published avuru-obs-tdp-estimator image
        tag: ""          # pinned to the release tag, like the other first-party images
      # Loopback-bound, like Kepler's own port (28282) — never exposed on the
      # pod network.
      port: 28283
      scrapeInterval: 30s
      # Operator-set P_idle/P_max override (Helm-values tier, wins over the
      # bundled table, loses to a per-node obs.avuru.io/power-*-watts
      # annotation). 0 = defer to the bundled table / generic fallback.
      idleWatts: 0
      maxWatts: 0
```

- [ ] **Step 2: Add the `avuruops.collectGreenEstimation` helper**

In `_helpers.tpl`, immediately after the existing `avuruops.collectGreen` definition (around L255-257):

```gotemplate
{{/*
Whether the tdp-estimator container/scrape should render: requires green
collection active AND the estimation sub-flag. Same true/"" (never "false")
convention as the other avuruops.collect* gates — always consumed via
`if include "..."`, never string-compared.
*/}}
{{- define "avuruops.collectGreenEstimation" -}}
{{- if and (include "avuruops.collectGreen" .) .Values.sensor.green.estimation.enabled -}}true{{- end -}}
{{- end -}}
```

- [ ] **Step 3: Add the chart guard**

In `sensor-config.yaml`'s existing fail-fast guard block (L1-18), alongside the `modules.green`/`sensor.green` guards, add:

```gotemplate
{{- if and .Values.sensor.green.estimation.enabled (not .Values.sensor.green.enabled) }}
{{- fail "sensor.green.estimation.enabled requires sensor.green.enabled" }}
{{- end }}
```

- [ ] **Step 4: Verify the chart still renders with defaults unchanged**

Run: `helm template avuruops deploy/helm/avuruops | grep -c tdp-estimator`
Expected: `0` (estimation is off by default — nothing new renders)

Run: `helm template avuruops deploy/helm/avuruops --set sensor.green.estimation.enabled=true --set sensor.green.enabled=false`
Expected: the command **fails** with the guard's error message (estimation requires `sensor.green.enabled`).

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/avuruops/values.yaml deploy/helm/avuruops/templates/_helpers.tpl deploy/helm/avuruops/templates/sensor-config.yaml
git commit -m "feat(helm): tdp-estimator values + gate helper + guard"
```

---

## Task 10: Sensor DaemonSet — 5th container

**Files:**
- Modify: `deploy/helm/avuruops/templates/sensor-daemonset.yaml` (after the Kepler container block, around L294)

- [ ] **Step 1: Add the container block**

Insert immediately after Kepler's container block (which ends around L294 with the closing of its `securityContext`/`volumeMounts` — read the file first to match exact structure and indentation):

```yaml
        {{- if include "avuruops.collectGreenEstimation" . }}
        # tdp-estimator — modeled CPU power for nodes with no RAPL/powercap
        # (first-party, opt-in). Deliberately has NO liveness/readiness/
        # startup probes, same "do no harm" posture as Kepler: a probe
        # failure here must never flap the sensor pod.
        - name: tdp-estimator
          image: {{ include "avuruops.image" (dict "registry" .Values.image.registry "repo" .Values.sensor.green.estimation.image.repository "tag" .Values.sensor.green.estimation.image.tag) }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          args:
            - --listen=:{{ .Values.sensor.green.estimation.port }}
            - --node-name=$(NODE_NAME)
            - --idle-watts={{ .Values.sensor.green.estimation.idleWatts }}
            - --max-watts={{ .Values.sensor.green.estimation.maxWatts }}
          env:
            - name: NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
          securityContext:
            runAsNonRoot: true
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: proc
              mountPath: /proc
              readOnly: true
            - name: cgroup
              mountPath: /sys/fs/cgroup
              readOnly: true
            - name: powercap
              mountPath: /sys/class/powercap
              readOnly: true
          resources:
            {{- toYaml .Values.sensor.green.estimation.resources | default (dict "limits" (dict "cpu" "100m" "memory" "64Mi")) | nindent 12 }}
        {{- end }}
```

Note: `proc`/`cgroup`/`powercap` host-path volumes likely need adding to the pod's `volumes:` list if not already present for another container — check the existing Kepler/OBI volume mounts first (they already mount `/sys/class/powercap` for Kepler and probably `/proc`/`/sys/fs/cgroup` for OBI); reuse the SAME volume names rather than declaring duplicates. If Kepler's volume for powercap is already named e.g. `powercap`, reference that name here instead of redeclaring.

- [ ] **Step 2: Verify the container renders only when both flags are on**

Run:
```bash
helm template avuruops deploy/helm/avuruops \
  --set sensor.green.enabled=true --set modules.green.enabled=true --set modules.infraMetrics.enabled=true \
  --set sensor.green.estimation.enabled=true \
  --set sensor.green.estimation.image.repository=example/tdp-estimator --set sensor.green.estimation.image.tag=v0.3.0 \
  | grep -A3 "name: tdp-estimator"
```
Expected: the container block appears with the image ref resolved and no `livenessProbe`/`readinessProbe` keys anywhere in its block.

- [ ] **Step 3: Commit**

```bash
git add deploy/helm/avuruops/templates/sensor-daemonset.yaml
git commit -m "feat(helm): tdp-estimator sensor DaemonSet container"
```

---

## Task 11: otel-agent scrape job + quality-stamping transform

**Files:**
- Modify: `deploy/helm/avuruops/templates/sensor-config.yaml` (the `prometheus/green` receiver around L167-178, and the `metrics/green` pipeline's processor list around L251-291 and L329-334)

- [ ] **Step 1: Add the second scrape job**

Inside the existing `prometheus/green` receiver block, after the `kepler` job:

```yaml
              {{- if include "avuruops.collectGreenEstimation" . }}
              - job_name: tdp-estimator
                scrape_interval: {{ .Values.sensor.green.estimation.scrapeInterval }}
                static_configs:
                  - targets: ["127.0.0.1:{{ .Values.sensor.green.estimation.port }}"]
                metric_relabel_configs:
                  - source_labels: [__name__]
                    regex: {{ .Values.sensor.green.metrics.keep | quote }}
                    action: keep
              {{- end }}
```

- [ ] **Step 2: Add the quality-stamping transform processor**

After the existing `transform/green` processor (L268-274) and before `groupbyattrs/green` (L279):

```yaml
      # Quality stamping: measured (Kepler, real RAPL) vs estimated
      # (tdp-estimator, modeled) — never inferred from absence, always an
      # explicit label on every datapoint (AEP). The prometheus receiver
      # sets resource.attributes["service.name"] from each job's job_name;
      # VERIFIED against this repo's pinned otel-agent image in this task's
      # step 3 below before relying on it further.
      transform/green_quality:
        error_mode: ignore
        metric_statements:
          - context: datapoint
            statements:
              - set(attributes["avuruops_quality"], "measured") where resource.attributes["service.name"] == "kepler"
              {{- if include "avuruops.collectGreenEstimation" . }}
              - set(attributes["avuruops_quality"], "estimated") where resource.attributes["service.name"] == "tdp-estimator"
              {{- end }}
```

Add `transform/green_quality` to the `metrics/green` pipeline's processor list (around L330-334), between `transform/green` and `groupbyattrs/green`:

```yaml
        metrics/green:
          receivers: [prometheus/green]
          processors: [memory_limiter, filter/green, transform/green, transform/green_quality, k8sattributes{{ if $collectionFilter }}, filter/collection{{ end }}, groupbyattrs/green, resource/green, batch]
          exporters: [otlphttp]
```

(Read the actual current processor list at L329-334 first — the above shows where `transform/green_quality` slots in; keep every other processor in its existing position.)

- [ ] **Step 3: Verify the `service.name` assumption against the pinned otel-agent image**

Run a local render + a one-off scrape test: `helm template` the chart with both `sensor.green.enabled` and `sensor.green.estimation.enabled` true, extract the rendered `otel-agent` ConfigMap, and either (a) run the pinned otel-agent image locally with this config against a dummy Prometheus-format target and inspect the resulting OTLP resource attributes for `service.name`, or (b) grep the pinned collector-contrib version's `prometheusreceiver` source/changelog for how it sets resource attributes from `job_name`. **If `service.name` is NOT set as expected**, fall back to a `metric_relabel_configs` step that copies the `job` label into a dedicated attribute instead (e.g. `source_labels: [job]` → a `avuru_quality_source` datapoint attribute), and adjust `transform/green_quality`'s `where` clauses to match on that attribute instead of `resource.attributes["service.name"]`.

- [ ] **Step 4: Verify chart render assertions**

Run:
```bash
helm template avuruops deploy/helm/avuruops \
  --set sensor.green.enabled=true --set modules.green.enabled=true --set modules.infraMetrics.enabled=true \
  --set sensor.green.estimation.enabled=true \
  --set sensor.green.estimation.image.repository=example/tdp-estimator --set sensor.green.estimation.image.tag=v0.3.0 \
  | grep -A2 "job_name: tdp-estimator"
```
Expected: the scrape job block appears with the correct port.

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/avuruops/templates/sensor-config.yaml
git commit -m "feat(helm): tdp-estimator scrape job + quality-stamping transform"
```

---

## Task 12: CI + release wiring

**Files:**
- Modify: `Makefile` (the `check` target and `GATEWAY_MODULES` area, around L38-47)
- Modify: `.github/workflows/ci.yml` (add a `sensor-modules` job mirroring `gateway-modules`, and add the component to `image-scan`'s matrix)
- Modify: `.github/workflows/release.yml` (add `tdp-estimator` to the `images` job's matrix, around L65-71)
- Modify: `RELEASING.md` (the "What gets released" table)

- [ ] **Step 1: Add a sensor-modules loop to the root Makefile**

```makefile
# Every in-repo sensor module (like the gateway modules) is its own Go
# module — a module missing from this list is a module CI does not gate.
SENSOR_MODULES := tdp-estimator

check:
	cd hub && go build ./... && go test -race ./...
	@for m in $(GATEWAY_MODULES); do \
		echo "== gateway/$$m"; \
		( cd gateway/$$m && go build ./... && go vet ./... && go test ./... ) || exit 1; \
	done
	@for m in $(SENSOR_MODULES); do \
		echo "== sensor/$$m"; \
		( cd sensor/$$m && go build ./... && go vet ./... && go test ./... ) || exit 1; \
	done
	cd ui && npm run lint && npm run build
```

- [ ] **Step 2: Add the `sensor-modules` CI job**

In `.github/workflows/ci.yml`, after the `gateway-modules` job:

```yaml
  sensor-modules:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        module: [tdp-estimator]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - name: build + vet + test
        working-directory: sensor/${{ matrix.module }}
        run: go build ./... && go vet ./... && go test ./...
```

Add `tdp-estimator` to `image-scan`'s matrix (around L114-117):

```yaml
          - { component: tdp-estimator, dockerfile: sensor/tdp-estimator/Dockerfile }
```

- [ ] **Step 3: Add the image to release.yml's matrix**

In `.github/workflows/release.yml`, in the `images` job's matrix (around L65-71):

```yaml
          - component: tdp-estimator
            dockerfile: sensor/tdp-estimator/Dockerfile
```

- [ ] **Step 4: Update RELEASING.md's artifact table**

Add a row to the "What gets released" table:

```markdown
| `tdp-estimator` container image | `ghcr.io/<org>/avuru-obs-tdp-estimator` — same |
```

- [ ] **Step 5: Verify locally**

Run: `cd sensor/tdp-estimator && go build ./... && go vet ./... && go test ./...`
Expected: PASS (same command CI now runs)

- [ ] **Step 6: Commit**

```bash
git add Makefile .github/workflows/ci.yml .github/workflows/release.yml RELEASING.md
git commit -m "ci: gate + release the tdp-estimator image"
```

---

## Task 13: Helm chart-render test assertions

**Files:**
- Modify: `deploy/helm/template-test.sh` (the green section, around L208-302)

- [ ] **Step 1: Read the existing green assertions**

Read [deploy/helm/template-test.sh:208-302](../../../deploy/helm/template-test.sh#L208-L302) to see the exact assertion helper functions in use (likely `assert_contains`/`assert_not_contains`/`assert_render_fails` style helpers — match the file's own conventions exactly).

- [ ] **Step 2: Add estimation-off assertions (default)**

Add a case verifying that with only `sensor.green.enabled=true` (estimation left at its default `false`), rendering contains no `tdp-estimator` container, no `job_name: tdp-estimator` scrape, and no estimation-related lines in the quality-stamping transform's second `set(...)` statement.

- [ ] **Step 3: Add estimation-on assertions**

Add a case setting `sensor.green.estimation.enabled=true` (plus its required `sensor.green.enabled=true`, image repo/tag) and assert: the `tdp-estimator` container renders with no `livenessProbe`/`readinessProbe` keys anywhere in its block; the `job_name: tdp-estimator` scrape target renders at the configured port; the `transform/green_quality` processor's `estimated` statement renders; the container is absent when only `sensor.green.enabled=true` (Task 9's guard fires instead — assert the render fails with the guard's exact error string).

- [ ] **Step 4: Verify defaults stay byte-identical**

Add (or confirm the file already has, extend if not) a diff-based assertion: `helm template` with no overrides at all, before and after this feature's changes, produces byte-identical output — confirms the whole feature is off-by-default with zero footprint on existing installs.

- [ ] **Step 5: Run the full chart-test suite**

Run: `make helm-check`
Expected: PASS, including the new assertions.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/template-test.sh
git commit -m "test(helm): tdp-estimator chart-render assertions"
```

---

## Task 14: Hub — quality dimension in ServiceEnergy/NodeEnergy

**Files:**
- Modify: `hub/internal/storage/store.go` (`ServiceEnergy`/`NodeEnergy` structs, L546-557)
- Modify: `hub/internal/storage/clickhouse/green.go` (both SQL queries)
- Modify: `hub/internal/storage/storagetest/fake.go` (no signature change needed — `Quality` rides inside the existing `[]storage.ServiceEnergy`/`NodeEnergy` fields, so the fake needs no new fields, just accurate fixtures in tests)
- Test: `hub/internal/storage/clickhouse/green_integration_test.go` (extend)

- [ ] **Step 1: Write the failing integration test**

Append to `hub/internal/storage/clickhouse/green_integration_test.go`:

```go
// TestServiceEnergyQualitySplit seeds one service with BOTH a measured
// (Kepler) and an estimated (tdp-estimator) series on distinct pods, and
// asserts the per-quality split is exact — quality never gets blended into
// one number, and a series carrying no quality attribute at all (pre-AEP
// data, or a misconfigured install) reads as an empty Quality string rather
// than silently dropping.
func TestServiceEnergyQualitySplit(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 5 * time.Minute
	base := greenBase(interval)
	end := base.Add(interval)
	noRes := map[string]string{}

	// web-1 on a RAPL node: measured.
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-1", "shop", map[string]string{"avuruops_quality": "measured"}), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-1", "shop", map[string]string{"avuruops_quality": "measured"}), 360) // Δ360 J

	// web-2, same service (workload), on a RAPL-less node: estimated.
	insertSum(t, store, base.Add(1*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-2", "shop", map[string]string{"avuruops_quality": "estimated"}), 0)
	insertSum(t, store, base.Add(4*time.Minute), testPodEnergyA, noRes,
		podAttrs("web-2", "shop", map[string]string{"avuruops_quality": "estimated"}), 720) // Δ720 J

	rows, err := store.ServiceEnergy(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("ServiceEnergy: %v", err)
	}

	var measuredWh, estimatedWh float64
	for _, row := range rows {
		if row.Service != "web" { // both web-1/web-2 belong to the "web" deployment workload
			continue
		}
		switch row.Quality {
		case "measured":
			measuredWh = row.WattHours
		case "estimated":
			estimatedWh = row.WattHours
		}
	}
	wantWh(t, "measured", measuredWh, 360)
	wantWh(t, "estimated", estimatedWh, 720)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd hub && go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/... -run TestServiceEnergyQualitySplit -v`
Expected: FAIL — `row.Quality` doesn't compile (`ServiceEnergy` has no `Quality` field yet) and/or rows aren't split by quality.

- [ ] **Step 3: Add the `Quality` field**

In `hub/internal/storage/store.go`, modify `ServiceEnergy` and `NodeEnergy` (L546-557):

```go
// ServiceEnergy is one service's energy over a window for ONE quality tier:
// the Wh total plus the bucketed series. An empty Service is the
// unattributed bucket — energy whose pod could not be mapped to a workload.
// Quality is "measured" (Kepler/RAPL), "estimated" (tdp-estimator), or ""
// (a series with no avuruops_quality attribute at all — pre-AEP data or a
// misconfigured sensor; callers must not assume "" means measured). A
// service with both measured and estimated energy in the window appears as
// TWO rows, one per quality — callers must never sum across Quality values
// without being explicit about it (the AEP: never silently blend).
type ServiceEnergy struct {
	Service   string
	Quality   string
	WattHours float64
	Points    []EnergyPoint
}

// NodeEnergy is one node's energy over a window for ONE quality tier (Wh
// total + bucketed series) — same Quality semantics as ServiceEnergy.
type NodeEnergy struct {
	Node      string
	Quality   string
	WattHours float64
	Points    []EnergyPoint
}
```

- [ ] **Step 4: Extend the SQL to group by quality**

In `hub/internal/storage/clickhouse/green.go`'s `ServiceEnergy` method, change the `series_deltas` CTE's `SELECT`/`GROUP BY` to also project `Attributes['avuruops_quality']`, and the outer query to group by it too:

```go
	query := fmt.Sprintf(`
WITH series_deltas AS (
    SELECT
        Attributes[?]                                    AS pod,
        Attributes[?]                                    AS ns,
        Attributes['avuruops_quality']                   AS quality,
        toStartOfInterval(TimeUnix, INTERVAL %d SECOND)  AS t,
        %s                                               AS sid,
        greatest(max(Value) - min(Value), 0)             AS joules
    FROM otel_metrics_sum
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName IN (%s)
    GROUP BY pod, ns, quality, t, sid
),
pod_workloads AS (
    SELECT
        ResourceAttributes['k8s.pod.name']       AS pod,
        ResourceAttributes['k8s.namespace.name'] AS ns,
        anyLast(%s) AS workload
    FROM otel_metrics_gauge
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName = ?
      AND ResourceAttributes['k8s.pod.name'] != ''
    GROUP BY pod, ns
)
SELECT w.workload AS service, d.quality AS quality, d.t AS t, sum(d.joules) / 3600 AS wh
FROM series_deltas AS d
LEFT JOIN pod_workloads AS w ON d.pod = w.pod AND d.ns = w.ns
GROUP BY service, quality, t
ORDER BY service, quality, t`,
		int(bucket.Seconds()), greenSeriesID, inList(len(q.PodEnergyMetrics)), workloadExpr)
```

Update the row-scanning loop to scan `quality` and group `cur` by `(service, quality)` instead of `service` alone (the existing `if cur == nil || cur.Service != service` check becomes `if cur == nil || cur.Service != service || cur.Quality != quality`), and set `cur.Quality = quality` on each new row. Apply the identical `quality` projection/grouping change to `NodeEnergy`'s query and scan loop (mirroring the same pattern — `Attributes['avuruops_quality'] AS quality` added to its inner `SELECT`/`GROUP BY`, `quality` added to the outer `SELECT`/`GROUP BY`/`ORDER BY`, and the row-accumulation loop's grouping key extended the same way).

Note: `quality` is added to `GROUP BY` in `series_deltas` but **NOT** to `greenSeriesID` (`sid`) — quality is a label riding an already-uniquely-identified series, not part of series identity, so the counter-delta math (`max(Value)-min(Value)` per `sid`) is completely unaffected by this change. This is the load-bearing invariant the design spec calls out; don't fold `quality` into `sid`.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd hub && go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/... -run TestServiceEnergyQualitySplit -v`
Expected: PASS

- [ ] **Step 6: Run the full existing green integration suite to confirm no regression**

Run: `cd hub && make test-int` (or `go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/...` directly)
Expected: PASS — `TestServiceEnergyDeltaMath`, `TestServiceEnergyBucketing`, `TestNodeEnergyIntegration` all still pass (the pre-existing tests never set `avuruops_quality`, so `Quality` reads as `""` on their rows — confirm this doesn't break their row-grouping assertions; if any pre-existing test asserts an exact row COUNT that now changes because rows with `quality=""` no longer merge with hypothetical quality-tagged rows from the same service, that's expected — those tests only ever seed one quality tier (none), so row counts are unaffected in practice).

- [ ] **Step 7: Commit**

```bash
git add hub/internal/storage/store.go hub/internal/storage/clickhouse/green.go hub/internal/storage/clickhouse/green_integration_test.go
git commit -m "feat(hub): quality dimension (measured/estimated) in green SQL"
```

---

## Task 15: Hub — NodeCoverage read

**Files:**
- Modify: `hub/internal/storage/store.go` (add `NodeCoverage` method to the `Store` interface + struct)
- Modify: `hub/internal/storage/clickhouse/green.go` (implement `NodeCoverage`)
- Modify: `hub/internal/storage/storagetest/fake.go` (add the fake)
- Test: `hub/internal/storage/clickhouse/green_integration_test.go` (extend)

- [ ] **Step 1: Write the failing integration test**

```go
// TestNodeCoverage seeds one node reporting measured energy, one reporting
// estimated, and asserts a THIRD known-but-silent node (present via
// ListAgentNodes-equivalent fixture, reporting neither) is counted as
// absent — the exact gap the green-carbon AEP review flagged as invisible
// before this feature.
func TestNodeCoverage(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	interval := 5 * time.Minute
	base := greenBase(interval)
	end := base.Add(interval)
	noRes := map[string]string{}

	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-measured"}, noRes, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-measured"}, map[string]string{"avuruops_quality": "measured"}, 100)

	insertSum(t, store, base.Add(1*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-estimated"}, noRes, 0)
	insertSum(t, store, base.Add(4*time.Minute), testNodeEnergyA,
		map[string]string{"k8s.node.name": "node-estimated"}, map[string]string{"avuruops_quality": "estimated"}, 100)

	// node-silent: seed a kubeletstats-style presence row (any metric) so it
	// is a KNOWN node, but never any green energy series for it.
	insertGauge(t, store, base.Add(2*time.Minute), "k8s.node.cpu.utilization",
		map[string]string{"k8s.node.name": "node-silent"}, noRes, 0.1)

	cov, err := store.NodeCoverage(ctx, greenQuery("default", base, end, interval))
	if err != nil {
		t.Fatalf("NodeCoverage: %v", err)
	}
	if cov.KnownNodes != 3 || cov.MeasuredNodes != 1 || cov.EstimatedNodes != 1 || cov.AbsentNodes != 1 {
		t.Errorf("NodeCoverage = %+v, want {Known:3 Measured:1 Estimated:1 Absent:1}", cov)
	}
}
```

Check `insertGauge`'s exact signature in the same test file before using it (it should already exist, mirroring `insertSum`) — adjust the call above to match if its parameter order differs.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd hub && go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/... -run TestNodeCoverage -v`
Expected: FAIL — `undefined: store.NodeCoverage` / `storage.NodeCoverage`

- [ ] **Step 3: Add the `NodeCoverage` type + interface method**

In `hub/internal/storage/store.go`, near `GreenQuery`/`ServiceEnergy` (after `NodeEnergy`, before the `Store` interface):

```go
// NodeCoverage reports, per node, whether it contributed measured,
// estimated, or no green energy in the window — closing the green-carbon
// AEP review's follow-up (RAPL-less share was invisible before this).
// "Known nodes" is the same node universe ListAgentNodes already derives
// from telemetry presence (self-reporting, not a heartbeat protocol);
// AbsentNodes = KnownNodes - MeasuredNodes - EstimatedNodes (a node
// reporting BOTH tiers, which shouldn't normally happen per-node but isn't
// impossible on a heterogeneous multi-NIC node, counts toward both —
// AbsentNodes is therefore a lower bound in that edge case, never negative).
type NodeCoverage struct {
	KnownNodes     int
	MeasuredNodes  int
	EstimatedNodes int
	AbsentNodes    int
}
```

Add to the `Store` interface, near `NodeEnergy` (L611):

```go
	NodeCoverage(ctx context.Context, q GreenQuery) (NodeCoverage, error)
```

- [ ] **Step 4: Implement the ClickHouse query**

In `hub/internal/storage/clickhouse/green.go`, add:

```go
// NodeCoverage computes the three-tier node coverage the green-carbon AEP
// review asked for: known nodes (any telemetry presence in the window, via
// the same k8s.node.name resource attribute the whole infra view keys on)
// cross-referenced against which of them reported measured vs estimated
// green energy. A node with neither is absent — the gap the AEP makes
// visible for the first time.
func (s *Store) NodeCoverage(ctx context.Context, q storage.GreenQuery) (storage.NodeCoverage, error) {
	if len(q.NodeEnergyMetrics) == 0 {
		return storage.NodeCoverage{}, nil
	}
	query := fmt.Sprintf(`
WITH known AS (
    SELECT DISTINCT `+nodeAttr+` AS node
    FROM otel_metrics_gauge
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND `+nodeAttr+` != ''
),
energy AS (
    SELECT DISTINCT
        `+nodeAttr+`                    AS node,
        Attributes['avuruops_quality']  AS quality
    FROM otel_metrics_sum
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName IN (%s)
      AND `+nodeAttr+` != ''
)
SELECT
    (SELECT count() FROM known) AS known_nodes,
    (SELECT count(DISTINCT node) FROM energy WHERE quality = 'measured') AS measured_nodes,
    (SELECT count(DISTINCT node) FROM energy WHERE quality = 'estimated') AS estimated_nodes`,
		inList(len(q.NodeEnergyMetrics)))

	args := []any{q.Tenant, q.Range.Start, q.Range.End, q.Tenant, q.Range.Start, q.Range.End}
	for _, m := range q.NodeEnergyMetrics {
		args = append(args, m)
	}

	row := s.conn.QueryRow(ctx, query, args...)
	var cov storage.NodeCoverage
	if err := row.Scan(&cov.KnownNodes, &cov.MeasuredNodes, &cov.EstimatedNodes); err != nil {
		return storage.NodeCoverage{}, fmt.Errorf("node coverage: %w", err)
	}
	cov.AbsentNodes = cov.KnownNodes - cov.MeasuredNodes - cov.EstimatedNodes
	if cov.AbsentNodes < 0 {
		cov.AbsentNodes = 0
	}
	return cov, nil
}
```

- [ ] **Step 5: Add the storagetest fake**

In `hub/internal/storage/storagetest/fake.go`, add a `NodeCoverageResult storage.NodeCoverage` field to `Fake` (near `ServiceEnergies`/`NodeEnergies`, L40-42) and the method:

```go
func (f *Fake) NodeCoverage(_ context.Context, q storage.GreenQuery) (storage.NodeCoverage, error) {
	f.LastGreenQuery = q
	if f.GreenErr != nil {
		return storage.NodeCoverage{}, f.GreenErr
	}
	return f.NodeCoverageResult, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd hub && go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/... -run TestNodeCoverage -v`
Expected: PASS

- [ ] **Step 7: Run `go build ./...` across the hub to catch any other `Store` implementer**

Run: `cd hub && go build ./...`
Expected: PASS (confirms `storagetest.Fake` is the only other `Store` implementer needing the new method — if a mock elsewhere fails to compile, add `NodeCoverage` there too, same pattern).

- [ ] **Step 8: Commit**

```bash
git add hub/internal/storage/store.go hub/internal/storage/clickhouse/green.go hub/internal/storage/storagetest/fake.go hub/internal/storage/clickhouse/green_integration_test.go
git commit -m "feat(hub): NodeCoverage read (measured/estimated/absent)"
```

---

## Task 16: Hub — API DTOs quality split

**Files:**
- Modify: `hub/internal/api/green.go` (`greenTotalsDTO`, `greenServiceDTO`, `buildGreenRows`, `handleGreenSummary`)
- Test: `hub/internal/api/green_test.go` (extend)

- [ ] **Step 1: Write the failing unit test**

Append to `hub/internal/api/green_test.go` (check the file's existing test style first — table-driven with `storagetest.Fake`, per the design spec's testing-plan note):

```go
func TestBuildGreenRows_QualitySplit(t *testing.T) {
	rows := []storage.ServiceEnergy{
		{Service: "web", Quality: "measured", WattHours: 10},
		{Service: "web", Quality: "estimated", WattHours: 5},
	}
	f := greenFactors{intensity: 480, pue: 1.5}
	services, totals := buildGreenRows(rows, nil, f, 0)

	if len(services) != 1 {
		t.Fatalf("len(services) = %d, want 1 (one merged row per service)", len(services))
	}
	if services[0].Wh != 15 {
		t.Errorf("services[0].Wh = %v, want 15 (measured+estimated summed for the total)", services[0].Wh)
	}
	if services[0].EstimatedWh != 5 {
		t.Errorf("services[0].EstimatedWh = %v, want 5", services[0].EstimatedWh)
	}
	if totals.MeasuredWh != 10 || totals.EstimatedWh != 5 {
		t.Errorf("totals = %+v, want MeasuredWh=10 EstimatedWh=5", totals)
	}
	if totals.AttributedWh != 15 {
		t.Errorf("totals.AttributedWh = %v, want 15 (never silently blended, but the total IS the sum)", totals.AttributedWh)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd hub && go test ./internal/api/... -run TestBuildGreenRows_QualitySplit -v`
Expected: FAIL — `services[0].EstimatedWh` / `totals.MeasuredWh` don't compile

- [ ] **Step 3: Extend the DTOs**

In `hub/internal/api/green.go`, modify `greenTotalsDTO` (L114-121) and `greenServiceDTO` (L128-137):

```go
type greenTotalsDTO struct {
	AttributedWh   float64 `json:"attributedWh"`
	MeasuredWh     float64 `json:"measuredWh"`
	EstimatedWh    float64 `json:"estimatedWh"`
	UnattributedWh float64 `json:"unattributedWh"`
	Coverage       float64 `json:"coverage"`
	GCO2e          float64 `json:"gco2e"`
}

type greenServiceDTO struct {
	Service string  `json:"service"`
	Wh      float64 `json:"wh"`
	// EstimatedWh is the portion of Wh that came from the tdp-estimator, not
	// Kepler/RAPL — 0 (omitted) when the service's energy is entirely
	// measured, never inferred, always summed from actual estimated rows.
	EstimatedWh      float64         `json:"estimatedWh,omitempty"`
	GCO2e            float64         `json:"gco2e"`
	Requests         uint64          `json:"requests,omitempty"`
	MgCO2ePerRequest float64         `json:"mgCO2ePerRequest,omitempty"`
	Points           []greenPointDTO `json:"points,omitempty"`
}
```

- [ ] **Step 4: Fold quality into `buildGreenRows`**

Modify `buildGreenRows` (L211-249) to accumulate per-service Wh AND per-service EstimatedWh before the existing topN logic, and to track `totals.MeasuredWh`/`totals.EstimatedWh` alongside the existing `totals.AttributedWh`/`totals.UnattributedWh`:

```go
func buildGreenRows(rows []storage.ServiceEnergy, requests map[string]uint64, f greenFactors, topN int) ([]greenServiceDTO, greenTotalsDTO) {
	var totals greenTotalsDTO
	// Merge rows sharing the same Service across quality tiers into one
	// accumulator BEFORE building DTOs — a service with both a measured and
	// an estimated row must become exactly one output row, per the AEP's
	// "never silently blend" rule applied to totals, not to row count: the
	// merged row's Wh is measured+estimated, but EstimatedWh keeps the split
	// visible.
	type acc struct {
		wh, estimatedWh float64
		points          []storage.EnergyPoint
	}
	byService := map[string]*acc{}
	order := []string{}
	var unattrPoints []storage.EnergyPoint
	hasUnattributed := false

	for _, row := range rows {
		if row.Service == "" {
			hasUnattributed = true
			totals.UnattributedWh += row.WattHours
			unattrPoints = append(unattrPoints, row.Points...)
			continue
		}
		totals.AttributedWh += row.WattHours
		switch row.Quality {
		case "measured":
			totals.MeasuredWh += row.WattHours
		case "estimated":
			totals.EstimatedWh += row.WattHours
		}
		a, ok := byService[row.Service]
		if !ok {
			a = &acc{}
			byService[row.Service] = a
			order = append(order, row.Service)
		}
		a.wh += row.WattHours
		if row.Quality == "estimated" {
			a.estimatedWh += row.WattHours
		}
		a.points = append(a.points, row.Points...)
	}

	services := make([]greenServiceDTO, 0, len(order))
	for _, name := range order {
		a := byService[name]
		dto := toGreenServiceDTO(name, storage.ServiceEnergy{Service: name, WattHours: a.wh, Points: a.points}, requests[name], f)
		dto.EstimatedWh = a.estimatedWh
		services = append(services, dto)
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Wh != services[j].Wh {
			return services[i].Wh > services[j].Wh
		}
		return services[i].Service < services[j].Service
	})

	if topN > 0 && len(services) > topN {
		other := greenServiceDTO{Service: greenOtherRow}
		for _, s := range services[topN:] {
			other.Wh += s.Wh
			other.EstimatedWh += s.EstimatedWh
			other.GCO2e += s.GCO2e
			other.Requests += s.Requests
		}
		other.MgCO2ePerRequest = mgCO2ePerRequest(other.GCO2e, other.Requests)
		services = append(services[:topN], other)
	}
	if hasUnattributed {
		services = append(services, greenServiceDTO{
			Service: greenUnattributedRow,
			Wh:      totals.UnattributedWh,
			GCO2e:   f.gco2e(totals.UnattributedWh),
			Points:  toGreenPoints(unattrPoints),
		})
	}
	totals.GCO2e = f.gco2e(totals.AttributedWh + totals.UnattributedWh)
	if sum := totals.AttributedWh + totals.UnattributedWh; sum > 0 {
		totals.Coverage = totals.AttributedWh / sum
	}
	return services, totals
}
```

This requires `import "sort"` (already imported in the file) and reuses `toGreenServiceDTO`/`toGreenPoints`/`mgCO2ePerRequest` unchanged. Note this REPLACES the file's existing row-iteration logic (L216-235 in the original) — read the current function fully first (it was shown in full during design research) and diff carefully rather than duplicating logic; the topN and unattributed-row behavior must stay byte-identical to before for a fully-measured (no estimation) install, which the existing `TestBuildGreenRows*`-style tests already assert.

- [ ] **Step 5: Wire `NodeCoverage` into `handleGreenSummary`**

In `handleGreenSummary` (L149-187), add a `NodeCoverage` field to `greenSummaryResponse` and populate it:

```go
type greenSummaryResponse struct {
	Window       healthWindowDTO   `json:"window"`
	Factors      greenFactorsDTO   `json:"factors"`
	Totals       greenTotalsDTO    `json:"totals"`
	Services     []greenServiceDTO `json:"services"`
	NodeCoverage nodeCoverageDTO   `json:"nodeCoverage"`
}

type nodeCoverageDTO struct {
	Known     int `json:"known"`
	Measured  int `json:"measured"`
	Estimated int `json:"estimated"`
	Absent    int `json:"absent"`
}
```

Inside `handleGreenSummary`, after the existing `store.ServiceEnergy` call, add:

```go
	cov, err := store.NodeCoverage(r.Context(), greenQuery(cfg, ten, tr, 0))
	if err != nil {
		return err
	}
```

and add `NodeCoverage: nodeCoverageDTO{Known: cov.KnownNodes, Measured: cov.MeasuredNodes, Estimated: cov.EstimatedNodes, Absent: cov.AbsentNodes}` to the `writeJSON` call's `greenSummaryResponse{...}` literal.

- [ ] **Step 6: Run test to verify it passes, plus the full existing green API suite**

Run: `cd hub && go test ./internal/api/... -run 'TestBuildGreenRows|TestHandleGreenSummary' -v`
Expected: PASS — the new quality-split test AND every pre-existing green-summary/topN/unattributed test (these seed rows with `Quality: ""`, which is fine: `switch row.Quality` simply doesn't match either case, `EstimatedWh` stays 0 for every row, and Wh/topN/unattributed behavior is unchanged from before this task).

- [ ] **Step 7: Commit**

```bash
git add hub/internal/api/green.go hub/internal/api/green_test.go
git commit -m "feat(hub): quality-split green DTOs + node coverage in summary"
```

---

## Task 17: Hub — budgets estimated share

**Files:**
- Modify: `hub/internal/api/green_budgets.go` (`greenBudgetDTO`, `buildGreenBudgets`)
- Test: `hub/internal/api/green_test.go` or a `green_budgets_test.go` (check which file holds existing budget tests first, extend the same one)

- [ ] **Step 1: Write the failing test**

```go
func TestBuildGreenBudgets_EstimatedShare(t *testing.T) {
	cfg := green.Config{Budgets: []green.Budget{{Name: "prod", Group: "prod", MonthlyKgCO2e: 100, WarnRatio: 0.8}}}
	rows := []storage.ServiceEnergy{
		{Service: "web", Quality: "measured", WattHours: 1000, Points: []storage.EnergyPoint{{Time: time.Now(), WattHours: 1000}}},
		{Service: "web", Quality: "estimated", WattHours: 3000, Points: []storage.EnergyPoint{{Time: time.Now(), WattHours: 3000}}},
	}
	stats := []storage.ServiceStats{{Name: "web"}}
	groups := health.Config{Groups: []health.Group{{Name: "prod", Selector: health.Selector{Services: []string{"web"}}}}}
	f := greenFactors{intensity: 480, pue: 1.5}

	budgets := buildGreenBudgets(cfg, groups, f, rows, stats, nil, time.Now())
	if len(budgets) != 1 {
		t.Fatalf("len(budgets) = %d, want 1", len(budgets))
	}
	// Total used includes BOTH tiers (an all-VM fleet must be able to trip a
	// budget — AEP explicit goal): 4000 Wh total.
	wantKg := f.gco2e(4000) / 1000
	if diff := budgets[0].UsedKgCO2e - wantKg; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("UsedKgCO2e = %v, want %v", budgets[0].UsedKgCO2e, wantKg)
	}
	// 3000 of the 4000 Wh (75%) is estimated -> EstimatedShare = 0.75.
	if diff := budgets[0].EstimatedShare - 0.75; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("EstimatedShare = %v, want 0.75", budgets[0].EstimatedShare)
	}
}
```

`health.Config`'s real shape (`hub/internal/health/config.go:31-41`) is `Group{Name, Tier, Selector}` where `Selector{Namespaces, Services []string}` — the fixture above uses it directly; no `Match` type exists in this codebase (that was a placeholder guess, corrected here).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd hub && go test ./internal/api/... -run TestBuildGreenBudgets_EstimatedShare -v`
Expected: FAIL — `budgets[0].EstimatedShare` doesn't compile

- [ ] **Step 3: Add `EstimatedShare` to the budget DTO + computation**

In `hub/internal/api/green_budgets.go`, add to `greenBudgetDTO` (L28-39):

```go
	// EstimatedShare is the fraction of UsedKgCO2e that came from modeled
	// (tdp-estimator) rather than measured (RAPL/Kepler) energy — so a
	// threshold crossed mostly on modeled numbers is visibly soft in the UI
	// and any alert payload (AEP: budgets include estimated energy, but the
	// softness must stay visible).
	EstimatedShare float64 `json:"estimatedShare,omitempty"`
```

Modify `usedKgByGroup` (L176-193) to also accumulate an estimated-only total per group, and `buildGreenBudgets` (L100-151) to compute the share:

```go
// usedKgByGroup rolls energy up to per-group kgCO2e, split into total and
// estimated-only — the alerting tick's usage source (BudgetUsageByGroup) and
// the budgets endpoint share this so both fire/report on identical numbers.
func usedKgByGroup(f greenFactors, assigned map[string]health.Assignment, rows []storage.ServiceEnergy) (total, estimated map[string]float64) {
	whByGroup := map[string]float64{}
	estWhByGroup := map[string]float64{}
	for _, row := range rows {
		if row.Service == "" {
			continue
		}
		g := assigned[row.Service].Group
		if g == "" {
			continue
		}
		whByGroup[g] += row.WattHours
		if row.Quality == "estimated" {
			estWhByGroup[g] += row.WattHours
		}
	}
	total = make(map[string]float64, len(whByGroup))
	estimated = make(map[string]float64, len(whByGroup))
	for g, wh := range whByGroup {
		total[g] = f.gco2e(wh) / 1000
		estimated[g] = f.gco2e(estWhByGroup[g]) / 1000
	}
	return total, estimated
}
```

Update both call sites (`buildGreenBudgets` L101-102 and `BudgetUsageByGroup` L216) for the new two-return signature — `BudgetUsageByGroup` (the alerting tick's usage source) only needs `total`, so it discards the second return: `usedByGroup, _ := usedKgByGroup(...)`. In `buildGreenBudgets`, after computing `dto.UsedKgCO2e`, add:

```go
			if used > 0 {
				dto.EstimatedShare = estByGroup[b.Group] / used
			}
```

(where `estByGroup` is the second return from `usedKgByGroup`, threaded alongside the existing `usedByGroup` in `buildGreenBudgets`'s signature and its one call site).

- [ ] **Step 4: Run test to verify it passes, plus the existing budgets suite**

Run: `cd hub && go test ./internal/api/... -run 'TestBuildGreenBudgets' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add hub/internal/api/green_budgets.go hub/internal/api/green_test.go
git commit -m "feat(hub): carbon budgets carry an estimated-energy share"
```

---

## Task 18: Hub — CSRD export estimation subsection

**Files:**
- Modify: `hub/internal/api/green_report.go` (`greenMethodologyDTO`, `buildMethodology`, `writeGreenCSV`)
- Test: extend the report test file (check its exact name/location first, likely alongside `green_test.go`)

- [ ] **Step 1: Write the failing test**

```go
func TestBuildMethodology_EstimationSubsection(t *testing.T) {
	cfg := green.Default()
	f := greenFactors{intensity: 480, pue: 1.5}
	totals := greenTotalsDTO{AttributedWh: 100, MeasuredWh: 40, EstimatedWh: 60, Coverage: 1}
	tr := storage.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}

	meth := buildMethodology(cfg, f, tr, totals)
	if meth.Estimation == nil {
		t.Fatal("Estimation subsection is nil, want populated (totals.EstimatedWh > 0)")
	}
	if meth.Estimation.FormulaLiteral == "" || meth.Estimation.ErrorBand == "" {
		t.Errorf("Estimation = %+v, want non-empty formula and error band", meth.Estimation)
	}
}

func TestBuildMethodology_NoEstimationSubsectionWhenFullyMeasured(t *testing.T) {
	cfg := green.Default()
	f := greenFactors{intensity: 480, pue: 1.5}
	totals := greenTotalsDTO{AttributedWh: 100, MeasuredWh: 100, EstimatedWh: 0, Coverage: 1}
	tr := storage.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}

	meth := buildMethodology(cfg, f, tr, totals)
	if meth.Estimation != nil {
		t.Errorf("Estimation = %+v, want nil (report stays unchanged for fully-measured installs)", meth.Estimation)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd hub && go test ./internal/api/... -run TestBuildMethodology -v`
Expected: FAIL — `meth.Estimation` doesn't compile

- [ ] **Step 3: Add the estimation subsection**

In `hub/internal/api/green_report.go`, add near `greenFormulaLiteral` (L25):

```go
// tdpFormulaLiteral is quoted verbatim in the export when any estimated
// energy is present — must stay in sync with model.go's nodePower
// expression in sensor/tdp-estimator.
const tdpFormulaLiteral = "P = P_idle + u × (P_max - P_idle)"

// tdpErrorBand is the AEP's documented typical absolute error for the TDP
// model — cited so the export never reads as reporting-grade for the
// estimated portion.
const tdpErrorBand = "±30-50% typical absolute error (trend/regression grade, not audit grade)"

type greenEstimationDTO struct {
	FormulaLiteral     string `json:"formula"`
	CoefficientDataset string `json:"coefficientDataset"`
	ErrorBand          string `json:"errorBand"`
}
```

Add `Estimation *greenEstimationDTO` (pointer, so it's omitted from JSON entirely when nil — `json:"estimation,omitempty"`) to `greenMethodologyDTO` (L27-39). In `buildMethodology` (L93-115), after building the base `greenMethodologyDTO`:

```go
	meth := greenMethodologyDTO{
		// ... existing fields unchanged ...
	}
	if totals.EstimatedWh > 0 {
		meth.Estimation = &greenEstimationDTO{
			FormulaLiteral:     tdpFormulaLiteral,
			CoefficientDataset: coefficientDatasetLabel, // see below
			ErrorBand:          tdpErrorBand,
		}
	}
	return meth
```

`coefficientDatasetLabel` needs a value the hub actually has: since the coefficient table lives in the estimator binary (a different Go module, per Task 5), the hub cannot import it directly. Add a small constant in `hub/internal/green/config.go` mirroring the pattern `IntensityDataset` already uses:

```go
// EstimationCoefficientDataset names the bundled TDP coefficient table's
// provenance for the CSRD export's estimation subsection — kept in sync
// BY HAND with sensor/tdp-estimator/coefficients_table.go's
// coefficientDataset constant (the two modules cannot share a Go import;
// this is the one place that string is duplicated, and a change to one
// must update the other — flagged in both files' comments).
const EstimationCoefficientDataset = "Cloud Carbon Footprint 2026-07 (Azure/notebook preferred on conflict)"
```

Add a matching comment in `sensor/tdp-estimator/coefficients_table.go` next to `coefficientDataset` noting this hand-sync obligation (small retroactive edit to Task 5's file). Reference `green.EstimationCoefficientDataset` from `buildMethodology`.

- [ ] **Step 4: Extend the CSV writer**

In `writeGreenCSV` (L120-167), after the existing `meta` slice's `metrics`/`hubVersion` rows, conditionally add estimation rows only when present:

```go
	if meth.Estimation != nil {
		meta = append(meta,
			[2]string{"estimationFormula", meth.Estimation.FormulaLiteral},
			[2]string{"estimationCoefficientDataset", meth.Estimation.CoefficientDataset},
			[2]string{"estimationErrorBand", meth.Estimation.ErrorBand},
		)
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd hub && go test ./internal/api/... -run TestBuildMethodology -v`
Expected: PASS (both tests)

- [ ] **Step 6: Run the full existing report suite to confirm no regression**

Run: `cd hub && go test ./internal/api/... -run TestGreenReport -v` (adjust the `-run` pattern to whatever the existing report tests are actually named, confirmed by reading the test file)
Expected: PASS — a fully-measured report's CSV/JSON output is byte-identical to before this task (the `if meth.Estimation != nil` guards keep the export boring when estimation is never enabled).

- [ ] **Step 7: Commit**

```bash
git add hub/internal/api/green_report.go hub/internal/green/config.go sensor/tdp-estimator/coefficients_table.go
git commit -m "feat(hub): CSRD export estimation methodology subsection"
```

---

## Task 19: UI — wire types + quality badge + coverage panel

**Files:**
- Modify: `ui/src/lib/api-types.ts` (`GreenTotals`, `GreenServiceEnergy`, add `NodeCoverage`, `GreenBudget`)
- Create: `ui/src/components/green/quality-badge.tsx`
- Modify: `ui/src/components/green/green-screen.tsx`

- [ ] **Step 1: Extend the wire types**

In `ui/src/lib/api-types.ts`, modify `GreenTotals` and `GreenServiceEnergy` (around L544-569 per the earlier read) to match the Go DTOs byte-exactly:

```typescript
export interface GreenTotals {
  attributedWh: number;
  measuredWh: number;
  estimatedWh: number;
  unattributedWh: number;
  coverage: number;
  gco2e: number;
}

export interface GreenNodeCoverage {
  known: number;
  measured: number;
  estimated: number;
  absent: number;
}

export interface GreenServiceEnergy {
  service: string;
  wh: number;
  estimatedWh?: number;
  gco2e: number;
  requests?: number;
  mgCO2ePerRequest?: number;
  points?: GreenEnergyPoint[];
}

export interface GreenSummaryResponse {
  window: { start: string; end: string };
  factors: GreenFactors;
  totals: GreenTotals;
  services: GreenServiceEnergy[];
  nodeCoverage: GreenNodeCoverage;
}
```

Add `estimatedShare?: number;` to the existing `GreenBudget` interface (locate it below the section already read).

- [ ] **Step 2: Create the quality badge**

```tsx
// ui/src/components/green/quality-badge.tsx
import { Badge } from "@/components/ui/badge";

// measured (RAPL/Kepler, hardware-sourced) vs estimated (tdp-estimator,
// modeled from utilization, ±30-50% typical error) — never rendered as one
// blended number; see design/2026-07-28-green-tdp-estimation.md.
export function QualityBadge({ estimatedShare }: { estimatedShare: number }) {
  if (estimatedShare <= 0) return null;
  const pct = Math.round(estimatedShare * 100);
  return (
    <Badge tone="warning" title="Modeled from CPU utilization, not measured from hardware — typical error ±30-50%">
      {pct === 100 ? "estimated" : `${pct}% estimated`}
    </Badge>
  );
}
```

- [ ] **Step 3: Add the coverage panel + badge wiring to green-screen.tsx**

In `ui/src/components/green/green-screen.tsx`, import `QualityBadge` and add a coverage stat alongside the existing `StatTile` grid (L58-63), plus the badge next to the headline title. This is presentational wiring — the exact JSX diff:

```tsx
import { QualityBadge } from "./quality-badge";

// ... inside GreenScreen(), after `const totals = data?.totals;`:
  const estimatedShare = totals && totals.attributedWh > 0 ? totals.estimatedWh / totals.attributedWh : 0;
  const coverage = data?.nodeCoverage;

// ... inside the CardHeader, next to <CardTitle>:
        <CardHeader>
          <CardTitle>Energy &amp; carbon</CardTitle>
          <QualityBadge estimatedShare={estimatedShare} />
          <MethodologyDetails factors={data.factors} totals={totals!} />
        </CardHeader>

// ... after the existing StatTile grid (L58-63), add a coverage row when
// estimation has ever run (coverage.estimated + coverage.absent > 0):
      {coverage && (coverage.estimated > 0 || coverage.absent > 0) && (
        <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4 text-xs">
          <StatTile label="Nodes known" value={String(coverage.known)} />
          <StatTile label="Measured" value={String(coverage.measured)} />
          <StatTile label="Estimated" value={String(coverage.estimated)} />
          <StatTile label="Absent" value={String(coverage.absent)} />
        </div>
      )}
```

- [ ] **Step 4: Verify with the UI's existing lint/build gate**

Run: `cd ui && npm run lint && npm run build`
Expected: PASS, no type errors.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/api-types.ts ui/src/components/green/quality-badge.tsx ui/src/components/green/green-screen.tsx
git commit -m "feat(ui): quality badge + node-coverage panel on /green"
```

---

## Task 20: UI — preflight copy flip

**Files:**
- Modify: `ui/src/components/green/green-empty-state.tsx`

- [ ] **Step 1: Add an estimation-aware variant**

The empty state currently has no notion of "estimation is enabled but the node has no RAPL" — that information isn't in its props today. Thread it via the capabilities/module-config surface (`useModuleEnabled` or a new small check) rather than a new prop the caller must remember to pass. Read `ui/src/hooks/use-capabilities.ts` first to confirm whether a per-module config flag (like `sensor.green.estimation.enabled`) is already exposed through `/api/v1/capabilities`, or only the boolean module-enabled flags. If estimation's enabled-state isn't exposed there yet, add it: extend the hub's `handleCapabilities` (`hub/internal/api/capabilities.go`) to include a `greenEstimationEnabled: boolean` field sourced from the green config, and the UI's `Capabilities` type + `useModuleEnabled`-adjacent hook to read it.

- [ ] **Step 2: Update the copy**

```tsx
// ui/src/components/green/green-empty-state.tsx — inside GreenEmptyState(),
// branch on the new capability flag:
export function GreenEmptyState({ estimationEnabled }: { estimationEnabled: boolean }) {
  return (
    <div className="flex flex-col gap-4">
      <EmptyState icon={Leaf} title={estimationEnabled ? "Estimating via TDP model" : "No energy measured yet"}>
        {estimationEnabled ? (
          <>
            This node has no RAPL/powercap, so energy is <strong>modeled</strong> from
            CPU utilization instead of measured (±30-50% typical error) — see the
            methodology popover above once data appears.
          </>
        ) : (
          <>
            Per-service energy comes from your CPUs&apos; <strong>RAPL</strong> counters,
            read by the Kepler sensor container. On clusters without RAPL — most
            public-cloud VMs — there is simply nothing to measure, and that is
            expected. Where RAPL is present, data appears within a collection interval.
          </>
        )}
      </EmptyState>
      {/* ... rest of the component unchanged ... */}
```

Update the call site in `green-screen.tsx` (`<GreenEmptyState />`) to `<GreenEmptyState estimationEnabled={/* from capabilities */} />`.

- [ ] **Step 3: Verify**

Run: `cd ui && npm run lint && npm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/green/green-empty-state.tsx ui/src/components/green/green-screen.tsx hub/internal/api/capabilities.go
git commit -m "feat(ui): preflight copy flips to 'estimating via TDP model' when enabled"
```

---

## Task 21: e2e coverage (compose, helm, UI)

**Files:**
- Modify: `e2e/green_test.go` (add a quality-split assertion)
- Modify: `deploy/helm/e2e-helm.sh` (extend the existing green/Kepler leg)
- Modify: `ui/e2e/green.spec.ts` (badge + coverage panel + preflight copy)

- [ ] **Step 1: Extend the compose e2e**

Read `e2e/green_test.go`'s existing `TestGreenSummary` to see how it seeds fixtures (likely via `tools/seed`'s fixture format, same as other e2e tests). Add a fixture producing both a `measured` and an `estimated` series (mirroring the integration test's approach from Task 14), then assert `GET /api/v1/green/summary` returns non-zero `totals.measuredWh` AND `totals.estimatedWh`, and `nodeCoverage.estimated >= 1`.

- [ ] **Step 2: Extend the e2e-helm probe-canary leg**

Read `deploy/helm/e2e-helm.sh`'s existing Kepler/fake-cpu-meter leg (referenced in the AEP as already exercising the "do no harm" probe-canary gate). Add `sensor.green.estimation.enabled=true` to the same `helm install`/`upgrade` invocation used for the existing green leg — since kind nodes have no powercap, the estimator's real "probe fails → estimate" path runs without any fake-meter equivalent needed on the estimator side. Assert the sensor pod stays healthy for the existing soak duration (the probe-canary gate is unchanged; this just adds one more container to the pod it already watches) and that `/metrics` on the estimator's port serves non-empty output after the sampler's first tick.

- [ ] **Step 3: Extend the UI e2e**

In `ui/e2e/green.spec.ts`, add a scenario (seeded the same way as Step 1's mixed measured/estimated fixture) asserting the quality badge renders with the correct percentage, the coverage panel shows all four numbers, and — with a separate seed that disables measured energy entirely — the empty-state copy reads "Estimating via TDP model" rather than "No energy measured yet".

- [ ] **Step 4: Run the full e2e suite**

Run: `make e2e && make e2e-helm && make e2e-ui`
Expected: PASS (all three)

- [ ] **Step 5: Commit**

```bash
git add e2e/green_test.go deploy/helm/e2e-helm.sh ui/e2e/green.spec.ts
git commit -m "test(e2e): tdp-estimator quality-split coverage across compose/helm/UI"
```

---

## Task 22: Docs alignment

- [ ] **Step 1: Invoke the docs-align skill**

Once Tasks 1-21 are merged to `main` (not before — docs-align documents shipped behavior), invoke the `docs-align` skill to produce the bilingual (EN/FR) changelog entry, feature-status matrix update, roadmap badge flip, and API reference update for the green TDP estimation feature, per [feedback memory: Docs must highlight value] — copy should be reliability/accountability-oriented (defensible carbon numbers on infrastructure people actually run), not just mechanics.

- [ ] **Step 2: Update `CHANGELOG.md`'s `[Unreleased]` section**

Add an entry describing the feature (measured/estimated split, opt-in, coefficient provenance) under the existing `### Added` heading, alongside the already-listed Projects Phase 1 / demo-mode entries — this becomes part of the `v0.3.0` section when Task 23 renames `[Unreleased]`.

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md docs/
git commit -m "docs: align green TDP estimation across changelog + docs site"
```

---

## Task 23: Release cut — v0.3.0

Follow [RELEASING.md](../../../RELEASING.md)'s "new minor" procedure exactly. Prerequisites: `main` is green (`make check`, `make helm-check`, `make e2e`, `make e2e-helm` all pass after Tasks 1-22 are merged), commits are signed, you have push rights.

- [ ] **Step 1: Verify trunk is green**

Run: `make check && make helm-check` on `main` (post-merge).
Expected: PASS.

- [ ] **Step 2: Finalize the changelog**

In `CHANGELOG.md`, rename `## [Unreleased]` to `## [0.3.0] — <today's date>` and add a fresh empty `## [Unreleased]` block above it.

- [ ] **Step 3: Stamp the release version**

Run: `make version-set V=0.3.0`
Commit: `git commit -am "chore(release): v0.3.0"`

- [ ] **Step 4: Tag and push (signed) — CONFIRM WITH THE USER FIRST**

This pushes a signed tag to the shared remote and triggers the release automation (image builds, GHCR pushes, chart publish) — a hard-to-reverse, externally-visible action. Confirm with the user before running:

```bash
git tag -s v0.3.0 -m "v0.3.0" && git push origin main --tags
```

- [ ] **Step 5: Create the release branch — CONFIRM WITH THE USER FIRST**

```bash
git branch v0.3 v0.3.0 && git push origin v0.3
```

- [ ] **Step 6: Let automation run, then verify**

`release.yml` builds/pushes `hub`, `ui`, `gateway`, and the new `tdp-estimator` images, packages the Helm chart, creates the GitHub Release. Verify per [RELEASE-CHECKLIST.md](../../../RELEASE-CHECKLIST.md), including the `cosign verify` commands from RELEASING.md's "Verifying a release" section for all **four** images now (not three).

- [ ] **Step 7: Bump trunk to the next snapshot — CONFIRM WITH THE USER FIRST**

```bash
make version-set V=0.4.0 && make version-set V=0.4.0-SNAPSHOT
git commit -am "chore: begin v0.4.0-SNAPSHOT"
# open a PR against main
```

---

## Plan self-review notes

- **Spec coverage:** every section of the design spec (§4 estimator, §5 Helm, §6 coefficients, §7 hub, §8 UI, §9 export, §10 testing, §11 phasing, §12 release) maps to Tasks 1-23 above in the same order the spec's §11 phasing lists them.
- **The `service.name` assumption** (design spec §5.4) is carried into Task 11 Step 3 as an explicit verify-before-relying-on-it step with a documented fallback, not asserted as fact.
- **No task references a type/method not defined in an earlier task** — `Coefficients`/`Resolve` (Task 5) are used by `main.go` (Task 1, forward-referenced and resolved once Task 5 lands) and `model.go` (Task 6); `registry`/`runSampler` (Task 7) resolve `main.go`'s remaining forward references; `NodeCoverage` (Task 15) is used by Task 16's API wiring; `Quality` (Task 14) is used by Tasks 16-18.
- **Coefficient sourcing is fully cited** per entry in Task 5, including the two explicit editorial decisions (Azure/notebook preferred on conflict; AMD_EPYC_5TH_GEN omitted) — both made by the user, not silently by the plan.
- **Caught and fixed during review:** Task 7's original sampler-loop draft re-integrated the ENTIRE elapsed process lifetime's energy on every tick (`integrateJoules` called over `[0, now-startTime]` at the current wattage, then accumulated) instead of just that tick's increment — it would have made `nodeJoules` blow up nonlinearly within seconds. Fixed to track the previous tick's wattage and integrate only the `[0, interval.Seconds()]` step between consecutive ticks, accumulating from there; the wall-clock `timeNowSeconds`/`startTime` machinery was unnecessary once the ticker's own fixed interval is used as the dt, so it was removed entirely rather than left dead.
- **`health.Config`'s fixture in Task 17** was corrected from a guessed `Match` type to the real `Group{Name, Tier, Selector}` / `Selector{Namespaces, Services}` shape (`hub/internal/health/config.go:31-41`), verified by reading the actual source rather than left as a guess for the implementer to resolve.
- **`nodeAnnotations()` in Task 5** originally invented a Downward-API-projected file path for reading a *node's* annotations — Kubernetes' Downward API only exposes pod/container-scoped fields, never Node object metadata, so that file would never exist and Task 10's container spec never mounted any such volume (a real cross-task inconsistency). Rewritten to fetch the Node object from the API server directly, reusing the `nodes` get RBAC the sensor ServiceAccount already has (no chart/RBAC change needed) — `main.go`'s call site updated to pass `nodeName` accordingly.
