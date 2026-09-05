package runarchive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func archiveFixture(t *testing.T, logs string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"run.json": `{"id":"run-1"}`, "run_steps.json": `[]`, "approvals.json": `[]`, "logs.ndjson": logs} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestArchiveVerifiesIdentityAndLogSequence(t *testing.T) {
	for _, test := range []struct {
		name, logs string
		count      int64
		valid      bool
	}{
		{"valid", "{\"id\":1,\"runId\":\"run-1\"}\n{\"id\":3,\"runId\":\"run-1\"}\n", 2, true},
		{"empty", "", 0, true},
		{"foreign-run", "{\"id\":1,\"runId\":\"run-2\"}\n", 1, false},
		{"duplicate-id", "{\"id\":1,\"runId\":\"run-1\"}\n{\"id\":1,\"runId\":\"run-1\"}\n", 2, false},
		{"wrong-count", "{\"id\":1,\"runId\":\"run-1\"}\n", 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "archive.tar.gz")
			if err := Pack(context.Background(), archiveFixture(t, test.logs), target, "run-1", test.count); err != nil {
				t.Fatal(err)
			}
			if err := Verify(target, "run-1"); (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if err := Verify(target, "run-2"); err == nil {
				t.Fatal("accepted another Run's archive")
			}
		})
	}
}

func TestArchiveCancellationAndExistingTarget(t *testing.T) {
	dir := archiveFixture(t, "")
	target := filepath.Join(t.TempDir(), "archive.tar.gz")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Pack(ctx, dir, target, "run-1", 0); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("partial file remains: %v", err)
	}
	if err := os.WriteFile(target, []byte("existing archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Pack(context.Background(), dir, target, "run-1", 0); err == nil {
		t.Fatal("overwrote existing target")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "existing archive" {
		t.Fatalf("existing target changed: %q, %v", data, err)
	}
}

func TestArchivePathRejectsTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "safe.tar.gz")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.tar.gz")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"../safe.tar.gz", target, "link.tar.gz", "safe.zip"} {
		if _, err := Path(root, relative); err == nil {
			t.Fatalf("accepted %q", relative)
		}
	}
	if path, err := Path(root, "safe.tar.gz"); err != nil || path != target {
		t.Fatalf("valid path: %q, %v", path, err)
	}
}
