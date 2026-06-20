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

var errStoreUnavailable = &apiError{status: http.StatusServiceUnavailable, message: "telemetry store unavailable"}

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
		switch {
		case errors.As(err, &ae):
			status, msg = ae.status, ae.message
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
