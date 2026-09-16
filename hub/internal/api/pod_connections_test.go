package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestPodConnections(t *testing.T) {
	f := &storagetest.Fake{PodConnectionRows: []storage.PodConnection{
		{Source: storage.PodRef{Name: "web-1", Namespace: "shop", Node: "n1", Service: "frontend"},
			Peer: "api.example.test", Calls: 10, Errors: 1, P95: 12 * time.Millisecond},
		{Peer: "overflow"},
	}}
	w := get(t, newMux(f), "/api/v1/infra/pod-connections?node=n1&limit=1&start=2026-09-15T10:00:00Z&end=2026-09-15T11:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp podConnectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Connections) != 1 || !resp.Truncated || resp.Limit != 1 || resp.Connections[0].P95Ms != 12 || resp.Connections[0].Source.Namespace != "shop" {
		t.Fatalf("response = %+v", resp)
	}
	q := f.LastPodConnectionQuery
	if q.Node != "n1" || q.Limit != 2 || q.Range.End.Sub(q.Range.Start) != time.Hour || q.Tenant != "default" {
		t.Fatalf("query = %+v", q)
	}
}

func TestPodConnectionsValidationAndFailures(t *testing.T) {
	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=501", "?limit=x", "?start=no", "?start=2026-01-02T00:00:00Z&end=2026-01-01T00:00:00Z"} {
		t.Run(query, func(t *testing.T) {
			f := &storagetest.Fake{}
			if w := get(t, newMux(f), "/api/v1/infra/pod-connections"+query); w.Code != http.StatusBadRequest || f.LastPodConnectionQuery.Limit != 0 {
				t.Fatalf("status %d, queried storage: %+v", w.Code, f.LastPodConnectionQuery)
			}
		})
	}
	f := &storagetest.Fake{}
	w := get(t, newMux(f), "/api/v1/infra/pod-connections")
	if w.Body.String() != "{\"connections\":[],\"truncated\":false,\"limit\":200}\n" {
		t.Fatalf("empty response: %s", w.Body.String())
	}
	f.PodConnectionErr = errors.New("storage unavailable")
	if w = get(t, newMux(f), "/api/v1/infra/pod-connections"); w.Code != http.StatusInternalServerError {
		t.Fatalf("store failure status %d", w.Code)
	}
}

func TestPodConnectionsAuthorizationAndModule(t *testing.T) {
	mux, cookie := authedMux(t)
	for _, tc := range []struct {
		project string
		want    int
	}{{"payments", 200}, {"prod", 403}} {
		w := authDo(mux, "GET", "/api/v1/infra/pod-connections", cookie, map[string]string{"X-Avuru-Tenant": tc.project})
		if w.Code != tc.want {
			t.Fatalf("project %s: got %d, want %d", tc.project, w.Code, tc.want)
		}
	}
	if w := authDo(mux, "GET", "/api/v1/infra/pod-connections", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status %d", w.Code)
	}
	w := meshGet(t, &storagetest.Fake{}, Config{Modules: modules.Set{modules.Core: true}}, "/api/v1/infra/pod-connections")
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled module status %d", w.Code)
	}
	amux, admin, f := adminMuxCfg(t, []string{"estate", "east", "west"})
	dbProject(f, "estate", "Estate", "east", "west")
	w = authDo(amux, "GET", "/api/v1/infra/pod-connections", admin, map[string]string{"X-Avuru-Tenant": "estate"})
	if w.Code != http.StatusOK || len(f.LastPodConnectionQuery.Tenants) != 2 {
		t.Fatalf("aggregate project: status %d query %+v", w.Code, f.LastPodConnectionQuery)
	}
}
