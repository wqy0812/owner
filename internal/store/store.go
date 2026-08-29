package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = "newplatform.db"
	}
	dsn := path
	if path == ":memory:" {
		dsn = fmt.Sprintf("file:newplatform-%d?mode=memory&cache=shared", time.Now().UnixNano())
	} else if !strings.HasPrefix(path, "file:") {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		dsn = "file:" + abs
	}
	// Both foreign_keys and busy_timeout are connection-local SQLite settings.
	// Put them in the DSN so every connection opened by database/sql receives
	// them, rather than only whichever pooled connection executes an initial
	// PRAGMA statement.
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	s := &Store{db: db}
	for _, pragma := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if path != ":memory:" {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := s.InitializeSchema(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) InitializeSchema(ctx context.Context) error {
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect database schema: %w", err)
	}
	if tables > 0 {
		var version string
		if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_contract WHERE id=1`).Scan(&version); err != nil {
			return fmt.Errorf("unsupported database schema: missing schema contract: %w", err)
		}
		if version == previousSchemaContract {
			if err := s.migrateSSHConnectivityChecks(ctx); err != nil {
				return err
			}
			version = schemaContract
		}
		if version != schemaContract {
			return fmt.Errorf("unsupported database schema contract %q: expected %q", version, schemaContract)
		}
	}
	content, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read first-version schema: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(content)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("initialize first-version schema: %w", err)
	}
	return tx.Commit()
}

func (s *Store) migrateSSHConnectivityChecks(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE environment_ssh_checks (
  id TEXT PRIMARY KEY,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  environment_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
  status TEXT NOT NULL CHECK (status IN ('healthy','degraded')),
  duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
  results_json TEXT NOT NULL CHECK (json_valid(results_json)),
  checked_at TEXT NOT NULL
);
CREATE TRIGGER environment_ssh_checks_active_environment_insert
BEFORE INSERT ON environment_ssh_checks
WHEN EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot record SSH checks');
END;
CREATE INDEX idx_environment_ssh_checks_latest
ON environment_ssh_checks(environment_id, checked_at DESC);
`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("migrate database schema from %q: %w", previousSchemaContract, err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE schema_contract SET version=? WHERE id=1 AND version=?`, schemaContract, previousSchemaContract)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("advance database schema contract: %w", err)
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		_ = tx.Rollback()
		return fmt.Errorf("advance database schema contract: updated=%d err=%v", updated, err)
	}
	return tx.Commit()
}

func (s *Store) Reset(ctx context.Context) error {
	tables := []string{"sessions", "component_image_build_logs", "component_image_mirrors", "component_image_builds", "component_artifact_mirrors", "component_release_artifacts", "environment_component_installations", "environment_ssh_checks", "environment_health_checks", "run_input_presets", "run_logs", "run_steps", "approvals", "runs", "notifications", "audit_events", "scenario_revisions", "scenarios", "environment_revisions", "environments", "action_definitions", "component_dependencies", "component_releases", "components", "users"}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS audit_events_no_update; DROP TRIGGER IF EXISTS audit_events_no_delete;`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			tx.Rollback()
			return err
		}
	}
	// Keep the publication fence monotonic across an in-place Demo reset so the
	// backup scheduler cannot mistake a reset Catalog for an older protected
	// generation. A physical database rebuild is reconciled by the guarded
	// deployment workflow after the new service has passed its health gates.
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events BEGIN SELECT RAISE(ABORT, 'audit events are append-only'); END;
CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events BEGIN SELECT RAISE(ABORT, 'audit events are append-only'); END;
`); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func nowText() string             { return time.Now().UTC().Format(time.RFC3339Nano) }
func timeText(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func ptrTimeText(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeText(*t)
}
func parseTime(v string) time.Time { t, _ := time.Parse(time.RFC3339Nano, v); return t }
func parseNullTime(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t := parseTime(v.String)
	return &t
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func decodeJSON[T any](raw string, fallback T) T {
	if raw == "" {
		return fallback
	}
	if err := json.Unmarshal([]byte(raw), &fallback); err != nil {
		return fallback
	}
	return fallback
}

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil && (strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "constraint failed") || strings.Contains(err.Error(), "environment rollback fence conflict")) {
		return fmt.Errorf("%w: %v", domain.ErrConflict, err)
	}
	return err
}

func (s *Store) UpsertUser(ctx context.Context, u domain.User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,role=excluded.role`, u.ID, u.Name, u.Role, timeText(u.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,role,created_at FROM users ORDER BY role,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		var u domain.User
		var created string
		if err := rows.Scan(&u.ID, &u.Name, &u.Role, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = parseTime(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,role,created_at FROM users WHERE id=?`, id).Scan(&u.ID, &u.Name, &u.Role, &created)
	u.CreatedAt = parseTime(created)
	return u, mapSQLError(err)
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)`, tokenHash, userID, nowText(), timeText(expires))
	return mapSQLError(err)
}

func (s *Store) UserBySession(ctx context.Context, tokenHash string) (domain.User, error) {
	var u domain.User
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.name,u.role,u.created_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, tokenHash, nowText()).Scan(&u.ID, &u.Name, &u.Role, &created)
	u.CreatedAt = parseTime(created)
	return u, mapSQLError(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}
