package api

import (
	"io"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/storage/profilesadapter"
)

// maxProfilesBody bounds one OTLP profiles export (the profiler batches
// aggressively; 32 MiB is generous).
const maxProfilesBody = 32 << 20

// handleProfilesIngest accepts OTLP/HTTP profiles at the otlphttp exporter's
// default path (v1development — the signal is alpha). This is a documented,
// profiles-only exception to "the hub is never in the telemetry byte-path":
// clickhouseexporter has no profiles support yet, and running the main
// gateway with a development feature gate is worse. Removed when the
// exporter grows profiles support. All wire-format knowledge lives in
// storage/profilesadapter.
func (a *API) handleProfilesIngest(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/x-protobuf" {
		return badRequest("unsupported content type %q (application/x-protobuf only)", ct)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProfilesBody+1))
	if err != nil {
		return badRequest("reading body: %v", err)
	}
	if len(body) > maxProfilesBody {
		return badRequest("profiles payload exceeds %d bytes", maxProfilesBody)
	}

	samples, err := profilesadapter.ParseProto(body, tenant(r))
	if err != nil {
		return badRequest("invalid profiles payload: %v", err)
	}
	if err := store.WriteProfileSamples(r.Context(), samples); err != nil {
		return err
	}

	// OTLP/HTTP full success: 200 with an empty ExportProfilesServiceResponse
	// (zero bytes is a valid empty proto message).
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	return nil
}
