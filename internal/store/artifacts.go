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
	return listComponentArtifacts(ctx, s.db, releaseID)
}

func listComponentArtifacts(ctx context.Context, q queryer, releaseID string) ([]domain.ComponentArtifact, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,release_id,alias,filename,sha256,size_bytes,source_url,source_updated_by,source_updated_at,created_by,created_at FROM component_release_artifacts WHERE release_id=? ORDER BY alias`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := []domain.ComponentArtifact{}
	for rows.Next() {
		var artifact domain.ComponentArtifact
		var sourceUpdated, created string
		if err := rows.Scan(&artifact.ID, &artifact.ReleaseID, &artifact.Alias, &artifact.Filename, &artifact.SHA256, &artifact.SizeBytes, &artifact.SourceURL, &artifact.SourceUpdatedBy, &sourceUpdated, &artifact.CreatedBy, &created); err != nil {
			return nil, err
		}
		artifact.SourceUpdatedAt = parseTime(sourceUpdated)
		artifact.CreatedAt = parseTime(created)
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

func (s *Store) UpsertDraftComponentArtifactAndInvalidate(ctx context.Context, artifact domain.ComponentArtifact) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateDraftReleaseDeliveryTx(ctx, tx, artifact.ReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO component_release_artifacts(id,release_id,alias,filename,sha256,size_bytes,source_url,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(release_id,alias) DO UPDATE SET id=excluded.id,filename=excluded.filename,sha256=excluded.sha256,size_bytes=excluded.size_bytes,source_url=excluded.source_url,source_updated_by=excluded.source_updated_by,source_updated_at=excluded.source_updated_at,created_by=excluded.created_by,created_at=excluded.created_at`, artifact.ID, artifact.ReleaseID, artifact.Alias, artifact.Filename, artifact.SHA256, artifact.SizeBytes, artifact.SourceURL, artifact.SourceUpdatedBy, timeText(artifact.SourceUpdatedAt), artifact.CreatedBy, timeText(artifact.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	return tx.Commit()
}

func (s *Store) UpdateComponentArtifactSource(ctx context.Context, releaseID, alias, sourceURL, actorID string, at time.Time) (domain.ComponentArtifact, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE component_release_artifacts SET source_url=?,source_updated_by=?,source_updated_at=? WHERE release_id=? AND alias=?`, sourceURL, actorID, timeText(at), releaseID, alias)
	if err != nil {
		return domain.ComponentArtifact{}, mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return domain.ComponentArtifact{}, domain.ErrNotFound
	}
	items, err := s.ListComponentArtifacts(ctx, releaseID)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	for _, item := range items {
		if item.Alias == alias {
			return item, nil
		}
	}
	return domain.ComponentArtifact{}, domain.ErrNotFound
}

func (s *Store) DeleteDraftComponentArtifactAndInvalidate(ctx context.Context, releaseID, alias string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateDraftReleaseDeliveryTx(ctx, tx, releaseID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM component_release_artifacts WHERE release_id=? AND alias=?`, releaseID, alias)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}
