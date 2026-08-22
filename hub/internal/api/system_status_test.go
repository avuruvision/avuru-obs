package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// statusForProject asks for the status of ONE project. The project rides the
// X-Avuru-Tenant header, exactly as the SPA sends it.
func statusForProject(mux *http.ServeMux, c *http.Cookie, project string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	r.Header.Set("X-Avuru-Tenant", project)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// errFake stands in for any store failure — the handler must not care which.
var errFake = errors.New("clickhouse unavailable")

func systemStatus(t *testing.T, mux *http.ServeMux, c *http.Cookie) systemStatusResponse {
	t.Helper()
	w := doBody(mux, http.MethodGet, "/api/v1/system/status", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", w.Code, w.Body.String())
	}
	var resp systemStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// The connection block answers "where is my telemetry?" — and must answer it
// during an outage too, when it is the first thing anyone wants to know.
func TestSystemStatusReportsTheConnection(t *testing.T) {
	f := &storagetest.Fake{}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.StorageConnection = StorageConnection{
			Address: "clickhouse:9000", Database: "otel", Username: "avuru",
		}
	})

	got := systemStatus(t, mux, cookie)
	if got.Connection == nil {
		t.Fatal("no connection reported")
	}
	if got.Connection.Address != "clickhouse:9000" || got.Connection.Database != "otel" ||
		got.Connection.Username != "avuru" || got.Connection.Protocol != "native" {
		t.Fatalf("connection = %+v", got.Connection)
	}
}

// The credential must never reach the browser. StorageConnection has no field
// for it, and this asserts over the whole serialized response so a future
// field cannot quietly carry one.
func TestSystemStatusNeverSendsACredential(t *testing.T) {
	f := &storagetest.Fake{}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.StorageConnection = StorageConnection{
			Address: "clickhouse:9000", Database: "otel", Username: "avuru",
		}
	})
	w := doBody(mux, http.MethodGet, "/api/v1/system/status", cookie, "")
	for _, secret := range []string{"password", "Password", "secret", "avuru-pw"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("response mentions %q: %s", secret, w.Body.String())
		}
	}
}

// Configured retention and the TTL ClickHouse enforces are reported side by
// side, because they can disagree: changing a retention value does nothing to
// tables that already exist until the migration re-applies the TTL.
func TestSystemStatusReportsConfiguredAndEnforcedRetention(t *testing.T) {
	f := &storagetest.Fake{Stats: storage.SystemStats{
		Signals: []storage.SignalStats{
			{Signal: "traces", Rows: 10, Bytes: 100, CompressedBytes: 10, TTLDays: 3},
		},
	}}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.RetentionTracesDays = 7
	})

	got := systemStatus(t, mux, cookie)
	if len(got.Signals) != 1 {
		t.Fatalf("signals = %+v", got.Signals)
	}
	if got.Signals[0].RetentionDays != 7 || got.Signals[0].TTLDays != 3 {
		t.Fatalf("retention = %d, ttl = %d — want the drift reported, not hidden",
			got.Signals[0].RetentionDays, got.Signals[0].TTLDays)
	}
}

// The whole endpoint is instance-wide configuration, so it stays admin-only —
// adding the connection to it must not have widened who can read it.
func TestSystemStatusStaysAdminOnly(t *testing.T) {
	f := &storagetest.Fake{}
	mux, _ := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.AnonymousIdentity = viewerAnywhere()
	})
	if w := doBody(mux, http.MethodGet, "/api/v1/system/status", nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
}

