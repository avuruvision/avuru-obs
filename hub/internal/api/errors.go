package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// apiError carries an HTTP status through handler returns; the central
// wrapper maps everything else to 500.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

func badRequest(format string, args ...any) error {
	return &apiError{status: http.StatusBadRequest, message: fmt.Sprintf(format, args...)}
}

func unauthorized() error {
	return &apiError{status: http.StatusUnauthorized, message: "authentication required"}
}

func forbidden(format string, args ...any) error {
	return &apiError{status: http.StatusForbidden, message: fmt.Sprintf(format, args...)}
}

var errStoreUnavailable = &apiError{status: http.StatusServiceUnavailable, message: "telemetry store unavailable"}

// decodeJSONError classifies a json.Decode error against a MaxBytesReader-
// wrapped body: an oversized body's *http.MaxBytesError is returned as-is so
// handle() maps it to 413, everything else (syntax errors, EOF) becomes a
// plain 400.
func decodeJSONError(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return err
	}
	return badRequest("invalid body")
}

type errorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// handle wraps a handler returning error and maps errors to HTTP statuses in
// ONE place (agent_docs/go_style.md rule 3).
func handle(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		status, msg := http.StatusInternalServerError, "internal error"
		var ae *apiError
		var oe *oauthAPIError
		var mbe *http.MaxBytesError
		switch {
		// OAuth clients parse RFC 6749's {error, error_description} by name,
		// so those routes answer in that shape rather than the hub's. Mapped
		// here with everything else — one place, per the rule above — instead
		// of writing the response inside the handler.
		case errors.As(err, &oe):
			writeJSON(w, oe.status, oauthErrorBody{
				Error:            oe.err.Code,
				ErrorDescription: oe.err.Description,
			})
			return
		case errors.As(err, &ae):
			status, msg = ae.status, ae.message
		case errors.As(err, &mbe):
			status, msg = http.StatusRequestEntityTooLarge, "request body too large"
		case errors.Is(err, storage.ErrNotFound):
			status, msg = http.StatusNotFound, "not found"
		default:
			slog.Error("handler error", "path", r.URL.Path, "error", err)
		}
		var body errorBody
		body.Error.Code = status
		body.Error.Message = msg
		writeJSON(w, status, body)
	}
}
