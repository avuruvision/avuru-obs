package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func dur(n float64) time.Duration { return time.Duration(n * float64(time.Millisecond)) }

// healthMux wires a fake store and a static groups config into the router. The
// resolver reads the fake's stored groups too, so a test can exercise either
// source (or both) through the same seam production uses.
func healthMux(fake *storagetest.Fake, cfg health.Config) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		Groups: health.NewResolver(
			func() health.Config { return cfg },
			func() health.GroupStore { return fake },
		),
	})
	return mux
}

func TestHealthGroupsHybridAndPropagation(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "web", SpanCount: 100, ErrorCount: 0, P95: dur(100)},       // healthy T2 (auto, ns shop)
			{Name: "payments", SpanCount: 100, ErrorCount: 20, P95: dur(100)}, // down T0 (config)
		},
		Labels: []storage.ServiceLabel{
			{Service: "web", K8sNamespace: "shop"},
			{Service: "payments", K8sNamespace: "payments"},
		},
		Edges: []storage.ServiceEdge{{Source: "web", Target: "payments", Count: 5}},
	}
	cfg := health.Config{
		DefaultTier: health.TierT2,
		Groups: []health.Group{
			{Name: "payments", Tier: health.TierT0, Selector: health.Selector{Namespaces: []string{"payments"}}},
		},
		Thresholds: health.ThresholdConfig{},
	}
	mux := healthMux(fake, cfg)

	rec := get(t, mux, "/api/v1/health/groups")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp healthGroupsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Overall != "down" {
		t.Errorf("overall = %q, want down", resp.Overall)
	}

	groups := map[string]healthGroupDTO{}
	for _, g := range resp.Groups {
		groups[g.Name] = g
	}
	pay, ok := groups["payments"]
	if !ok || pay.Source != "config" || pay.Tier != "T0" || pay.Status != "down" {
		t.Errorf("payments group wrong: %+v (ok=%v)", pay, ok)
	}
	shop, ok := groups["shop"]
	if !ok || shop.Source != "auto" || shop.Tier != "T2" {
		t.Errorf("shop auto group wrong: %+v (ok=%v)", shop, ok)
	}
	// web is healthy on its own but degraded because a critical (T0) dep is down.
	if len(shop.Members) != 1 {
		t.Fatalf("shop members = %d, want 1", len(shop.Members))
	}
	web := shop.Members[0]
	if web.BaseStatus != "healthy" || web.EffectiveStatus != "degraded" {
		t.Errorf("web base=%q effective=%q, want healthy/degraded", web.BaseStatus, web.EffectiveStatus)
	}
	if len(web.Dependencies) != 1 || web.Dependencies[0].Service != "payments" || !web.Dependencies[0].Critical {
		t.Errorf("web dependencies wrong: %+v", web.Dependencies)
	}

	// The ExcludeAux default flows through to the store query.
	if !fake.LastServiceQuery.ExcludeAux {
		t.Errorf("ExcludeAux should default true")
	}
}

func TestHealthGroupDrilldown(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "web", SpanCount: 100, ErrorCount: 0, P95: dur(100)}},
		Labels:   []storage.ServiceLabel{{Service: "web", K8sNamespace: "shop"}},
	}
	mux := healthMux(fake, health.Default())

	if rec := get(t, mux, "/api/v1/health/groups/shop"); rec.Code != http.StatusOK {
		t.Fatalf("known group: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec := get(t, mux, "/api/v1/health/groups/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown group: got %d, want 404", rec.Code)
	}
}

func TestHealthGroupsStoreUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return nil }, Config{})
	if rec := get(t, mux, "/api/v1/health/groups"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("store down: got %d, want 503", rec.Code)
	}
}

func TestHealthGroupsTenantHeader(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := healthMux(fake, health.Default())
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/health/groups", nil)
	req.Header.Set("X-Avuru-Tenant", "prod-eu")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.LastServiceQuery.Tenant != "prod-eu" {
		t.Errorf("tenant header not honored: %q", fake.LastServiceQuery.Tenant)
	}
}
