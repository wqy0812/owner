package ansible

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (r *Runner) canonicalRoot() (string, error) {
	if strings.TrimSpace(r.AllowedRoot) == "" {
		return "", fmt.Errorf("%w: allowed root is required", ErrInvalidRequest)
	}
	abs, err := filepath.Abs(r.AllowedRoot)
	if err != nil {
		return "", fmt.Errorf("allowed root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("allowed root: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("allowed root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: allowed root is not a directory", ErrInvalidPath)
	}
	return real, nil
}

func (r *Runner) resolvePlaybook(relative string) (root, resolved, clean string, err error) {
	root, err = r.canonicalRoot()
	if err != nil {
		return "", "", "", err
	}
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return "", "", "", fmt.Errorf("%w: a relative playbook is required", ErrInvalidPath)
	}
	clean = filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", "", fmt.Errorf("%w: %q", ErrOutsideRoot, relative)
	}
	candidate := filepath.Join(root, clean)
	resolved, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve playbook: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", "", fmt.Errorf("%w: %q", ErrOutsideRoot, relative)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", "", fmt.Errorf("stat playbook: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", "", fmt.Errorf("%w: playbook is not a regular file", ErrInvalidPath)
	}
	return root, resolved, clean, nil
}

func FileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TreeDigest hashes relative paths, file contents, and symlink targets in
// filepath.WalkDir's deterministic lexical order.
func TreeDigest(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == abs || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(h, filepath.ToSlash(rel))
		_, _ = h.Write([]byte{0})
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(h, "symlink\x00"+target)
			_, _ = h.Write([]byte{0})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
