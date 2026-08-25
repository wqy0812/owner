package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) CreateNotification(ctx context.Context, n domain.Notification) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO notifications(id,user_id,type,title,body,resource_url,payload_json,read_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, n.ID, n.UserID, n.Type, n.Title, n.Body, n.ResourceURL, jsonText(n.Payload), ptrTimeText(n.ReadAt), timeText(n.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) CreateNotifications(ctx context.Context, notifications []domain.Notification) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range notifications {
		_, err = tx.ExecContext(ctx, `INSERT INTO notifications(id,user_id,type,title,body,resource_url,payload_json,read_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, n.ID, n.UserID, n.Type, n.Title, n.Body, n.ResourceURL, jsonText(n.Payload), ptrTimeText(n.ReadAt), timeText(n.CreatedAt))
		if err != nil {
			return mapSQLError(err)
		}
	}
	return tx.Commit()
}

func scanNotification(row scanner) (domain.Notification, error) {
	var n domain.Notification
	var payload, created string
	var read sql.NullString
	err := row.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &n.ResourceURL, &payload, &read, &created)
	n.Payload = decodeJSON(payload, map[string]any{})
	n.ReadAt = parseNullTime(read)
	n.CreatedAt = parseTime(created)
	return n, err
}

func (s *Store) GetNotification(ctx context.Context, id string) (domain.Notification, error) {
	n, err := scanNotification(s.db.QueryRowContext(ctx, `SELECT id,user_id,type,title,body,resource_url,payload_json,read_at,created_at FROM notifications WHERE id=?`, id))
	return n, mapSQLError(err)
}
func (s *Store) ListNotifications(ctx context.Context, userID string, unreadOnly bool) ([]domain.Notification, error) {
	q := `SELECT id,user_id,type,title,body,resource_url,payload_json,read_at,created_at FROM notifications WHERE user_id=?`
	if unreadOnly {
		q += ` AND read_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) MarkNotificationRead(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at=? WHERE id=? AND user_id=?`, nowText(), id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at=? WHERE user_id=? AND read_at IS NULL`, nowText(), userID)
	return err
}

func (s *Store) AppendAudit(ctx context.Context, e domain.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, e.ID, e.ActorID, e.Action, e.ResourceType, e.ResourceID, jsonText(e.Metadata), timeText(e.CreatedAt))
	return mapSQLError(err)
}
func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,actor_id,action,resource_type,resource_id,metadata_json,created_at FROM audit_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		var metadata, created string
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.ResourceType, &e.ResourceID, &metadata, &created); err != nil {
			return nil, err
		}
		e.Metadata = decodeJSON(metadata, map[string]any{})
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) FirstAuditForResourceAfter(ctx context.Context, resourceType, resourceID string, after time.Time, actions []string) (domain.AuditEvent, error) {
	var event domain.AuditEvent
	if len(actions) == 0 {
		return event, domain.ErrNotFound
	}
	var metadata, created string
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(actions)), ",")
	args := []any{resourceType, resourceID, timeText(after)}
	for _, action := range actions {
		args = append(args, action)
	}
	err := s.db.QueryRowContext(ctx, `
SELECT id,actor_id,action,resource_type,resource_id,metadata_json,created_at
FROM audit_events
WHERE resource_type=? AND resource_id=? AND created_at>? AND action IN (`+placeholders+`)
ORDER BY created_at ASC
LIMIT 1`, args...).Scan(
		&event.ID, &event.ActorID, &event.Action, &event.ResourceType, &event.ResourceID, &metadata, &created,
	)
	if err != nil {
		return event, mapSQLError(err)
	}
	event.Metadata = decodeJSON(metadata, map[string]any{})
	event.CreatedAt = parseTime(created)
	return event, nil
}
