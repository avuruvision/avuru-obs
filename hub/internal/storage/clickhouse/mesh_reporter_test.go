package clickhouse

import (
	"reflect"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func row(metric, reporter, src, dst, policy string, n uint64) meshTrafficRow {
	return meshTrafficRow{
		Metric: metric, Reporter: reporter,
		SrcNS: "shop", Src: src, DstNS: "shop", Dst: dst,
		Policy: policy, N: n, Series: 1,
	}
}

// The preference rule, end to end: which side of an edge is believed, and
// that the other side's numbers never leak into the totals.
func TestPreferReporter(t *testing.T) {
	cases := []struct {
		name          string
		rows          []meshTrafficRow
		wantReporter  string
		wantCounts    storage.MeshSecurityCounts
		wantCallers   []storage.MeshCaller
		wantEdgeCount int
	}{
		{
			name: "destination beats source",
			rows: []meshTrafficRow{
				row(meshRequestsMetric, "source", "web", "api", meshPolicyMTLS, 35),
				row(meshRequestsMetric, "destination", "web", "api", meshPolicyMTLS, 40),
			},
			wantReporter:  "destination",
			wantCounts:    storage.MeshSecurityCounts{MTLSRequests: 40},
			wantEdgeCount: 1,
		},
		{
			name: "waypoint beats source",
			rows: []meshTrafficRow{
				row(meshRequestsMetric, "source", "web", "api", meshPolicyMTLS, 35),
				row(meshRequestsMetric, "waypoint", "web", "api", meshPolicyMTLS, 38),
			},
			wantReporter:  "waypoint",
			wantCounts:    storage.MeshSecurityCounts{MTLSRequests: 38},
			wantEdgeCount: 1,
		},
		{
			name: "source-only is kept, not dropped",
			rows: []meshTrafficRow{
				row(meshRequestsMetric, "source", "web", "api", meshPolicyMTLS, 35),
			},
			wantReporter:  "source",
			wantCounts:    storage.MeshSecurityCounts{MTLSRequests: 35},
			wantEdgeCount: 1,
		},
		{
			name: "plaintext callers listed loudest first, across both units",
			rows: []meshTrafficRow{
				row(meshRequestsMetric, "destination", "web", "api", meshPolicyMTLS, 100),
				row(meshRequestsMetric, "destination", "legacy", "api", meshPolicyNone, 7),
				row(meshTCPOpenedMetric, "destination", "batch", "api", meshPolicyNone, 3),
				row(meshTCPOpenedMetric, "destination", "cron", "api", meshPolicyNone, 0),
				row(meshRequestsMetric, "destination", "probe", "api", "", 2),
			},
			wantReporter: "destination",
			wantCounts: storage.MeshSecurityCounts{
				MTLSRequests: 100, PlaintextRequests: 7, UnknownRequests: 2, PlaintextConnections: 3,
			},
			wantCallers: []storage.MeshCaller{
				{Namespace: "shop", Workload: "legacy", Units: 7},
				{Namespace: "shop", Workload: "batch", Units: 3},
			},
			wantEdgeCount: 5,
		},
		{
			name: "idle workload is observed, not absent",
			rows: []meshTrafficRow{
				row(meshRequestsMetric, "destination", "web", "api", meshPolicyMTLS, 0),
			},
			wantReporter:  "destination",
			wantCounts:    storage.MeshSecurityCounts{},
			wantEdgeCount: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workloads, edges := preferReporter(tc.rows)
			if len(workloads) != 1 {
				t.Fatalf("workloads = %+v, want exactly one", workloads)
			}
			w := workloads[0]
			if w.Reporter != tc.wantReporter {
				t.Errorf("reporter = %q, want %q", w.Reporter, tc.wantReporter)
			}
			if w.Counts != tc.wantCounts {
				t.Errorf("counts = %+v, want %+v", w.Counts, tc.wantCounts)
			}
			if !reflect.DeepEqual(w.PlaintextCallers, tc.wantCallers) {
				t.Errorf("plaintext callers = %+v, want %+v", w.PlaintextCallers, tc.wantCallers)
			}
			if len(edges) != tc.wantEdgeCount {
				t.Errorf("edges = %+v, want %d", edges, tc.wantEdgeCount)
			}
			for _, e := range edges {
				if e.Reporter != tc.wantReporter {
					t.Errorf("edge %s->%s reporter = %q, want %q", e.Source, e.Target, e.Reporter, tc.wantReporter)
				}
			}
		})
	}
}

