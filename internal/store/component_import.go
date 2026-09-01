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
		if _, err := tx.ExecContext(ctx, `INSERT INTO components(id,slug,name,description,layer,tags_json,owner_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, component.ID, component.Slug, component.Name, component.Description, component.Layer, jsonText(nonNilStrings(component.Tags)), component.OwnerID, timeText(component.CreatedAt), timeText(component.UpdatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	for _, release := range releases {
		if err := insertComponentRelease(ctx, tx, release); err != nil {
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
