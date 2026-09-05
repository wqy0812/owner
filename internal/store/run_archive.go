package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func validateRunIDs(ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return fmt.Errorf("%w: 每批必须明确选择 1–100 条记录", domain.ErrInvalid)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return fmt.Errorf("%w: Run ID 为空或重复", domain.ErrInvalid)
		}
		seen[id] = true
	}
	return nil
}
func (s *Store) RunRetentionPolicy(ctx context.Context) (domain.RunRetentionPolicy, error) {
	var p domain.RunRetentionPolicy
	err := s.db.QueryRowContext(ctx, `SELECT auto_archive,auto_cleanup,archive_days,cleanup_days FROM run_retention_policy WHERE id=1`).Scan(&p.AutoArchive, &p.AutoCleanup, &p.ArchiveDays, &p.CleanupDays)
	return p, err
}
func (s *Store) SaveRunRetentionPolicy(ctx context.Context, p domain.RunRetentionPolicy, actor string, now time.Time) error {
	if p.ArchiveDays < 1 || p.ArchiveDays > 36500 || p.CleanupDays < 1 || p.CleanupDays > 36500 {
		return fmt.Errorf("%w: 保留天数必须为 1–36500", domain.ErrInvalid)
	}
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE run_retention_policy SET auto_archive=?,auto_cleanup=?,archive_days=?,cleanup_days=? WHERE id=1`, p.AutoArchive, p.AutoCleanup, p.ArchiveDays, p.CleanupDays); err != nil {
		return err
	}
	if err = archiveAudit(ctx, tx, actor, "run.retention_updated", "policy", now, map[string]any{"policy": p}); err != nil {
		return err
	}
	return tx.Commit()
}
func archiveAudit(ctx context.Context, tx *sql.Tx, actor, action, id string, now time.Time, metadata map[string]any) error {
	return appendAuditTx(ctx, tx, domain.AuditEvent{ID: fmt.Sprintf("audit-%s-%s-%d", action, id, now.UnixNano()), ActorID: actor, Action: action, ResourceType: "run", ResourceID: id, Metadata: metadata, CreatedAt: now})
}
func (s *Store) EnqueueRunArchives(ctx context.Context, ids []string, source, actor string, now time.Time) ([]domain.RunArchive, error) {
	if err := validateRunIDs(ids); err != nil {
		return nil, err
	}
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var status string
		var finished sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT status,finished_at FROM runs WHERE id=?`, id).Scan(&status, &finished); err != nil {
			return nil, mapSQLError(err)
		}
		if status != "succeeded" || !finished.Valid {
			return nil, fmt.Errorf("%w: %s 仅已结束成功记录可以归档", domain.ErrConflict, id)
		}
	}
	out := []domain.RunArchive{}
	for _, id := range ids {
		_, err = tx.ExecContext(ctx, `INSERT INTO run_archive_tasks(run_id,status,source,actor_id,updated_at) VALUES(?,'queued',?,?,?) ON CONFLICT(run_id) DO UPDATE SET status='queued',source=excluded.source,actor_id=excluded.actor_id,updated_at=excluded.updated_at,error_text='',lease_until=NULL,lease_token='' WHERE run_archive_tasks.status='failed'`, id, source, actor, timeText(now))
		if err != nil {
			return nil, err
		}
		item, err := readRunArchive(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, tx.Commit()
}

const archiveSelect = `SELECT t.run_id,t.status,t.source,t.actor_id,t.error_text,t.updated_at,t.lease_token,f.archived_at,COALESCE(f.size_bytes,0),COALESCE(f.sha256,''),COALESCE(f.relative_path,''),COALESCE(f.format_version,'') FROM run_archive_tasks t LEFT JOIN run_archive_files f ON f.run_id=t.run_id`

func scanArchive(row scanner) (domain.RunArchive, error) {
	var a domain.RunArchive
	var updated string
	var archived sql.NullString
	err := row.Scan(&a.RunID, &a.Status, &a.Source, &a.ActorID, &a.Error, &updated, &a.LeaseToken, &archived, &a.SizeBytes, &a.SHA256, &a.RelativePath, &a.FormatVersion)
	a.UpdatedAt = parseTime(updated)
	a.ArchivedAt = parseNullTime(archived)
	return a, mapSQLError(err)
}
func readRunArchive(ctx context.Context, q queryer, id string) (domain.RunArchive, error) {
	return scanArchive(q.QueryRowContext(ctx, archiveSelect+` WHERE t.run_id=?`, id))
}
func (s *Store) GetRunArchive(ctx context.Context, id string) (domain.RunArchive, error) {
	return readRunArchive(ctx, s.db, id)
}
func (s *Store) ClaimRunArchive(ctx context.Context, token string, now time.Time) (domain.RunArchive, error) {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return domain.RunArchive{}, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM run_archive_tasks WHERE (status='queued' OR (status='running' AND lease_until<=?)) AND NOT EXISTS(SELECT 1 FROM run_archive_tasks WHERE status='running' AND lease_until>?) ORDER BY updated_at,run_id LIMIT 1`, timeText(now), timeText(now)).Scan(&id)
	if err != nil {
		return domain.RunArchive{}, mapSQLError(err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE run_archive_tasks SET status='running',lease_token=?,lease_until=?,updated_at=? WHERE run_id=?`, token, timeText(now.Add(5*time.Minute)), timeText(now), id)
	if err != nil {
		return domain.RunArchive{}, err
	}
	a, err := readRunArchive(ctx, tx, id)
	if err != nil {
		return a, err
	}
	return a, tx.Commit()
}
func (s *Store) RenewRunArchive(ctx context.Context, id, token string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE run_archive_tasks SET lease_until=? WHERE run_id=? AND status='running' AND lease_token=? AND lease_until>?`, timeText(now.Add(5*time.Minute)), id, token, timeText(now))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (s *Store) FailRunArchive(ctx context.Context, id, token, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE run_archive_tasks SET status='failed',error_text=?,updated_at=?,lease_until=NULL WHERE run_id=? AND lease_token=? AND status='running'`, message, timeText(now), id, token)
	return err
}

