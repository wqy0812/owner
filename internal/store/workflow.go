package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) CreateWorkflowSession(ctx context.Context, value domain.WorkflowSession) (domain.WorkflowSession, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflow_sessions(id,kind,owner_id,request_key,request_digest,status,version,input_json,output_json,created_at,updated_at) VALUES(?,?,?,?,?,?,1,?,?,?,?) ON CONFLICT(owner_id,kind,request_key) DO NOTHING`, value.ID, value.Kind, value.OwnerID, value.RequestKey, value.RequestDigest, value.Status, string(value.Input), string(value.Output), timeText(value.CreatedAt), timeText(value.UpdatedAt))
	if err != nil {
		return value, err
	}
	found, err := scanWorkflow(s.db.QueryRowContext(ctx, `SELECT id,kind,owner_id,request_key,request_digest,status,version,input_json,output_json,created_at,updated_at FROM workflow_sessions WHERE owner_id=? AND kind=? AND request_key=?`, value.OwnerID, value.Kind, value.RequestKey))
	if err == nil && found.RequestDigest != value.RequestDigest {
		return found, fmt.Errorf("%w: request key was already used for different input", domain.ErrConflict)
	}
	return found, err
}
func scanWorkflow(row scanner) (domain.WorkflowSession, error) {
	var v domain.WorkflowSession
	var input, output, created, updated string
	err := row.Scan(&v.ID, &v.Kind, &v.OwnerID, &v.RequestKey, &v.RequestDigest, &v.Status, &v.Version, &input, &output, &created, &updated)
	if err == sql.ErrNoRows {
		return v, domain.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.Input = []byte(input)
	v.Output = []byte(output)
	v.CreatedAt = parseTime(created)
	v.UpdatedAt = parseTime(updated)
	return v, nil
}
func (s *Store) GetWorkflowSession(ctx context.Context, id string) (domain.WorkflowSession, error) {
	return scanWorkflow(s.db.QueryRowContext(ctx, `SELECT id,kind,owner_id,request_key,request_digest,status,version,input_json,output_json,created_at,updated_at FROM workflow_sessions WHERE id=?`, id))
}
func (s *Store) ListWorkflowSessions(ctx context.Context, kind, userID string) ([]domain.WorkflowSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,owner_id,request_key,request_digest,status,version,input_json,output_json,created_at,updated_at FROM workflow_sessions WHERE kind=? AND owner_id=? ORDER BY updated_at DESC LIMIT 200`, kind, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.WorkflowSession{}
	for rows.Next() {
		v, e := scanWorkflow(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateWorkflowSession(ctx context.Context, v domain.WorkflowSession, expected int64) (domain.WorkflowSession, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_sessions SET status=?,input_json=?,output_json=?,updated_at=?,version=version+1 WHERE id=? AND version=?`, v.Status, string(v.Input), string(v.Output), timeText(time.Now().UTC()), v.ID, expected)
	if err != nil {
		return v, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return v, fmt.Errorf("%w: workflow changed; refresh it", domain.ErrConflict)
	}
	return s.GetWorkflowSession(ctx, v.ID)
}
func (s *Store) InterruptPreparations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE workflow_sessions SET status='interrupted',version=version+1,updated_at=? WHERE kind='preparation' AND status IN ('queued','running')`, timeText(time.Now().UTC()))
	return err
}
