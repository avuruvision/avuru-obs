package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// stubHub answers the endpoints the plugin reads and records the requests, so
// the tests can assert on headers as well as frames.
func stubHub(t *testing.T, bodies map[string]any) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(r.Context()))
		if r.Header.Get("Authorization") != "Bearer avurut_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func newTestDatasource(t *testing.T, url, project string) *Datasource {
	t.Helper()
	jsonData, _ := json.Marshal(settings{URL: url, Project: project})
	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{
		JSONData:                jsonData,
		DecryptedSecureJSONData: map[string]string{"apiToken": "avurut_test"},
	})
	if err != nil {
		t.Fatalf("NewDatasource: %v", err)
	}
	return inst.(*Datasource)
}

func queryFor(t *testing.T, m queryModel) backend.DataQuery {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	return backend.DataQuery{
		RefID: "A",
		JSON:  raw,
		TimeRange: backend.TimeRange{
			From: now.Add(-15 * time.Minute),
			To:   now,
		},
	}
}

var servicesBody = map[string]any{
	"/api/v1/services": map[string]any{"services": []map[string]any{
		{"name": "checkout", "ratePerSec": 12.5, "errorRate": 0.12, "p50Ms": 40.0, "p95Ms": 420.0, "p99Ms": 900.0, "spanCount": 9000},
		{"name": "payments", "ratePerSec": 3.0, "errorRate": 0.0, "p50Ms": 10.0, "p95Ms": 60.0, "p99Ms": 80.0, "spanCount": 400},
	}},
	"/api/v1/capabilities": map[string]any{"version": "0.7.0", "modules": []string{"core", "logs"}},
}

func TestServicesQueryProducesOneRowPerService(t *testing.T) {
	srv, seen := stubHub(t, servicesBody)
	ds := newTestDatasource(t, srv.URL, "")

	resp := ds.query(context.Background(), queryFor(t, queryModel{Kind: QueryServices}))
	if resp.Error != nil {
		t.Fatalf("query failed: %v", resp.Error)
	}
	if len(resp.Frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(resp.Frames))
	}
	f := resp.Frames[0]
	if got := f.Rows(); got != 2 {
		t.Errorf("rows = %d, want 2", got)
	}
	if f.Fields[0].At(0).(string) != "checkout" {
		t.Errorf("first service = %v, want checkout", f.Fields[0].At(0))
	}
	// Units are what make a panel readable without per-dashboard fiddling.
	if u := f.Fields[3].Config.Unit; u != "ms" {
		t.Errorf("p50 unit = %q, want ms", u)
	}

	// The panel's time range has to reach the hub, or every dashboard shows the
	// same default window whatever the picker says.
	q := (*seen)[len(*seen)-1].URL.Query()
	if q.Get("start") == "" || q.Get("end") == "" {
		t.Errorf("time range not sent: %v", q)
	}
}

func TestProjectIsSentAsTheTenantHeader(t *testing.T) {
	srv, seen := stubHub(t, servicesBody)
	ds := newTestDatasource(t, srv.URL, "staging")

	if resp := ds.query(context.Background(), queryFor(t, queryModel{Kind: QueryServices})); resp.Error != nil {
		t.Fatalf("query failed: %v", resp.Error)
	}
	if h := (*seen)[len(*seen)-1].Header.Get("X-Avuru-Tenant"); h != "staging" {
		t.Errorf("tenant header = %q, want staging", h)
	}
}

// A panel naming its own project must beat the data source default, or a
// dashboard cannot show two environments side by side.
func TestPanelProjectBeatsTheDataSourceDefault(t *testing.T) {
	srv, seen := stubHub(t, servicesBody)
	ds := newTestDatasource(t, srv.URL, "staging")

	if resp := ds.query(context.Background(), queryFor(t, queryModel{Kind: QueryServices, Project: "prod"})); resp.Error != nil {
		t.Fatalf("query failed: %v", resp.Error)
	}
	if h := (*seen)[len(*seen)-1].Header.Get("X-Avuru-Tenant"); h != "prod" {
		t.Errorf("tenant header = %q, want the panel's prod", h)
	}
}

