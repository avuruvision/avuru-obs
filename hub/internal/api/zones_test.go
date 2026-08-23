package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestZoneTrafficEndpoint(t *testing.T) {
	fake := &storagetest.Fake{
		Zones: []storage.ZoneTraffic{
			{SrcZone: "eu-west-1a", DstZone: "eu-west-1b", Bytes: 42815332352},
			{SrcZone: "eu-west-1b", DstZone: "eu-west-1a", Bytes: 1048576},
		},
	}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/network/zones?windowSec=3600")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp zonesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding zones: %v", err)
	}
	if len(resp.Zones) != 2 {
		t.Fatalf("got %d zone pairs, want 2: %+v", len(resp.Zones), resp.Zones)
	}
	// Order is the store's (bytes desc) and must survive the handler — the UI
	// shows the top pairs and does not re-sort.
	if resp.Zones[0].SrcZone != "eu-west-1a" || resp.Zones[0].Bytes != 42815332352 {
		t.Errorf("first pair wrong: %+v", resp.Zones[0])
	}
	if fake.LastServiceQuery.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", fake.LastServiceQuery.Tenant)
	}
}

// No rows is the common case — accounting is opt-in at the sensor and a
// single-zone cluster never produces one. It must serialize as [], not null,
// so the UI can branch on length without a null check.
func TestZoneTrafficEmptyIsAnArray(t *testing.T) {
	rec := get(t, newMux(&storagetest.Fake{}), "/api/v1/network/zones")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "{\"zones\":[]}\n" && got != "{\"zones\":[]}" {
		t.Errorf("empty body = %q, want an empty array", got)
	}
}

func TestZoneTrafficNeedsInfraMetrics(t *testing.T) {
	mux := muxWithModules(t, "logs")
	if rec := get(t, mux, "/api/v1/network/zones"); rec.Code != http.StatusNotFound {
		t.Errorf("/api/v1/network/zones: got %d, want 404 without infra-metrics", rec.Code)
	}
}
