package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"codex/platform-demo/internal/domain"
)

type RunLogSnapshot struct {
	Run        domain.Run         `json:"run"`
	CapturedAt time.Time          `json:"capturedAt"`
	LastLogID  int64              `json:"lastLogId"`
	LogCount   int64              `json:"logCount"`
	Archive    *domain.RunArchive `json:"-"`
}

// StreamRunLogSnapshot binds the run, steps, archive decision and log boundary
// to one read transaction. Export never holds the snapshot during HTTP delivery.
func (s *Store) StreamRunLogSnapshot(ctx context.Context, id string, visit func(domain.RunLog) error) (out RunLogSnapshot, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Run, err = getRunRecord(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.CapturedAt = time.Now().UTC()
	out.Run.Steps, err = listRunSteps(ctx, tx, id)
	if err != nil {
		return out, err
	}
	a, err := readRunArchive(ctx, tx, id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && a.Status == "archived" {
		out.Archive = &a
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,COALESCE(step_id,''),stream,message,created_at FROM run_logs WHERE run_id=? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.RunLog
		var created string
		if err = rows.Scan(&l.ID, &l.RunID, &l.StepID, &l.Stream, &l.Message, &created); err != nil {
			return out, err
		}
		l.CreatedAt = parseTime(created)
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if err = visit(l); err != nil {
			return out, err
		}
		out.LastLogID = l.ID
		out.LogCount++
	}
	return out, rows.Err()
}
