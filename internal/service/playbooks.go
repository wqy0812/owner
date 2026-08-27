package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"codex/platform-demo/internal/domain"
)

const MaxPlaybookBytes = 1 << 20

type PlaybookFile struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	Content   string `json:"content"`
	SHA256    string `json:"sha256"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

var managedPlaybookFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:yml|yaml)$`)

func (p *Platform) ReadReleasePlaybook(ctx context.Context, user domain.User, releaseID, relativePath string) (PlaybookFile, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, false)
	if err != nil {
		return PlaybookFile{}, err
	}
	clean, resolved, err := p.resolveManagedPlaybookPath(component, release, relativePath, false)
	if err != nil {
		return PlaybookFile{}, err
	}
	if !releaseReferencesPlaybook(release, clean) && !strings.HasPrefix(clean, managedReleasePrefix(component, release)) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook is not part of this release", domain.ErrForbidden)
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return PlaybookFile{}, domain.ErrNotFound
		}
		return PlaybookFile{}, fmt.Errorf("read playbook: %w", err)
	}
	if len(contents) > MaxPlaybookBytes {
		return PlaybookFile{}, fmt.Errorf("%w: playbook exceeds 1 MiB", domain.ErrInvalid)
	}
	info, _ := os.Stat(resolved)
	return playbookFile(clean, contents, info), nil
}

func (p *Platform) SaveReleasePlaybook(ctx context.Context, user domain.User, releaseID string, filename string, contents []byte) (PlaybookFile, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return PlaybookFile{}, err
	}
	filename = filepath.Base(strings.TrimSpace(filename))
	if !managedPlaybookFilename.MatchString(filename) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook filename must end in .yml or .yaml and contain only letters, digits, dots, dashes, and underscores", domain.ErrInvalid)
	}
	if len(contents) == 0 || len(contents) > MaxPlaybookBytes {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must contain 1 byte to 1 MiB", domain.ErrInvalid)
	}
	if !utf8.Valid(contents) || strings.ContainsRune(string(contents), 0) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must be UTF-8 text without NUL bytes", domain.ErrInvalid)
	}
	relative := managedReleasePrefix(component, release) + filename
	clean, resolved, err := p.resolveManagedPlaybookPath(component, release, relative, true)
	if err != nil {
		return PlaybookFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o750); err != nil {
		return PlaybookFile{}, fmt.Errorf("create managed playbook directory: %w", err)
	}
	rootPath, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return PlaybookFile{}, fmt.Errorf("resolve playbook root: %w", err)
	}
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return PlaybookFile{}, fmt.Errorf("resolve playbook root: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(resolved))
	if err != nil {
		return PlaybookFile{}, fmt.Errorf("resolve managed playbook directory: %w", err)
	}
	if rel, relErr := filepath.Rel(root, parent); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return PlaybookFile{}, fmt.Errorf("%w: managed Playbook directory escapes the allowed root", domain.ErrInvalid)
	}
	resolved = filepath.Join(parent, filepath.Base(resolved))
	temporary, err := os.CreateTemp(filepath.Dir(resolved), ".playbook-*.tmp")
	if err != nil {
		return PlaybookFile{}, fmt.Errorf("stage playbook: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return PlaybookFile{}, fmt.Errorf("set playbook permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return PlaybookFile{}, fmt.Errorf("write playbook: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return PlaybookFile{}, fmt.Errorf("sync playbook: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return PlaybookFile{}, fmt.Errorf("close playbook: %w", err)
	}
	// A Playbook write changes executable evidence even when the Draft action
	// already points at this managed path. Revoke delivery state before making
	// the file visible so a crash cannot leave changed executable content marked
	// as verified or shared with a scenario owner.
	if err := p.store.InvalidateDraftReleaseDelivery(ctx, release.ID); err != nil {
		return PlaybookFile{}, fmt.Errorf("invalidate component verification: %w", err)
	}
	if err := os.Rename(temporaryPath, resolved); err != nil {
		return PlaybookFile{}, fmt.Errorf("publish playbook: %w", err)
	}
	info, _ := os.Stat(resolved)
	result := playbookFile(clean, contents, info)
	p.audit(ctx, user, "component_playbook.saved", "component_release", release.ID, map[string]any{
		"path": result.Path, "sha256": result.SHA256, "size": len(contents),
	})
	return result, nil
}

func (p *Platform) authorizePlaybook(ctx context.Context, user domain.User, releaseID string, requireDraft bool) (domain.ComponentRelease, domain.Component, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return release, domain.Component{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, component, err
	}
	if requireDraft {
		if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
			return release, component, err
		}
		if release.Status != domain.ReleaseDraft {
			return release, component, fmt.Errorf("%w: released versions and their Playbooks are immutable", domain.ErrConflict)
		}
		if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
			return release, component, activeErr
		} else if active {
			return release, component, fmt.Errorf("%w: wait for the active component test before editing its Playbook", domain.ErrConflict)
		}
		return release, component, nil
	}
	if user.Role == domain.RoleComponentOwner && user.ID == component.OwnerID {
		return release, component, nil
	}
	if !user.Role.Valid() || (release.Status != domain.ReleaseReleased && !(release.Status == domain.ReleaseDraft && release.Candidate && release.Verified)) {
		return release, component, domain.ErrForbidden
	}
	return release, component, nil
}

