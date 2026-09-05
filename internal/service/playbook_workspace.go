package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

const (
	MaxPlaybookBytes      = 1 << 20
	MaxWorkspaceFileBytes = 10 << 20
	MaxWorkspaceBytes     = 50 << 20
	MaxWorkspaceFiles     = 1000
	MaxWorkspaceDepth     = 16
)

type PlaybookWorkspace struct {
	Root       string                         `json:"root"`
	TreeSHA256 string                         `json:"treeSha256"`
	Files      []domain.ComponentPlaybookFile `json:"files"`
}

type WorkspaceFile struct {
	domain.ComponentPlaybookFile
	Content  string `json:"content,omitempty"`
	Editable bool   `json:"editable"`
}

func workspaceSegment(value string) (string, error) {
	value = strings.TrimSpace(value)
	var out strings.Builder
	separator := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			out.WriteRune(unicode.ToLower(r))
			separator = false
		case r == '.' || r == '_' || r == '-':
			if out.Len() > 0 && !separator {
				out.WriteRune(r)
				separator = true
			}
		case unicode.IsSpace(r):
			if out.Len() > 0 && !separator {
				out.WriteByte('-')
				separator = true
			}
		default:
			if out.Len() > 0 && !separator {
				out.WriteByte('-')
				separator = true
			}
		}
	}
	result := strings.Trim(out.String(), "._-")
	if result == "" || result == "." || result == ".." {
		return "", fmt.Errorf("%w: value cannot form a safe workspace directory", domain.ErrInvalid)
	}
	return result, nil
}

func managedReleasePrefix(component domain.Component, release domain.ComponentRelease) string {
	if root := strings.TrimSpace(release.PlaybookWorkspaceRoot); root != "" {
		return strings.TrimSuffix(filepath.ToSlash(root), "/") + "/"
	}
	return generatedManagedReleasePrefix(component, release)
}

func generatedManagedReleasePrefix(component domain.Component, release domain.ComponentRelease) string {
	componentKey, err := workspaceSegment(component.Slug)
	if err != nil {
		componentKey = component.ID
	}
	lineKey, err := workspaceSegment(release.LineName)
	if err != nil {
		lineKey = release.LineID
	}
	versionKey, err := workspaceSegment(release.Version)
	if err != nil {
		versionKey = release.ID
	}
	// Human-readable names are not identities: different names can normalize to
	// the same segment. The immutable full Release ID makes every physical root
	// unambiguous without sacrificing operator readability.
	releaseKey, err := workspaceSegment(release.ID)
	if err != nil {
		return "managed/" + componentKey + "/" + lineKey + "/" + release.ID + "/"
	}
	return "managed/" + componentKey + "/" + lineKey + "/" + versionKey + "--" + releaseKey + "/"
}

func actionPlaybookPath(component domain.Component, release domain.ComponentRelease, kind domain.ActionKind) (string, error) {
	if !validActionKind(kind) {
		return "", fmt.Errorf("%w: invalid action kind %q", domain.ErrInvalid, kind)
	}
	return managedReleasePrefix(component, release) + string(kind) + ".yml", nil
}

func cleanWorkspaceRelative(relative string) (string, error) {
	relative = filepath.ToSlash(strings.TrimSpace(relative))
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return "", fmt.Errorf("%w: a workspace-relative path is required", domain.ErrInvalid)
	}
	clean := filepath.ToSlash(filepath.Clean(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: workspace path escapes its release", domain.ErrInvalid)
	}
	parts := strings.Split(clean, "/")
	if len(parts) > MaxWorkspaceDepth {
		return "", fmt.Errorf("%w: workspace path exceeds %d levels", domain.ErrInvalid, MaxWorkspaceDepth)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || part == ".git" || part == ".clusterforge" || strings.HasPrefix(part, ".workspace-") {
			return "", fmt.Errorf("%w: workspace path contains a reserved segment", domain.ErrInvalid)
		}
	}
	return clean, nil
}

func isActionEntrypoint(path string) bool {
	for _, kind := range []domain.ActionKind{domain.ActionInspect, domain.ActionPreflight, domain.ActionInstall, domain.ActionConfigure, domain.ActionVerify, domain.ActionUpgrade, domain.ActionRollback, domain.ActionUninstall} {
		if path == string(kind)+".yml" {
			return true
		}
	}
	return false
}

