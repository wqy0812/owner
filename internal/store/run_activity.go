package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"codex/platform-demo/internal/domain"
)

type RunActivity struct {
	RunID               string            `json:"runId"`
	Status              domain.RunStatus  `json:"status"`
	Logs                []domain.RunLog   `json:"logs"`
	NextAfterID         int64             `json:"nextAfterId"`
	HasMore             bool              `json:"hasMore"`
	WaitingObservations []json.RawMessage `json:"waitingObservations"`
	Archived            bool              `json:"archived"`
}

// The first read fixes status, log cursor, archive state and waits to one WAL
// snapshot. No execution snapshot, step history or full log scan is needed.
func (s *Store) ReadRunActivity(ctx context.Context, id string, afterID *int64, limit int) (out RunActivity, err error) {
	out = RunActivity{RunID: id, Logs: []domain.RunLog{}, WaitingObservations: []json.RawMessage{}}
	if afterID != nil && *afterID < 0 || limit < 1 || limit > 2000 {
		return out, fmt.Errorf("%w: invalid activity cursor or limit", domain.ErrInvalid)
	}
	if afterID != nil {
		out.NextAfterID = *afterID
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT status,EXISTS(SELECT 1 FROM run_archive_files WHERE run_id=runs.id) FROM runs WHERE id=?`, id).Scan(&out.Status, &out.Archived)
	if err != nil {
		return out, mapSQLError(err)
	}
	if out.Archived {
		return out, tx.Commit()
	}
	query, args := `SELECT id,run_id,step_id,stream,message,created_at FROM run_logs WHERE run_id=? ORDER BY id DESC LIMIT 200`, []any{id}
	if afterID != nil {
		query, args = `SELECT id,run_id,step_id,stream,message,created_at FROM run_logs WHERE run_id=? AND id>? ORDER BY id LIMIT ?`, []any{id, *afterID, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item domain.RunLog
		var created string
		if err = rows.Scan(&item.ID, &item.RunID, &item.StepID, &item.Stream, &item.Message, &created); err != nil {
			rows.Close()
			return out, err
		}
		item.CreatedAt = parseTime(created)
		out.Logs = append(out.Logs, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if afterID == nil {
		slices.Reverse(out.Logs)
	} else if len(out.Logs) > limit {
		out.HasMore = true
		out.Logs = out.Logs[:limit]
	}
	if len(out.Logs) > 0 {
		out.NextAfterID = out.Logs[len(out.Logs)-1].ID
	}
	if out.Status == domain.RunRunning {
		out.WaitingObservations, err = listRunWaitingObservations(ctx, tx, id)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}
