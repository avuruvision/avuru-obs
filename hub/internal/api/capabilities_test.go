package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func muxWithModules(t *testing.T, list string) *http.ServeMux {
	t.Helper()
	set, err := modules.Parse(list)
	if err != nil {
		t.Fatalf("parsing modules %q: %v", list, err)
	}
	fake := &storagetest.Fake{}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{Modules: set})
	return mux
}

func TestCapabilitiesDefaultAllModules(t *testing.T) {
	// Zero-value Config (no Modules) = every module on — the
	// backward-compatible default relied on by existing installs.
	mux := newMux(&storagetest.Fake{})

	var resp capabilitiesResponse
	rec := get(t, mux, "/api/v1/capabilities")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"core", "logs", "infra-metrics", "profiling", "error-tracking", "service-health", "alerting", "mesh", "green", "cost", "ai"}
	if !reflect.DeepEqual(resp.Modules, want) {
		t.Errorf("modules = %v, want %v", resp.Modules, want)
	}
	if resp.Version == "" {
		t.Errorf("version missing: %+v", resp)
	}
}

// TestCapabilitiesCollectionRuntimeControl: the SPA decides whether to show
// the collection editor from this flag, so it must track Config exactly —
// including the default-off case, where the routes are not even registered.
func TestCapabilitiesCollectionRuntimeControl(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{{"default off", false}, {"enabled", true}} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			fake := &storagetest.Fake{}
			Register(mux, func() storage.Store { return fake }, Config{CollectionRuntimeControlEnabled: tc.want})

			rec := get(t, mux, "/api/v1/capabilities")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var resp capabilitiesResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.CollectionRuntimeControl != tc.want {
				t.Errorf("collectionRuntimeControl = %v, want %v", resp.CollectionRuntimeControl, tc.want)
			}
		})
	}
}

func TestModuleRouteGating(t *testing.T) {
	mux := muxWithModules(t, "core")

	// Core routes stay up.
	for _, path := range []string{"/api/v1/services", "/api/v1/traces", "/api/v1/metrics/red", "/api/v1/system/status"} {
		if rec := get(t, mux, path); rec.Code == http.StatusNotFound {
			t.Errorf("%s: core route missing (404)", path)
		}
	}
	// Disabled modules' routes are not registered.
	for _, path := range []string{
		"/api/v1/logs",
		"/api/v1/traces/abc/logs",
		"/api/v1/profiles/services",
		"/api/v1/profiles/flamegraph",
		"/api/v1/infra/nodes",
		"/api/v1/infra/pods",
		"/api/v1/agents",
		"/api/v1/health/groups",
		"/api/v1/health/groups/payments",
		"/api/v1/alerts",
		"/api/v1/alerts/rules",
		"/api/v1/green/summary",
		"/api/v1/green/budgets",
		"/api/v1/green/report",
	} {
		if rec := get(t, mux, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404 with module disabled", path, rec.Code)
		}
	}

	var resp capabilitiesResponse
	rec := get(t, mux, "/api/v1/capabilities")
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(resp.Modules, []string{"core"}) {
		t.Errorf("modules = %v, want [core]", resp.Modules)
	}
}

// TestGreenRoutesWhenEnabled proves the module-on side of the green contract:
// with green (and its infra-metrics dependency) active, the routes exist and
// capabilities reports the module.
func TestGreenRoutesWhenEnabled(t *testing.T) {
	mux := muxWithModules(t, "green,infra-metrics")

	for _, path := range []string{"/api/v1/green/summary", "/api/v1/green/budgets", "/api/v1/green/report"} {
		if rec := get(t, mux, path); rec.Code == http.StatusNotFound {
			t.Errorf("%s: green route missing (404) with green enabled", path)
		}
	}

	var resp capabilitiesResponse
	rec := get(t, mux, "/api/v1/capabilities")
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(resp.Modules, []string{"core", "infra-metrics", "green"}) {
		t.Errorf("modules = %v, want [core infra-metrics green]", resp.Modules)
	}
}

func TestModuleRouteGatingPartial(t *testing.T) {
	mux := muxWithModules(t, "logs,profiling")

	if rec := get(t, mux, "/api/v1/logs"); rec.Code == http.StatusNotFound {
		t.Errorf("/api/v1/logs should be registered with logs enabled")
	}
	if rec := get(t, mux, "/api/v1/profiles/services"); rec.Code == http.StatusNotFound {
		t.Errorf("/api/v1/profiles/services should be registered with profiling enabled")
	}
	if rec := get(t, mux, "/api/v1/infra/nodes"); rec.Code != http.StatusNotFound {
		t.Errorf("/api/v1/infra/nodes: got %d, want 404 with infra-metrics disabled", rec.Code)
	}
}
