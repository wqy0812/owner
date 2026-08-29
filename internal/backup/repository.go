package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codex/platform-demo/internal/domain"
)

type RepositoryStatus struct {
	Configured           bool            `json:"configured"`
	Path                 string          `json:"path,omitempty"`
	Branch               string          `json:"branch"`
	AllowedRoot          string          `json:"allowedRoot"`
	RecoveryPoints       []RecoveryPoint `json:"recoveryPoints,omitempty"`
	RestoreTargetKnown   bool            `json:"restoreTargetKnown"`
	TargetCatalogEmpty   bool            `json:"targetCatalogEmpty"`
	TargetComponentCount int             `json:"targetComponentCount"`
	TargetScenarioCount  int             `json:"targetScenarioCount"`
	Behind               bool            `json:"behind"`
	CurrentGeneration    int64           `json:"currentGeneration"`
	BackedUpGeneration   int64           `json:"backedUpGeneration"`
	LastSuccessfulAt     *time.Time      `json:"lastSuccessfulAt,omitempty"`
	LastError            string          `json:"lastError,omitempty"`
	LastErrorAt          *time.Time      `json:"lastErrorAt,omitempty"`
}

type RecoveryPoint struct {
	Ref       string    `json:"ref"`
	Commit    string    `json:"commit"`
	CreatedAt time.Time `json:"createdAt"`
}

type RecoveryResult struct {
	RestorePlan
	Restored bool `json:"restored"`
}

type repositorySelection struct {
	Path      string `json:"path"`
	ClonePath string `json:"clonePath"`
}

type repositoryCommandRunner func(context.Context, string, string, ...string) (string, error)

// RepositoryController owns the Environment Owner workflow for local private
// repositories. All user-provided paths are confined to AllowedRoot.
type RepositoryController struct {
	mu          sync.Mutex
	base        Config
	allowedRoot string
	statePath   string
	scheduler   *Scheduler
	database    *sql.DB
	selection   repositorySelection
	runCommand  repositoryCommandRunner
}

