package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestTagsEndpoint(t *testing.T) {
	fake := &storagetest.Fake{
		Tags: []storage.TagKey{
			{Key: "avuru.tag.team", Values: []string{"payments", "storefront"}},
			{Key: "avuru.tag.tier", Values: []string{"critical"}},
		},
	}
	rec := get(t, newMux(fake), "/api/v1/tags")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp tagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding tags: %v", err)
	}
	if len(resp.Tags) != 2 {
		t.Fatalf("got %d tags, want 2: %+v", len(resp.Tags), resp.Tags)
	}
	// The filter string carries the full key; a person reads the short name.
	// Both are served so the UI never has to know the reserved prefix.
	if resp.Tags[0].Key != "avuru.tag.team" || resp.Tags[0].Name != "team" {
		t.Errorf("first tag wrong: %+v", resp.Tags[0])
	}
	if len(resp.Tags[0].Values) != 2 {
		t.Errorf("values lost: %+v", resp.Tags[0])
	}
}

// Nothing is tagged until an operator maps a label, so empty is the normal
// answer and must serialize as [] for the UI to branch on length.
func TestTagsEmptyIsAnArray(t *testing.T) {
	rec := get(t, newMux(&storagetest.Fake{}), "/api/v1/tags")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"tags\":[]}\n" && got != "{\"tags\":[]}" {
		t.Errorf("empty body = %q, want an empty array", got)
	}
}

// Tags ride resource attributes on traces, so discovery must survive an
// install running no optional module at all.
func TestTagsSurviveCoreOnly(t *testing.T) {
	mux := muxWithModules(t, "")
	if rec := get(t, mux, "/api/v1/tags"); rec.Code == http.StatusNotFound {
		t.Error("/api/v1/tags should be registered on a core-only install")
	}
}

// A business tag reaches the store as an ordinary tag filter; the storage layer
// owns the routing to resource attributes. This pins that the API does not
// strip or rewrite the reserved prefix on the way through.
func TestLogsPassBusinessTagsThrough(t *testing.T) {
	fake := &storagetest.Fake{}
	rec := get(t, newMux(fake), "/api/v1/logs?tags=avuru.tag.team%3Dpayments,level%3Dwarn")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := fake.LastLogQuery.Tags["avuru.tag.team"]; got != "payments" {
		t.Errorf("business tag reached the store as %q, want payments (%v)", got, fake.LastLogQuery.Tags)
	}
	if got := fake.LastLogQuery.Tags["level"]; got != "warn" {
		t.Errorf("ordinary tag lost: %v", fake.LastLogQuery.Tags)
	}
}
