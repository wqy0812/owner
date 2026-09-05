package backup

import (
	"codex/platform-demo/internal/runarchive"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type HistoryFile struct {
	RunID     string `json:"runId"`
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}
type HistoryManifest struct {
	FormatVersion  string        `json:"formatVersion"`
	DatabaseSHA256 string        `json:"databaseSha256"`
	Archives       []HistoryFile `json:"archives"`
}

func historyFiles(ctx context.Context, database string) ([]HistoryFile, error) {
	db, err := openReadOnly(database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT run_id,relative_path,sha256,size_bytes FROM run_archive_files ORDER BY run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []HistoryFile{}
	for rows.Next() {
		var f HistoryFile
		if err = rows.Scan(&f.RunID, &f.Name, &f.SHA256, &f.SizeBytes); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
func copyHistoryFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
func copyHistoryArchives(ctx context.Context, database, root, target string) ([]HistoryFile, error) {
	files, err := historyFiles(ctx, database)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(target, 0700); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		path, e := runarchive.Path(root, f.Name)
		if e != nil {
			return nil, e
		}
		if e = copyHistoryFile(path, filepath.Join(target, f.Name)); e != nil {
			return nil, e
		}
		hash, size, e := runarchive.HashFile(filepath.Join(target, f.Name))
		if e != nil || hash != f.SHA256 || size != f.SizeBytes {
			return nil, fmt.Errorf("Run archive %s checksum mismatch", f.RunID)
		}
		if e = runarchive.Verify(filepath.Join(target, f.Name), f.RunID); e != nil {
			return nil, e
		}
	}
	return files, runarchive.SyncDir(target)
}
func verifyHistoryArchives(ctx context.Context, database, root string, files []HistoryFile) error {
	expected, err := historyFiles(ctx, database)
	if err != nil {
		return err
	}
	if files == nil {
		files = []HistoryFile{}
	}
	a, _ := json.Marshal(expected)
	b, _ := json.Marshal(files)
	if string(a) != string(b) {
		return fmt.Errorf("archive manifest does not match database")
	}
	for _, f := range files {
		path, e := runarchive.Path(root, f.Name)
		if e != nil {
			return e
		}
		hash, size, e := runarchive.HashFile(path)
		if e != nil || hash != f.SHA256 || size != f.SizeBytes {
			return fmt.Errorf("Run archive %s checksum mismatch", f.RunID)
		}
		if e = runarchive.Verify(path, f.RunID); e != nil {
			return e
		}
	}
	return nil
}

// SnapshotRunHistory works without Git and only creates a new destination.
func SnapshotRunHistory(ctx context.Context, database, root, target string) (err error) {
	if target == "" {
		return fmt.Errorf("target is required")
	}
	if err = os.Mkdir(target, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(target)
		}
	}()
	db := filepath.Join(target, "platform.db")
	if err = createSQLiteSnapshot(ctx, database, db); err != nil {
		return err
	}
	if err = verifySQLite(ctx, db); err != nil {
		return err
	}
	files, err := copyHistoryArchives(ctx, db, root, filepath.Join(target, "archives"))
	if err != nil {
		return err
	}
	hash, err := sha256File(db)
	if err != nil {
		return err
	}
	if err = writeJSONAtomic(filepath.Join(target, "history-manifest.json"), HistoryManifest{FormatVersion: "clusterforge-run-history-v1", DatabaseSHA256: hash, Archives: files}, 0600); err != nil {
		return err
	}
	return runarchive.SyncDir(target)
}
func VerifyRunHistory(ctx context.Context, root string) error {
	raw, err := os.ReadFile(filepath.Join(root, "history-manifest.json"))
	if err != nil {
		return err
	}
	var m HistoryManifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.FormatVersion != "clusterforge-run-history-v1" {
		return fmt.Errorf("unsupported history manifest")
	}
	db := filepath.Join(root, "platform.db")
	if err = verifySQLite(ctx, db); err != nil {
		return err
	}
	hash, err := sha256File(db)
	if err != nil || hash != m.DatabaseSHA256 {
		return fmt.Errorf("history database checksum mismatch")
	}
	return verifyHistoryArchives(ctx, db, filepath.Join(root, "archives"), m.Archives)
}

// RestoreRunHistory copies an already verified complete history to a new root.
// It never replaces a running instance or rewrites immutable execution data.
func RestoreRunHistory(ctx context.Context, source, target string) (err error) {
	if err = VerifyRunHistory(ctx, source); err != nil {
		return err
	}
	if target == "" {
		return fmt.Errorf("target is required")
	}
	if err = os.Mkdir(target, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(target)
		}
	}()
	if err = copyHistoryFile(filepath.Join(source, "platform.db"), filepath.Join(target, "platform.db")); err != nil {
		return err
	}
	if _, err = copyHistoryArchives(ctx, filepath.Join(target, "platform.db"), filepath.Join(source, "archives"), filepath.Join(target, "archives")); err != nil {
		return err
	}
	if err = copyHistoryFile(filepath.Join(source, "history-manifest.json"), filepath.Join(target, "history-manifest.json")); err != nil {
		return err
	}
	return VerifyRunHistory(ctx, target)
}
