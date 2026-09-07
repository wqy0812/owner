package store

import (
	"context"
	"fmt"

	"codex/platform-demo/internal/domain"
)

type EnvironmentRevisionDeletionImpact struct {
	EnvironmentID    string `json:"environmentId"`
	EnvironmentName  string `json:"environmentName"`
	RevisionID       string `json:"revisionId"`
	Revision         int    `json:"revision"`
	OwnerID          string `json:"-"`
	Current          bool   `json:"current"`
	Archived         bool   `json:"archived"`
	RunCount         int    `json:"runCount"`
	ImageBuildCount  int    `json:"imageBuildCount"`
	HealthCheckCount int    `json:"healthCheckCount"`
	SSHCheckCount    int    `json:"sshCheckCount"`
}

// The counts and lifecycle state come from one SQLite read snapshot.
func environmentRevisionDeletionImpact(ctx context.Context, q queryer, environmentID, revisionID string) (EnvironmentRevisionDeletionImpact, error) {
	var impact EnvironmentRevisionDeletionImpact
	err := q.QueryRowContext(ctx, `SELECT e.id,e.name,r.id,r.revision,e.owner_id,
		COALESCE(e.current_revision_id,'')=r.id,e.archived_at IS NOT NULL,
		(SELECT COUNT(*) FROM retained_run_history WHERE environment_revision_id=r.id),
		(SELECT COUNT(*) FROM component_image_builds WHERE environment_revision_id=r.id),
		(SELECT COUNT(*) FROM environment_health_checks WHERE environment_revision_id=r.id),
		(SELECT COUNT(*) FROM environment_ssh_checks WHERE environment_revision_id=r.id)
		FROM environment_revisions r JOIN environments e ON e.id=r.environment_id
		WHERE e.id=? AND r.id=?`, environmentID, revisionID).Scan(
		&impact.EnvironmentID, &impact.EnvironmentName, &impact.RevisionID, &impact.Revision, &impact.OwnerID,
		&impact.Current, &impact.Archived, &impact.RunCount, &impact.ImageBuildCount, &impact.HealthCheckCount, &impact.SSHCheckCount)
	return impact, mapSQLError(err)
}

func (s *Store) EnvironmentRevisionDeletionImpact(ctx context.Context, environmentID, revisionID string) (EnvironmentRevisionDeletionImpact, error) {
	return environmentRevisionDeletionImpact(ctx, s.db, environmentID, revisionID)
}

// Return the state observed under the writer lock so the service can explain
// a conflict without a second, potentially different, read.
type EnvironmentRevisionDeletionConflict struct {
	Impact EnvironmentRevisionDeletionImpact
}

func (e *EnvironmentRevisionDeletionConflict) Error() string {
	return "环境版本状态或引用已变化，请重新核对删除影响"
}

func (e *EnvironmentRevisionDeletionConflict) Unwrap() error { return domain.ErrConflict }

func (s *Store) DeleteEnvironmentRevision(ctx context.Context, environmentID, revisionID, ownerID string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	impact, err := environmentRevisionDeletionImpact(ctx, tx, environmentID, revisionID)
	if err != nil {
		return err
	}
	if impact.OwnerID != ownerID || audit.ActorID != ownerID {
		return fmt.Errorf("%w: 仅环境 Owner 可删除自有环境的历史版本", domain.ErrForbidden)
	}
	if impact.Current || impact.Archived || impact.RunCount > 0 || impact.ImageBuildCount > 0 {
		return &EnvironmentRevisionDeletionConflict{Impact: impact}
	}
	for _, query := range []string{
		`DELETE FROM environment_health_checks WHERE environment_revision_id=?`,
		`DELETE FROM environment_ssh_checks WHERE environment_revision_id=?`,
		`DELETE FROM environment_revisions WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, revisionID); err != nil {
			return mapSQLError(err)
		}
	}
	audit.Metadata = map[string]any{
		"environmentId": environmentID, "environmentName": impact.EnvironmentName,
		"revisionId": revisionID, "revision": impact.Revision,
		"healthCheckCount": impact.HealthCheckCount, "sshCheckCount": impact.SSHCheckCount,
	}
	if err := insertEnvironmentLifecycleAudit(ctx, tx, audit); err != nil {
		return err
	}
	return mapSQLError(tx.Commit())
}
