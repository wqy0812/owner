package store

import (
	"context"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) HasComponentArtifactMirror(ctx context.Context, targetStation, relativePath, sha256 string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_artifact_mirrors WHERE target_file_station=? AND relative_path=? AND sha256=?`, targetStation, relativePath, sha256).Scan(&count)
	return count > 0, err
}

func (s *Store) RecordComponentArtifactMirror(ctx context.Context, sourceStation, targetStation, relativePath, sha256 string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO component_artifact_mirrors(target_file_station,relative_path,sha256,source_file_station,mirrored_at) VALUES(?,?,?,?,?) ON CONFLICT(target_file_station,relative_path,sha256) DO UPDATE SET source_file_station=excluded.source_file_station,mirrored_at=excluded.mirrored_at`, targetStation, relativePath, sha256, sourceStation, timeText(at))
	return err
}

func (s *Store) ListComponentArtifacts(ctx context.Context, releaseID string) ([]domain.ComponentArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,release_id,alias,file_station,relative_path,filename,sha256,size_bytes,source_mode,environment_id,environment_revision_id,created_by,created_at FROM component_release_artifacts WHERE release_id=? ORDER BY alias`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := []domain.ComponentArtifact{}
	for rows.Next() {
		var artifact domain.ComponentArtifact
		var created string
		if err := rows.Scan(&artifact.ID, &artifact.ReleaseID, &artifact.Alias, &artifact.FileStation, &artifact.RelativePath, &artifact.Filename, &artifact.SHA256, &artifact.SizeBytes, &artifact.SourceMode, &artifact.EnvironmentID, &artifact.EnvironmentRevisionID, &artifact.CreatedBy, &created); err != nil {
			return nil, err
		}
		artifact.CreatedAt = parseTime(created)
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

func (s *Store) UpsertComponentArtifact(ctx context.Context, artifact domain.ComponentArtifact) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO component_release_artifacts(id,release_id,alias,file_station,relative_path,filename,sha256,size_bytes,source_mode,environment_id,environment_revision_id,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(release_id,alias) DO UPDATE SET id=excluded.id,file_station=excluded.file_station,relative_path=excluded.relative_path,filename=excluded.filename,sha256=excluded.sha256,size_bytes=excluded.size_bytes,source_mode=excluded.source_mode,environment_id=excluded.environment_id,environment_revision_id=excluded.environment_revision_id,created_by=excluded.created_by,created_at=excluded.created_at`, artifact.ID, artifact.ReleaseID, artifact.Alias, artifact.FileStation, artifact.RelativePath, artifact.Filename, artifact.SHA256, artifact.SizeBytes, artifact.SourceMode, artifact.EnvironmentID, artifact.EnvironmentRevisionID, artifact.CreatedBy, timeText(artifact.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) DeleteComponentArtifact(ctx context.Context, releaseID, alias string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM component_release_artifacts WHERE release_id=? AND alias=?`, releaseID, alias)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) CloneComponentArtifacts(ctx context.Context, sourceReleaseID, targetReleaseID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO component_release_artifacts(id,release_id,alias,file_station,relative_path,filename,sha256,size_bytes,source_mode,environment_id,environment_revision_id,created_by,created_at) SELECT 'artifact-' || lower(hex(randomblob(12))),?,alias,file_station,relative_path,filename,sha256,size_bytes,source_mode,environment_id,environment_revision_id,created_by,created_at FROM component_release_artifacts WHERE release_id=?`, targetReleaseID, sourceReleaseID)
	return mapSQLError(err)
}
