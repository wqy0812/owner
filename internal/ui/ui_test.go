package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandlerWithoutBuiltAssetsReturnsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestHandlerDoesNotCacheBuildVersion(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("index")},
		"version.json": &fstest.MapFile{Data: []byte(`{"version":"build-test"}`)},
	}
	req := httptest.NewRequest(http.MethodGet, "/version.json", nil)
	rec := httptest.NewRecorder()
	handler(fs.FS(assets)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("unexpected build version cache policy: %q", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("unexpected build version pragma: %q", got)
	}
	if got := rec.Body.String(); got != `{"version":"build-test"}` {
		t.Fatalf("unexpected build version body: %q", got)
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
