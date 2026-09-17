package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestSearchLogsComposesSeveralServicesAndWorkloads(t *testing.T) {
	fake := &storagetest.Fake{
		Workloads: map[string]storage.ServiceWorkload{
			"checkout-service": {Namespace: "shop", Workload: "checkout"},
		},
		LogPage: storage.LogPage{Logs: []storage.LogRecord{
			{Service: "checkout", Body: "paid"},
			{Service: "ztunnel", Body: "checkout-abc"},
		}},
	}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	rec := meshGet(t, fake, cfg,
		"/api/v1/logs?service=checkout-service&service=inventory&workload=ops/worker&source=app,ztunnel&q=paid")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.LastLogQuery.Service != "" {
		t.Errorf("legacy service filter remained set: %+v", fake.LastLogQuery)
	}
	got := sourceServices(fake.LastLogQuery)
	for _, want := range []string{"checkout", "checkout.shop", "checkout-service", "inventory", "worker", "worker.ops", "ztunnel"} {
		if !containsString(got, want) {
			t.Errorf("composed services %v do not contain %q", got, want)
		}
	}
	if fake.LastLogQuery.Query != "paid" {
		t.Errorf("message filter = %q", fake.LastLogQuery.Query)
	}

	var resp struct {
		Logs []struct {
			Service string `json:"service"`
			Source  string `json:"source"`
		} `json:"logs"`
		Resolutions []struct {
			Service   string `json:"service"`
			Namespace string `json:"namespace"`
			Workload  string `json:"workload"`
		} `json:"resolutions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Logs) != 2 || resp.Logs[0].Source != "application" || resp.Logs[1].Source != "ztunnel" {
		t.Errorf("classified logs = %+v", resp.Logs)
	}
	if len(resp.Resolutions) != 3 || resp.Resolutions[0].Workload != "checkout" || resp.Resolutions[1].Service != "inventory" || resp.Resolutions[2].Namespace != "ops" {
		t.Errorf("resolutions = %+v", resp.Resolutions)
	}
}

func TestSearchLogsRejectsUnknownSource(t *testing.T) {
	rec := get(t, newMux(&storagetest.Fake{}), "/api/v1/logs?source=app,bogus")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestSearchLogsAcceptsRepeatedSources(t *testing.T) {
	fake := &storagetest.Fake{Workloads: map[string]storage.ServiceWorkload{
		"checkout": {Namespace: "shop", Workload: "checkout"},
	}}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}},
		"/api/v1/logs?service=checkout&source=app&source=ztunnel")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	categories := map[string]bool{}
	for _, source := range fake.LastLogQuery.Sources {
		categories[source.Category] = true
	}
	if !categories["application"] || !categories["ztunnel"] {
		t.Errorf("repeated sources produced %+v", fake.LastLogQuery.Sources)
	}
}

func TestSearchLogsDoesNotBroadenAnUnavailableProxySource(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := Config{Modules: modules.Set{modules.Core: true, modules.Logs: true}}
	rec := meshGet(t, fake, cfg, "/api/v1/logs?service=checkout&source=ztunnel")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !fake.LastLogQuery.MatchNone {
		t.Errorf("unavailable proxy selection broadened to %+v", fake.LastLogQuery)
	}
	var resp multiLogsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Resolutions) != 1 || resp.Resolutions[0].ProxiesUnavailable == "" {
		t.Errorf("missing visible resolution reason: %+v", resp.Resolutions)
	}
}

func TestSearchLogsDoesNotAttachAnonymousOtherLogsToAService(t *testing.T) {
	fake := &storagetest.Fake{}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/logs?service=checkout&source=other")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !fake.LastLogQuery.MatchNone || len(fake.LastLogQuery.Sources) != 0 {
		t.Errorf("subject-scoped other logs broadened to %+v", fake.LastLogQuery)
	}
}

func TestLogServicesSuggestionsIncludeLogOnlyServicesAndWorkloads(t *testing.T) {
	fake := &storagetest.Fake{Presence: []storage.ServicePresence{
		{Name: "checkout", LogRecords: 2},
		{Name: "public-api", LogRecords: 1},
		{Name: "log-only", LogRecords: 4},
		{Name: "silent", LogRecords: 0},
	}, Workloads: map[string]storage.ServiceWorkload{
		"checkout":   {Namespace: "ops", Workload: "checkout"},
		"public-api": {Namespace: "shop", Workload: "orders"},
	}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: meshconfig.Snapshot{
		State: meshconfig.StateOK,
		Workloads: []meshconfig.Workload{
			{Namespace: "shop", Name: "checkout"},
			{Namespace: "ops", Name: "checkout"},
			{Namespace: "shop", Name: "orders"},
			{Namespace: "private", Name: "unseen"},
		},
	}}}
	rec := meshGet(t, fake, cfg, "/api/v1/logs/services?start=2026-09-17T10:00:00Z&end=2026-09-17T11:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Services  []string `json:"services"`
		Workloads []string `json:"workloads"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(resp.Services, []string{"checkout", "log-only", "public-api"}) {
		t.Errorf("services = %v", resp.Services)
	}
	if !reflect.DeepEqual(resp.Workloads, []string{"ops/checkout", "shop/orders"}) {
		t.Errorf("workloads = %v", resp.Workloads)
	}
}

func TestLogCursorCarriesServiceAndAcceptsLegacyFormat(t *testing.T) {
	in := &storage.LogCursor{Timestamp: time.Unix(0, 42).UTC(), Service: "checkout", TraceID: "trace", SpanID: "span"}
	req := httptest.NewRequest(http.MethodGet, "/?cursor="+encodeLogCursor(in), nil)
	out, err := parseLogCursor(req)
	if err != nil {
		t.Fatalf("new cursor: %v", err)
	}
	if out.Legacy || out.Service != "checkout" || out.TraceID != "trace" || out.SpanID != "span" {
		t.Errorf("new cursor = %+v", out)
	}

	legacy := base64.RawURLEncoding.EncodeToString([]byte("42,trace,span"))
	req = httptest.NewRequest(http.MethodGet, "/?cursor="+legacy, nil)
	out, err = parseLogCursor(req)
	if err != nil {
		t.Fatalf("legacy cursor: %v", err)
	}
	if !out.Legacy || out.Service != "" || out.TraceID != "trace" || out.SpanID != "span" || out.Timestamp.UnixNano() != 42 {
		t.Errorf("legacy cursor = %+v", out)
	}
	if got := encodeLogCursor(out); got != legacy {
		t.Errorf("legacy cursor changed wire format: %q, want %q", got, legacy)
	}
}

func TestSearchLogsUsesCompactResolutionTokenOnLaterPages(t *testing.T) {
	fake := &storagetest.Fake{Workloads: map[string]storage.ServiceWorkload{
		"checkout-service": {Namespace: "shop", Workload: "checkout"},
	}, LogPage: storage.LogPage{NextCursor: &storage.LogCursor{Timestamp: time.Unix(0, 42).UTC()}}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, cfg)
	const query = "start=2026-09-17T10:00:00Z&end=2026-09-17T11:00:00Z&service=checkout-service&source=app,ztunnel"
	first := get(t, mux, "/api/v1/logs?"+query)
	if first.Code != http.StatusOK {
		t.Fatalf("first status %d: %s", first.Code, first.Body.String())
	}
	var page multiLogsResponse
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if page.ResolutionToken == "" || len(page.ResolutionToken) > maxLogResolutionTokenBytes {
		t.Fatalf("resolution token = %q, want a compact opaque token", page.ResolutionToken)
	}
	stableSources := append([]storage.LogSource(nil), fake.LastLogQuery.Sources...)
	fake.Workloads = map[string]storage.ServiceWorkload{
		"checkout-service": {Namespace: "moved", Workload: "replacement"},
	}
	secondMux := http.NewServeMux()
	Register(secondMux, func() storage.Store { return fake }, cfg)
	rec := get(t, secondMux, "/api/v1/logs?"+query+"&cursor="+page.NextCursor+"&resolution="+page.ResolutionToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.WorkloadCalls != 1 {
		t.Errorf("resolver ran %d times, want only the first page", fake.WorkloadCalls)
	}
	if !reflect.DeepEqual(fake.LastLogQuery.Sources, stableSources) {
		t.Errorf("stable source snapshot changed: got %+v, want %+v", fake.LastLogQuery.Sources, stableSources)
	}
}

func TestLogResolutionTokenCompactsLargePodSets(t *testing.T) {
	resolutions := make([]logResolutionDTO, 0, 12)
	for workload := 0; workload < 12; workload++ {
		needles := make([]string, 0, maxLogNeedles)
		for pod := 0; pod < maxLogNeedles; pod++ {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d", workload, pod)))
			needles = append(needles, fmt.Sprintf("pod-%x", sum))
		}
		name := fmt.Sprintf("workload-%d", workload)
		resolutions = append(resolutions, logResolutionDTO{
			Namespace: "shop", Workload: name,
			SourceBranches: []logSourceBranchDTO{{Category: logSourceZtunnel, Services: []string{"ztunnel"}, BodyAll: [][]string{needles}}},
		})
	}
	now := time.Unix(1_800_000_000, 0)
	key := deriveLogResolutionKey("shared-secret")
	token, stable, err := encodeLogResolutionToken(key, "scope", resolutions, now)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(token) > maxLogResolutionTokenBytes {
		t.Fatalf("token length = %d, max %d", len(token), maxLogResolutionTokenBytes)
	}
	if stable[0].ProxiesFallback == "" || reflect.DeepEqual(stable[0].SourceBranches[0].BodyAll, resolutions[0].SourceBranches[0].BodyAll) {
		t.Fatalf("large precise resolution was not compacted: %+v", stable[0])
	}
	decoded, err := decodeLogResolutionToken(key, token, "scope", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(decoded, stable) {
		t.Fatalf("decoded resolution changed: got %+v, want %+v", decoded[0], stable[0])
	}
	if _, err := decodeLogResolutionToken(deriveLogResolutionKey("different-secret"), token, "scope", now.Add(time.Minute)); err == nil {
		t.Fatal("token signed by another replica secret was accepted")
	}
}

func TestSearchLogsRejectsResolutionTokenForDifferentSelection(t *testing.T) {
	fake := &storagetest.Fake{LogPage: storage.LogPage{NextCursor: &storage.LogCursor{Timestamp: time.Unix(0, 42).UTC()}}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, cfg)
	const timeQuery = "start=2026-09-17T10:00:00Z&end=2026-09-17T11:00:00Z"
	first := get(t, mux, "/api/v1/logs?"+timeQuery+"&workload=shop%2Fcheckout&source=ztunnel")
	var page multiLogsResponse
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	rec := get(t, mux, "/api/v1/logs?"+timeQuery+"&workload=ops%2Fworker&source=ztunnel&resolution="+page.ResolutionToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func containsString(values []string, want string) bool {
	return strings.Contains("|"+strings.Join(values, "|")+"|", "|"+want+"|")
}
