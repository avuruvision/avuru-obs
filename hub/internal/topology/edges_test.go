package topology

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func svc(name string, evidence bool) storage.ServiceStats {
	return storage.ServiceStats{Name: name, TransportEvidence: evidence}
}

func edge(src, dst string, count uint64) storage.ServiceEdge {
	return storage.ServiceEdge{Source: src, Target: dst, Count: count}
}

func TestLabelledTransportReadsTheRowsRatherThanQuerying(t *testing.T) {
	got := LabelledTransport([]storage.ServiceStats{
		svc("frontend", false), svc("istio-ingressgateway", true), svc("payments", false),
	})
	if len(got) != 1 || got[0] != "istio-ingressgateway" {
		t.Fatalf("want only the labelled workload, got %v", got)
	}
}

func TestTransportNamesAreSortedSoTheQueryArgIsStable(t *testing.T) {
	cls := New(Config{}).WithEvidence([]string{"zeta-proxy", "alpha-proxy"})
	got := TransportNames(cls, []storage.ServiceStats{
		svc("zeta-proxy", true), svc("app", false), svc("alpha-proxy", true),
	})
	if len(got) != 2 || got[0] != "alpha-proxy" || got[1] != "zeta-proxy" {
		t.Fatalf("want sorted transport names, got %v", got)
	}
}

// A pair that talks BOTH ways — some calls through the mesh, some around it —
// is one dependency, so the counts add rather than one replacing the other.
func TestMergeCollapsedAddsToAPairThatAlsoTalksDirectly(t *testing.T) {
	direct := edge("a", "b", 10)
	direct.ErrorCount = 1
	recovered := storage.ServiceEdge{
		Source: "a", Target: "b", Count: 40, ErrorCount: 4,
		CollapsedCount: 40, CollapsedErrors: 4,
		ViaTransport: []string{"proxy"}, P50: 9, P95: 21,
	}

	got := MergeCollapsed([]storage.ServiceEdge{direct}, []storage.ServiceEdge{recovered})
	if len(got) != 1 {
		t.Fatalf("want one merged edge, got %d", len(got))
	}
	if got[0].Count != 50 || got[0].ErrorCount != 5 {
		t.Fatalf("counts must add: got %d calls / %d errors", got[0].Count, got[0].ErrorCount)
	}
	if got[0].CollapsedCount != 40 {
		t.Fatalf("the mesh-carried portion must stay marked, got %d", got[0].CollapsedCount)
	}
	if len(got[0].ViaTransport) != 1 {
		t.Fatalf("viaTransport must survive the merge, got %v", got[0].ViaTransport)
	}
	// Latency describes the dominant path — quantiles over two populations
	// cannot be averaged after the fact.
	if got[0].P95 != 21 {
		t.Fatalf("want the busier path's p95, got %v", got[0].P95)
	}
}

func TestMergeCollapsedAppendsAPairNothingObservedDirectly(t *testing.T) {
	got := MergeCollapsed(nil, []storage.ServiceEdge{edge("a", "b", 5)})
	if len(got) != 1 || got[0].Source != "a" {
		t.Fatalf("want the recovered edge appended, got %v", got)
	}
}

// The double-count rule: the hops and the dependency recovered from them are
// the same requests, so a caller sees one representation, never both.
func TestHideTransportDropsTheHopsAndKeepsTheRecoveredEdge(t *testing.T) {
	services := []storage.ServiceStats{svc("frontend", false), svc("payments", false), svc("proxy", true)}
	cls := New(Config{}).WithEvidence([]string{"proxy"})
	edges := []storage.ServiceEdge{
		edge("frontend", "proxy", 40),
		edge("proxy", "payments", 40),
		edge("frontend", "payments", 40), // recovered
	}

	got := HideTransport(services, edges, cls, "")
	if len(got) != 1 {
		t.Fatalf("want only the recovered dependency, got %d edges: %v", len(got), got)
	}
	if got[0].Source != "frontend" || got[0].Target != "payments" {
		t.Fatalf("wrong edge survived: %v", got[0])
	}
}

// A caller whose SUBJECT is a proxy must not have that proxy hidden — doing so
// deletes the thing being described.
func TestHideTransportKeepsTheOneItIsAsked(t *testing.T) {
	services := []storage.ServiceStats{svc("frontend", false), svc("proxy", true), svc("other-proxy", true)}
	cls := New(Config{}).WithEvidence([]string{"proxy", "other-proxy"})
	edges := []storage.ServiceEdge{
		edge("frontend", "proxy", 10),
		edge("proxy", "other-proxy", 3),
	}

	got := HideTransport(services, edges, cls, "proxy")
	if len(got) != 1 || got[0].Source != "frontend" || got[0].Target != "proxy" {
		t.Fatalf("want the kept proxy's own edge, got %v", got)
	}
}

// An edge can legitimately point at something absent from the services list —
// a workload the sensor saw traffic to that never sent telemetry. Keeping it is
// what lets a caller report that peer instead of deleting the connection.
func TestHideTransportKeepsAnEdgeToSomethingUnknown(t *testing.T) {
	services := []storage.ServiceStats{svc("frontend", false), svc("proxy", true)}
	cls := New(Config{}).WithEvidence([]string{"proxy"})
	edges := []storage.ServiceEdge{edge("frontend", "cache-nobody-instrumented", 7)}

	got := HideTransport(services, edges, cls, "")
	if len(got) != 1 {
		t.Fatalf("an unresolved peer's edge must survive, got %v", got)
	}
}

func TestHideTransportIsAPassThroughOnAnUnmeshedInstall(t *testing.T) {
	services := []storage.ServiceStats{svc("a", false), svc("b", false)}
	edges := []storage.ServiceEdge{edge("a", "b", 3)}

	got := HideTransport(services, edges, New(Config{}), "")
	if len(got) != 1 {
		t.Fatalf("want the edge set unchanged, got %v", got)
	}
}
