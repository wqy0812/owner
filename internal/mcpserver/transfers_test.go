package mcpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func transferStore(t *testing.T) *Transfers {
	t.Helper()
	s, err := NewTransfers(filepath.Join(t.TempDir(), "transfers"), 2<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func sha(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }

func TestTransfersChunksIsolationIntegrityAndExpiry(t *testing.T) {
	s := transferStore(t)
	data := bytes.Repeat([]byte("x"), ChunkBytes+17)
	f, err := s.Create("owner", "input.bin", "application/octet-stream", int64(len(data)), sha(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish("owner", f.TransferID); err == nil {
		t.Fatal("incomplete upload finished")
	}
	if _, err := s.Write("other", f.TransferID, 0, data[:1]); err == nil {
		t.Fatal("cross-client write accepted")
	}
	if _, err := s.Write("owner", f.TransferID, 1, data[:1]); err == nil {
		t.Fatal("nonsequential chunk accepted")
	}
	if _, err := s.Write("owner", f.TransferID, 0, data); err == nil {
		t.Fatal("oversize chunk accepted")
	}
	if n, err := s.Write("owner", f.TransferID, 0, data[:ChunkBytes]); err != nil || n != ChunkBytes {
		t.Fatalf("write=%d %v", n, err)
	}
	if _, err := s.Write("owner", f.TransferID, 0, data[:ChunkBytes]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("owner", f.TransferID, 0, []byte("different")); err == nil {
		t.Fatal("changed retry accepted")
	}
	if _, err := s.Write("owner", f.TransferID, ChunkBytes, data[ChunkBytes:]); err != nil {
		t.Fatal(err)
	}
	completed, err := s.Finish("owner", f.TransferID)
	if err != nil || !completed.Complete {
		t.Fatal(completed, err)
	}
	if _, err := s.Write("owner", f.TransferID, 0, data[:1]); err == nil {
		t.Fatal("completed upload changed")
	}
	file, _, release, err := s.Open("owner", f.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("owner", f.TransferID); err == nil {
		t.Fatal("deleted pinned file")
	}
	read := make([]byte, len(data))
	if _, err := file.ReadAt(read, 0); err != nil || !bytes.Equal(read, data) {
		t.Fatal("file bytes differ")
	}
	release()
	if _, _, _, err := s.Open("other", f.TransferID); err == nil {
		t.Fatal("cross-client read accepted")
	}
	s.mu.Lock()
	s.files[f.TransferID].ExpiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	s.sweep(time.Now())
	if _, err := os.Stat(filepath.Join(s.dir, f.TransferID)); !os.IsNotExist(err) {
		t.Fatalf("expired file remains: %v", err)
	}
	if s.reserved != 0 {
		t.Fatal("expired reservation remains")
	}
}

func TestTransfersQuotaChecksumLockAndCrashCleanup(t *testing.T) {
	s := transferStore(t)
	if _, err := NewTransfers(s.dir, 2<<20, 8<<20); err == nil {
		t.Fatal("second server acquired directory")
	}
	if _, err := s.Create("owner", "../escape", "", 0, sha(nil)); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := s.Create("owner", "big", "", 3<<20, sha(nil)); err == nil {
		t.Fatal("oversize accepted")
	}
	f, err := s.Create("owner", "bad", "", 1, sha([]byte("a")))
	if err != nil {
		t.Fatal(err)
	}
	s.Write("owner", f.TransferID, 0, []byte("b"))
	if _, err := s.Finish("owner", f.TransferID); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if err := s.Delete("owner", f.TransferID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Create("owner", "reserved", "", 2<<20, sha(nil)); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 4 {
		t.Fatalf("quota race: accepted=%d", accepted)
	}
	dir := s.dir
	s.Close()
	orphan := filepath.Join(dir, randomID())
	os.WriteFile(orphan, []byte("orphan"), 0600)
	unrelated := filepath.Join(dir, "keep.txt")
	os.WriteFile(unrelated, []byte("keep"), 0600)
	reopened, err := NewTransfers(dir, 2<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan not removed")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated file removed")
	}
}

func TestConfigValidationAndCredentialExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, content := range []string{`{}`, `{"credentials":[]}`} {
		os.WriteFile(path, []byte(content), 0600)
		c, err := ReadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := c.credential(strings.Repeat("a", 64), time.Now()); ok {
			t.Fatal("empty config authorized")
		}
	}
	for _, content := range []string{`{"unknown":1}`, `{} {}`, `{"credentials":[{}]}`, `{"allowedOrigins":["*"]}`, `{"maxFileBytes":-1}`, `{"maxFileBytes":2097152,"maxTotalBytes":1048576}`, strings.Repeat(" ", 65537)} {
		os.WriteFile(path, []byte(content), 0600)
		if _, err := ReadConfig(path); err == nil {
			t.Fatalf("accepted invalid config %s", content[:min(len(content), 80)])
		}
	}
	token := strings.Repeat("a", 64)
	c := Config{Credentials: []Credential{{ClientID: "client", UserID: "user", TokenSHA256: sha([]byte(token)), ExpiresAt: time.Now().Add(time.Minute)}}}
	if _, ok := c.credential(token, time.Now()); !ok {
		t.Fatal("valid token rejected")
	}
	if _, ok := c.credential(token, time.Now().Add(time.Hour)); ok {
		t.Fatal("expired token accepted")
	}
}
