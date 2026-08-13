package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerWithoutBuiltAssetsReturnsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}
