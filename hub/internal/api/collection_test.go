package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// collectionMux mounts the API with runtime collection control on. Auth stays
// nil (auth disabled), which is how every other admin-route test in this
// package reaches a securedAdmin handler.
func collectionMux(fake *storagetest.Fake) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{CollectionRuntimeControlEnabled: true})
	return mux
}

func decodeOverlay(t *testing.T, body string) collectionOverlayResponse {
	t.Helper()
	var resp collectionOverlayResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&resp); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return resp
}

// TestCollectionOverlayGetUnset: nothing saved yet is not an error — the UI
// gets an empty overlay meaning "everything is at chart defaults".
func TestCollectionOverlayGetUnset(t *testing.T) {
	rec := get(t, collectionMux(&storagetest.Fake{}), "/api/v1/collection/overlay")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"overlay":{}`) {
		t.Errorf("want an empty overlay object, got %s", rec.Body.String())
	}
}

// TestCollectionOverlayPutPersists: a PUT writes exactly one overlay and the
// next GET reads it back.
func TestCollectionOverlayPutPersists(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := collectionMux(fake)

	rec := do(t, mux, http.MethodPut, "/api/v1/collection/overlay", `{"obiEnabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if len(fake.SavedOverlays) != 1 {
		t.Fatalf("want exactly 1 saved overlay, got %d: %+v", len(fake.SavedOverlays), fake.SavedOverlays)
	}

	rec = get(t, mux, "/api/v1/collection/overlay")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	resp := decodeOverlay(t, rec.Body.String())
	if resp.Overlay.ObiEnabled == nil || *resp.Overlay.ObiEnabled {
		t.Errorf("obiEnabled did not round-trip as false: %s", rec.Body.String())
	}
}

// TestCollectionOverlayPutRejectsUnknownField: the closed schema is the whole
// point — a free-form collector-config field must never be accepted.
func TestCollectionOverlayPutRejectsUnknownField(t *testing.T) {
	fake := &storagetest.Fake{}
	rec := do(t, collectionMux(fake), http.MethodPut, "/api/v1/collection/overlay", `{"freeformCollectorConfig":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: got %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if len(fake.SavedOverlays) != 0 {
		t.Errorf("a rejected overlay must not be persisted: %+v", fake.SavedOverlays)
	}
}

// TestCollectionOverlayPutRejectsEmptyBody: a bodyless PUT must not be read as
// "reset everything to chart defaults" — DELETE is the explicit reset.
func TestCollectionOverlayPutRejectsEmptyBody(t *testing.T) {
	fake := &storagetest.Fake{}
	rec := do(t, collectionMux(fake), http.MethodPut, "/api/v1/collection/overlay", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: got %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if len(fake.SavedOverlays) != 0 {
		t.Errorf("an empty PUT must not persist anything: %+v", fake.SavedOverlays)
	}
}

// TestCollectionOverlayDeleteResets: delete is a write of the empty overlay
// ("back to chart defaults"), not a row removal.
func TestCollectionOverlayDeleteResets(t *testing.T) {
	fake := &storagetest.Fake{
		Overlay:    storage.CollectionOverlay{Overlay: `{"obiEnabled":false}`, UpdatedBy: "admin@example.com"},
		OverlaySet: true,
	}
	rec := do(t, collectionMux(fake), http.MethodDelete, "/api/v1/collection/overlay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(fake.SavedOverlays) != 1 {
		t.Fatalf("want exactly 1 saved overlay, got %d: %+v", len(fake.SavedOverlays), fake.SavedOverlays)
	}
	if got := fake.SavedOverlays[len(fake.SavedOverlays)-1].Overlay; got != "{}" && got != "" {
		t.Errorf("delete should persist an empty overlay, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), `"overlay":{}`) {
		t.Errorf("delete should answer with an empty overlay, got %s", rec.Body.String())
	}
}

// TestCollectionOverlayDisabledNotRegistered: with the capability off the
// routes do not exist at all (404), so an install that never opted in has no
// write surface to attack.
func TestCollectionOverlayDisabledNotRegistered(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return &storagetest.Fake{} }, Config{})
	if rec := get(t, mux, "/api/v1/collection/overlay"); rec.Code != http.StatusNotFound {
		t.Errorf("capability off: got %d, want 404", rec.Code)
	}
}

// failingApplier stands in for a cluster the hub cannot reach.
type failingApplier struct{}

func (failingApplier) Apply(context.Context, collection.Overlay) error {
	return errors.New("cluster unreachable")
}

// TestCollectionOverlayPutApplierFailure: the overlay is persisted BEFORE the
// applier runs, so an apply failure must surface as 502 while the write
// stands — storage and cluster are then knowingly divergent until the next
// apply, which is why the status is a gateway error and not a 500.
func TestCollectionOverlayPutApplierFailure(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		CollectionRuntimeControlEnabled: true,
		CollectionApplier:               failingApplier{},
	})

	rec := do(t, mux, http.MethodPut, "/api/v1/collection/overlay", `{"obiEnabled":false}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
	if len(fake.SavedOverlays) != 1 {
		t.Errorf("SavedOverlays = %d, want the overlay persisted despite the apply failure", len(fake.SavedOverlays))
	}
}