func NewRepositoryController(base Config, allowedRoot string, scheduler *Scheduler, databases ...*sql.DB) (*RepositoryController, error) {
	root, err := filepath.Abs(strings.TrimSpace(allowedRoot))
	if err != nil || strings.TrimSpace(allowedRoot) == "" {
		return nil, fmt.Errorf("configure private repository root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base.BackupDir, 0o700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	c := &RepositoryController{base: base, allowedRoot: root, statePath: filepath.Join(base.BackupDir, "repository.json"), scheduler: scheduler, runCommand: runCommand}
	if len(databases) > 0 {
		c.database = databases[0]
	}
	data, err := os.ReadFile(c.statePath)
	if errors.Is(err, os.ErrNotExist) {
		scheduler.SetEnabled(false)
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &c.selection); err != nil {
		return nil, fmt.Errorf("read Catalog repository selection: %w", err)
	}
	manager, err := c.managerFor(c.selection.ClonePath)
	if err != nil {
		return nil, fmt.Errorf("load selected Catalog repository: %w", err)
	}
	scheduler.SetManager(manager)
	scheduler.SetEnabled(true)
	return c, nil
}

func SelectedRepositoryConfig(base Config) (Config, bool, error) {
	contents, err := os.ReadFile(filepath.Join(base.BackupDir, "repository.json"))
	if errors.Is(err, os.ErrNotExist) {
		return base, false, nil
	}
	if err != nil {
		return base, false, err
	}
	var selection repositorySelection
	if err := json.Unmarshal(contents, &selection); err != nil {
		return base, false, fmt.Errorf("read Catalog repository selection: %w", err)
	}
	if strings.TrimSpace(selection.Path) == "" || strings.TrimSpace(selection.ClonePath) == "" {
		return base, false, fmt.Errorf("Catalog repository selection is incomplete")
	}
	if _, err := os.Stat(filepath.Join(selection.ClonePath, ".git")); err != nil {
		return base, false, fmt.Errorf("selected Catalog repository clone is unavailable: %w", err)
	}
	base.CatalogRepo, base.CatalogRemote = selection.ClonePath, "origin"
	return base, true, nil
}

func (c *RepositoryController) Configured() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selection.Path != ""
}

func (c *RepositoryController) Status(ctx context.Context) (RepositoryStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := RepositoryStatus{Configured: c.selection.Path != "", Path: c.selection.Path, Branch: c.base.CatalogBranch, AllowedRoot: c.allowedRoot}
	c.applyRestoreTargetStatusLocked(ctx, &status)
	if !status.Configured {
		health := c.healthLocked(ctx)
		status.LastError, status.LastErrorAt = health.LastError, health.LastErrorAt
		return status, nil
	}
	health := c.healthLocked(ctx)
	status.Behind = health.Behind
	status.CurrentGeneration, status.BackedUpGeneration = health.CurrentGeneration, health.BackedUpGeneration
	status.LastSuccessfulAt, status.LastError, status.LastErrorAt = health.LastSuccessfulAt, health.LastError, health.LastErrorAt
	if _, err := runCommand(ctx, c.selection.ClonePath, "git", "-C", c.selection.ClonePath, "fetch", "--prune", "--tags", "origin"); err != nil {
		now := time.Now().UTC()
		status.LastError, status.LastErrorAt = "同步所选私有仓库失败: "+err.Error(), &now
		return status, nil
	}
	points, err := c.recoveryPoints(ctx, c.selection.ClonePath)
	if err != nil {
		now := time.Now().UTC()
		status.LastError, status.LastErrorAt = "读取远端恢复点失败: "+err.Error(), &now
		return status, nil
	}
	status.RecoveryPoints = points
	return status, nil
}

func (c *RepositoryController) applyRestoreTargetStatusLocked(ctx context.Context, status *RepositoryStatus) {
	if c.database == nil {
		return
	}
	state, err := inspectTargetCatalog(ctx, c.database)
	if err != nil {
		return
	}
	status.RestoreTargetKnown = true
	status.TargetCatalogEmpty = state.ComponentCount == 0 && state.ScenarioCount == 0
	status.TargetComponentCount = state.ComponentCount
	status.TargetScenarioCount = state.ScenarioCount
}

func (c *RepositoryController) CatalogBackupHealth(ctx context.Context) (domain.CatalogBackupHealth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthLocked(ctx), nil
}

func (c *RepositoryController) healthLocked(ctx context.Context) domain.CatalogBackupHealth {
	health := domain.CatalogBackupHealth{Configured: c.selection.Path != "", RepositoryPath: c.selection.Path}
	if !health.Configured {
		return health
	}
	needed, generation, err := c.scheduler.Manager().NeedsSnapshot(ctx)
	health.CurrentGeneration, health.Behind = generation, needed
	if err != nil {
		health.Behind = true
		health.LastError = "检查最近恢复点失败: " + err.Error()
		now := time.Now().UTC()
		health.LastErrorAt = &now
	} else if latest, latestErr := c.scheduler.Manager().LatestSuccessful(); latestErr == nil {
		health.BackedUpGeneration = latest.PublicationGeneration
		health.LastSuccessfulAt = latest.CompletedAt
	}
	status := c.scheduler.Status()
	if status.LastError != "" && (health.LastSuccessfulAt == nil || status.LastErrorAt == nil || status.LastErrorAt.After(*health.LastSuccessfulAt)) {
		health.LastError, health.LastErrorAt = status.LastError, status.LastErrorAt
	}
	return health
}

func (c *RepositoryController) Create(ctx context.Context, inputPath string) (RepositoryStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, err := c.resolveCreatePath(inputPath)
	if err != nil {
		return RepositoryStatus{}, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return RepositoryStatus{}, fmt.Errorf("%w: repository path already exists", domain.ErrConflict)
		}
		return RepositoryStatus{}, err
	}
	existingParent, err := deepestExistingDirectory(filepath.Dir(path), c.allowedRoot)
	if err != nil {
		return RepositoryStatus{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return RepositoryStatus{}, err
	}
	keepRepository := false
	defer func() {
		if !keepRepository {
			if err := safeRemoveTree(path, c.allowedRoot); err == nil {
				_ = removeEmptyParents(filepath.Dir(path), existingParent, c.allowedRoot)
			}
		}
	}()
	if _, err := runCommand(ctx, c.allowedRoot, "git", "init", "--bare", "--shared=false", path); err != nil {
		return RepositoryStatus{}, err
	}
	_ = os.Chmod(path, 0o700)
	if err := c.seedRepository(ctx, path); err != nil {
		return RepositoryStatus{}, err
	}
	status, err := c.connectLocked(ctx, path)
	if err == nil {
		keepRepository = true
		c.scheduler.Request("private-repository-created")
	}
	return status, err
}

func (c *RepositoryController) Connect(ctx context.Context, inputPath string) (RepositoryStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, err := c.resolveExistingPath(inputPath)
	if err != nil {
		return RepositoryStatus{}, err
	}
	status, err := c.connectLocked(ctx, path)
	if err == nil {
		c.scheduler.Request("private-repository-connected")
	}
	return status, err
}

// Snapshot creates a recovery point in the repository currently selected by
// the Environment Owner. Holding the controller lock keeps a concurrent
// repository reconfiguration from changing the target midway through a backup.
func (c *RepositoryController) Snapshot(ctx context.Context, reason string) (Manifest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.selection.Path == "" {
		return Manifest{}, fmt.Errorf("%w: no private Catalog repository is connected", domain.ErrConflict)
	}
	return c.scheduler.SnapshotNow(ctx, reason)
}

func (c *RepositoryController) Plan(ctx context.Context, ref string) (RestorePlan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.selection.Path == "" {
		return RestorePlan{}, fmt.Errorf("no private Catalog repository is connected")
	}
	if c.database == nil {
		return RestorePlan{}, fmt.Errorf("live database restore is not configured")
	}
	if err := c.requireRecoveryPointLocked(ctx, ref); err != nil {
		return RestorePlan{}, err
	}
	return c.scheduler.Manager().CurrentDatabaseRestorePlan(ctx, ref, c.database)
}

func (c *RepositoryController) Restore(ctx context.Context, ref, expectedPlanDigest, confirmation string) (RecoveryResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.selection.Path == "" {
		return RecoveryResult{}, fmt.Errorf("no private Catalog repository is connected")
	}
	if c.database == nil {
		return RecoveryResult{}, fmt.Errorf("live database restore is not configured")
	}
	if confirmation != "恢复发布目录" {
		return RecoveryResult{}, fmt.Errorf("type 恢复发布目录 to confirm")
	}
	if err := c.requireRecoveryPointLocked(ctx, ref); err != nil {
		return RecoveryResult{}, err
	}
	result, err := c.scheduler.Manager().RestoreCurrentDatabase(ctx, ref, expectedPlanDigest, c.database, c.base.PlaybookRoot)
	if err != nil {
		return RecoveryResult{}, err
	}
	c.scheduler.Request("catalog-restored-from-git")
	return RecoveryResult{RestorePlan: result, Restored: true}, nil
}

func (c *RepositoryController) requireRecoveryPointLocked(ctx context.Context, ref string) error {
	if _, err := runCommand(ctx, c.selection.ClonePath, "git", "-C", c.selection.ClonePath, "fetch", "--prune", "--tags", "origin"); err != nil {
		return err
	}
	points, err := c.recoveryPoints(ctx, c.selection.ClonePath)
	if err != nil {
		return err
	}
	for _, point := range points {
		if point.Ref == ref {
			return nil
		}
	}
	return fmt.Errorf("recovery point %q is not an available backup tag", ref)
}

func (c *RepositoryController) connectLocked(ctx context.Context, path string) (RepositoryStatus, error) {
	if _, err := runCommand(ctx, path, "git", "-C", path, "rev-parse", "--git-dir"); err != nil {
		return RepositoryStatus{}, fmt.Errorf("path is not a Git repository: %w", err)
	}
	clonePath := filepath.Join(c.base.BackupDir, "repositories", repositoryKey(path))
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(clonePath), 0o700); err != nil {
			return RepositoryStatus{}, err
		}
		if _, err := runCommand(ctx, filepath.Dir(clonePath), "git", "clone", "--no-checkout", path, clonePath); err != nil {
			return RepositoryStatus{}, err
		}
	} else if err != nil {
		return RepositoryStatus{}, err
	}
	if _, err := runCommand(ctx, clonePath, "git", "-C", clonePath, "remote", "set-url", "origin", path); err != nil {
		return RepositoryStatus{}, err
	}
	if _, err := runCommand(ctx, clonePath, "git", "-C", clonePath, "fetch", "--prune", "--tags", "origin"); err != nil {
		return RepositoryStatus{}, err
	}
	if _, err := runCommand(ctx, clonePath, "git", "-C", clonePath, "rev-parse", "--verify", "refs/remotes/origin/"+c.base.CatalogBranch); err != nil {
		return RepositoryStatus{}, fmt.Errorf("repository has no %s branch", c.base.CatalogBranch)
	}
	manager, err := c.managerFor(clonePath)
	if err != nil {
		return RepositoryStatus{}, err
	}
	points, err := c.recoveryPoints(ctx, clonePath)
	if err != nil {
		return RepositoryStatus{}, err
	}
	selection := repositorySelection{Path: path, ClonePath: clonePath}
	if err := writeJSONAtomic(c.statePath, selection, 0o600); err != nil {
		return RepositoryStatus{}, err
	}
	c.selection = selection
	c.scheduler.SetManager(manager)
	c.scheduler.SetEnabled(true)
	health := c.healthLocked(ctx)
	status := RepositoryStatus{
		Configured: true, Path: path, Branch: c.base.CatalogBranch, AllowedRoot: c.allowedRoot, RecoveryPoints: points,
		Behind: health.Behind, CurrentGeneration: health.CurrentGeneration,
		BackedUpGeneration: health.BackedUpGeneration, LastSuccessfulAt: health.LastSuccessfulAt,
		LastError: health.LastError, LastErrorAt: health.LastErrorAt,
	}
	c.applyRestoreTargetStatusLocked(ctx, &status)
	return status, nil
}

