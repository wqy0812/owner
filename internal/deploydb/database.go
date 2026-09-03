// Package deploydb provides database inspection and consistent file snapshots
// for deployment. It never initializes, migrates, or repairs a source database.
package deploydb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite"
)

func openReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = "mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func Contract(ctx context.Context, path string) (string, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var found int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_contract'`).Scan(&found); err != nil {
		return "", err
	}
	if found == 0 {
		return "__missing__", nil
	}
	var version string
	err = db.QueryRowContext(ctx, `SELECT version FROM schema_contract WHERE id=1`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "__missing__", nil
	}
	return version, err
}

func ActiveRuns(ctx context.Context, path string) ([]string, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,status,created_at FROM runs WHERE status IN ('running','queued','awaiting_approval') ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id, status, created string
		if err := rows.Scan(&id, &status, &created); err != nil {
			return nil, err
		}
		result = append(result, id+"\t"+status+"\t"+created)
	}
	return result, rows.Err()
}

func Verify(ctx context.Context, path, expectedContract string) error {
	if expectedContract != "" {
		contract, err := Contract(ctx, path)
		if err != nil {
			return err
		}
		if contract != expectedContract {
			return fmt.Errorf("unexpected schema contract %q: expected %q", contract, expectedContract)
		}
	}
	db, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", integrity)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("SQLite foreign key check failed")
	}
	return rows.Err()
}

func Snapshot(ctx context.Context, source, target string) (err error) {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	db, err := openReadOnly(source)
	if err != nil {
		return err
	}
	defer db.Close()
	// Reserve a new, private destination; never truncate or remove an existing
	// file, including when a caller accidentally specifies the source itself.
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(target)
		}
	}()
	if err = file.Close(); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return fmt.Errorf("snapshot SQLite: %w", err)
	}
	if err = Verify(ctx, target, ""); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err = os.Chown(target, int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
	}
	if err = os.Chmod(target, info.Mode().Perm()); err != nil {
		return err
	}
	file, err = os.Open(target)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
