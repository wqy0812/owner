package runfixture

import (
	"context"
	"database/sql"
)

// CorruptSnapshot is exclusively for tests of damaged persisted records. Normal
// writers cannot rewrite frozen snapshots; tests of that guard use SQL directly.
// Restore the exact trigger inside this transaction before exposing the record.
func CorruptSnapshot(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var trigger string
	if err = tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='trigger' AND name='runs_snapshot_frozen'").Scan(&trigger); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "DROP TRIGGER runs_snapshot_frozen"); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, trigger); err != nil {
		return nil, err
	}
	return result, tx.Commit()
}