func (c *RepositoryController) seedRepository(ctx context.Context, path string) error {
	temp, err := os.MkdirTemp(c.base.BackupDir, ".repository-seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if _, err := c.runCommand(ctx, c.base.BackupDir, "git", "clone", path, temp); err != nil {
		return err
	}
	if _, err := c.runCommand(ctx, temp, "git", "checkout", "--orphan", c.base.CatalogBranch); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temp, ".gitkeep"), nil, 0o600); err != nil {
		return err
	}
	if _, err := c.runCommand(ctx, temp, "git", "add", ".gitkeep"); err != nil {
		return err
	}
	if _, err := c.runCommand(ctx, temp, "git", "-c", "user.name=ClusterForge Backup", "-c", "user.email=backup@clusterforge.local", "commit", "-m", "Initialize private Catalog repository"); err != nil {
		return err
	}
	_, err = c.runCommand(ctx, temp, "git", "push", "origin", c.base.CatalogBranch)
	return err
}

func (c *RepositoryController) managerFor(clonePath string) (*Manager, error) {
	config := c.base
	config.CatalogRepo, config.CatalogRemote = clonePath, "origin"
	return NewManager(config)
}

func (c *RepositoryController) recoveryPoints(ctx context.Context, clonePath string) ([]RecoveryPoint, error) {
	// Fetch --prune --tags does not delete tags removed from the remote. Treat
	// ls-remote as the authority instead of trusting the clone's tag namespace;
	// this also avoids prune-tags deleting a concurrently created local tag
	// before the snapshot worker has pushed it.
	remoteRefs, err := remoteRecoveryPointRefs(ctx, clonePath)
	if err != nil {
		return nil, err
	}
	output, err := runCommand(ctx, clonePath, "git", "-C", clonePath, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)|%(objectname)|%(creatordate:iso-strict)", "refs/tags/backup")
	if err != nil {
		return nil, err
	}
	var points []RecoveryPoint
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		if !remoteRefs[parts[0]] {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339, parts[2])
		if err != nil {
			continue
		}
		points = append(points, RecoveryPoint{Ref: parts[0], Commit: parts[1], CreatedAt: createdAt})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].CreatedAt.After(points[j].CreatedAt) })
	return points, nil
}

