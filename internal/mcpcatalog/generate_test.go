package mcpcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRejectsUncoveredRouteRegistrations(t *testing.T) {
	for _, registration := range []string{
		`h.router.HandleFunc(pattern, h.list)`,
		`h.router.Handle("GET /api/v1/new", h.list)`,
		`h.router.HandleFunc("GET /api/v1/new", wrap(h.list))`,
	} {
		t.Run(registration, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "internal", "api")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			source := "package api\nfunc (h *Handler) routes() { " + registration + " }\nfunc (h *Handler) list() {}\n"
			if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Generate(root); err == nil || !strings.Contains(err.Error(), "route") {
				t.Fatalf("unsupported route was silently omitted: %v", err)
			}
		})
	}
}
