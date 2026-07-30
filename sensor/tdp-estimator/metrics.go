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
	mu       sync.Mutex
	nodeName string
	nodeJ    float64
	podJ     map[podKey]float64
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
// (kepler_{node,pod}_cpu_joules_total). A `# TYPE ... counter` line MUST
// precede each family: without it the metric is "untyped", and the
// otel-agent's prometheus receiver maps untyped series to Gauge datapoints
// (otel_metrics_gauge) instead of Sum (otel_metrics_sum) — silently breaking
// the hub's green SQL and this estimator's whole purpose of matching Kepler's
// own (TYPE-hinted) counter semantics.
func (r *registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if r.nodeName != "" {
		fmt.Fprintf(w, "# TYPE kepler_node_cpu_joules_total counter\n")
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
	if len(keys) > 0 {
		fmt.Fprintf(w, "# TYPE kepler_pod_cpu_joules_total counter\n")
	}
	for _, k := range keys {
		fmt.Fprintf(w, "kepler_pod_cpu_joules_total{pod_name=%q,pod_namespace=%q} %s\n",
			k.name, k.namespace, strconv.FormatFloat(r.podJ[k], 'f', -1, 64))
	}
}
