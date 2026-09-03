package ansible

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBatchPlaybookValidationPreservesPathChecks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "valid.yml"), "- hosts: all\n  tasks: []\n", 0o600)
	outside := filepath.Join(t.TempDir(), "outside.yml")
	writeTestFile(t, outside, "outside", 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "escape.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("valid.yml", filepath.Join(root, "inside.yml")); err != nil {
		t.Fatal(err)
	}
	paths := []string{"valid.yml", "valid.yml", "missing.yml", "../outside.yml", outside, "escape.yml", "inside.yml", ".", ""}
	runner := &Runner{AllowedRoot: root}
	results := runner.ValidatePlaybooks(paths)
	for _, path := range paths {
		got, found := results[path]
		_, _, want := runner.Digest(path)
		if !found || (got == nil) != (want == nil) || (want != nil && got.Error() != want.Error()) {
			t.Fatalf("%q: batch=%v single=%v found=%v", path, got, want, found)
		}
	}
	if err := os.Remove(filepath.Join(root, "valid.yml")); err != nil {
		t.Fatal(err)
	}
	if next := runner.ValidatePlaybooks([]string{"valid.yml"}); next["valid.yml"] == nil {
		t.Fatal("next validation reused a deleted file")
	}
}

func TestBatchPlaybookValidationScansTreeOnceAndPropagatesFailure(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first.yml", "second.yml"} {
		writeTestFile(t, filepath.Join(root, name), "- hosts: all\n  tasks: []\n", 0o600)
	}
	runner := &Runner{AllowedRoot: root}
	scans := 0
	treeFailure := errors.New("tree unreadable")
	results := runner.validatePlaybooks([]string{"first.yml", "second.yml", "first.yml", "missing.yml"}, func(string) (string, error) {
		scans++
		return "", treeFailure
	})
	if scans != 1 || len(results) != 3 || !errors.Is(results["first.yml"], treeFailure) || !errors.Is(results["second.yml"], treeFailure) {
		t.Fatalf("tree scans=%d results=%v", scans, results)
	}
	if !errors.Is(results["missing.yml"], os.ErrNotExist) {
		t.Fatalf("file-specific error was lost: %v", results["missing.yml"])
	}
	runner.validatePlaybooks([]string{"missing.yml"}, func(string) (string, error) {
		t.Fatal("invalid paths must not trigger a tree scan")
		return "", nil
	})
}