func TestHealthQueryCarriesTheOverallRollup(t *testing.T) {
	srv, _ := stubHub(t, map[string]any{
		"/api/v1/health/groups": map[string]any{
			"overall": "degraded",
			"groups": []map[string]any{
				{"name": "payments", "environment": "prod", "tier": "T0", "status": "healthy", "ratePerSec": 1.0, "errorRate": 0.0, "p95Ms": 10.0},
			},
		},
	})
	ds := newTestDatasource(t, srv.URL, "")

	resp := ds.query(context.Background(), queryFor(t, queryModel{Kind: QueryHealth}))
	if resp.Error != nil {
		t.Fatalf("query failed: %v", resp.Error)
	}
	meta := resp.Frames[0].Meta
	if meta == nil {
		t.Fatal("no frame metadata")
	}
	custom, ok := meta.Custom.(map[string]any)
	if !ok || custom["overall"] != "degraded" {
		// A panel recomputing the rollup would compute it differently from the
		// product, which is worse than not showing it.
		t.Errorf("overall rollup missing from frame metadata: %+v", meta.Custom)
	}
}

func TestTracesQueryPassesItsFilters(t *testing.T) {
	srv, seen := stubHub(t, map[string]any{
		"/api/v1/traces": map[string]any{"traces": []map[string]any{
			{"traceId": "abc", "rootService": "checkout", "rootOperation": "POST /checkout",
				"startTime": "2026-08-23T10:00:00Z", "durationMs": 250.0, "spanCount": 4, "errorCount": 1},
		}},
	})
	ds := newTestDatasource(t, srv.URL, "")

	resp := ds.query(context.Background(), queryFor(t, queryModel{
		Kind: QueryTraces, Service: "checkout", Status: "error", Tags: "avuru.tag.team=payments", Limit: 7,
	}))
	if resp.Error != nil {
		t.Fatalf("query failed: %v", resp.Error)
	}
	q := (*seen)[len(*seen)-1].URL.Query()
	for k, want := range map[string]string{
		"service": "checkout", "status": "error", "tags": "avuru.tag.team=payments", "limit": "7",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if resp.Frames[0].Fields[0].At(0).(time.Time).IsZero() {
		t.Error("start time not parsed into the time field")
	}
}

// An unparseable timestamp must not lose the row: the trace id is still the
// useful part.
func TestTraceWithABadTimestampKeepsItsRow(t *testing.T) {
	srv, _ := stubHub(t, map[string]any{
		"/api/v1/traces": map[string]any{"traces": []map[string]any{
			{"traceId": "abc", "rootService": "checkout", "startTime": "not-a-time", "durationMs": 1.0},
		}},
	})
	ds := newTestDatasource(t, srv.URL, "")

	resp := ds.query(context.Background(), queryFor(t, queryModel{Kind: QueryTraces}))
	if resp.Error != nil {
		t.Fatalf("query failed: %v", resp.Error)
	}
	if rows := resp.Frames[0].Rows(); rows != 1 {
		t.Fatalf("rows = %d, want the row kept", rows)
	}
}

func TestUnknownQueryKindIsRejected(t *testing.T) {
	srv, _ := stubHub(t, servicesBody)
	ds := newTestDatasource(t, srv.URL, "")

	resp := ds.query(context.Background(), queryFor(t, queryModel{Kind: "nonsense"}))
	if resp.Error == nil {
		t.Fatal("unknown kind should be an error")
	}
	if resp.ErrorSource != backend.ErrorSourceDownstream && resp.Status != backend.StatusBadRequest {
		t.Errorf("status = %v, want a 4xx — the query is the caller's mistake", resp.Status)
	}
}

// The health check has to exercise the credential, not merely reach the host:
// a green "Save & test" that only proved DNS works is worse than none.
func TestHealthCheckReportsTheRealFailure(t *testing.T) {
	srv, _ := stubHub(t, servicesBody)

	ok := newTestDatasource(t, srv.URL, "")
	res, err := ok.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if res.Status != backend.HealthStatusOk {
		t.Errorf("healthy hub reported %v: %s", res.Status, res.Message)
	}

	jsonData, _ := json.Marshal(settings{URL: srv.URL})
	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{
		JSONData:                jsonData,
		DecryptedSecureJSONData: map[string]string{"apiToken": "avurut_wrong"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err = inst.(*Datasource).CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if res.Status != backend.HealthStatusError {
		t.Fatalf("bad token reported %v", res.Status)
	}
	if res.Message != "the API token is missing, expired or revoked" {
		t.Errorf("message = %q — it should say what to fix", res.Message)
	}
}

func TestSettingsWithoutAURLAreRejected(t *testing.T) {
	if _, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{}); err == nil {
		t.Error("a data source with no hub URL should fail to instantiate")
	}
}
