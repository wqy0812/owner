package fss

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadRegisterAndRead(t *testing.T) {
	root := t.TempDir()
	server, err := New(root, []string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("component-media")
	digest := sha256.Sum256(contents)
	checksum := hex.EncodeToString(digest[:])
	path := "components/runtime/1.0.0/runtime.tar.gz"

	upload := httptest.NewRequest(http.MethodPut, "/api/v1/files?path="+url.QueryEscape(path)+"&sha256="+checksum, bytes.NewReader(contents))
	upload.RemoteAddr = "127.0.0.1:12345"
	uploadResult := httptest.NewRecorder()
	server.ServeHTTP(uploadResult, upload)
	if uploadResult.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", uploadResult.Code, uploadResult.Body.String())
	}
	stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || !bytes.Equal(stored, contents) {
		t.Fatalf("stored=%q err=%v", stored, err)
	}

	register := httptest.NewRequest(http.MethodPost, "/api/v1/register", strings.NewReader(`{"path":"`+path+`","sha256":"`+checksum+`"}`))
	register.RemoteAddr = "127.0.0.1:12345"
	registerResult := httptest.NewRecorder()
	server.ServeHTTP(registerResult, register)
	if registerResult.Code != http.StatusOK || !strings.Contains(registerResult.Body.String(), checksum) {
		t.Fatalf("register status=%d body=%s", registerResult.Code, registerResult.Body.String())
	}

	read := httptest.NewRequest(http.MethodGet, "/"+path, nil)
	readResult := httptest.NewRecorder()
	server.ServeHTTP(readResult, read)
	if readResult.Code != http.StatusOK || !bytes.Equal(readResult.Body.Bytes(), contents) {
		t.Fatalf("read status=%d body=%q", readResult.Code, readResult.Body.String())
	}
}

func TestWriteAuthorizationChecksumAndPathSafety(t *testing.T) {
	root := t.TempDir()
	server, err := New(root, []string{"192.0.2.55/32"})
	if err != nil {
		t.Fatal(err)
	}
	checksum := strings.Repeat("0", 64)

	spoofed := httptest.NewRequest(http.MethodPut, "/api/v1/files?path=x&sha256="+checksum, strings.NewReader("x"))
	spoofed.RemoteAddr = "127.0.0.1:12345"
	spoofed.Header.Set("X-Forwarded-For", "192.0.2.55")
	spoofedResult := httptest.NewRecorder()
	server.ServeHTTP(spoofedResult, spoofed)
	if spoofedResult.Code != http.StatusForbidden {
		t.Fatalf("spoofed source status=%d", spoofedResult.Code)
	}

	badChecksum := httptest.NewRequest(http.MethodPut, "/api/v1/files?path=safe/file&sha256="+checksum, strings.NewReader("wrong"))
	badChecksum.RemoteAddr = "192.0.2.55:12345"
	badChecksumResult := httptest.NewRecorder()
	server.ServeHTTP(badChecksumResult, badChecksum)
	if badChecksumResult.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad checksum status=%d body=%s", badChecksumResult.Code, badChecksumResult.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "safe", "file")); !os.IsNotExist(err) {
		t.Fatalf("checksum mismatch left a target file: %v", err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	escape := httptest.NewRequest(http.MethodPut, "/api/v1/files?path=escape/file&sha256="+checksum, strings.NewReader("x"))
	escape.RemoteAddr = "192.0.2.55:12345"
	escapeResult := httptest.NewRecorder()
	server.ServeHTTP(escapeResult, escape)
	if escapeResult.Code != http.StatusBadRequest {
		t.Fatalf("symlink escape status=%d body=%s", escapeResult.Code, escapeResult.Body.String())
	}
}