func (p *Platform) workspaceDirectory(component domain.Component, release domain.ComponentRelease, create bool) (string, error) {
	if strings.TrimSpace(p.playbookRoot) == "" {
		return "", fmt.Errorf("playbook management is not configured")
	}
	root, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve playbook root: %w", err)
	}
	relative := strings.TrimSuffix(managedReleasePrefix(component, release), "/")
	directory := filepath.Join(root, filepath.FromSlash(relative))
	if create {
		if err := ensureRealDirectories(root, relative); err != nil {
			return "", fmt.Errorf("create Playbook workspace: %w", err)
		}
	} else if parent, parentErr := filepath.EvalSymlinks(filepath.Dir(directory)); parentErr == nil {
		if rel, relErr := filepath.Rel(root, parent); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: workspace escapes the Playbook root", domain.ErrInvalid)
		}
	} else if !os.IsNotExist(parentErr) {
		return "", fmt.Errorf("resolve Playbook workspace parent: %w", parentErr)
	}
	if info, statErr := os.Lstat(directory); statErr == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return "", fmt.Errorf("%w: workspace is not a real directory", domain.ErrInvalid)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	return directory, nil
}

func ensureRealDirectories(root, relative string) error {
	current := root
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: unsafe workspace directory", domain.ErrInvalid)
		}
		current = filepath.Join(current, filepath.FromSlash(segment))
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o750); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%w: workspace directory contains a symlink or non-directory", domain.ErrInvalid)
		}
	}
	return nil
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func replaceWorkspaceFileAtomically(target string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".workspace-write-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func removeWorkspaceFileDurably(target string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func restoreWorkspaceFile(target string, existed bool, contents []byte) error {
	if existed {
		return replaceWorkspaceFileAtomically(target, contents)
	}
	return removeWorkspaceFileDurably(target)
}

func (p *Platform) beginActionFileMutation(ctx context.Context, release domain.ComponentRelease, component domain.Component, relative string, beforeExists bool, before []byte) (string, error) {
	id := newID("action-file-mutation")
	err := p.store.CreatePendingActionFileMutation(ctx, store.PendingActionFileMutation{
		ID: id, ReleaseID: release.ID, WorkspaceRoot: managedReleasePrefix(component, release), RelativePath: relative,
		BeforeExists: beforeExists, BeforeContents: append([]byte(nil), before...), CreatedAt: time.Now().UTC(),
	})
	return id, err
}

func (p *Platform) discardActionFileMutation(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return p.store.DeletePendingActionFileMutation(ctx, id)
}

// RecoverActionFileMutations restores the pre-request entrypoint whenever a
// durable mutation marker survived a process exit. A successful catalog
// transaction deletes the marker in the same commit as the Action/manifest,
// so a remaining marker unambiguously means the filesystem change must roll
// back before the API starts serving traffic.
func (p *Platform) RecoverActionFileMutations(ctx context.Context) error {
	if strings.TrimSpace(p.playbookRoot) == "" {
		return nil
	}
	root, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	mutations, err := p.store.ListPendingActionFileMutations(ctx)
	if err != nil {
		return err
	}
	for _, mutation := range mutations {
		workspace, err := cleanWorkspaceRelative(strings.TrimSuffix(filepath.ToSlash(mutation.WorkspaceRoot), "/"))
		if err != nil || !strings.HasPrefix(workspace, "managed/") {
			return fmt.Errorf("%w: pending Action mutation %s has an unsafe workspace root", domain.ErrInvalid, mutation.ID)
		}
		relative, err := cleanWorkspaceRelative(mutation.RelativePath)
		if err != nil || !isActionEntrypoint(relative) {
			return fmt.Errorf("%w: pending Action mutation %s has an unsafe entrypoint", domain.ErrInvalid, mutation.ID)
		}
		fullRelative := workspace + "/" + relative
		parentRelative := filepath.ToSlash(filepath.Dir(fullRelative))
		if mutation.BeforeExists {
			if err := ensureRealDirectories(root, parentRelative); err != nil {
				return fmt.Errorf("recover pending Action mutation %s: %w", mutation.ID, err)
			}
		}
		target := filepath.Join(root, filepath.FromSlash(fullRelative))
		if err := restoreWorkspaceFile(target, mutation.BeforeExists, mutation.BeforeContents); err != nil {
			return fmt.Errorf("recover pending Action mutation %s: %w", mutation.ID, err)
		}
		if err := p.store.DeletePendingActionFileMutation(ctx, mutation.ID); err != nil {
			return fmt.Errorf("finish pending Action mutation %s recovery: %w", mutation.ID, err)
		}
	}
	return nil
}

