package api

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGzipNegotiation(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"", false}, {"br, gzip", true}, {"gzip;q=0", false}, {"gzip;q=0, *;q=1", false},
		{"*;q=0.5", true}, {"gzip;q=0.2", true}, {"gzip;q=bad", false}, {"gzip;q=NaN, *;q=1", false}, {"gzip;q=2", false}, {"GZIP; q=0.5", true},
	} {
		if got := acceptsGzip(tc.header); got != tc.want {
			t.Errorf("%q: %v", tc.header, got)
		}
	}
}

func TestJSONCompressionWireContract(t *testing.T) {
	for _, tc := range []struct {
		name, method, contentType, encoding, disposition string
		status, size                                     int
		compressed, empty                                bool
	}{
		{"small", "GET", "application/json", "", "", 200, 1023, false, false},
		{"threshold", "GET", "application/json", "", "", 200, 1024, true, false},
		{"error", "GET", "application/json", "", "", 400, 4096, true, false},
		{"head", "HEAD", "application/json", "", "", 200, 4096, false, true},
		{"no-content", "GET", "application/json", "", "", 204, 4096, false, true},
		{"not-modified", "GET", "application/json", "", "", 304, 4096, false, true},
		{"file", "GET", "application/json", "", "attachment", 200, 4096, false, false},
		{"encoded", "GET", "application/json", "br", "", 200, 4096, false, false},
		{"text", "GET", "text/plain", "", "", 200, 4096, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/api/v1/test", nil)
			w := &jsonCompressionWriter{ResponseWriter: recorder, request: req}
			w.Header().Set("Content-Type", tc.contentType)
			w.Header().Set("Content-Encoding", tc.encoding)
			w.Header().Set("Content-Disposition", tc.disposition)
			w.WriteHeader(tc.status)
			body := `"` + strings.Repeat("x", tc.size-2) + `"`
			_, _ = w.Write([]byte(body[:10]))
			_, _ = w.Write([]byte(body[10:]))
			w.finish()
			if recorder.Code != tc.status {
				t.Fatalf("status %d", recorder.Code)
			}
			got := recorder.Body.Bytes()
			if compressed := recorder.Header().Get("Content-Encoding") == "gzip"; compressed != tc.compressed {
				t.Fatalf("compression %v", compressed)
			}
			if tc.compressed {
				r, err := gzip.NewReader(recorder.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				got, err = io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.empty {
				body = ""
			}
			if string(got) != body {
				t.Fatal("response body changed")
			}
		})
	}
}

func TestCompressionHandlerAndSSEFlush(t *testing.T) {
	f := newAPIFixture(t)
	// Use a valid fixture session without depending on user aliases.
	users, err := f.database.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cookie := f.session(users[0].ID)
	req := httptest.NewRequest("GET", "/api/v1/components?view=contracts", nil)
	req.AddCookie(cookie)
	req.Header.Set("Accept-Encoding", "gzip")
	compressed := httptest.NewRecorder()
	f.handler.ServeHTTP(compressed, req)
	if compressed.Code != 200 || compressed.Header().Get("Content-Encoding") != "gzip" || compressed.Header().Get("Cache-Control") != "no-store" || compressed.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers %d %v", compressed.Code, compressed.Header())
	}
	req.Header.Set("Accept-Encoding", "gzip;q=0")
	plain := httptest.NewRecorder()
	f.handler.ServeHTTP(plain, req)
	zr, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil || string(body) != plain.Body.String() {
		t.Fatal("negotiated JSON differs")
	}
	server := httptest.NewServer(f.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	eventReq, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events", nil)
	eventReq.AddCookie(cookie)
	eventReq.Header.Set("Accept-Encoding", "gzip")
	response, err := http.DefaultClient.Do(eventReq)
	if err != nil {
		t.Fatalf("SSE did not flush: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE headers: %v", response.Header)
	}
	buf := make([]byte, 1)
	if _, err = response.Body.Read(buf); err != nil {
		t.Fatalf("SSE buffered: %v", err)
	}
}
