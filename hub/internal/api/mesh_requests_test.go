package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// Flags sorted loudest first, each with the proxy's reason in words, and the
// path parameters reaching storage unmangled.
func TestMeshWorkloadRequestsBreaksDownByFlag(t *testing.T) {
	fake := &storagetest.Fake{MeshBreakdown: storage.MeshRequestBreakdown{
		Measured: true, Reporter: "destination",
		ResponseFlags:       map[string]uint64{"-": 900, "UO": 40, "ZZ": 3},
		DestinationVersions: map[string]uint64{"v1": 600, "v2": 343},
		Callers: []storage.MeshCallerOutcome{
			{Namespace: "shop", Workload: "frontend", Requests: 900, Errors5xx: 12},
		},
	}}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/workloads/shop/checkout/requests")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.LastMeshBreakdownKey != [2]string{"shop", "checkout"} {
		t.Errorf("storage was asked for %v, want shop/checkout", fake.LastMeshBreakdownKey)
	}
	var resp meshWorkloadRequestsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Measured || resp.Reporter != "destination" {
		t.Fatalf("measured=%v reporter=%q", resp.Measured, resp.Reporter)
	}
	if len(resp.ResponseFlags) != 3 || resp.ResponseFlags[0].Flag != "-" || resp.ResponseFlags[1].Flag != "UO" {
		t.Errorf("flags = %+v, want sorted by requests desc", resp.ResponseFlags)
	}
	if got := resp.ResponseFlags[1].Meaning; !strings.Contains(got, "circuit breaker") {
		t.Errorf("UO meaning = %q", got)
	}
	// An unknown flag is the proxy's word: it passes through, unexplained.
	if resp.ResponseFlags[2].Flag != "ZZ" || resp.ResponseFlags[2].Meaning != "" {
		t.Errorf("unknown flag = %+v, want ZZ with no meaning", resp.ResponseFlags[2])
	}
	if resp.DestinationVersions[0].Version != "v1" {
		t.Errorf("versions = %+v, want v1 first", resp.DestinationVersions)
	}
	if len(resp.Callers) != 1 || resp.Callers[0].Errors5xx != 12 {
		t.Errorf("callers = %+v", resp.Callers)
	}
	if !strings.Contains(resp.UpstreamStatsHint, "proxyStatsMatcher") {
		t.Errorf("the hint must name the setting that exposes the missing counters: %q", resp.UpstreamStatsHint)
	}
}

// Nothing for this workload is a stated absence naming the workload — and
// without the metrics module, the module.
func TestMeshWorkloadRequestsStatesAbsence(t *testing.T) {
	t.Run("no series", func(t *testing.T) {
		rec := meshGet(t, &storagetest.Fake{}, Config{Modules: modules.AllSet()}, "/api/v1/mesh/workloads/shop/checkout/requests")
		var resp meshWorkloadRequestsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Measured || !strings.Contains(resp.Reason, "shop/checkout") {
			t.Errorf("measured=%v reason=%q", resp.Measured, resp.Reason)
		}
		if resp.ResponseFlags == nil || resp.UpstreamStatsHint == "" {
			t.Error("empty lists and the hint must still be present")
		}
	})
	t.Run("no infra-metrics", func(t *testing.T) {
		active := modules.Set{modules.Core: true, modules.Mesh: true}
		rec := meshGet(t, &storagetest.Fake{}, Config{Modules: active}, "/api/v1/mesh/workloads/shop/checkout/requests")
		var resp meshWorkloadRequestsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Measured || !strings.Contains(resp.Reason, "infra-metrics") {
			t.Errorf("measured=%v reason=%q, want the module named", resp.Measured, resp.Reason)
		}
	})
}
