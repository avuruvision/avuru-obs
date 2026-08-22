//go:build e2e

// Per-project (tenant) isolation against the compose stack. The staging
// fixtures (deploy/compose/seed/fixtures/*_staging.json) carry
// avuru.tenant=staging as a resource attribute — ClickHouse derives the
// Tenant column from it (the real production mechanism, no test shortcuts) —
// and the hub scopes every query by the X-Avuru-Tenant header.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	stagingTraceID = "bbbb1111cccc2222dddd3333eeee4444"
	defaultSvc     = "seed-checkout"
	stagingSvc     = "seed-checkout-staging"
)

// getJSONTenant is getJSON with the tenancy header ("" = no header).
func getJSONTenant(path, tenant string, out any) (int, error) {
	req, err := http.NewRequest(http.MethodGet, hubURL+path, nil)
	if err != nil {
		return 0, err
	}
	if tenant != "" {
		req.Header.Set("X-Avuru-Tenant", tenant)
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("GET %s (tenant %q): %s", path, tenant, resp.Status)
	}
	if out != nil {
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

type serviceListResponse struct {
	Services []struct {
		Name string `json:"name"`
	} `json:"services"`
}

func serviceNames(t *testing.T, tenant string) map[string]bool {
	t.Helper()
	var resp serviceListResponse
	if _, err := getJSONTenant("/api/v1/services?start="+queryStart()+"&end="+queryEnd(), tenant, &resp); err != nil {
		t.Fatalf("listing services (tenant %q): %v", tenant, err)
	}
	out := map[string]bool{}
	for _, s := range resp.Services {
		out[s.Name] = true
	}
	return out
}

func queryStart() string { return time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) }
func queryEnd() string   { return time.Now().Add(time.Minute).UTC().Format(time.RFC3339) }

func TestTenantIsolation(t *testing.T) {
	// Both tenants' fixtures must have landed.
	poll(t, 60*time.Second, func() error {
		if got := serviceNames(t, ""); !got[defaultSvc] {
			return fmt.Errorf("default tenant missing %s: %v", defaultSvc, got)
		}
		if got := serviceNames(t, "staging"); !got[stagingSvc] {
			return fmt.Errorf("staging tenant missing %s: %v", stagingSvc, got)
		}
		return nil
	})

	// Row-level isolation, both directions.
	if got := serviceNames(t, ""); got[stagingSvc] {
		t.Errorf("default tenant sees staging service: %v", got)
	}
	if got := serviceNames(t, "staging"); got[defaultSvc] {
		t.Errorf("staging tenant sees default service: %v", got)
	}

	// By-ID fetches are tenant-scoped too — a leaked trace ID must not
	// cross projects.
	if code, _ := getJSONTenant("/api/v1/traces/"+seededTraceID, "staging", nil); code != http.StatusNotFound {
		t.Errorf("default trace via staging tenant: got %d, want 404", code)
	}
	if code, _ := getJSONTenant("/api/v1/traces/"+stagingTraceID, "staging", nil); code != http.StatusOK {
		t.Errorf("staging trace via staging tenant: got %d, want 200", code)
	}
	if code, _ := getJSONTenant("/api/v1/traces/"+stagingTraceID, "", nil); code != http.StatusNotFound {
		t.Errorf("staging trace via default tenant: got %d, want 404", code)
	}
}

// TestAgentsEndpointServes guards the /agents SQL against schema drift (its
// probes touch all four signal tables). Compose has no k8s sensor, so an
// empty inventory is the expected answer — a 500 means a broken query.
func TestAgentsEndpointServes(t *testing.T) {
	var resp struct {
		Sensors       []any `json:"sensors"`
		WindowSeconds int   `json:"windowSeconds"`
	}
	if _, err := getJSONTenant("/api/v1/agents", "", &resp); err != nil {
		t.Fatalf("agents endpoint: %v", err)
	}
	if resp.WindowSeconds <= 0 {
		t.Errorf("windowSeconds = %d, want > 0", resp.WindowSeconds)
	}
}

func TestProjectsList(t *testing.T) {
	var resp struct {
		Projects []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
		} `json:"projects"`
	}
	if _, err := getJSONTenant("/api/v1/projects", "", &resp); err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	found := map[string]string{}
	for _, p := range resp.Projects {
		found[p.ID] = p.Source
	}
	if _, ok := found["default"]; !ok {
		t.Errorf("projects missing default: %v", found)
	}
	// staging is BOTH config-defined (AVURUOBS_PROJECTS in compose) and
	// data-observed; config wins the source label.
	if src, ok := found["staging"]; !ok || src != "config" {
		t.Errorf("projects staging = %q (present=%v), want config", src, ok)
	}
}

// TestAggregateProjectUnionsMembers is the member-projects story end to end: an
// admin creates a project, adds the two seeded tenants as members, and every
// read through it answers with the union — while the members themselves stay
// isolated (TestTenantIsolation above still holds).
//
// It writes real state, so it cleans up after itself: the project is deleted on
// the way out whether the assertions passed or not.
func TestAggregateProjectUnionsMembers(t *testing.T) {
	admin, err := loginAs("admin", adminPassword)
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	const agg = "e2e-estate"

	do := func(method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, hubURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := do(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"id":%q,"label":"E2E estate"}`, agg))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		t.Fatalf("create project: %s", resp.Status)
	}
	t.Cleanup(func() {
		r := do(http.MethodDelete, "/api/v1/projects/"+agg, "")
		r.Body.Close()
	})

	resp = do(http.MethodPut, "/api/v1/projects/"+agg, `{"members":["default","staging"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set members: %s", resp.Status)
	}

	// The membership memo is per-replica with a 30s TTL; the write invalidates
	// this replica's copy, so the union is readable immediately.
	got := serviceNamesAs(t, admin, agg)
	if !got[defaultSvc] || !got[stagingSvc] {
		t.Fatalf("aggregate services = %v, want both %s and %s", got, defaultSvc, stagingSvc)
	}

	// By-id reads span the members too — the same tenant slice reaches the
	// single-entity queries.
	for _, id := range []string{seededTraceID, stagingTraceID} {
		req, _ := http.NewRequest(http.MethodGet, hubURL+"/api/v1/traces/"+id, nil)
		req.Header.Set("X-Avuru-Tenant", agg)
		r, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("trace %s through the aggregate: got %s, want 200", id, r.Status)
		}
	}

	// An aggregate stores nothing of its own, so per-tenant writes are refused
	// rather than landing where no member would read them.
	r := do(http.MethodPost, "/api/v1/projects/"+agg+"/keys", `{"name":"nope"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusConflict {
		t.Errorf("ingest key on an aggregate: got %s, want 409", r.Status)
	}

	// Members keep their own boundaries.
	if names := serviceNamesAs(t, admin, "staging"); names[defaultSvc] {
		t.Errorf("staging still leaks the default service after aggregation: %v", names)
	}
}

// serviceNamesAs is serviceNames for a specific client (the admin session).
func serviceNamesAs(t *testing.T, client *http.Client, tenant string) map[string]bool {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		hubURL+"/api/v1/services?start="+queryStart()+"&end="+queryEnd(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Avuru-Tenant", tenant)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("listing services (tenant %q): %v", tenant, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing services (tenant %q): %s", tenant, resp.Status)
	}
	var out serviceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	names := map[string]bool{}
	for _, s := range out.Services {
		names[s.Name] = true
	}
	return names
}
