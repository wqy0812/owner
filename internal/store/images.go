package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) ListComponentImages(ctx context.Context, releaseID string) ([]domain.ComponentImage, error) {
	return listComponentImages(ctx, s.db, releaseID)
}

func listComponentImages(ctx context.Context, q queryer, releaseID string) ([]domain.ComponentImage, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,release_id,logical_name,digest,source_ref,source_updated_by,source_updated_at,created_by,created_at FROM component_release_images WHERE release_id=? ORDER BY logical_name`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ComponentImage{}
	for rows.Next() {
		var item domain.ComponentImage
		var sourceUpdated, created string
		if err := rows.Scan(&item.ID, &item.ReleaseID, &item.LogicalName, &item.Digest, &item.SourceRef, &item.SourceUpdatedBy, &sourceUpdated, &item.CreatedBy, &created); err != nil {
			return nil, err
		}
		item.SourceUpdatedAt, item.CreatedAt = parseTime(sourceUpdated), parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetComponentImage(ctx context.Context, releaseID, logicalName string) (domain.ComponentImage, error) {
	var item domain.ComponentImage
	var sourceUpdated, created string
	err := s.db.QueryRowContext(ctx, `SELECT id,release_id,logical_name,digest,source_ref,source_updated_by,source_updated_at,created_by,created_at FROM component_release_images WHERE release_id=? AND logical_name=?`, releaseID, logicalName).Scan(&item.ID, &item.ReleaseID, &item.LogicalName, &item.Digest, &item.SourceRef, &item.SourceUpdatedBy, &sourceUpdated, &item.CreatedBy, &created)
	if err != nil {
		return item, mapSQLError(err)
	}
	item.SourceUpdatedAt, item.CreatedAt = parseTime(sourceUpdated), parseTime(created)
	return item, nil
}

func upsertDraftComponentImageTx(ctx context.Context, tx *sql.Tx, image domain.ComponentImage) error {
	var existingDigest string
	err := tx.QueryRowContext(ctx, `SELECT digest FROM component_release_images WHERE release_id=? AND logical_name=?`, image.ReleaseID, image.LogicalName).Scan(&existingDigest)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows || existingDigest != image.Digest {
		if err := invalidateDraftReleaseDeliveryTx(ctx, tx, image.ReleaseID); err != nil {
			return err
		}
	} else {
		var status domain.ReleaseStatus
		if err := tx.QueryRowContext(ctx, `SELECT status FROM component_releases WHERE id=?`, image.ReleaseID).Scan(&status); err != nil {
			return mapSQLError(err)
		}
		if status != domain.ReleaseDraft {
			return fmt.Errorf("%w: image content can only be changed on a draft release", domain.ErrConflict)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO component_release_images(id,release_id,logical_name,digest,source_ref,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(release_id,logical_name) DO UPDATE SET digest=excluded.digest,source_ref=excluded.source_ref,source_updated_by=excluded.source_updated_by,source_updated_at=excluded.source_updated_at`, image.ID, image.ReleaseID, image.LogicalName, image.Digest, image.SourceRef, image.SourceUpdatedBy, timeText(image.SourceUpdatedAt), image.CreatedBy, timeText(image.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) UpsertDraftComponentImage(ctx context.Context, image domain.ComponentImage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertDraftComponentImageTx(ctx, tx, image); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteComponentImageBuild(ctx context.Context, buildID, pushedDigest string, image domain.ComponentImage, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE component_image_builds SET status='succeeded',image_digest=?,error_text='',finished_at=? WHERE id=? AND release_id=? AND status='running'`, pushedDigest, timeText(at), buildID, image.ReleaseID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return domain.ErrConflict
	}
	if err := upsertDraftComponentImageTx(ctx, tx, image); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateComponentImageSource(ctx context.Context, releaseID, logicalName, sourceRef, actorID string, at time.Time) (domain.ComponentImage, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE component_release_images SET source_ref=?,source_updated_by=?,source_updated_at=? WHERE release_id=? AND logical_name=?`, sourceRef, actorID, timeText(at), releaseID, logicalName)
	if err != nil {
		return domain.ComponentImage{}, mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return domain.ComponentImage{}, domain.ErrNotFound
	}
	return s.GetComponentImage(ctx, releaseID, logicalName)
}

func (s *Store) DeleteDraftComponentImage(ctx context.Context, releaseID, logicalName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateDraftReleaseDeliveryTx(ctx, tx, releaseID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM component_release_images WHERE release_id=? AND logical_name=?`, releaseID, logicalName)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}