func resolveWorkspaceReadPath(directory, relative string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(directory, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(directory, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: workspace path resolves outside its release", domain.ErrInvalid)
	}
	return resolved, nil
}

// moveDraftWorkspace prepares a business-name path change before the database
// transaction. The returned function restores the old location if the
// transaction fails; published releases never call this path.
func (p *Platform) moveDraftWorkspace(component domain.Component, before, after domain.ComponentRelease) (func(), error) {
	if managedReleasePrefix(component, before) == managedReleasePrefix(component, after) {
		return func() {}, nil
	}
	oldDirectory, err := p.workspaceDirectory(component, before, false)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Lstat(oldDirectory); os.IsNotExist(statErr) {
		return func() {}, nil
	} else if statErr != nil {
		return nil, statErr
	}
	newDirectory, err := p.workspaceDirectory(component, after, false)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(newDirectory); err == nil {
		return nil, fmt.Errorf("%w: target Playbook workspace already exists", domain.ErrConflict)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	root, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	parentRelative := filepath.ToSlash(filepath.Dir(strings.TrimSuffix(managedReleasePrefix(component, after), "/")))
	if err := ensureRealDirectories(root, parentRelative); err != nil {
		return nil, err
	}
	if err := os.Rename(oldDirectory, newDirectory); err != nil {
		return nil, fmt.Errorf("move Playbook workspace: %w", err)
	}
	return func() { _ = os.Rename(newDirectory, oldDirectory) }, nil
}

func scanWorkspace(releaseID, directory string) ([]domain.ComponentPlaybookFile, string, error) {
	files := []domain.ComponentPlaybookFile{}
	normalizedPaths := map[string]string{}
	total := int64(0)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: workspace cannot contain symlinks", domain.ErrInvalid)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: workspace contains a non-regular file", domain.ErrInvalid)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		relative, err = cleanWorkspaceRelative(relative)
		if err != nil {
			return err
		}
		identity := strings.ToLower(relative)
		if prior := normalizedPaths[identity]; prior != "" && prior != relative {
			return fmt.Errorf("%w: normalized workspace path conflict between %s and %s", domain.ErrConflict, prior, relative)
		}
		normalizedPaths[identity] = relative
		if info.Size() > MaxWorkspaceFileBytes {
			return fmt.Errorf("%w: %s exceeds 10 MiB", domain.ErrInvalid, relative)
		}
		total += info.Size()
		if total > MaxWorkspaceBytes {
			return fmt.Errorf("%w: workspace exceeds 50 MiB", domain.ErrInvalid)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		mediaType := mime.TypeByExtension(filepath.Ext(relative))
		if mediaType == "" {
			mediaType = http.DetectContentType(contents)
		}
		files = append(files, domain.ComponentPlaybookFile{ReleaseID: releaseID, Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(digest[:]), SizeBytes: info.Size(), MediaType: mediaType, UpdatedAt: info.ModTime().UTC()})
		if len(files) > MaxWorkspaceFiles {
			return fmt.Errorf("%w: workspace exceeds %d files", domain.ErrInvalid, MaxWorkspaceFiles)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return []domain.ComponentPlaybookFile{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, workspaceTreeSHA(files), nil
}

func workspaceTreeSHA(files []domain.ComponentPlaybookFile) string {
	files = append([]domain.ComponentPlaybookFile(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	hash := sha256.New()
	for _, file := range files {
		_, _ = hash.Write([]byte(file.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.SHA256))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type workspaceActionMutation struct {
	upsert     *domain.ActionDefinition
	deleteKind *domain.ActionKind
	journalID  string
}

func (p *Platform) publishWorkspaceMetadata(ctx context.Context, release domain.ComponentRelease, component domain.Component, directory string) (PlaybookWorkspace, error) {
	return p.publishWorkspaceMetadataWithAction(ctx, release, component, directory, workspaceActionMutation{})
}

func (p *Platform) publishWorkspaceMetadataWithAction(ctx context.Context, release domain.ComponentRelease, component domain.Component, directory string, mutation workspaceActionMutation) (PlaybookWorkspace, error) {
	files, treeSHA, err := scanWorkspace(release.ID, directory)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	digests := map[string]string{}
	byPath := map[string]string{}
	for _, file := range files {
		byPath[file.Path] = file.SHA256
	}
	for _, action := range release.Actions {
		workspacePath := strings.TrimPrefix(filepath.ToSlash(action.Playbook), managedReleasePrefix(component, release))
		if digest := byPath[workspacePath]; digest != "" {
			digests[action.Playbook] = digest
		}
	}
	workspaceRoot := managedReleasePrefix(component, release)
	if mutation.upsert != nil {
		mutation.upsert.PlaybookSHA256 = byPath[string(mutation.upsert.Kind)+".yml"]
		digests[mutation.upsert.Playbook] = mutation.upsert.PlaybookSHA256
		err = p.store.ReplaceDraftPlaybookFilesAndUpsertAction(ctx, release.ID, workspaceRoot, treeSHA, files, digests, *mutation.upsert, mutation.journalID)
	} else if mutation.deleteKind != nil {
		err = p.store.ReplaceDraftPlaybookFilesAndDeleteAction(ctx, release.ID, workspaceRoot, treeSHA, files, digests, *mutation.deleteKind, mutation.journalID)
	} else {
		err = p.store.ReplaceDraftPlaybookFilesAndInvalidate(ctx, release.ID, workspaceRoot, treeSHA, files, digests)
	}
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	return PlaybookWorkspace{Root: workspaceRoot, TreeSHA256: treeSHA, Files: files}, nil
}

func (p *Platform) ListReleasePlaybookWorkspace(ctx context.Context, user domain.User, releaseID string) (PlaybookWorkspace, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	_, err = p.workspaceDirectory(component, release, false)
	if err != nil && !os.IsNotExist(err) {
		return PlaybookWorkspace{}, err
	}
	files := release.PlaybookFiles
	if files == nil {
		files = []domain.ComponentPlaybookFile{}
	}
	return PlaybookWorkspace{Root: managedReleasePrefix(component, release), TreeSHA256: release.PlaybookTreeSHA256, Files: files}, nil
}

func (p *Platform) ReadReleaseWorkspaceFile(ctx context.Context, user domain.User, releaseID, relative string) (WorkspaceFile, []byte, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, false)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	directory, err := p.workspaceDirectory(component, release, false)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	path, err := resolveWorkspaceReadPath(directory, clean)
	if os.IsNotExist(err) {
		return WorkspaceFile{}, nil, domain.ErrNotFound
	}
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WorkspaceFile{}, nil, domain.ErrNotFound
		}
		return WorkspaceFile{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return WorkspaceFile{}, nil, fmt.Errorf("%w: workspace file is not regular", domain.ErrInvalid)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	digest := sha256.Sum256(contents)
	mediaType := mime.TypeByExtension(filepath.Ext(clean))
	if mediaType == "" {
		mediaType = http.DetectContentType(contents)
	}
	file := WorkspaceFile{ComponentPlaybookFile: domain.ComponentPlaybookFile{ReleaseID: releaseID, Path: clean, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(contents)), MediaType: mediaType, UpdatedAt: info.ModTime().UTC()}, Editable: len(contents) <= MaxPlaybookBytes && utf8.Valid(contents) && !strings.ContainsRune(string(contents), 0)}
	if file.Editable {
		file.Content = string(contents)
	}
	return file, contents, nil
}

func (p *Platform) SaveReleaseWorkspaceFile(ctx context.Context, user domain.User, releaseID, relative string, contents []byte) (WorkspaceFile, error) {
	return p.SaveReleaseWorkspaceFileConditional(ctx, user, releaseID, relative, contents, nil)
}

// SaveReleaseWorkspaceFileConditional rejects a stale browser edit when
// expectedSHA256 is present. An empty expected value means the path must not
// exist; a non-empty value must match the current bytes.
func (p *Platform) SaveReleaseWorkspaceFileConditional(ctx context.Context, user domain.User, releaseID, relative string, contents []byte, expectedSHA256 *string) (WorkspaceFile, error) {
	return p.SaveReleaseWorkspaceFileWithExpectation(ctx, user, releaseID, relative, contents, expectedSHA256, nil)
}

func (p *Platform) SaveReleaseWorkspaceFileWithExpectation(ctx context.Context, user domain.User, releaseID, relative string, contents []byte, expectedSHA256, expectedTreeSHA256 *string) (WorkspaceFile, error) {
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
	return p.saveReleaseWorkspaceFileLocked(ctx, user, releaseID, relative, contents, expectedSHA256, expectedTreeSHA256, workspaceActionMutation{})
}

func (p *Platform) saveReleaseWorkspaceActionAtomic(ctx context.Context, user domain.User, releaseID string, action domain.ActionDefinition, contents []byte, expectedSHA256, expectedTreeSHA256 *string) (WorkspaceFile, domain.ActionDefinition, error) {
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return WorkspaceFile{}, action, err
	}
	if !validActionKind(action.Kind) {
		return WorkspaceFile{}, action, fmt.Errorf("%w: invalid action kind", domain.ErrInvalid)
	}
	existingIndex := -1
	for index, current := range release.Actions {
		if action.ID != "" && current.ID == action.ID {
			if current.Kind != action.Kind {
				return WorkspaceFile{}, action, fmt.Errorf("%w: saved action kind is immutable; delete it and create a new action", domain.ErrConflict)
			}
			existingIndex = index
			continue
		}
		if current.Kind == action.Kind {
			return WorkspaceFile{}, action, fmt.Errorf("%w: action kind %s already exists", domain.ErrConflict, action.Kind)
		}
	}
	if action.ID != "" && existingIndex < 0 {
		return WorkspaceFile{}, action, fmt.Errorf("%w: action %s no longer exists", domain.ErrConflict, action.ID)
	}
	if action.ID == "" {
		action.ID = newID("action")
	}
	action.ReleaseID = release.ID
	if strings.TrimSpace(action.Name) == "" {
		action.Name = string(action.Kind)
	}
	if action.TimeoutSeconds <= 0 {
		action.TimeoutSeconds = 1800
	}
	if action.RiskLevel == "" {
		action.RiskLevel = domain.RiskLow
	}
	path, err := actionPlaybookPath(component, release, action.Kind)
	if err != nil {
		return WorkspaceFile{}, action, err
	}
	action.Playbook = path
	prospective := release
	prospective.Actions = append([]domain.ActionDefinition(nil), release.Actions...)
	if existingIndex >= 0 {
		prospective.Actions[existingIndex] = action
	} else {
		prospective.Actions = append(prospective.Actions, action)
	}
	if err := p.validateReleaseContract(ctx, prospective, false); err != nil {
		return WorkspaceFile{}, action, err
	}
	file, err := p.saveReleaseWorkspaceFileLocked(ctx, user, releaseID, string(action.Kind)+".yml", contents, expectedSHA256, expectedTreeSHA256, workspaceActionMutation{upsert: &action})
	return file, action, err
}

func (p *Platform) saveReleaseWorkspaceFileLocked(ctx context.Context, user domain.User, releaseID, relative string, contents []byte, expectedSHA256, expectedTreeSHA256 *string, mutation workspaceActionMutation) (WorkspaceFile, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if expectedTreeSHA256 != nil && !strings.EqualFold(*expectedTreeSHA256, release.PlaybookTreeSHA256) {
		return WorkspaceFile{}, fmt.Errorf("%w: workspace changed since it was loaded", domain.ErrConflict)
	}
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if len(contents) > MaxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("%w: workspace file exceeds 10 MiB", domain.ErrInvalid)
	}
	directory, err := p.workspaceDirectory(component, release, true)
	if err != nil {
		return WorkspaceFile{}, err
	}
	target := filepath.Join(directory, filepath.FromSlash(clean))
	parentRelative := filepath.ToSlash(filepath.Dir(clean))
	if parentRelative != "." {
		if err := ensureRealDirectories(directory, parentRelative); err != nil {
			return WorkspaceFile{}, err
		}
	}
	if existing, statErr := os.Lstat(target); statErr == nil && (existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular()) {
		return WorkspaceFile{}, fmt.Errorf("%w: workspace target is not a regular file", domain.ErrInvalid)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return WorkspaceFile{}, statErr
	}
	var previous []byte
	hadPrevious := false
	if data, readErr := os.ReadFile(target); readErr == nil {
		previous, hadPrevious = data, true
	} else if !os.IsNotExist(readErr) {
		return WorkspaceFile{}, readErr
	}
	if expectedSHA256 != nil {
		if !hadPrevious && *expectedSHA256 != "" {
			return WorkspaceFile{}, fmt.Errorf("%w: workspace file was deleted or renamed by another edit", domain.ErrConflict)
		}
		if hadPrevious {
			current := sha256.Sum256(previous)
			if *expectedSHA256 == "" || !strings.EqualFold(*expectedSHA256, hex.EncodeToString(current[:])) {
				return WorkspaceFile{}, fmt.Errorf("%w: workspace file changed since it was loaded", domain.ErrConflict)
			}
		}
	}
	var total int64
	count := len(release.PlaybookFiles)
	found := false
	for _, file := range release.PlaybookFiles {
		if strings.EqualFold(file.Path, clean) && file.Path != clean {
			return WorkspaceFile{}, fmt.Errorf("%w: normalized workspace path conflicts with %s", domain.ErrConflict, file.Path)
		}
		total += file.SizeBytes
		if file.Path == clean {
			total -= file.SizeBytes
			found = true
		}
	}
	if !found {
		count++
	}
	if count > MaxWorkspaceFiles || total+int64(len(contents)) > MaxWorkspaceBytes {
		return WorkspaceFile{}, fmt.Errorf("%w: workspace limits would be exceeded", domain.ErrInvalid)
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(target)); err != nil || !strings.HasPrefix(parent+string(filepath.Separator), directory+string(filepath.Separator)) {
		return WorkspaceFile{}, fmt.Errorf("%w: workspace parent is unsafe", domain.ErrInvalid)
	}
	if mutation.upsert != nil {
		mutation.journalID, err = p.beginActionFileMutation(ctx, release, component, clean, hadPrevious, previous)
		if err != nil {
			return WorkspaceFile{}, err
		}
	}
	if err := replaceWorkspaceFileAtomically(target, contents); err != nil {
		recoveryErr := restoreWorkspaceFile(target, hadPrevious, previous)
		if recoveryErr == nil {
			recoveryErr = p.discardActionFileMutation(ctx, mutation.journalID)
		}
		if recoveryErr != nil {
			return WorkspaceFile{}, fmt.Errorf("write workspace file: %w; recovery remains pending: %v", err, recoveryErr)
		}
		return WorkspaceFile{}, err
	}
	workspace, err := p.publishWorkspaceMetadataWithAction(ctx, release, component, directory, mutation)
	if err != nil {
		recoveryErr := restoreWorkspaceFile(target, hadPrevious, previous)
		if recoveryErr == nil {
			recoveryErr = p.discardActionFileMutation(ctx, mutation.journalID)
		}
		if recoveryErr != nil {
			return WorkspaceFile{}, fmt.Errorf("publish workspace metadata: %w; recovery remains pending: %v", err, recoveryErr)
		}
		return WorkspaceFile{}, err
	}
	for _, file := range workspace.Files {
		if file.Path == clean {
			editable := len(contents) <= MaxPlaybookBytes && utf8.Valid(contents) && !strings.ContainsRune(string(contents), 0)
			result := WorkspaceFile{ComponentPlaybookFile: file, Editable: editable}
			if editable {
				result.Content = string(contents)
			}
			p.audit(ctx, user, "component_playbook_file.saved", "component_release", releaseID, map[string]any{"path": clean, "sha256": file.SHA256, "size": file.SizeBytes})
			return result, nil
		}
	}
	return WorkspaceFile{}, domain.ErrNotFound
}

func (p *Platform) RenameReleaseWorkspaceFile(ctx context.Context, user domain.User, releaseID, from, to string) (PlaybookWorkspace, error) {
	return p.RenameReleaseWorkspaceFileConditional(ctx, user, releaseID, from, to, nil)
}

func (p *Platform) RenameReleaseWorkspaceFileConditional(ctx context.Context, user domain.User, releaseID, from, to string, expectedSHA256 *string) (PlaybookWorkspace, error) {
	return p.RenameReleaseWorkspaceFileWithExpectation(ctx, user, releaseID, from, to, expectedSHA256, nil)
}

func (p *Platform) RenameReleaseWorkspaceFileWithExpectation(ctx context.Context, user domain.User, releaseID, from, to string, expectedSHA256, expectedTreeSHA256 *string) (PlaybookWorkspace, error) {
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if expectedTreeSHA256 != nil && !strings.EqualFold(*expectedTreeSHA256, release.PlaybookTreeSHA256) {
		return PlaybookWorkspace{}, fmt.Errorf("%w: workspace changed since it was loaded", domain.ErrConflict)
	}
	from, err = cleanWorkspaceRelative(from)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	to, err = cleanWorkspaceRelative(to)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if isActionEntrypoint(from) || isActionEntrypoint(to) {
		return PlaybookWorkspace{}, fmt.Errorf("%w: action entrypoints cannot be renamed", domain.ErrConflict)
	}
	directory, err := p.workspaceDirectory(component, release, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	source, err := resolveWorkspaceReadPath(directory, from)
	if os.IsNotExist(err) {
		return PlaybookWorkspace{}, domain.ErrNotFound
	}
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if expectedSHA256 != nil {
		contents, readErr := os.ReadFile(source)
		if readErr != nil {
			return PlaybookWorkspace{}, readErr
		}
		current := sha256.Sum256(contents)
		if !strings.EqualFold(*expectedSHA256, hex.EncodeToString(current[:])) {
			return PlaybookWorkspace{}, fmt.Errorf("%w: workspace file changed since it was loaded", domain.ErrConflict)
		}
	}
	target := filepath.Join(directory, filepath.FromSlash(to))
	if _, err := os.Lstat(target); err == nil {
		return PlaybookWorkspace{}, fmt.Errorf("%w: target workspace path already exists", domain.ErrConflict)
	}
	parentRelative := filepath.ToSlash(filepath.Dir(to))
	if parentRelative != "." {
		if err := ensureRealDirectories(directory, parentRelative); err != nil {
			return PlaybookWorkspace{}, err
		}
	}
	if err := os.Rename(source, target); err != nil {
		return PlaybookWorkspace{}, err
	}
	workspace, err := p.publishWorkspaceMetadata(ctx, release, component, directory)
	if err != nil {
		_ = os.Rename(target, source)
	}
	if err == nil {
		p.audit(ctx, user, "component_playbook_file.renamed", "component_release", releaseID, map[string]any{"from": from, "to": to})
	}
	return workspace, err
}

func (p *Platform) DeleteReleaseWorkspaceFile(ctx context.Context, user domain.User, releaseID, relative string) (PlaybookWorkspace, error) {
	return p.deleteReleaseWorkspaceFile(ctx, user, releaseID, relative, false, nil, nil)
}

func (p *Platform) DeleteReleaseWorkspaceFileConditional(ctx context.Context, user domain.User, releaseID, relative string, expectedSHA256 *string) (PlaybookWorkspace, error) {
	return p.deleteReleaseWorkspaceFile(ctx, user, releaseID, relative, false, expectedSHA256, nil)
}

func (p *Platform) DeleteReleaseWorkspaceFileWithExpectation(ctx context.Context, user domain.User, releaseID, relative string, expectedSHA256, expectedTreeSHA256 *string) (PlaybookWorkspace, error) {
	return p.deleteReleaseWorkspaceFile(ctx, user, releaseID, relative, false, expectedSHA256, expectedTreeSHA256)
}

func (p *Platform) DeleteReleaseActionPlaybook(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind) (PlaybookWorkspace, error) {
	return p.DeleteReleaseActionPlaybookConditional(ctx, user, releaseID, kind, nil)
}

func (p *Platform) DeleteReleaseActionPlaybookConditional(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind, expectedSHA256 *string) (PlaybookWorkspace, error) {
	return p.DeleteReleaseActionPlaybookWithExpectation(ctx, user, releaseID, kind, expectedSHA256, nil)
}

func (p *Platform) DeleteReleaseActionPlaybookWithExpectation(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind, expectedSHA256, expectedTreeSHA256 *string) (PlaybookWorkspace, error) {
	if !validActionKind(kind) {
		return PlaybookWorkspace{}, fmt.Errorf("%w: invalid action kind", domain.ErrInvalid)
	}
	return p.deleteReleaseWorkspaceFile(ctx, user, releaseID, string(kind)+".yml", true, expectedSHA256, expectedTreeSHA256)
}

func (p *Platform) DeleteReleaseActionAtomic(ctx context.Context, user domain.User, releaseID string, kind domain.ActionKind, expectedSHA256, expectedTreeSHA256 *string) (PlaybookWorkspace, error) {
	if !validActionKind(kind) {
		return PlaybookWorkspace{}, fmt.Errorf("%w: invalid action kind", domain.ErrInvalid)
	}
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
	release, _, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	found := false
	for _, action := range release.Actions {
		if action.Kind == kind {
			found = true
			break
		}
	}
	if !found {
		return PlaybookWorkspace{}, fmt.Errorf("%w: action kind %s no longer exists", domain.ErrConflict, kind)
	}
	workspace, err := p.deleteReleaseWorkspaceFileLocked(ctx, user, releaseID, string(kind)+".yml", true, expectedSHA256, expectedTreeSHA256, workspaceActionMutation{deleteKind: &kind})
	if err == nil {
		p.audit(ctx, user, "component_action.deleted", "component_release", releaseID, map[string]any{"kind": kind})
	}
	return workspace, err
}

func (p *Platform) deleteReleaseWorkspaceFile(ctx context.Context, user domain.User, releaseID, relative string, allowEntrypoint bool, expectedSHA256, expectedTreeSHA256 *string) (PlaybookWorkspace, error) {
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
	return p.deleteReleaseWorkspaceFileLocked(ctx, user, releaseID, relative, allowEntrypoint, expectedSHA256, expectedTreeSHA256, workspaceActionMutation{})
}

func (p *Platform) deleteReleaseWorkspaceFileLocked(ctx context.Context, user domain.User, releaseID, relative string, allowEntrypoint bool, expectedSHA256, expectedTreeSHA256 *string, mutation workspaceActionMutation) (PlaybookWorkspace, error) {
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if expectedTreeSHA256 != nil && !strings.EqualFold(*expectedTreeSHA256, release.PlaybookTreeSHA256) {
		return PlaybookWorkspace{}, fmt.Errorf("%w: workspace changed since it was loaded", domain.ErrConflict)
	}
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if isActionEntrypoint(clean) && !allowEntrypoint {
		return PlaybookWorkspace{}, fmt.Errorf("%w: remove the action before deleting its entrypoint", domain.ErrConflict)
	}
	directory, err := p.workspaceDirectory(component, release, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	target, err := resolveWorkspaceReadPath(directory, clean)
	if os.IsNotExist(err) {
		return PlaybookWorkspace{}, domain.ErrNotFound
	}
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return PlaybookWorkspace{}, domain.ErrNotFound
		}
		return PlaybookWorkspace{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PlaybookWorkspace{}, fmt.Errorf("%w: only regular workspace files may be deleted", domain.ErrInvalid)
	}
	contents, readErr := os.ReadFile(target)
	if readErr != nil {
		return PlaybookWorkspace{}, readErr
	}
	if expectedSHA256 != nil {
		current := sha256.Sum256(contents)
		if !strings.EqualFold(*expectedSHA256, hex.EncodeToString(current[:])) {
			return PlaybookWorkspace{}, fmt.Errorf("%w: workspace file changed since it was loaded", domain.ErrConflict)
		}
	}
	if mutation.deleteKind != nil {
		mutation.journalID, err = p.beginActionFileMutation(ctx, release, component, clean, true, contents)
		if err != nil {
			return PlaybookWorkspace{}, err
		}
		if err := removeWorkspaceFileDurably(target); err != nil {
			recoveryErr := restoreWorkspaceFile(target, true, contents)
			if recoveryErr == nil {
				recoveryErr = p.discardActionFileMutation(ctx, mutation.journalID)
			}
			if recoveryErr != nil {
				return PlaybookWorkspace{}, fmt.Errorf("delete Action entrypoint: %w; recovery remains pending: %v", err, recoveryErr)
			}
			return PlaybookWorkspace{}, err
		}
		workspace, err := p.publishWorkspaceMetadataWithAction(ctx, release, component, directory, mutation)
		if err != nil {
			recoveryErr := restoreWorkspaceFile(target, true, contents)
			if recoveryErr == nil {
				recoveryErr = p.discardActionFileMutation(ctx, mutation.journalID)
			}
			if recoveryErr != nil {
				return PlaybookWorkspace{}, fmt.Errorf("publish Action deletion: %w; recovery remains pending: %v", err, recoveryErr)
			}
			return PlaybookWorkspace{}, err
		}
		p.audit(ctx, user, "component_playbook_file.deleted", "component_release", releaseID, map[string]any{"path": clean})
		return workspace, nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(directory), ".workspace-delete-*")
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return PlaybookWorkspace{}, err
	}
	_ = os.Remove(temporaryPath)
	defer os.Remove(temporaryPath)
	if err := os.Rename(target, temporaryPath); err != nil {
		return PlaybookWorkspace{}, err
	}
	workspace, err := p.publishWorkspaceMetadataWithAction(ctx, release, component, directory, mutation)
	if err != nil {
		_ = os.Rename(temporaryPath, target)
	}
	if err == nil {
		p.audit(ctx, user, "component_playbook_file.deleted", "component_release", releaseID, map[string]any{"path": clean})
	}
	return workspace, err
}
