package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/mcpserver"
)

func TestGeneratePrivateFilesWithoutOverwriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credential")
	if err := generate(dir, "agent", "component-alice", time.Hour); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(data))
	digest := sha256.Sum256([]byte(token))
	c, err := mcpserver.ReadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 || c.Credentials[0].TokenSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("token/config mismatch")
	}
	for _, name := range []string{"token", "config.json"} {
		info, _ := os.Stat(filepath.Join(dir, name))
		if info.Mode().Perm() != 0600 {
			t.Fatal("unsafe file permissions")
		}
	}
	if err := generate(dir, "another", "user", time.Hour); err == nil {
		t.Fatal("existing files overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "token"))
	if string(after) != string(data) {
		t.Fatal("existing token changed")
	}
}