func (p *Platform) resolveManagedPlaybookPath(component domain.Component, release domain.ComponentRelease, relative string, forWrite bool) (string, string, error) {
	if strings.TrimSpace(p.playbookRoot) == "" {
		return "", "", fmt.Errorf("playbook management is not configured")
	}
	root, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve playbook root: %w", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return "", "", fmt.Errorf("resolve playbook root: %w", err)
	}
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return "", "", fmt.Errorf("%w: a relative playbook path is required", domain.ErrInvalid)
	}
	clean := filepath.ToSlash(filepath.Clean(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", fmt.Errorf("%w: playbook path escapes the allowed root", domain.ErrInvalid)
	}
	resolved := filepath.Join(root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: playbook path escapes the allowed root", domain.ErrInvalid)
	}
	if !forWrite {
		resolved, err = filepath.EvalSymlinks(resolved)
		if os.IsNotExist(err) {
			return "", "", domain.ErrNotFound
		}
		if err != nil {
			return "", "", fmt.Errorf("resolve playbook: %w", err)
		}
		rel, err = filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("%w: playbook symlink escapes the allowed root", domain.ErrInvalid)
		}
	}
	if forWrite && !strings.HasPrefix(clean, managedReleasePrefix(component, release)) {
		return "", "", fmt.Errorf("%w: Playbooks can only be written to this Draft's managed directory", domain.ErrForbidden)
	}
	return clean, resolved, nil
}

func managedReleasePrefix(component domain.Component, release domain.ComponentRelease) string {
	return "managed/" + component.Slug + "/" + release.ID + "/"
}

// resolveManagedPlaybookWriteTarget validates both the logical managed path
// and its resolved parent. The second check prevents a symlink created below
// playbookRoot from redirecting an import write or cleanup outside the root.
func (p *Platform) resolveManagedPlaybookWriteTarget(component domain.Component, release domain.ComponentRelease, relative string, createParent bool) (string, string, error) {
	clean, target, err := p.resolveManagedPlaybookPath(component, release, relative, true)
	if err != nil {
		return "", "", err
	}
	rootPath, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve playbook root: %w", err)
	}
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve playbook root: %w", err)
	}
	parentPath := filepath.Dir(target)
	rel, err := filepath.Rel(root, parentPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: managed Playbook directory escapes the allowed root", domain.ErrInvalid)
	}
	parent := root
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		parent = filepath.Join(parent, segment)
		info, statErr := os.Lstat(parent)
		if os.IsNotExist(statErr) && createParent {
			if mkdirErr := os.Mkdir(parent, 0o750); mkdirErr != nil {
				return "", "", fmt.Errorf("create managed Playbook directory: %w", mkdirErr)
			}
			continue
		}
		if statErr != nil {
			return "", "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", "", fmt.Errorf("%w: managed Playbook directory contains a symlink or non-directory", domain.ErrInvalid)
		}
	}
	return clean, filepath.Join(parent, filepath.Base(target)), nil
}

func (p *Platform) copyManagedPlaybooksForClone(component domain.Component, sourceID string, target *domain.ComponentRelease) (componentImportManifest, error) {
	sourceRelease := *target
	sourceRelease.ID = sourceID
	sourcePrefix, targetPrefix := managedReleasePrefix(component, sourceRelease), managedReleasePrefix(component, *target)
	files := make([]componentImportFile, 0)
	createdTargets := map[string]bool{}
	for index := range target.Actions {
		path := filepath.ToSlash(filepath.Clean(target.Actions[index].Playbook))
		if !strings.HasPrefix(path, sourcePrefix) {
			continue
		}
		filename := strings.TrimPrefix(path, sourcePrefix)
		if filename == "" || strings.Contains(filename, "/") || !managedPlaybookFilename.MatchString(filename) {
			return componentImportManifest{}, fmt.Errorf("%w: managed Playbook path is invalid", domain.ErrInvalid)
		}
		_, sourcePath, err := p.resolveManagedPlaybookPath(component, sourceRelease, path, false)
		if err != nil {
			return componentImportManifest{}, err
		}
		contents, err := os.ReadFile(sourcePath)
		if err != nil {
			return componentImportManifest{}, fmt.Errorf("read source managed Playbook: %w", err)
		}
		if len(contents) == 0 || len(contents) > MaxPlaybookBytes || !utf8.Valid(contents) {
			return componentImportManifest{}, fmt.Errorf("%w: source managed Playbook is not valid", domain.ErrInvalid)
		}
		targetRelative := targetPrefix + filename
		if createdTargets[targetRelative] {
			target.Actions[index].Playbook = targetRelative
			continue
		}
		files = append(files, componentImportFile{Component: component, Release: *target, RelativePath: targetRelative, Content: string(contents)})
		createdTargets[targetRelative] = true
		target.Actions[index].Playbook = targetRelative
	}
	manifest, err := p.stageComponentImportFiles(files)
	if err != nil {
		return componentImportManifest{}, err
	}
	if err := p.promoteComponentImportFiles(manifest); err != nil {
		_ = p.cleanupComponentImportManifest(manifest, true)
		return componentImportManifest{}, err
	}
	return manifest, nil
}

func releaseReferencesPlaybook(release domain.ComponentRelease, path string) bool {
	for _, action := range release.Actions {
		if filepath.ToSlash(filepath.Clean(action.Playbook)) == path {
			return true
		}
	}
	return false
}

func playbookFile(path string, contents []byte, info os.FileInfo) PlaybookFile {
	digest := sha256.Sum256(contents)
	result := PlaybookFile{Path: path, Filename: filepath.Base(path), Content: string(contents), SHA256: hex.EncodeToString(digest[:])}
	if info != nil {
		result.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return result
}
