package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

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
