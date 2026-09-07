package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (p *CatalogService) DeletePlaybookWorkspaceDirectory(ctx context.Context, user domain.User, releaseID, relative, expectedTree string) (PlaybookWorkspace, error) {
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	release, component, err := p.authorizePlaybook(ctx, user, releaseID, true)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if expectedTree == "" {
		return PlaybookWorkspace{}, fmt.Errorf("%w: expectedTreeSha256 is required", domain.ErrInvalid)
	}
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	root, err := p.workspace.workspaceDirectory(component, release, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	files, tree, err := scanWorkspace(releaseID, root)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if !strings.EqualFold(expectedTree, tree) || !strings.EqualFold(expectedTree, release.PlaybookTreeSHA256) {
		return PlaybookWorkspace{}, fmt.Errorf("%w: workspace changed since it was loaded", domain.ErrConflict)
	}
	target, err := resolveWorkspaceReadPath(root, clean)
	if os.IsNotExist(err) {
		return PlaybookWorkspace{}, domain.ErrNotFound
	}
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return PlaybookWorkspace{}, fmt.Errorf("%w: only real workspace directories may be deleted", domain.ErrInvalid)
	}
	for _, action := range release.Actions {
		path := strings.TrimPrefix(filepath.ToSlash(action.Playbook), managedReleasePrefix(component, release))
		if strings.HasPrefix(path, clean+"/") {
			return PlaybookWorkspace{}, fmt.Errorf("%w: 目录包含动作入口 %s，请先删除对应动作", domain.ErrConflict, path)
		}
	}
	originals := []directoryOriginalFile{}
	for _, file := range files {
		if !strings.HasPrefix(file.Path, clean+"/") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return PlaybookWorkspace{}, err
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != file.SHA256 {
			return PlaybookWorkspace{}, fmt.Errorf("%w: workspace file changed since it was loaded", domain.ErrConflict)
		}
		originals = append(originals, directoryOriginalFile{Path: file.Path, Contents: contents})
	}
	payload, err := json.Marshal(originals)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	journalID := newID("directory-mutation")
	if err := p.store.CreatePendingActionFileMutation(ctx, store.PendingActionFileMutation{
		ID: journalID, Kind: "directory", ReleaseID: release.ID, WorkspaceRoot: managedReleasePrefix(component, release),
		RelativePath: clean, BeforeExists: true, BeforeContents: payload, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return PlaybookWorkspace{}, err
	}
	rollback := func(cause error) (PlaybookWorkspace, error) {
		if err := restoreWorkspaceDirectory(root, "", clean, payload); err != nil {
			return PlaybookWorkspace{}, fmt.Errorf("%w; directory recovery remains pending: %v", cause, err)
		}
		if err := p.discardActionFileMutation(context.Background(), journalID); err != nil {
			return PlaybookWorkspace{}, fmt.Errorf("%w; directory recovery remains pending: %v", cause, err)
		}
		return PlaybookWorkspace{}, cause
	}
	// Keep the directories until commit, so startup recovery can restore every
	// removed file after a process exit. No unjournaled recursive removal occurs.
	for _, file := range originals {
		if err := removeWorkspaceFileDurably(filepath.Join(root, filepath.FromSlash(file.Path))); err != nil {
			return rollback(err)
		}
	}
	workspace, err := p.publishWorkspaceMetadataWithAction(ctx, release, component, root, workspaceActionMutation{journalID: journalID})
	if err != nil {
		return rollback(err)
	}
	directories := []string{}
	_ = filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			directories = append(directories, path)
		}
		return err
	})
	for i := len(directories) - 1; i >= 0; i-- {
		_ = os.Remove(directories[i])
	}
	p.audit.Record(ctx, user, "component_playbook_directory.deleted", "component_release", releaseID, map[string]any{"path": clean, "fileCount": len(originals)})
	return workspace, nil
}

// A directory is one durable mutation, preserving the release-wide journal lock.
// The entire snapshot survives until all files and their manifest have committed.
type directoryOriginalFile struct {
	Path     string `json:"path"`
	Contents []byte `json:"contents"`
}

func restoreWorkspaceDirectory(root, workspace, directory string, payload []byte) error {
	var files []directoryOriginalFile
	if err := json.Unmarshal(payload, &files); err != nil {
		return err
	}
	// Validate every path before restoring anything, including parents on disk.
	for _, file := range files {
		clean, err := cleanWorkspaceRelative(file.Path)
		if err != nil || clean != file.Path || !strings.HasPrefix(clean, directory+"/") {
			return fmt.Errorf("%w: directory recovery contains an unsafe file path", domain.ErrInvalid)
		}
	}
	if err := ensureRealDirectories(root, filepath.ToSlash(filepath.Join(workspace, directory))); err != nil {
		return err
	}
	for _, file := range files {
		relative := filepath.Join(workspace, filepath.FromSlash(file.Path))
		if err := ensureRealDirectories(root, filepath.ToSlash(filepath.Dir(relative))); err != nil {
			return err
		}
		if err := restoreWorkspaceFile(filepath.Join(root, relative), true, file.Contents); err != nil {
			return err
		}
	}
	return nil
}
