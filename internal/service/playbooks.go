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

type PlaybookFile struct {
	Path      string                   `json:"path"`
	Filename  string                   `json:"filename"`
	Content   string                   `json:"content"`
	SHA256    string                   `json:"sha256"`
	UpdatedAt string                   `json:"updatedAt,omitempty"`
	Action    *domain.ActionDefinition `json:"action,omitempty"`
}

var managedPlaybookFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:yml|yaml)$`)

func (p *Platform) ReadReleasePlaybook(ctx context.Context, user domain.User, releaseID, relativePath string) (PlaybookFile, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, false)
	if err != nil {
		return PlaybookFile{}, err
	}
	if strings.HasPrefix(filepath.ToSlash(relativePath), managedReleasePrefix(component, release)) {
		workspacePath := strings.TrimPrefix(filepath.ToSlash(relativePath), managedReleasePrefix(component, release))
		file, contents, err := p.ReadReleaseWorkspaceFile(ctx, user, releaseID, workspacePath)
		if err != nil {
			return PlaybookFile{}, err
		}
		if !file.Editable {
			return PlaybookFile{}, fmt.Errorf("%w: Playbook entrypoint must be UTF-8 text no larger than 1 MiB", domain.ErrInvalid)
		}
		return PlaybookFile{Path: relativePath, Filename: filepath.Base(relativePath), Content: string(contents), SHA256: file.SHA256, UpdatedAt: file.UpdatedAt.UTC().Format(time.RFC3339Nano)}, nil
	}
	return p.readReleasePlaybookFile(release, component, relativePath)
}

func (p *Platform) readReleasePlaybookFile(release domain.ComponentRelease, component domain.Component, relativePath string) (PlaybookFile, error) {
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
	filename = filepath.Base(strings.TrimSpace(filename))
	if !managedPlaybookFilename.MatchString(filename) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook filename must end in .yml or .yaml and contain only letters, digits, dots, dashes, and underscores", domain.ErrInvalid)
	}
	kind := domain.ActionKind(strings.TrimSuffix(strings.TrimSuffix(filename, ".yaml"), ".yml"))
	return p.SaveReleaseActionPlaybook(ctx, user, releaseID, kind, contents)
}

func (p *Platform) SaveReleaseActionPlaybook(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind, contents []byte) (PlaybookFile, error) {
	return p.SaveReleaseActionPlaybookConditional(ctx, user, releaseID, kind, contents, nil)
}

func (p *Platform) SaveReleaseActionPlaybookConditional(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind, contents []byte, expectedSHA256 *string) (PlaybookFile, error) {
	return p.SaveReleaseActionPlaybookWithExpectation(ctx, user, releaseID, kind, contents, expectedSHA256, nil)
}

func (p *Platform) SaveReleaseActionPlaybookWithExpectation(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind, contents []byte, expectedSHA256, expectedTreeSHA256 *string) (PlaybookFile, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return PlaybookFile{}, err
	}
	path, err := actionPlaybookPath(component, release, kind)
	if err != nil {
		return PlaybookFile{}, err
	}
	if len(contents) == 0 || len(contents) > MaxPlaybookBytes {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must contain 1 byte to 1 MiB", domain.ErrInvalid)
	}
	if !utf8.Valid(contents) || strings.ContainsRune(string(contents), 0) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must be UTF-8 text without NUL bytes", domain.ErrInvalid)
	}
	file, err := p.SaveReleaseWorkspaceFileWithExpectation(ctx, user, releaseID, string(kind)+".yml", contents, expectedSHA256, expectedTreeSHA256)
	if err != nil {
		return PlaybookFile{}, err
	}
	if err := p.store.SetDraftActionPlaybook(ctx, releaseID, kind, path, file.SHA256); err != nil {
		return PlaybookFile{}, err
	}
	result := PlaybookFile{Path: path, Filename: string(kind) + ".yml", Content: string(contents), SHA256: file.SHA256, UpdatedAt: file.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	p.audit(ctx, user, "component_playbook.saved", "component_release", release.ID, map[string]any{
		"path": result.Path, "sha256": result.SHA256, "size": len(contents),
	})
	return result, nil
}

// SaveReleaseActionAtomic persists the complete Action and its entrypoint as
// one catalog mutation. Filesystem publication is compensated on transaction
// failure and protected across process exit by a durable recovery marker;
// the SQLite manifest, Action row, and marker removal share one transaction.
func (p *Platform) SaveReleaseActionAtomic(ctx context.Context, user domain.User, releaseID string, action domain.ActionDefinition, contents []byte, expectedSHA256, expectedTreeSHA256 *string) (PlaybookFile, error) {
	if len(contents) == 0 || len(contents) > MaxPlaybookBytes {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must contain 1 byte to 1 MiB", domain.ErrInvalid)
	}
	if !utf8.Valid(contents) || strings.ContainsRune(string(contents), 0) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must be UTF-8 text without NUL bytes", domain.ErrInvalid)
	}
	file, persistedAction, err := p.saveReleaseWorkspaceActionAtomic(ctx, user, releaseID, action, contents, expectedSHA256, expectedTreeSHA256)
	if err != nil {
		return PlaybookFile{}, err
	}
	result := PlaybookFile{Path: persistedAction.Playbook, Filename: string(persistedAction.Kind) + ".yml", Content: string(contents), SHA256: file.SHA256, UpdatedAt: file.UpdatedAt.UTC().Format(time.RFC3339Nano), Action: &persistedAction}
	p.audit(ctx, user, "component_action.saved", "component_release", releaseID, map[string]any{"actionId": persistedAction.ID, "kind": persistedAction.Kind, "path": result.Path, "sha256": result.SHA256, "size": len(contents)})
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
	if !user.Role.Valid() || (release.Status != domain.ReleaseReleased && !(release.Status == domain.ReleaseDraft && release.Candidate)) {
		return release, component, domain.ErrForbidden
	}
	if release.Status == domain.ReleaseDraft {
		if err := p.validateReleaseForCandidate(ctx, release); err != nil {
			return release, component, domain.ErrForbidden
		}
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

func (p *Platform) removeManagedReleasePlaybooks(component domain.Component, release domain.ComponentRelease) error {
	if strings.TrimSpace(p.playbookRoot) == "" {
		return nil
	}
	_, placeholder, err := p.resolveManagedPlaybookWriteTarget(component, release, managedReleasePrefix(component, release)+"delete-placeholder.yml", false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(placeholder))
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
	persistedSource, err := p.store.GetComponentRelease(context.Background(), sourceID)
	if err != nil {
		return componentImportManifest{}, err
	}
	if len(persistedSource.Actions) == 0 && len(persistedSource.PlaybookFiles) == 0 {
		return componentImportManifest{}, nil
	}
	if err := p.validateWorkspaceManifest(context.Background(), persistedSource); err != nil {
		return componentImportManifest{}, err
	}
	sourceDirectory, err := p.workspaceDirectory(component, persistedSource, false)
	if err != nil {
		return componentImportManifest{}, err
	}
	files := make([]componentImportFile, 0, len(persistedSource.PlaybookFiles))
	target.PlaybookFiles = make([]domain.ComponentPlaybookFile, 0, len(persistedSource.PlaybookFiles))
	for _, file := range persistedSource.PlaybookFiles {
		contents, err := os.ReadFile(filepath.Join(sourceDirectory, filepath.FromSlash(file.Path)))
		if err != nil {
			return componentImportManifest{}, fmt.Errorf("read source workspace file %s: %w", file.Path, err)
		}
		files = append(files, componentImportFile{Component: component, Release: *target, RelativePath: managedReleasePrefix(component, *target) + file.Path, Content: string(contents)})
		file.ReleaseID, file.UpdatedAt = target.ID, time.Now().UTC()
		target.PlaybookFiles = append(target.PlaybookFiles, file)
	}
	target.PlaybookTreeSHA256 = workspaceTreeSHA(target.PlaybookFiles)
	for index := range target.Actions {
		path, err := actionPlaybookPath(component, *target, target.Actions[index].Kind)
		if err != nil {
			return componentImportManifest{}, err
		}
		target.Actions[index].Playbook = path
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
