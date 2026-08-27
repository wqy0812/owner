package store

import (
	"context"

	"codex/platform-demo/internal/domain"
)

// CreateComponentImport commits a fully validated component import as one
// SQLite transaction. Files are staged and promoted by the service before this
// call; keeping every business row and audit event in this transaction means a
// failed import never exposes a partial component catalog.
func (s *Store) CreateComponentImport(ctx context.Context, components []domain.Component, releases []domain.ComponentRelease, audits []domain.AuditEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, component := range components {
		if _, err := tx.ExecContext(ctx, `INSERT INTO components(id,slug,name,description,layer,category,component_kind,requiredness,owner_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, component.ID, component.Slug, component.Name, component.Description, component.Layer, component.Category, component.Kind, component.Requiredness, component.OwnerID, timeText(component.CreatedAt), timeText(component.UpdatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	for _, release := range releases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO component_releases(id,component_id,version,release_type,status,release_notes,breaking,verified,candidate,risk_level,environment_constraints_json,parameters_json,created_at,released_at,deprecated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, release.ID, release.ComponentID, release.Version, release.Type, release.Status, release.ReleaseNotes, release.Breaking, release.Verified, release.Candidate, release.RiskLevel, jsonText(release.EnvironmentConstraints), jsonText(release.Parameters), timeText(release.CreatedAt), ptrTimeText(release.ReleasedAt), ptrTimeText(release.DeprecatedAt)); err != nil {
			return mapSQLError(err)
		}
		if err := replaceReleaseChildren(ctx, tx, release); err != nil {
			return err
		}
	}
	for _, event := range audits {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, event.ID, event.ActorID, event.Action, event.ResourceType, event.ResourceID, jsonText(event.Metadata), timeText(event.CreatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	return tx.Commit()
}
