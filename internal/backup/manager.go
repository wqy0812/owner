package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Manager struct {
	config Config
	now    func() time.Time
}

func NewManager(config Config) (*Manager, error) {
	if config.CatalogRemote == "" {
		config.CatalogRemote = "origin"
	}
	if config.CatalogBranch == "" {
		config.CatalogBranch = "catalog"
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Manager{config: config, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (m *Manager) Config() Config { return m.config }

func (m *Manager) Snapshot(ctx context.Context, reason string) (manifest Manifest, err error) {
	if strings.TrimSpace(reason) == "" {
		reason = "manual"
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > 200 {
		reason = reason[:200]
	}
	unlock, err := m.lock()
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	createdAt := m.now()
	backupID := createdAt.Format("20060102T150405.000000000Z") + "-" + strconv.Itoa(os.Getpid())
	backupPath := filepath.Join(m.config.BackupDir, backupID)
	if err := os.MkdirAll(backupPath, 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create backup directory: %w", err)
	}
	manifest = Manifest{
		FormatVersion: CatalogFormatVersion, BackupID: backupID, Status: StatusPartial,
		Reason: reason, CreatedAt: createdAt, DatabaseFile: "platform.db", Counts: map[string]int{},
	}
	defer func() {
		if err != nil {
			manifest.Status = StatusPartial
			manifest.Error = err.Error()
			_ = m.writeManifest(backupPath, manifest)
		}
	}()
	if err = m.writeManifest(backupPath, manifest); err != nil {
		return manifest, err
	}
	snapshotPath := filepath.Join(backupPath, manifest.DatabaseFile)
	if err = createSQLiteSnapshot(ctx, m.config.DatabasePath, snapshotPath); err != nil {
		return manifest, err
	}
	if err = verifySQLite(ctx, snapshotPath); err != nil {
		return manifest, err
	}
	manifest.DatabaseSHA256, err = sha256File(snapshotPath)
	if err != nil {
		return manifest, err
	}
	worktree, cleanup, err := m.catalogWorktree(ctx, backupID)
	if err != nil {
		return manifest, err
	}
	defer cleanup()
	if err = cleanCatalogWorktree(worktree); err != nil {
		return manifest, err
	}
	catalog, catalogBytes, err := ExportCatalog(ctx, snapshotPath, m.config.PlaybookRoot, worktree)
	if err != nil {
		return manifest, err
	}
	manifest.SchemaContract = catalog.SchemaContract
	manifest.PublicationGeneration = catalog.PublicationGeneration
	manifest.Counts = catalogCounts(catalog)
	manifest.CatalogSHA256, err = CatalogDigest(catalogBytes, worktree, catalog.Playbooks)
	if err != nil {
		return manifest, err
	}
	metadata := SnapshotMetadata{
		FormatVersion: CatalogFormatVersion, BackupID: backupID, CreatedAt: createdAt, Reason: reason,
		DatabaseSHA256: manifest.DatabaseSHA256, SchemaContract: manifest.SchemaContract,
		PublicationGeneration: manifest.PublicationGeneration, Counts: manifest.Counts,
	}
	if err = writeJSONAtomic(filepath.Join(worktree, "snapshot.json"), metadata, 0o600); err != nil {
		return manifest, err
	}
	manifest.GitTag = "backup/" + backupID
	manifest.GitCommit, err = m.commitCatalog(ctx, worktree, manifest)
	if err != nil {
		return manifest, err
	}
	if err = m.pushCatalog(ctx, manifest); err != nil {
		return manifest, err
	}
	completed := m.now()
	manifest.Status, manifest.CompletedAt, manifest.Error = StatusSuccess, &completed, ""
	if err = m.writeManifest(backupPath, manifest); err != nil {
		return manifest, err
	}
	if err = m.writeLatestSuccessful(manifest); err != nil {
		return manifest, err
	}
	_ = m.pruneLocalSnapshots()
	return manifest, nil
}

func (m *Manager) Resume(ctx context.Context, backupID string) (Manifest, error) {
	if err := validateBackupID(backupID); err != nil {
		return Manifest{}, err
	}
	unlock, err := m.lock()
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	backupPath := filepath.Join(m.config.BackupDir, filepath.Base(backupID))
	manifest, err := readManifest(filepath.Join(backupPath, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	if manifest.BackupID != backupID || manifest.GitCommit == "" || manifest.GitTag == "" {
		return manifest, fmt.Errorf("backup %s has no resumable Git commit", backupID)
	}
	if err := m.pushCatalog(ctx, manifest); err != nil {
		manifest.Error = err.Error()
		_ = m.writeManifest(backupPath, manifest)
		return manifest, err
	}
	completed := m.now()
	manifest.Status, manifest.CompletedAt, manifest.Error = StatusSuccess, &completed, ""
	if err := m.writeManifest(backupPath, manifest); err != nil {
		return manifest, err
	}
	if err := m.writeLatestSuccessful(manifest); err != nil {
		return manifest, err
	}
	_ = m.pruneLocalSnapshots()
	return manifest, nil
}

func (m *Manager) LatestSuccessful() (Manifest, error) {
	return readManifest(m.latestSuccessfulPath())
}

func (m *Manager) latestSuccessfulPath() string {
	identity := m.config.CatalogRepo + "\x00" + m.config.CatalogRemote + "\x00" + m.config.CatalogBranch
	return filepath.Join(m.config.BackupDir, "latest-successful-"+repositoryKey(identity)+".json")
}

func (m *Manager) writeLatestSuccessful(manifest Manifest) error {
	if err := writeJSONAtomic(m.latestSuccessfulPath(), manifest, 0o600); err != nil {
		return err
	}
	// Keep the unscoped pointer as an operator-friendly record only. Catch-up
	// must never compare one repository against another repository's snapshot.
	return writeJSONAtomic(filepath.Join(m.config.BackupDir, "latest-successful.json"), manifest, 0o600)
}

func (m *Manager) List() ([]Manifest, error) {
	entries, err := os.ReadDir(m.config.BackupDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifests []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := readManifest(filepath.Join(m.config.BackupDir, entry.Name(), "manifest.json"))
		if err == nil {
			manifests = append(manifests, manifest)
		}
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].CreatedAt.After(manifests[j].CreatedAt) })
	return manifests, nil
}

func (m *Manager) NeedsSnapshot(ctx context.Context) (bool, int64, error) {
	database, err := openReadOnly(m.config.DatabasePath)
	if err != nil {
		return false, 0, err
	}
	defer database.Close()
	var generation int64
	var schemaContract string
	if err := database.QueryRowContext(ctx, `SELECT (SELECT generation FROM publication_state WHERE id=1),(SELECT version FROM schema_contract WHERE id=1)`).Scan(&generation, &schemaContract); err != nil {
		return false, 0, err
	}
	latest, err := m.LatestSuccessful()
	if errors.Is(err, os.ErrNotExist) {
		return true, generation, nil
	}
	if err != nil {
		return false, generation, err
	}
	// Publication generations are monotonic during ordinary operation, but a
	// guarded database rebuild starts a new database at a lower generation.
	// A database replacement can also change the contract. Either mismatch
	// therefore needs a fresh
	// recovery point before the current database can be restored through the UI.
	return generation != latest.PublicationGeneration || schemaContract != latest.SchemaContract, generation, nil
}

func (m *Manager) Verify(ctx context.Context, backupID string, verifyGit bool) (Manifest, error) {
	if err := validateBackupID(backupID); err != nil {
		return Manifest{}, err
	}
	manifest, err := readManifest(filepath.Join(m.config.BackupDir, filepath.Base(backupID), "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Status != StatusSuccess {
		return manifest, fmt.Errorf("backup %s is not complete", backupID)
	}
	databasePath := filepath.Join(m.config.BackupDir, filepath.Base(backupID), manifest.DatabaseFile)
	if err := verifySQLite(ctx, databasePath); err != nil {
		return manifest, err
	}
	digest, err := sha256File(databasePath)
	if err != nil || digest != manifest.DatabaseSHA256 {
		return manifest, fmt.Errorf("database SHA-256 mismatch: got %s, expected %s", digest, manifest.DatabaseSHA256)
	}
	if verifyGit {
		contents, err := m.gitShow(ctx, manifest.GitCommit, "snapshot.json")
		if err != nil {
			return manifest, err
		}
		var metadata SnapshotMetadata
		if err := json.Unmarshal(contents, &metadata); err != nil {
			return manifest, err
		}
		if metadata.BackupID != manifest.BackupID || metadata.DatabaseSHA256 != manifest.DatabaseSHA256 {
			return manifest, fmt.Errorf("Git snapshot metadata does not match backup manifest")
		}
	}
	return manifest, nil
}

func (m *Manager) lock() (func(), error) {
	if err := os.MkdirAll(m.config.BackupDir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(m.config.BackupDir, ".backup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another ClusterForge backup is already running")
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

func (m *Manager) writeManifest(backupPath string, manifest Manifest) error {
	return writeJSONAtomic(filepath.Join(backupPath, "manifest.json"), manifest, 0o600)
}

func readManifest(path string) (Manifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if manifest.FormatVersion != CatalogFormatVersion {
		return Manifest{}, fmt.Errorf("unsupported backup manifest format %q", manifest.FormatVersion)
	}
	return manifest, nil
}

func createSQLiteSnapshot(ctx context.Context, source, target string) error {
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("snapshot target already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	database, err := sql.Open("sqlite", "file:"+absSource+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer database.Close()
	escaped := strings.ReplaceAll(target, "'", "''")
	if _, err := database.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return fmt.Errorf("create SQLite snapshot: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func verifySQLite(ctx context.Context, path string) error {
	database, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity_check returned %q", integrity)
	}
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("SQLite foreign_key_check reported a violation")
	}
	return rows.Err()
}

func cleanCatalogWorktree(root string) error {
	for _, name := range []string{"catalog.json", "snapshot.json", "playbooks"} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) catalogWorktree(ctx context.Context, backupID string) (string, func(), error) {
	if _, err := os.Stat(filepath.Join(m.config.CatalogRepo, ".git")); err != nil {
		return "", nil, fmt.Errorf("Catalog repository is not initialized at %s", m.config.CatalogRepo)
	}
	if _, err := m.git(ctx, "fetch", "--prune", m.config.CatalogRemote, m.config.CatalogBranch); err != nil {
		return "", nil, err
	}
	worktree, err := os.MkdirTemp(m.config.BackupDir, ".catalog-worktree-"+backupID+"-")
	if err != nil {
		return "", nil, err
	}
	_ = os.Remove(worktree)
	ref := "refs/remotes/" + m.config.CatalogRemote + "/" + m.config.CatalogBranch
	if _, err := m.git(ctx, "worktree", "add", "--detach", worktree, ref); err != nil {
		os.RemoveAll(worktree)
		return "", nil, err
	}
	cleanup := func() {
		_, _ = m.git(context.Background(), "worktree", "remove", "--force", worktree)
		_ = os.RemoveAll(worktree)
	}
	return worktree, cleanup, nil
}

func (m *Manager) commitCatalog(ctx context.Context, worktree string, manifest Manifest) (string, error) {
	if _, err := runCommand(ctx, worktree, "git", "add", "-A"); err != nil {
		return "", err
	}
	message := fmt.Sprintf("backup: %s (%s)", manifest.BackupID, manifest.Reason)
	if _, err := runCommand(ctx, worktree, "git", "-c", "user.name=ClusterForge Backup", "-c", "user.email=backup@clusterforge.local", "commit", "-m", message); err != nil {
		return "", err
	}
	commit, err := runCommand(ctx, worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	ref := "refs/clusterforge-backups/" + manifest.BackupID
	if _, err := m.git(ctx, "update-ref", ref, commit); err != nil {
		return "", err
	}
	if _, err := m.git(ctx, "-c", "user.name=ClusterForge Backup", "-c", "user.email=backup@clusterforge.local", "tag", "-a", manifest.GitTag, "-m", message, commit); err != nil {
		return "", err
	}
	return commit, nil
}

func (m *Manager) pushCatalog(ctx context.Context, manifest Manifest) error {
	branchRef := manifest.GitCommit + ":refs/heads/" + m.config.CatalogBranch
	if _, err := m.git(ctx, "push", m.config.CatalogRemote, branchRef); err != nil {
		return err
	}
	if _, err := m.git(ctx, "push", m.config.CatalogRemote, "refs/tags/"+manifest.GitTag); err != nil {
		return err
	}
	return nil
}

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	return runCommand(ctx, m.config.CatalogRepo, "git", append([]string{"-C", m.config.CatalogRepo}, args...)...)
}

func (m *Manager) gitShow(ctx context.Context, commit, path string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	process := exec.CommandContext(ctx, "git", "-C", m.config.CatalogRepo, "show", commit+":"+filepath.ToSlash(path))
	process.Stdout, process.Stderr = &stdout, &stderr
	if err := process.Run(); err != nil {
		return nil, fmt.Errorf("git show %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func runCommand(ctx context.Context, directory, command string, args ...string) (string, error) {
	process := exec.CommandContext(ctx, command, args...)
	process.Dir = directory
	output, err := process.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", command, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func safeRemoveTree(path, root string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refuse to remove path outside backup root")
	}
	return os.RemoveAll(pathAbs)
}

func (m *Manager) pruneLocalSnapshots() error {
	manifests, err := m.List()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	days, weeks := map[string]bool{}, map[string]bool{}
	dailyCount, weeklyCount := 0, 0
	cutoff := m.now().Add(-24 * time.Hour)
	partialCutoff := m.now().Add(-7 * 24 * time.Hour)
	for _, manifest := range manifests {
		if manifest.Status == StatusPartial {
			if manifest.CreatedAt.After(partialCutoff) {
				keep[manifest.BackupID] = true
			}
			continue
		}
		if manifest.CreatedAt.After(cutoff) {
			keep[manifest.BackupID] = true
		}
		day := manifest.CreatedAt.UTC().Format("2006-01-02")
		if !days[day] && dailyCount < 14 {
			days[day], keep[manifest.BackupID] = true, true
			dailyCount++
		}
		year, week := manifest.CreatedAt.UTC().ISOWeek()
		weekKey := fmt.Sprintf("%04d-%02d", year, week)
		if !weeks[weekKey] && weeklyCount < 8 {
			weeks[weekKey], keep[manifest.BackupID] = true, true
			weeklyCount++
		}
	}
	for _, manifest := range manifests {
		if keep[manifest.BackupID] {
			continue
		}
		if err := safeRemoveTree(filepath.Join(m.config.BackupDir, manifest.BackupID), m.config.BackupDir); err != nil {
			return err
		}
	}
	return nil
}
