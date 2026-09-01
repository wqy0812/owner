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
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

// execWithBusyRetry retries a complete single-statement write on a fresh
// SQLite snapshot. busy_timeout covers ordinary lock contention, while WAL can
// return SQLITE_BUSY_SNAPSHOT immediately when a pooled connection's snapshot
// is no longer writable; retrying the statement is the required recovery.
func (s *Store) execWithBusyRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	delays := []time.Duration{0, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	var last error
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		result, err := s.db.ExecContext(ctx, query, args...)
		if err == nil {
			return result, nil
		}
		if !isSQLiteBusy(err) {
			return nil, err
		}
		last = err
	}
	return nil, last
}
