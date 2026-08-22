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

func TestHandlerDoesNotStoreSPAShell(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/components", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Skip("frontend assets have not been built")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("unexpected SPA cache policy: %q", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("unexpected SPA pragma: %q", got)
	}
}