func remoteRecoveryPointRefs(ctx context.Context, clonePath string) (map[string]bool, error) {
	output, err := runCommand(ctx, clonePath, "git", "-C", clonePath, "ls-remote", "--refs", "--tags", "origin", "refs/tags/backup/*")
	if err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "refs/tags/backup/") {
			continue
		}
		refs[strings.TrimPrefix(fields[1], "refs/tags/")] = true
	}
	return refs, nil
}

func (c *RepositoryController) resolveExistingPath(input string) (string, error) {
	path, err := c.resolvePath(input)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%w: repository path does not exist", domain.ErrInvalid)
	}
	if !withinRoot(c.allowedRoot, resolved) {
		return "", fmt.Errorf("%w: repository path must be inside %s", domain.ErrInvalid, c.allowedRoot)
	}
	return resolved, nil
}

func (c *RepositoryController) resolveCreatePath(input string) (string, error) {
	path, err := c.resolvePath(input)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(parent)
		if resolveErr == nil {
			if !withinRoot(c.allowedRoot, resolved) {
				return "", fmt.Errorf("%w: repository path must be inside %s", domain.ErrInvalid, c.allowedRoot)
			}
			break
		}
		if filepath.Clean(parent) == c.allowedRoot {
			return "", resolveErr
		}
		parent = filepath.Dir(parent)
	}
	return path, nil
}

