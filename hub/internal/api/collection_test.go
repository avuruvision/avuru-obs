package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// collectionMux mounts the API with runtime collection control on. Auth stays
// nil (auth disabled), which is how every other admin-route test in this
// package reaches a securedAdmin handler.
func collectionMux(fake *storagetest.Fake) *http.ServeMux {
	return collectionMuxWith(fake, nil)
}

// collectionMuxWith is collectionMux with a caller-supplied applier (nil →
// Register's NoopApplier default).
func collectionMuxWith(fake *storagetest.Fake, applier collection.Applier) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		CollectionRuntimeControlEnabled: true,
		CollectionApplier:               applier,
	})
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
	mux := collectionMuxWith(fake, failingApplier{})

	rec := do(t, mux, http.MethodPut, "/api/v1/collection/overlay", `{"obiEnabled":false}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
	if len(fake.SavedOverlays) != 1 {
		t.Errorf("SavedOverlays = %d, want the overlay persisted despite the apply failure", len(fake.SavedOverlays))
	}
}

// recordingApplier is a cluster stand-in that remembers the order overlays
// reached it. The sleep widens the window between the store write and this
// record — the exact window two concurrent PUTs would interleave in.
type recordingApplier struct {
	mu      sync.Mutex
	applied []collection.Overlay
}

func (r *recordingApplier) Apply(_ context.Context, ov collection.Overlay) error {
	time.Sleep(time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied = append(r.applied, ov)
	return nil
}

func (r *recordingApplier) last() (collection.Overlay, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.applied) == 0 {
		return collection.Overlay{}, false
	}
	return r.applied[len(r.applied)-1], true
}

// TestCollectionOverlayConcurrentPutsConverge: whichever PUT wins the race,
// storage and the cluster must agree afterwards. Without the save→apply lock
// two writes interleave (save A, save B, apply B, apply A) and the sensors end
// up running an overlay the API no longer reports — the worst failure this
// endpoint has, since "collection is off" would be a lie. Run under -race.
func TestCollectionOverlayConcurrentPutsConverge(t *testing.T) {
	const n = 8
	fake := &storagetest.Fake{}
	applier := &recordingApplier{}
	mux := collectionMuxWith(fake, applier)

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"excludeNamespaces":["ns-%d"]}`, i)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/collection/overlay", strings.NewReader(body))
			mux.ServeHTTP(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()

	if len(fake.SavedOverlays) != n {
		t.Fatalf("saved %d overlays, want %d", len(fake.SavedOverlays), n)
	}
	appliedLast, ok := applier.last()
	if !ok {
		t.Fatal("no overlay reached the applier")
	}
	encoded, err := appliedLast.Encode()
	if err != nil {
		t.Fatalf("encode last applied overlay: %v", err)
	}
	if stored := fake.SavedOverlays[len(fake.SavedOverlays)-1].Overlay; stored != encoded {
		t.Fatalf("storage and cluster diverged: stored %s, last applied %s", stored, encoded)
	}
}

// effectiveApplier is an applier that can also report an effective config —
// the shape the real K8sApplier has.
type effectiveApplier struct {
	collection.NoopApplier
}

func (effectiveApplier) Effective(_ context.Context, ov collection.Overlay) (collection.Effective, error) {
	eff := collection.Effective{Obi: true, ExcludeNamespaces: []string{"kube-system"}}
	if ov.LogsEnabled == nil || *ov.LogsEnabled {
		eff.Logs = true
	}
	return eff, nil
}

// TestCollectionOverlayGetIncludesEffective: the overlay alone does not tell an
// admin what is being collected (a signal can be off because its module is off
// at install time). The GET carries the resolved state when the applier can
// report one.
func TestCollectionOverlayGetIncludesEffective(t *testing.T) {
	fake := &storagetest.Fake{
		Overlay:    storage.CollectionOverlay{Overlay: `{"logsEnabled":false}`, UpdatedBy: "admin@example.com"},
		OverlaySet: true,
	}
	rec := get(t, collectionMuxWith(fake, effectiveApplier{}), "/api/v1/collection/overlay")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeOverlay(t, rec.Body.String())
	if resp.Effective == nil {
		t.Fatalf("no effective config in the response: %s", rec.Body.String())
	}
	if resp.Effective.Logs {
		t.Errorf("effective config ignored the stored overlay: %s", rec.Body.String())
	}
	if !resp.Effective.Obi {
		t.Errorf("effective config lost the applier's answer: %s", rec.Body.String())
	}
}

// The unset case still resolves: chart defaults are exactly what an install
// with no overlay collects.
func TestCollectionOverlayGetUnsetIncludesEffective(t *testing.T) {
	rec := get(t, collectionMuxWith(&storagetest.Fake{}, effectiveApplier{}), "/api/v1/collection/overlay")
	resp := decodeOverlay(t, rec.Body.String())
	if resp.Effective == nil || !resp.Effective.Logs {
		t.Fatalf("no effective config for an install with no overlay: %s", rec.Body.String())
	}
}

// TestCollectionOverlayGetOmitsEffectiveWithoutReporter: with no cluster to
// read base values from, the key is ABSENT — a false-filled Effective would
// read as "nothing is being collected", which is a very different claim.
func TestCollectionOverlayGetOmitsEffectiveWithoutReporter(t *testing.T) {
	rec := get(t, collectionMuxWith(&storagetest.Fake{}, collection.NoopApplier{}), "/api/v1/collection/overlay")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"effective"`) {
		t.Errorf("effective reported without an applier that can resolve it: %s", rec.Body.String())
	}
}

// failingEffective is a reachable-but-broken cluster: the overlay read must
// still succeed, because the stored overlay is what an admin cannot get
// anywhere else.
type failingEffective struct {
	collection.NoopApplier
}

func (failingEffective) Effective(context.Context, collection.Overlay) (collection.Effective, error) {
	return collection.Effective{}, errors.New("cluster unreachable")
}

func TestCollectionOverlayGetSurvivesEffectiveFailure(t *testing.T) {
	fake := &storagetest.Fake{
		Overlay:    storage.CollectionOverlay{Overlay: `{"obiEnabled":false}`},
		OverlaySet: true,
	}
	rec := get(t, collectionMuxWith(fake, failingEffective{}), "/api/v1/collection/overlay")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want the overlay served anyway: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"effective"`) {
		t.Errorf("a failed resolve was reported as a config: %s", rec.Body.String())
	}
	resp := decodeOverlay(t, rec.Body.String())
	if resp.Overlay.ObiEnabled == nil || *resp.Overlay.ObiEnabled {
		t.Errorf("stored overlay lost: %s", rec.Body.String())
	}
}