type ArchiveBoundary struct {
	Digest   string
	LogCount int64
}

// archiveRecords streams deterministic records from one snapshot. The source
// digest covers raw data; the export callback applies the normal redaction rule.
func archiveRecords(ctx context.Context, q queryer, id string, write func(string, []byte) error) (ArchiveBoundary, error) {
	var boundary ArchiveBoundary
	run, err := getRunRecord(ctx, q, id)
	if err != nil {
		return boundary, err
	}
	if run.Status != domain.RunSucceeded || run.FinishedAt == nil {
		return boundary, domain.ErrConflict
	}
	h := sha256.New()
	emit := func(name string, v any) error {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{'\n'})
		if write != nil {
			return write(name, b)
		}
		return nil
	}
	if err = emit("run.json", run); err != nil {
		return boundary, err
	}
	// Typed row maps preserve every column, including future optional approval fields.
	for _, table := range []string{"run_steps", "approvals"} {
		rows, e := q.QueryContext(ctx, "SELECT * FROM "+table+" WHERE run_id=? ORDER BY rowid", id)
		if e != nil {
			return boundary, e
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return boundary, e
		}
		records := []map[string]any{}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if e = rows.Scan(ptrs...); e != nil {
				rows.Close()
				return boundary, e
			}
			m := map[string]any{}
			for i, c := range cols {
				if b, ok := vals[i].([]byte); ok {
					m[c] = string(b)
				} else {
					m[c] = vals[i]
				}
			}
			records = append(records, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return boundary, e
		}
		if e = emit(table+".json", records); e != nil {
			return boundary, e
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT id,run_id,COALESCE(step_id,''),stream,message,created_at FROM run_logs WHERE run_id=? ORDER BY id`, id)
	if err != nil {
		return boundary, err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.RunLog
		var created string
		if err = rows.Scan(&l.ID, &l.RunID, &l.StepID, &l.Stream, &l.Message, &created); err != nil {
			return boundary, err
		}
		l.CreatedAt = parseTime(created)
		if err = emit("logs.ndjson", l); err != nil {
			return boundary, err
		}
		boundary.LogCount++
	}
	if err = rows.Err(); err != nil {
		return boundary, err
	}
	boundary.Digest = hex.EncodeToString(h.Sum(nil))
	return boundary, nil
}
func (s *Store) ExportRunArchive(ctx context.Context, id string, write func(string, []byte) error) (ArchiveBoundary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ArchiveBoundary{}, err
	}
	defer tx.Rollback()
	return archiveRecords(ctx, tx, id, write)
}
func (s *Store) CompleteRunArchive(ctx context.Context, a domain.RunArchive, b ArchiveBoundary, now time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_archive_tasks WHERE run_id=? AND status='running' AND lease_token=? AND lease_until>?`, a.RunID, a.LeaseToken, timeText(now)).Scan(&valid); err != nil {
		return err
	}
	if valid != 1 {
		return domain.ErrConflict
	}
	current, err := archiveRecords(ctx, tx, a.RunID, nil)
	if err != nil {
		return err
	}
	if current != b {
		return fmt.Errorf("%w: 归档期间运行信息或日志发生变化", domain.ErrConflict)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run_archive_files(run_id,relative_path,format_version,size_bytes,sha256,source_digest,log_count,archived_at) VALUES(?,?,?,?,?,?,?,?)`, a.RunID, a.RelativePath, a.FormatVersion, a.SizeBytes, a.SHA256, b.Digest, b.LogCount, timeText(now))
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE run_archive_tasks SET status='archived',updated_at=?,lease_until=NULL,error_text='' WHERE run_id=? AND lease_token=?`, timeText(now), a.RunID, a.LeaseToken); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM run_logs WHERE run_id=?`, a.RunID); err != nil {
		return err
	}
	if err = archiveAudit(ctx, tx, a.ActorID, "run.archived", a.RunID, now, map[string]any{"sha256": a.SHA256, "sizeBytes": a.SizeBytes, "logCount": b.LogCount, "source": a.Source}); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RunArchiveHealth(ctx context.Context) (domain.RunArchiveHealth, error) {
	h := domain.RunArchiveHealth{Tasks: []domain.RunArchive{}, CleanupResults: []domain.RunRetentionResult{}}
	var err error
	h.Policy, err = s.RunRetentionPolicy(ctx)
	if err != nil {
		return h, err
	}
	var scan sql.NullString
	if err = s.db.QueryRowContext(ctx, `SELECT last_scan_at,last_scan_result FROM run_retention_policy WHERE id=1`).Scan(&scan, &h.LastScanResult); err != nil {
		return h, err
	}
	h.LastScanAt = parseNullTime(scan)
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_archive_tasks WHERE status IN ('queued','running')`).Scan(&h.Pending); err != nil {
		return h, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0) FROM run_archive_files`).Scan(&h.SizeBytes); err != nil {
		return h, err
	}
	rows, err := s.db.QueryContext(ctx, archiveSelect+` ORDER BY t.updated_at DESC LIMIT 100`)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		a, e := scanArchive(rows)
		if e != nil {
			return h, e
		}
		h.Tasks = append(h.Tasks, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return h, err
	}
	rows.Close()
	results, err := s.db.QueryContext(ctx, `SELECT resource_id,action,metadata_json,created_at FROM audit_events WHERE action IN ('run.cleaned','run.cleanup_skipped','run.cleanup_failed') ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return h, err
	}
	defer results.Close()
	for results.Next() {
		var row domain.RunRetentionResult
		var action, raw, at string
		if err = results.Scan(&row.RunID, &action, &raw, &at); err != nil {
			return h, err
		}
		row.At = parseTime(at)
		metadata := decodeJSON(raw, map[string]any{})
		row.Reason, _ = metadata["reason"].(string)
		switch action {
		case "run.cleaned":
			row.Status = "cleaned"
			row.Reason = "已按保留策略清理"
		case "run.cleanup_skipped":
			row.Status = "skipped"
		case "run.cleanup_failed":
			row.Status = "failed"
		}
		h.CleanupResults = append(h.CleanupResults, row)
	}
	return h, results.Err()
}
func (s *Store) RetentionCandidates(ctx context.Context, status string, before time.Time, afterTime, afterID string, limit int) ([]string, string, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,finished_at FROM runs WHERE status=? AND finished_at IS NOT NULL AND finished_at<? AND (finished_at>? OR (finished_at=? AND id>?)) AND NOT EXISTS(SELECT 1 FROM run_archive_files f WHERE f.run_id=runs.id) ORDER BY finished_at,id LIMIT ?`, status, timeText(before), afterTime, afterTime, afterID, limit)
	if err != nil {
		return nil, "", "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id, finished string
		if err = rows.Scan(&id, &finished); err != nil {
			return nil, "", "", err
		}
		ids = append(ids, id)
		afterTime, afterID = finished, id
	}
	return ids, afterTime, afterID, rows.Err()
}
func (s *Store) RecordRetentionScan(ctx context.Context, now time.Time, result string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE run_retention_policy SET last_scan_at=?,last_scan_result=? WHERE id=1`, timeText(now), result)
	return err
}
func isMissingArchive(err error) bool { return errors.Is(err, domain.ErrNotFound) }
func (s *Store) RetentionCursor(ctx context.Context, status string) (string, string, error) {
	var at, id string
	err := s.db.QueryRowContext(ctx, `SELECT finished_at,run_id FROM run_retention_cursors WHERE status=?`, status).Scan(&at, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return at, id, err
}
func (s *Store) SaveRetentionCursor(ctx context.Context, status, at, id string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_retention_cursors(status,finished_at,run_id) VALUES(?,?,?) ON CONFLICT(status) DO UPDATE SET finished_at=excluded.finished_at,run_id=excluded.run_id`, status, at, id)
	return err
}
func (s *Store) ProtectedArchivePaths(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT relative_path FROM run_archive_files UNION ALL SELECT lease_token||char(0)||run_id FROM run_archive_tasks WHERE status='running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			return nil, err
		}
		out[path] = true
	}
	return out, rows.Err()
}
func (s *Store) RunArchiveBytes(ctx context.Context, id string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(message AS BLOB))+512),0)+(SELECT length(input_snapshot_json) FROM runs WHERE id=?) FROM run_logs WHERE run_id=?`, id, id).Scan(&n)
	return n, err
}
