package ui

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Handler serves the built React application and falls back to index.html for
// client-side routes. In development web/dist may not exist; Vite is expected
// to serve the UI and proxy /api requests to the Go server.
func Handler() http.Handler {
	assets := assetFS()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "static asset unavailable", http.StatusInternalServerError)
				return
			}
			data, err = fs.ReadFile(assets, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if strings.Contains(name, "/assets/") || strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}
