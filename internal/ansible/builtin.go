package ansible

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	BuiltinConnectivityPlaybookName  = "ssh-connectivity-check.yml"
	builtinPlaybookDirectory         = "builtin-playbooks"
	builtinConnectivityWorkDirectory = "connectivity-runs"
)

//go:embed builtin/ssh-connectivity-check.yml
var builtinConnectivityPlaybook []byte

// BuiltinPlaybookAsset is the immutable identity and runtime location of one
// platform-owned Playbook. Root is content-addressed so an older binary uses
// its own asset automatically after a rollback.
type BuiltinPlaybookAsset struct {
	Root           string `json:"root"`
	Playbook       string `json:"playbook"`
	PlaybookSHA256 string `json:"playbookSha256"`
	TreeSHA256     string `json:"treeSha256"`
}

// BuiltinRunner permits exactly the embedded connectivity Playbook. It keeps
// platform probes out of the owner-managed Catalog executable tree.
type BuiltinRunner struct {
	runner *Runner
	asset  BuiltinPlaybookAsset
}

func EmbeddedConnectivityPlaybookSHA256() string {
	digest := sha256.Sum256(builtinConnectivityPlaybook)
	return hex.EncodeToString(digest[:])
}

// PrepareBuiltinConnectivityPlaybook atomically materializes the embedded
// Playbook below a private, content-addressed runtime directory. Existing
// assets are accepted only when their type, mode, contents, and tree digest all
// still match the binary.
func PrepareBuiltinConnectivityPlaybook(workRoot string) (BuiltinPlaybookAsset, error) {
	runtimeRoot, err := canonicalRuntimeRoot(workRoot)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	parent, err := ensureReservedDirectory(runtimeRoot, builtinPlaybookDirectory, 0o700)
	if err != nil {
		return BuiltinPlaybookAsset{}, fmt.Errorf("prepare built-in Playbook directory: %w", err)
	}
	digest := EmbeddedConnectivityPlaybookSHA256()
	target := filepath.Join(parent, digest)
	if _, err := os.Lstat(target); err == nil {
		return verifyBuiltinConnectivityPlaybook(target, digest)
	} else if !os.IsNotExist(err) {
		return BuiltinPlaybookAsset{}, fmt.Errorf("inspect built-in Playbook target: %w", err)
	}

	staged, err := os.MkdirTemp(parent, ".connectivity-stage-")
	if err != nil {
		return BuiltinPlaybookAsset{}, fmt.Errorf("stage built-in Playbook: %w", err)
	}
	defer func() {
		if staged != "" {
			_ = os.Chmod(staged, 0o700)
			_ = os.RemoveAll(staged)
		}
	}()
	playbookPath := filepath.Join(staged, BuiltinConnectivityPlaybookName)
	file, err := os.OpenFile(playbookPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return BuiltinPlaybookAsset{}, fmt.Errorf("create built-in Playbook: %w", err)
	}
	if _, err := file.Write(builtinConnectivityPlaybook); err != nil {
		_ = file.Close()
		return BuiltinPlaybookAsset{}, fmt.Errorf("write built-in Playbook: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return BuiltinPlaybookAsset{}, fmt.Errorf("sync built-in Playbook: %w", err)
	}
	if err := file.Chmod(0o400); err != nil {
		_ = file.Close()
		return BuiltinPlaybookAsset{}, fmt.Errorf("protect built-in Playbook: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return BuiltinPlaybookAsset{}, fmt.Errorf("sync protected built-in Playbook: %w", err)
	}
	if err := file.Close(); err != nil {
		return BuiltinPlaybookAsset{}, fmt.Errorf("close built-in Playbook: %w", err)
	}
	if err := os.Chmod(staged, 0o500); err != nil {
		return BuiltinPlaybookAsset{}, fmt.Errorf("protect built-in Playbook tree: %w", err)
	}
	if err := os.Rename(staged, target); err != nil {
		if asset, verifyErr := verifyBuiltinConnectivityPlaybook(target, digest); verifyErr == nil {
			return asset, nil
		}
		return BuiltinPlaybookAsset{}, fmt.Errorf("publish built-in Playbook: %w", err)
	}
	staged = ""
	return verifyBuiltinConnectivityPlaybook(target, digest)
}

func NewBuiltinConnectivityRunner(workRoot string, template Runner) (*BuiltinRunner, BuiltinPlaybookAsset, error) {
	asset, err := PrepareBuiltinConnectivityPlaybook(workRoot)
	if err != nil {
		return nil, BuiltinPlaybookAsset{}, err
	}
	runtimeRoot := filepath.Dir(filepath.Dir(asset.Root))
	connectivityWorkRoot, err := ensureReservedDirectory(runtimeRoot, builtinConnectivityWorkDirectory, 0o700)
	if err != nil {
		return nil, BuiltinPlaybookAsset{}, fmt.Errorf("prepare connectivity Run directory: %w", err)
	}
	template.AllowedRoot = asset.Root
	template.WorkRoot = connectivityWorkRoot
	if template.Env != nil {
		template.Env = cloneEnvironment(template.Env)
	}
	return &BuiltinRunner{runner: &template, asset: asset}, asset, nil
}

func (r *BuiltinRunner) Run(ctx context.Context, request Request) (Result, error) {
	if r == nil || r.runner == nil {
		return Result{}, fmt.Errorf("%w: built-in connectivity runner is not configured", ErrInvalidRequest)
	}
	if request.Playbook != BuiltinConnectivityPlaybookName {
		return Result{}, fmt.Errorf("%w: built-in runner only permits %s", ErrInvalidRequest, BuiltinConnectivityPlaybookName)
	}
	asset, err := verifyBuiltinConnectivityPlaybook(r.asset.Root, r.asset.PlaybookSHA256)
	if err != nil {
		return Result{}, fmt.Errorf("%w: verify built-in connectivity Playbook: %v", ErrArtifactChanged, err)
	}
	if request.ExpectedPlaybookSHA256 != "" && request.ExpectedPlaybookSHA256 != asset.PlaybookSHA256 {
		return Result{}, fmt.Errorf("%w: built-in playbook digest mismatch", ErrArtifactChanged)
	}
	if request.ExpectedTreeSHA256 != "" && request.ExpectedTreeSHA256 != asset.TreeSHA256 {
		return Result{}, fmt.Errorf("%w: built-in tree digest mismatch", ErrArtifactChanged)
	}
	request.ExpectedPlaybookSHA256 = asset.PlaybookSHA256
	request.ExpectedTreeSHA256 = asset.TreeSHA256
	return r.runner.Run(ctx, request)
}

func (r *BuiltinRunner) Asset() BuiltinPlaybookAsset {
	if r == nil {
		return BuiltinPlaybookAsset{}
	}
	return r.asset
}

func canonicalRuntimeRoot(workRoot string) (string, error) {
	if strings.TrimSpace(workRoot) == "" {
		return "", fmt.Errorf("%w: work root is required", ErrInvalidRequest)
	}
	abs, err := filepath.Abs(workRoot)
	if err != nil {
		return "", fmt.Errorf("resolve work root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("create work root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve work root links: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("inspect work root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: work root is not a directory", ErrInvalidPath)
	}
	return real, nil
}

func ensureReservedDirectory(parent, name string, mode os.FileMode) (string, error) {
	path := filepath.Join(parent, name)
	if err := os.Mkdir(path, mode); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%w: reserved path is a symlink or non-directory", ErrInvalidPath)
	}
	if info.Mode().Perm() != mode.Perm() {
		return "", fmt.Errorf("%w: reserved directory mode is %04o, expected %04o", ErrInvalidPath, info.Mode().Perm(), mode.Perm())
	}
	return path, nil
}

func verifyBuiltinConnectivityPlaybook(root, expectedDigest string) (BuiltinPlaybookAsset, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	if resolvedRoot != absRoot {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in asset path traverses a symlink", ErrInvalidPath)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in asset root is a symlink or non-directory", ErrInvalidPath)
	}
	if info.Mode().Perm() != 0o500 {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in asset directory mode is %04o, expected 0500", ErrInvalidPath, info.Mode().Perm())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	if len(entries) != 1 || entries[0].Name() != BuiltinConnectivityPlaybookName {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in asset tree contains unexpected entries", ErrInvalidPath)
	}
	playbookPath := filepath.Join(root, BuiltinConnectivityPlaybookName)
	playbookInfo, err := os.Lstat(playbookPath)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	if playbookInfo.Mode()&os.ModeSymlink != 0 || !playbookInfo.Mode().IsRegular() {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in Playbook is a symlink or non-regular file", ErrInvalidPath)
	}
	if playbookInfo.Mode().Perm() != 0o400 {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in Playbook mode is %04o, expected 0400", ErrInvalidPath, playbookInfo.Mode().Perm())
	}
	playbookDigest, err := FileDigest(playbookPath)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	if playbookDigest != expectedDigest {
		return BuiltinPlaybookAsset{}, fmt.Errorf("%w: built-in Playbook digest mismatch", ErrArtifactChanged)
	}
	treeDigest, err := TreeDigest(root)
	if err != nil {
		return BuiltinPlaybookAsset{}, err
	}
	return BuiltinPlaybookAsset{Root: root, Playbook: BuiltinConnectivityPlaybookName, PlaybookSHA256: playbookDigest, TreeSHA256: treeDigest}, nil
}

func cloneEnvironment(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