// The Status page answers two different questions: what the INSTALL holds, and
// what the SELECTED project holds. The second is the one an operator asks
// before deciding whether a project's retention is worth changing.
func TestSystemStatusReportsPerProjectUsage(t *testing.T) {
	f := &storagetest.Fake{
		Stats: storage.SystemStats{Signals: []storage.SignalStats{{Signal: "traces", Rows: 100}}},
		Usage: storage.TenantUsage{Signals: []storage.TenantSignalUsage{
			{Signal: "traces", Rows: 40, EstimatedBytes: 4096, RowsPerMinute: 2.5},
		}},
		Projects: map[string]storage.Project{
			"staging": {ID: "staging", Members: []string{}, RetentionDays: 3},
		},
	}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.RetentionTracesDays = 7
	})

	w := statusForProject(mux, cookie, "staging")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", w.Code, w.Body.String())
	}
	var resp systemStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Project == nil {
		t.Fatal("no per-project block")
	}
	if resp.Project.ID != "staging" || len(resp.Project.Signals) != 1 {
		t.Fatalf("project = %+v", resp.Project)
	}
	sig := resp.Project.Signals[0]
	if sig.Rows != 40 || sig.EstimatedBytes != 4096 || sig.RowsPerMinute != 2.5 {
		t.Fatalf("signal = %+v", sig)
	}
	// The project keeps 3 days of its own, so 7 (the install's) is NOT the
	// number that applies to it.
	if sig.RetentionDays != 3 || sig.Inherited {
		t.Fatalf("retention = %d inherited=%v, want the project's own 3 days", sig.RetentionDays, sig.Inherited)
	}
}

// A project without a window of its own reports the install's, and says that
// is where it came from — "7 days" must never be ambiguous about its source.
func TestSystemStatusPerProjectRetentionInherits(t *testing.T) {
	f := &storagetest.Fake{
		Usage:    storage.TenantUsage{Signals: []storage.TenantSignalUsage{{Signal: "traces", Rows: 1}}},
		Projects: map[string]storage.Project{"team-a": {ID: "team-a", Members: []string{}}},
	}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.RetentionTracesDays = 7
	})

	w := statusForProject(mux, cookie, "team-a")
	var resp systemStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Project == nil || len(resp.Project.Signals) != 1 {
		t.Fatalf("project = %+v", resp.Project)
	}
	if got := resp.Project.Signals[0]; got.RetentionDays != 7 || !got.Inherited {
		t.Fatalf("retention = %d inherited=%v, want 7 inherited", got.RetentionDays, got.Inherited)
	}
}

// An aggregate must report the union its screens actually read — the members,
// not the aggregate id, which owns no rows. And when the members keep different
// windows there is no single honest number, so it says so instead of averaging.
func TestSystemStatusAggregateReportsItsMembers(t *testing.T) {
	f := &storagetest.Fake{
		Usage: storage.TenantUsage{Signals: []storage.TenantSignalUsage{{Signal: "traces", Rows: 9}}},
		Projects: map[string]storage.Project{
			"estate":  {ID: "estate", Members: []string{"prod-eu", "prod-us"}},
			"prod-eu": {ID: "prod-eu", Members: []string{}, RetentionDays: 3},
			"prod-us": {ID: "prod-us", Members: []string{}, RetentionDays: 30},
		},
	}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.RetentionTracesDays = 30
	})

	w := statusForProject(mux, cookie, "estate")
	var resp systemStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Project == nil {
		t.Fatal("no per-project block")
	}
	if len(f.UsageTenants) == 0 || len(f.UsageTenants[0]) != 2 {
		t.Fatalf("usage asked for %v, want both members", f.UsageTenants)
	}
	if !resp.Project.RetentionVaries {
		t.Fatal("members keep 3 and 30 days — that must read as varying, not as one number")
	}
}

// One project's counts failing must not take the instance-wide half of the page
// with them: the disks, the schema verdict and the connection are what an
// operator needs during exactly that kind of outage.
func TestSystemStatusSurvivesAFailingProjectRead(t *testing.T) {
	f := &storagetest.Fake{
		Stats:    storage.SystemStats{Signals: []storage.SignalStats{{Signal: "traces", Rows: 100}}},
		UsageErr: errFake,
	}
	mux, cookie := adminMuxWithConfigFor(t, f, func(cfg *Config) {
		cfg.StorageConnection = StorageConnection{Address: "clickhouse:9000", Database: "otel"}
	})

	got := systemStatus(t, mux, cookie)
	if got.Project != nil {
		t.Fatalf("project = %+v, want it omitted rather than half-filled", got.Project)
	}
	if got.Connection == nil || len(got.Signals) != 1 {
		t.Fatal("the instance-wide half must still render")
	}
	var found bool
	for _, c := range got.Components {
		if c.Name == "Project usage" && c.Status == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("components = %+v, want the failure named", got.Components)
	}
}
