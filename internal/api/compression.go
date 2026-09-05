package api

import (
	"bytes"
	"compress/gzip"
	"math"
	"net/http"
	"strconv"
	"strings"
)

func acceptsGzip(value string) bool {
	explicit, wildcard := -1.0, 0.0
	for _, entry := range strings.Split(value, ",") {
		parts := strings.Split(entry, ";")
		name := strings.TrimSpace(strings.ToLower(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, val, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				n, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(n) || n < 0 || n > 1 {
					quality = 0
				} else {
					quality = n
				}
			}
		}
		if name == "gzip" {
			explicit = quality
		}
		if name == "*" {
			wildcard = quality
		}
	}
	if explicit >= 0 {
		return explicit > 0
	}
	return wildcard > 0
}

// Buffer only the compression threshold. Streams and attachments bypass this
// writer; large JSON is compressed incrementally rather than retained twice.
type jsonCompressionWriter struct {
	http.ResponseWriter
	request *http.Request
	pending bytes.Buffer
	status  int
	started bool
	gzip    *gzip.Writer
}

func (w *jsonCompressionWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
}
func (w *jsonCompressionWriter) start(compress bool) {
	if w.started {
		return
	}
	w.started = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if compress {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.gzip, _ = gzip.NewWriterLevel(w.ResponseWriter, gzip.BestSpeed)
	}
	w.ResponseWriter.WriteHeader(w.status)
}
func (w *jsonCompressionWriter) eligible() bool {
	return strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") && w.Header().Get("Content-Encoding") == "" && w.Header().Get("Content-Disposition") == "" && w.request.Method != http.MethodHead && w.status != http.StatusNoContent && w.status != http.StatusNotModified
}
func (w *jsonCompressionWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.started {
		if !w.eligible() {
			w.start(false)
		} else {
			if w.pending.Len()+len(p) < 1024 {
				return w.pending.Write(p)
			}
			w.start(true)
			if _, err := w.gzip.Write(w.pending.Bytes()); err != nil {
				return 0, err
			}
			w.pending.Reset()
		}
	}
	if w.gzip != nil {
		return w.gzip.Write(p)
	}
	if w.request.Method == http.MethodHead || w.status == http.StatusNoContent || w.status == http.StatusNotModified {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
func (w *jsonCompressionWriter) finish() {
	if !w.started {
		w.start(false)
		if w.request.Method != http.MethodHead && w.status != http.StatusNoContent && w.status != http.StatusNotModified {
			_, _ = w.ResponseWriter.Write(w.pending.Bytes())
		}
	}
	if w.gzip != nil {
		_ = w.gzip.Close()
	}
}