// The preference is per DESTINATION: one workload seen from its own sidecar
// and another seen only from its callers each keep the best account they have,
// and the edges of each add up to its workload.
func TestPreferReporterIsPerDestination(t *testing.T) {
	now := time.Now()
	rows := []meshTrafficRow{
		row(meshRequestsMetric, "destination", "web", "api", meshPolicyMTLS, 40),
		row(meshRequestsMetric, "source", "web", "api", meshPolicyMTLS, 35),
		row(meshRequestsMetric, "source", "web", "legacy-db", meshPolicyNone, 9),
	}
	rows[0].Newest = now
	workloads, edges := preferReporter(rows)
	if len(workloads) != 2 || len(edges) != 2 {
		t.Fatalf("got %d workloads, %d edges; want 2 and 2", len(workloads), len(edges))
	}
	api, legacy := workloads[0], workloads[1]
	if api.Workload != "api" || api.Reporter != "destination" || api.Counts.MTLSRequests != 40 {
		t.Errorf("api = %+v", api)
	}
	if !api.LastSeen.Equal(now) {
		t.Errorf("api lastSeen = %v, want %v", api.LastSeen, now)
	}
	if legacy.Workload != "legacy-db" || legacy.Reporter != "source" || legacy.Counts.PlaintextRequests != 9 {
		t.Errorf("legacy-db = %+v", legacy)
	}
	var edgeTotal uint64
	for _, e := range edges {
		edgeTotal += e.Counts.MTLSRequests + e.Counts.PlaintextRequests
	}
	if edgeTotal != 49 {
		t.Errorf("edges total %d, want 49 — the losing side leaked into an edge", edgeTotal)
	}
}

func TestFoldRequests(t *testing.T) {
	req := func(reporter, src, flags, code, version string, n uint64) meshRequestRow {
		return meshRequestRow{Reporter: reporter, SrcNS: "shop", Src: src, Flags: flags, Code: code, Version: version, N: n}
	}
	t.Run("nothing measured", func(t *testing.T) {
		got := foldRequests(nil)
		if got.Measured || len(got.ResponseFlags) != 0 || len(got.Callers) != 0 {
			t.Errorf("empty fold = %+v", got)
		}
	})
	t.Run("flags, versions and 5xx by caller, from the preferred side only", func(t *testing.T) {
		got := foldRequests([]meshRequestRow{
			req("destination", "web", "-", "200", "v1", 90),
			req("destination", "web", "UO", "503", "v1", 5),
			req("destination", "mobile", "-", "500", "v2", 3),
			req("destination", "mobile", "-", "200", "v2", 1),
			req("source", "web", "-", "200", "v1", 1000), // the caller's account, dropped
		})
		if !got.Measured || got.Reporter != "destination" {
			t.Fatalf("measured=%v reporter=%q", got.Measured, got.Reporter)
		}
		wantFlags := map[string]uint64{"-": 94, "UO": 5}
		if !reflect.DeepEqual(got.ResponseFlags, wantFlags) {
			t.Errorf("flags = %v, want %v", got.ResponseFlags, wantFlags)
		}
		wantVersions := map[string]uint64{"v1": 95, "v2": 4}
		if !reflect.DeepEqual(got.DestinationVersions, wantVersions) {
			t.Errorf("versions = %v, want %v", got.DestinationVersions, wantVersions)
		}
		wantCallers := []storage.MeshCallerOutcome{
			{Namespace: "shop", Workload: "web", Requests: 95, Errors5xx: 5},
			{Namespace: "shop", Workload: "mobile", Requests: 4, Errors5xx: 3},
		}
		if !reflect.DeepEqual(got.Callers, wantCallers) {
			t.Errorf("callers = %+v, want %+v", got.Callers, wantCallers)
		}
	})
}
