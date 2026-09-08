package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") || strings.Contains(message, "sqlite_busy") || strings.Contains(message, "sqlite_locked")
}

// execWithBusyRetry retries a complete single-statement write on a fresh
// SQLite snapshot. busy_timeout covers ordinary lock contention, while WAL can
// return SQLITE_BUSY_SNAPSHOT immediately when a pooled connection's snapshot
// is no longer writable; retrying the statement is the required recovery.
func (s *Store) execWithBusyRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := withSQLiteBusyRetry(ctx, func() (err error) {
		result, err = s.db.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

// Each attempt must be one complete statement, never an in-progress transaction.
func withSQLiteBusyRetry(ctx context.Context, write func() error) error {
	delays := []time.Duration{0, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	var last error
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err := write()
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) {
			return err
		}
		last = err
	}
	return last
}
