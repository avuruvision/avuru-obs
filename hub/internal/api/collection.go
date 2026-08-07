package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// maxOverlayBody bounds the overlay request body. The closed schema is a
// handful of booleans plus a namespace list — 64 KiB is already absurdly
// generous, and the cap is what stops an unbounded read.
const maxOverlayBody = 1 << 16

type collectionOverlayResponse struct {
	Overlay collection.Overlay `json:"overlay"`
	// Effective is the resolved base ⊕ overlay state — what the sensor
	// actually collects. Omitted (not false-filled) when this install has no
	// applier that can read the base values: absent means "unknown here",
	// which a UI must not confuse with "nothing is being collected".
	Effective *collection.Effective `json:"effective,omitempty"`
	UpdatedAt string                `json:"updatedAt,omitempty"`
	UpdatedBy string                `json:"updatedBy,omitempty"`
}

// handleGetCollectionOverlay returns the current overlay plus, where the
// applier can resolve it, the effective config it produces. "Never saved" is
// not an error — it is the empty overlay, i.e. everything at chart defaults
// (which still has an effective config worth reporting).
func (a *API) handleGetCollectionOverlay(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	rec, err := store.LoadCollectionOverlay(r.Context())
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, collectionOverlayResponse{
			Effective: a.effectiveOrNil(r.Context(), collection.Overlay{}),
		})
		return nil
	}
	if err != nil {
		return err
	}
	ov, err := collection.ParseOverlay(rec.Overlay)
	if err != nil {
		// A stored row that fails to parse is a server-side bug (it passed
		// validation on the way in), not a client error — surface as 500 via
		// the default error path.
		return fmt.Errorf("parse stored collection overlay: %w", err)
	}
	resp := collectionOverlayResponse{
		Overlay:   ov,
		Effective: a.effectiveOrNil(r.Context(), ov),
		UpdatedBy: rec.UpdatedBy,
	}
	if !rec.UpdatedAt.IsZero() {
		resp.UpdatedAt = rec.UpdatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// effectiveOrNil resolves the effective config through the applier when it can
// report one. A failure is logged and dropped rather than returned: the stored
// overlay is the part an admin cannot get anywhere else, and a briefly
// unreachable cluster must not turn this read into a 500.
func (a *API) effectiveOrNil(ctx context.Context, ov collection.Overlay) *collection.Effective {
	reporter, ok := a.collectionApplier.(collection.EffectiveReporter)
	if !ok {
		return nil
	}
	eff, err := reporter.Effective(ctx, ov)
	if err != nil {
		slog.Warn("could not resolve the effective collection config", "error", err)
		return nil
	}
	return &eff
}

// handlePutCollectionOverlay replaces the overlay wholesale (there is no PATCH
// merge: the UI always sends the full desired state).
func (a *API) handlePutCollectionOverlay(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	ov, err := readOverlayBody(w, r)
	if err != nil {
		return err
	}
	encoded, err := ov.Encode()
	if err != nil {
		return fmt.Errorf("encode collection overlay: %w", err)
	}

	updatedBy := requestedBy(r)
	// Everything above is per-request validation and must not serialize; from
	// here the save and the apply are one unit (see collectionMu).
	a.collectionMu.Lock()
	defer a.collectionMu.Unlock()

	if err := store.SaveCollectionOverlay(r.Context(), storage.CollectionOverlay{
		Overlay: encoded, UpdatedBy: updatedBy,
	}); err != nil {
		return err
	}
	// Audit trail: the store keeps only the newest overlay (ReplacingMergeTree
	// singleton), so without this line there is no record of who turned
	// collection off, when, or what it was before. For an API whose whole
	// purpose is blinding the sensors, that history has to live somewhere.
	slog.Info("collection overlay updated", "actor", updatedBy, "overlay", encoded)
	if err := a.collectionApplier.Apply(r.Context(), ov); err != nil {
		return &apiError{status: http.StatusBadGateway, message: fmt.Sprintf("overlay saved but applying it failed: %s", err)}
	}
	writeJSON(w, http.StatusOK, collectionOverlayResponse{Overlay: ov, UpdatedBy: updatedBy})
	return nil
}

// handleDeleteCollectionOverlay resets to chart defaults. Storage has no
// delete for the singleton — writing the empty overlay IS the reset, and it
// keeps the audit trail (who reset it, when) that a row removal would lose.
func (a *API) handleDeleteCollectionOverlay(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	updatedBy := requestedBy(r)
	empty := collection.Overlay{}
	encoded, err := empty.Encode()
	if err != nil {
		return fmt.Errorf("encode empty overlay: %w", err)
	}
	// Same save→apply pairing as the PUT path: a reset racing a PUT must not
	// leave the sensors on the overlay storage no longer records.
	a.collectionMu.Lock()
	defer a.collectionMu.Unlock()

	if err := store.SaveCollectionOverlay(r.Context(), storage.CollectionOverlay{
		Overlay: encoded, UpdatedBy: updatedBy,
	}); err != nil {
		return err
	}
	slog.Info("collection overlay reset to chart defaults", "actor", updatedBy)
	if err := a.collectionApplier.Apply(r.Context(), empty); err != nil {
		return &apiError{status: http.StatusBadGateway, message: fmt.Sprintf("overlay reset but applying it failed: %s", err)}
	}
	writeJSON(w, http.StatusOK, collectionOverlayResponse{})
	return nil
}

// readOverlayBody parses the request body through collection.ParseOverlay.
// The raw bytes go to ParseOverlay verbatim rather than through an
// intermediate json.Decode: ParseOverlay is what enforces the closed schema
// (DisallowUnknownFields + no trailing data), and a plain decode into
// collection.Overlay first would silently DROP unknown keys, so the re-encoded
// value would always look clean and a free-form field would sneak through as
// 200. An empty body is rejected rather than read as the empty overlay
// (ParseOverlay's contract for ""): a bodyless PUT is a client bug, and
// treating it as "reset everything to chart defaults" is far too destructive
// an interpretation — DELETE is the explicit reset.
func readOverlayBody(w http.ResponseWriter, r *http.Request) (collection.Overlay, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOverlayBody))
	if err != nil {
		return collection.Overlay{}, decodeJSONError(err)
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return collection.Overlay{}, badRequest("empty request body")
	}
	ov, err := collection.ParseOverlay(body)
	if err != nil {
		return collection.Overlay{}, badRequest("%s", err)
	}
	return ov, nil
}

// requestedBy resolves the audit actor from the authenticated identity (set on
// the request context by a.securedAdmin), falling back to "unknown" when auth
// is disabled (a.cfg.Auth == nil, so securedAdmin never populates an identity).
func requestedBy(r *http.Request) string {
	id := identityFrom(r.Context())
	if id == nil || id.Email == "" {
		return "unknown"
	}
	return id.Email
}
