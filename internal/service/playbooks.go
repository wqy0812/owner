package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	ansiblerunner "codex/platform-demo/internal/ansible"
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

func (p *CatalogService) ReadPlaybook(ctx context.Context, user domain.User, releaseID, relativePath string) (PlaybookFile, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, false)
	if err != nil {
		return PlaybookFile{}, err
	}
	if strings.HasPrefix(filepath.ToSlash(relativePath), managedReleasePrefix(component, release)) {
		workspacePath := strings.TrimPrefix(filepath.ToSlash(relativePath), managedReleasePrefix(component, release))
		file, contents, err := p.ReadPlaybookWorkspaceFile(ctx, user, releaseID, workspacePath)
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

func (p *CatalogService) readReleasePlaybookFile(release domain.ComponentRelease, component domain.Component, relativePath string) (PlaybookFile, error) {
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

// SaveReleaseActionAtomic persists the complete Action and its entrypoint as
// one catalog mutation. Filesystem publication is compensated on transaction
// failure and protected across process exit by a durable recovery marker;
// the SQLite manifest, Action row, and marker removal share one transaction.
func (p *CatalogService) SaveActionAtomic(ctx context.Context, user domain.User, releaseID string, action domain.ActionDefinition, contents []byte, expectedSHA256, expectedTreeSHA256 *string, confirmYAMLMigration ...bool) (PlaybookFile, error) {
	release, _, authorizeErr := p.authorizePlaybook(ctx, user, releaseID, true)
	if authorizeErr != nil {
		return PlaybookFile{}, authorizeErr
	}
	if err := domain.ValidateResourceContract(action.ResourceContract, release.Parameters, false); err != nil {
		return PlaybookFile{}, err
	}
	if len(contents) == 0 || len(contents) > MaxPlaybookBytes {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must contain 1 byte to 1 MiB", domain.ErrInvalid)
	}
	if !utf8.Valid(contents) || strings.ContainsRune(string(contents), 0) {
		return PlaybookFile{}, fmt.Errorf("%w: playbook must be UTF-8 text without NUL bytes", domain.ErrInvalid)
	}
	if err := ansiblerunner.ValidateRoleTasks(contents, action.Kind == domain.ActionCheck); err != nil {
		return PlaybookFile{}, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	file, persistedAction, err := p.saveReleaseWorkspaceActionAtomic(ctx, user, releaseID, action, contents, expectedSHA256, expectedTreeSHA256, len(confirmYAMLMigration) > 0 && confirmYAMLMigration[0])
	if err != nil {
		return PlaybookFile{}, err
	}
	result := PlaybookFile{Path: persistedAction.Playbook, Filename: string(persistedAction.Kind) + ".yml", Content: string(contents), SHA256: file.SHA256, UpdatedAt: file.UpdatedAt.UTC().Format(time.RFC3339Nano), Action: &persistedAction}
	p.audit.Record(ctx, user, "component_action.saved", "component_release", releaseID, map[string]any{"actionId": persistedAction.ID, "kind": persistedAction.Kind, "path": result.Path, "sha256": result.SHA256, "size": len(contents)})
	return result, nil
}

func (p *CatalogService) authorizePlaybook(ctx context.Context, user domain.User, releaseID string, requireDraft bool) (domain.ComponentRelease, domain.Component, error) {
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
		if err := p.releaseRules.validateReleaseForCandidate(ctx, release); err != nil {
			return release, component, domain.ErrForbidden
		}
	}
	return release, component, nil
}

func (p *CatalogService) resolveManagedPlaybookPath(component domain.Component, release domain.ComponentRelease, relative string, forWrite bool) (string, string, error) {
	prefix := managedReleasePrefix(component, release)
	if forWrite {
		if err := domain.ValidateComponentWorkspaceRoot(prefix); err != nil {
			return "", "", err
		}
	}
	if forWrite && prefix != generatedManagedReleasePrefix(component, release) {
		return "", "", fmt.Errorf("%w: 工作区不属于平台为当前组件版本分配的目录", domain.ErrInvalid)
	}
	if strings.TrimSpace(p.workspace.root) == "" {
		return "", "", fmt.Errorf("playbook management is not configured")
	}
	root, err := filepath.Abs(p.workspace.root)
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
	if !strings.HasPrefix(clean, prefix) && (forWrite || !releaseReferencesPlaybook(release, clean)) {
		return "", "", fmt.Errorf("%w: Playbook must belong to this component workspace", domain.ErrForbidden)
	}
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

func (p *CatalogService) removeManagedReleasePlaybooks(component domain.Component, release domain.ComponentRelease) error {
	if strings.TrimSpace(p.workspace.root) == "" {
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
func (p *CatalogService) resolveManagedPlaybookWriteTarget(component domain.Component, release domain.ComponentRelease, relative string, createParent bool) (string, string, error) {
	clean, target, err := p.resolveManagedPlaybookPath(component, release, relative, true)
	if err != nil {
		return "", "", err
	}
	rootPath, err := filepath.Abs(p.workspace.root)
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

func (p *CatalogService) copyManagedPlaybooksForClone(component domain.Component, sourceID string, target *domain.ComponentRelease) (componentImportManifest, error) {
	persistedSource, err := p.store.GetComponentRelease(context.Background(), sourceID)
	if err != nil {
		return componentImportManifest{}, err
	}
	if len(persistedSource.Actions) == 0 && len(persistedSource.PlaybookFiles) == 0 {
		return componentImportManifest{}, nil
	}
	if err := p.releaseRules.validateWorkspaceManifest(context.Background(), persistedSource); err != nil {
		return componentImportManifest{}, err
	}
	sourceDirectory, err := p.workspace.workspaceDirectory(component, persistedSource, false)
	if err != nil {
		return componentImportManifest{}, err
	}
	renamed := map[string]string{}
	retained := map[string]bool{}
	for _, action := range target.Actions {
		sourcePath := strings.TrimPrefix(action.Playbook, managedReleasePrefix(component, persistedSource))
		targetPath, err := domain.ActionTaskPath(action)
		if err != nil {
			return componentImportManifest{}, err
		}
		renamed[sourcePath] = targetPath
		retained[sourcePath] = true
	}
	files := make([]componentImportFile, 0, len(persistedSource.PlaybookFiles))
	target.PlaybookFiles = make([]domain.ComponentPlaybookFile, 0, len(persistedSource.PlaybookFiles))
	for _, file := range persistedSource.PlaybookFiles {
		if isActionEntrypoint(file.Path) && !retained[file.Path] {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(sourceDirectory, filepath.FromSlash(file.Path)))
		if err != nil {
			return componentImportManifest{}, fmt.Errorf("read source workspace file %s: %w", file.Path, err)
		}
		contents, err = rebaseRoleIncludes(file.Path, contents, renamed, func(name string) bool {
			for _, source := range persistedSource.PlaybookFiles {
				if source.Path == name {
					return true
				}
			}
			return false
		})
		if err != nil {
			return componentImportManifest{}, err
		}
		digest := sha256.Sum256(contents)
		file.SHA256, file.SizeBytes = hex.EncodeToString(digest[:]), int64(len(contents))
		if next := renamed[file.Path]; next != "" {
			file.Path = next
		}
		files = append(files, componentImportFile{Component: component, Release: *target, RelativePath: managedReleasePrefix(component, *target) + file.Path, Content: string(contents)})
		file.ReleaseID, file.UpdatedAt = target.ID, time.Now().UTC()
		target.PlaybookFiles = append(target.PlaybookFiles, file)
	}
	target.PlaybookTreeSHA256 = workspaceTreeSHA(target.PlaybookFiles)
	for index := range target.Actions {
		path, err := actionSourcePath(component, *target, target.Actions[index])
		if err != nil {
			return componentImportManifest{}, err
		}
		target.Actions[index].Playbook = path
		for _, file := range target.PlaybookFiles {
			if managedReleasePrefix(component, *target)+file.Path == path {
				target.Actions[index].PlaybookSHA256 = file.SHA256
			}
		}
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

func (p *CatalogService) ReadActionSource(ctx context.Context, user domain.User, releaseID, actionID string) (PlaybookFile, error) {
	release, _, err := p.authorizePlaybook(ctx, user, releaseID, false)
	if err != nil {
		return PlaybookFile{}, err
	}
	action, ok := release.ActionByID(actionID)
	if !ok {
		return PlaybookFile{}, domain.ErrNotFound
	}
	file, err := p.ReadPlaybook(ctx, user, releaseID, action.Playbook)
	file.Action = &action
	return file, err
}
