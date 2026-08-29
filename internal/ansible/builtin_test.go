package ansible

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareBuiltinConnectivityPlaybookMaterializesExactReadOnlyAsset(t *testing.T) {
	workRoot := t.TempDir()
	asset, err := PrepareBuiltinConnectivityPlaybook(workRoot)
	if err != nil {
		t.Fatal(err)
	}
	allowBuiltinAssetCleanup(t, asset.Root)
	if asset.Playbook != BuiltinConnectivityPlaybookName || asset.PlaybookSHA256 != "b62e5079588924bbd207a846ac7023bb6c2efc0b014bb2d60522159b931e8bb6" || asset.TreeSHA256 == "" {
		t.Fatalf("asset=%+v", asset)
	}
	if filepath.Base(asset.Root) != asset.PlaybookSHA256 {
		t.Fatalf("content-addressed root=%q digest=%q", asset.Root, asset.PlaybookSHA256)
	}
	rootInfo, err := os.Lstat(asset.Root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o500 {
		t.Fatalf("asset root info=%v err=%v", rootInfo, err)
	}
	playbookInfo, err := os.Lstat(filepath.Join(asset.Root, asset.Playbook))
	if err != nil || !playbookInfo.Mode().IsRegular() || playbookInfo.Mode().Perm() != 0o400 {
		t.Fatalf("playbook info=%v err=%v", playbookInfo, err)
	}
	again, err := PrepareBuiltinConnectivityPlaybook(workRoot)
	if err != nil || again != asset {
		t.Fatalf("second prepare asset=%+v err=%v want=%+v", again, err, asset)
	}
}

func TestPrepareBuiltinConnectivityPlaybookRejectsReservedPathSymlink(t *testing.T) {
	workRoot := t.TempDir()
	redirect := t.TempDir()
	if err := os.Symlink(redirect, filepath.Join(workRoot, builtinPlaybookDirectory)); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareBuiltinConnectivityPlaybook(workRoot); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("prepare error=%v, want ErrInvalidPath", err)
	}
}

func TestPrepareBuiltinConnectivityPlaybookRejectsNonDirectoryDigestTarget(t *testing.T) {
	workRoot := t.TempDir()
	parent := filepath.Join(workRoot, builtinPlaybookDirectory)
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, EmbeddedConnectivityPlaybookSHA256())
	if err := os.WriteFile(target, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareBuiltinConnectivityPlaybook(workRoot); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("prepare error=%v, want ErrInvalidPath", err)
	}
}

func TestPrepareBuiltinConnectivityPlaybookRejectsTamperedAsset(t *testing.T) {
	workRoot := t.TempDir()
	asset, err := PrepareBuiltinConnectivityPlaybook(workRoot)
	if err != nil {
		t.Fatal(err)
	}
	allowBuiltinAssetCleanup(t, asset.Root)
	playbook := filepath.Join(asset.Root, asset.Playbook)
	if err := os.Chmod(asset.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(playbook, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playbook, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(playbook, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(asset.Root, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareBuiltinConnectivityPlaybook(workRoot); !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("prepare error=%v, want ErrArtifactChanged", err)
	}
}

func TestBuiltinConnectivityRunnerAllowsOnlyEmbeddedAsset(t *testing.T) {
	workRoot := t.TempDir()
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	runner, asset, err := NewBuiltinConnectivityRunner(workRoot, Runner{Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	allowBuiltinAssetCleanup(t, asset.Root)
	if _, err := runner.Run(context.Background(), Request{Playbook: "other.yml", Inventory: []byte("[all]\nlocalhost ansible_connection=local\n")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unexpected Playbook error=%v", err)
	}
	result, err := runner.Run(context.Background(), Request{
		Playbook: BuiltinConnectivityPlaybookName, Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"),
	})
	if err != nil || !result.Successful || result.PlaybookSHA256 != asset.PlaybookSHA256 || result.TreeSHA256 != asset.TreeSHA256 {
		t.Fatalf("result=%+v err=%v asset=%+v", result, err, asset)
	}
	playbook := filepath.Join(asset.Root, asset.Playbook)
	if err := os.Chmod(asset.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(playbook, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playbook, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(playbook, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(asset.Root, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), Request{Playbook: BuiltinConnectivityPlaybookName, Inventory: []byte("[all]\nlocalhost ansible_connection=local\n")}); !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("tampered asset Run error=%v, want ErrArtifactChanged", err)
	}
}

func allowBuiltinAssetCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
}