func (c *RepositoryController) resolvePath(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("%w: repository path is required", domain.ErrInvalid)
	}
	path := input
	if !filepath.IsAbs(path) {
		cleanRelative := filepath.Clean(path)
		rootWithoutSeparator := strings.TrimPrefix(filepath.Clean(c.allowedRoot), string(filepath.Separator))
		if cleanRelative == rootWithoutSeparator || strings.HasPrefix(cleanRelative, rootWithoutSeparator+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: absolute repository paths must start with %s; otherwise enter only the path relative to %s", domain.ErrInvalid, string(filepath.Separator), c.allowedRoot)
		}
		path = filepath.Join(c.allowedRoot, path)
	}
	path = filepath.Clean(path)
	return path, nil
}

func deepestExistingDirectory(path, root string) (string, error) {
	current := filepath.Clean(path)
	for {
		if !withinRoot(root, current) {
			return "", fmt.Errorf("%w: repository path must be inside %s", domain.ErrInvalid, root)
		}
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("repository parent path is not a directory")
			}
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Clean(current) == filepath.Clean(root) {
			return "", err
		}
		current = filepath.Dir(current)
	}
}

func removeEmptyParents(path, stop, root string) error {
	current := filepath.Clean(path)
	stop = filepath.Clean(stop)
	for current != stop {
		if !withinRoot(root, current) || current == filepath.Clean(root) {
			return fmt.Errorf("refuse to remove parent outside repository root")
		}
		if err := os.Remove(current); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				current = filepath.Dir(current)
				continue
			}
			return err
		}
		current = filepath.Dir(current)
	}
	return nil
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func repositoryKey(path string) string {
	digest := sha256.Sum256([]byte(path))
	return hex.EncodeToString(digest[:8])
}
