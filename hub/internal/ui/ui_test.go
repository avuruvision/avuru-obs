package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerWithoutBuiltUI(t *testing.T) {
	// In a source checkout dist/ holds only .gitkeep, so the handler must
	// serve the "not built" notice rather than erroring.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "Avuru Obs") {
		t.Errorf("body does not mention Avuru Obs: %q", rec.Body.String())
	}
}
