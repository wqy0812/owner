package store

import (
	"context"
	"database/sql"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) CreateComponentImageBuild(ctx context.Context, build domain.ComponentImageBuild) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO component_image_builds(id,release_id,environment_id,environment_revision_id,requested_by,status,dockerfile_sha256,image_tag,image_ref,image_digest,error_text,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		build.ID, build.ReleaseID, nullString(build.EnvironmentID), nullString(build.EnvironmentRevisionID), build.RequestedBy, build.Status, build.DockerfileSHA256, build.ImageTag, build.ImageRef, build.ImageDigest, build.Error, timeText(build.CreatedAt), ptrTimeText(build.StartedAt), ptrTimeText(build.FinishedAt))
	return mapSQLError(err)
}

func (s *Store) GetComponentImageBuild(ctx context.Context, id string) (domain.ComponentImageBuild, error) {
	var build domain.ComponentImageBuild
	var created string
	var environmentID, environmentRevisionID, started, finished sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,release_id,environment_id,environment_revision_id,requested_by,status,dockerfile_sha256,image_tag,image_ref,image_digest,error_text,created_at,started_at,finished_at FROM component_image_builds WHERE id=?`, id).Scan(
		&build.ID, &build.ReleaseID, &environmentID, &environmentRevisionID, &build.RequestedBy, &build.Status, &build.DockerfileSHA256, &build.ImageTag, &build.ImageRef, &build.ImageDigest, &build.Error, &created, &started, &finished)
	if err != nil {
		return build, mapSQLError(err)
	}
	build.CreatedAt = parseTime(created)
	build.EnvironmentID = environmentID.String
	build.EnvironmentRevisionID = environmentRevisionID.String
	build.StartedAt = parseNullTime(started)
	build.FinishedAt = parseNullTime(finished)
	build.Logs, err = s.ListComponentImageBuildLogs(ctx, id, 400)
	return build, err
}

func (s *Store) ListComponentImageBuilds(ctx context.Context, releaseID string, limit int) ([]domain.ComponentImageBuild, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,release_id,environment_id,environment_revision_id,requested_by,status,dockerfile_sha256,image_tag,image_ref,image_digest,error_text,created_at,started_at,finished_at FROM component_image_builds WHERE release_id=? ORDER BY created_at DESC LIMIT ?`, releaseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	builds := make([]domain.ComponentImageBuild, 0)
	for rows.Next() {
		var build domain.ComponentImageBuild
		var created string
		var environmentID, environmentRevisionID, started, finished sql.NullString
		if err := rows.Scan(&build.ID, &build.ReleaseID, &environmentID, &environmentRevisionID, &build.RequestedBy, &build.Status, &build.DockerfileSHA256, &build.ImageTag, &build.ImageRef, &build.ImageDigest, &build.Error, &created, &started, &finished); err != nil {
			return nil, err
		}
		build.CreatedAt = parseTime(created)
		build.EnvironmentID = environmentID.String
		build.EnvironmentRevisionID = environmentRevisionID.String
		build.StartedAt = parseNullTime(started)
		build.FinishedAt = parseNullTime(finished)
		builds = append(builds, build)
	}
	return builds, rows.Err()
}

func (s *Store) UpdateComponentImageBuildStatus(ctx context.Context, id string, from []domain.ImageBuildStatus, to domain.ImageBuildStatus, digest, errorText string, at time.Time) error {
	query := `UPDATE component_image_builds SET status=?,image_digest=?,error_text=?,started_at=CASE WHEN ?='running' THEN ? ELSE started_at END,finished_at=CASE WHEN ? IN ('succeeded','failed','cancelled','interrupted') THEN ? ELSE finished_at END WHERE id=?`
	args := []any{to, digest, errorText, to, timeText(at), to, timeText(at), id}
	if len(from) > 0 {
		query += ` AND status IN (`
		for index, status := range from {
			if index > 0 {
				query += `,`
			}
			query += `?`
			args = append(args, status)
		}
		query += `)`
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return mapSQLError(err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) AppendComponentImageBuildLog(ctx context.Context, log domain.ImageBuildLog) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO component_image_build_logs(build_id,stream,message,created_at) VALUES(?,?,?,?)`, log.BuildID, log.Stream, log.Message, timeText(log.CreatedAt))
	return err
}

func (s *Store) ListComponentImageBuildLogs(ctx context.Context, buildID string, limit int) ([]domain.ImageBuildLog, error) {
	if limit <= 0 || limit > 2000 {
		limit = 400
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,build_id,stream,message,created_at FROM (SELECT id,build_id,stream,message,created_at FROM component_image_build_logs WHERE build_id=? ORDER BY id DESC LIMIT ?) ORDER BY id`, buildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]domain.ImageBuildLog, 0)
	for rows.Next() {
		var log domain.ImageBuildLog
		var created string
		if err := rows.Scan(&log.ID, &log.BuildID, &log.Stream, &log.Message, &created); err != nil {
			return nil, err
		}
		log.CreatedAt = parseTime(created)
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (s *Store) MarkComponentImageBuildsInterrupted(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE component_image_builds SET status='interrupted',error_text='platform restarted during image build',finished_at=? WHERE status IN ('queued','running')`, timeText(at))
	return err
}
