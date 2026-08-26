package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func costGet(t *testing.T, fake *storagetest.Fake, cfg Config, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, cfg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decodeCost(t *testing.T, rec *httptest.ResponseRecorder) costResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp costResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// Idle capacity is reserved minus the PEAK, not minus the mean. A request
// cannot be cut below the peak without risking eviction, so subtracting the
// mean would report as reclaimable exactly the headroom the workload
// demonstrably used.
func TestIdleIsMeasuredAgainstThePeakNotTheMean(t *testing.T) {
	fake := &storagetest.Fake{
		WorkloadCostRows: []storage.WorkloadCost{{
			Workload: "checkout", Namespace: "shop", Pods: 3,
			ReservedCPUCores: 2, UsedCPUCoresPeak: 1.5, UsedCPUCoresMean: 0.1,
			ReservedMemBytes: 4 << 30, UsedMemBytesPeak: 3 << 30, UsedMemBytesMean: 1 << 30,
		}},
	}
	resp := decodeCost(t, costGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/cost/workloads"))
	if len(resp.Workloads) != 1 {
		t.Fatalf("got %d workloads, want 1", len(resp.Workloads))
	}
	w := resp.Workloads[0]
	if w.IdleCPUCores != 0.5 {
		t.Errorf("idle cpu = %v, want 0.5 (2 reserved − 1.5 peak); using the mean would say 1.9", w.IdleCPUCores)
	}
	if want := float64(1 << 30); w.IdleMemBytes != want {
		t.Errorf("idle mem = %v, want %v", w.IdleMemBytes, want)
	}
}

// A workload that never used its reservation must not report NEGATIVE idle
// capacity when usage briefly exceeded the request — bursting over a request
// is normal and is not negative waste.
func TestIdleNeverGoesNegative(t *testing.T) {
	fake := &storagetest.Fake{
		WorkloadCostRows: []storage.WorkloadCost{{
			Workload: "burst", ReservedCPUCores: 0.5, UsedCPUCoresPeak: 2,
			ReservedMemBytes: 1 << 30, UsedMemBytesPeak: 2 << 30,
		}},
	}
	resp := decodeCost(t, costGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/cost/workloads"))
	if got := resp.Workloads[0].IdleCPUCores; got != 0 {
		t.Errorf("idle cpu = %v, want 0 — a workload over its request is not negative waste", got)
	}
	if got := resp.Workloads[0].IdleMemBytes; got != 0 {
		t.Errorf("idle mem = %v, want 0", got)
	}
}

// A workload with no declared request is the loudest finding on the screen,
// and it has to be distinguishable from one that reserved almost nothing —
// only the first is unschedulable by accident and first in line for eviction.
func TestRequestsNothingIsItsOwnState(t *testing.T) {
	fake := &storagetest.Fake{
		WorkloadCostRows: []storage.WorkloadCost{
			{Workload: "unbounded", UsedCPUCoresPeak: 1.2},
			{Workload: "tiny", ReservedCPUCores: 0.001, UsedCPUCoresPeak: 0.9},
		},
	}
	resp := decodeCost(t, costGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/cost/workloads"))
	byName := map[string]workloadCostDTO{}
	for _, w := range resp.Workloads {
		byName[w.Workload] = w
	}
	if !byName["unbounded"].RequestsNothing {
		t.Error("a workload with no request must report requestsNothing")
	}
	if byName["tiny"].RequestsNothing {
		t.Error("a workload with a small request is not the same as one with none")
	}
}

// Money appears only when the operator declared BOTH rates. Pricing CPU while
// treating memory as free would rank the wrong workloads first, which is the
// only thing the screen is for.
func TestMoneyNeedsBothRates(t *testing.T) {
	rows := []storage.WorkloadCost{{
		Workload: "checkout", ReservedCPUCores: 2, UsedCPUCoresPeak: 1,
		ReservedMemBytes: 2 * bytesPerGiB, UsedMemBytesPeak: bytesPerGiB,
	}}
	for _, tc := range []struct {
		name  string
		rates CostRates
		want  bool
	}{
		{"neither", CostRates{}, false},
		{"cpu only", CostRates{CPUCoreHour: 0.03, Currency: "EUR"}, false},
		{"memory only", CostRates{MemGiBHour: 0.004, Currency: "EUR"}, false},
		{"both", CostRates{CPUCoreHour: 0.03, MemGiBHour: 0.004, Currency: "EUR"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &storagetest.Fake{WorkloadCostRows: rows}
			resp := decodeCost(t, costGet(t, fake,
				Config{Modules: modules.AllSet(), CostRates: tc.rates}, "/api/v1/cost/workloads"))
			if resp.Priced != tc.want {
				t.Fatalf("priced = %v, want %v", resp.Priced, tc.want)
			}
			w := resp.Workloads[0]
			if tc.want {
				if w.ReservedCostPerHour == nil || w.IdleCostPerHour == nil {
					t.Fatal("priced response carries no money")
				}
				// 2 cores × 0.03 + 2 GiB × 0.004 = 0.068
				if got := *w.ReservedCostPerHour; got < 0.0679 || got > 0.0681 {
					t.Errorf("reserved cost = %v, want ~0.068", got)
				}
				// idle = 1 core + 1 GiB → 0.034
				if got := *w.IdleCostPerHour; got < 0.0339 || got > 0.0341 {
					t.Errorf("idle cost = %v, want ~0.034", got)
				}
				if resp.Currency != "EUR" {
					t.Errorf("currency = %q, want EUR", resp.Currency)
				}
				return
			}
			if w.ReservedCostPerHour != nil || w.IdleCostPerHour != nil {
				t.Error("money rendered without both rates — a zero would read as free")
			}
			if resp.Currency != "" {
				t.Errorf("currency = %q on an unpriced install", resp.Currency)
			}
		})
	}
}

// The module gates the whole surface: with it off the routes are not
// registered at all, so a bookmark 404s rather than answering with zeros an
// install never collected.
func TestCostRoutesGatedOnTheModule(t *testing.T) {
	set := modules.AllSet()
	delete(set, modules.Cost)
	for _, path := range []string{"/api/v1/cost/workloads", "/api/v1/cost/nodes"} {
		rec := costGet(t, &storagetest.Fake{}, Config{Modules: set}, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 with the cost module off", path, rec.Code)
		}
	}
}

// An install collecting nothing yet must answer with an empty list, not null:
// the screen renders "nothing to report", and a null would be a client crash.
func TestCostEmptyIsAListNotNull(t *testing.T) {
	rec := costGet(t, &storagetest.Fake{}, Config{Modules: modules.AllSet()}, "/api/v1/cost/workloads")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"workloads":[]`) {
		t.Errorf("body = %s, want an empty array", got)
	}
}
