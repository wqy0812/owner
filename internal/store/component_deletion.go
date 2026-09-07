package store

import (
	"context"
	"fmt"

	"codex/platform-demo/internal/domain"
)

// ComponentDeletionConflict describes the state checked under the writer lock.
type ComponentDeletionConflict struct {
	Code    string
	Message string
}

func (e *ComponentDeletionConflict) Error() string { return e.Message }
func (e *ComponentDeletionConflict) Unwrap() error { return domain.ErrConflict }

type componentDeletionState struct {
	owner, name, slug                               string
	releases, dependencies, installations, receipts int
}

func readComponentDeletionState(ctx context.Context, q queryer, id string) (componentDeletionState, error) {
	var state componentDeletionState
	err := q.QueryRowContext(ctx, `SELECT c.owner_id,c.name,c.slug,
		(SELECT COUNT(*) FROM component_releases r WHERE r.component_id=c.id
		 OR r.line_id IN (SELECT id FROM component_release_lines WHERE component_id=c.id)),
		(SELECT COUNT(*) FROM component_dependencies WHERE upstream_component_id=c.id),
		(SELECT COUNT(*) FROM environment_component_installations WHERE component_id=c.id),
		(SELECT COUNT(*) FROM action_execution_receipts WHERE component_id=c.id)
		FROM components c WHERE c.id=?`, id).Scan(&state.owner, &state.name, &state.slug, &state.releases, &state.dependencies, &state.installations, &state.receipts)
	return state, mapSQLError(err)
}

func (state componentDeletionState) conflict() *ComponentDeletionConflict {
	if state.releases > 0 {
		return &ComponentDeletionConflict{Code: "component.not_empty", Message: "该组件仍有版本，仅没有任何版本的组件可以删除"}
	}
	if state.dependencies+state.installations+state.receipts > 0 {
		return &ComponentDeletionConflict{Code: "component.still_referenced", Message: "该组件仍有依赖、环境安装记录或执行回执引用，不能删除"}
	}
	return nil
}

func (s *Store) CanDeleteComponent(ctx context.Context, id, ownerID string) (bool, error) {
	state, err := readComponentDeletionState(ctx, s.db, id)
	return err == nil && state.owner == ownerID && state.conflict() == nil, err
}

func (s *Store) DeleteEmptyComponent(ctx context.Context, id, ownerID string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := readComponentDeletionState(ctx, tx, id)
	if err != nil {
		return err
	}
	if state.owner != ownerID || audit.ActorID != ownerID {
		return fmt.Errorf("%w: 仅组件 Owner 可删除自己负责的空组件", domain.ErrForbidden)
	}
	if conflict := state.conflict(); conflict != nil {
		return conflict
	}
	// All versions (including versions reached through a release line) were
	// checked above. Cascades may remove empty lines, never Release history.
	result, err := tx.ExecContext(ctx, `DELETE FROM components WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed != 1 {
		return domain.ErrNotFound
	}
	audit.Metadata = map[string]any{"componentId": id, "componentName": state.name, "componentSlug": state.slug, "ownerId": state.owner}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, audit.ID, audit.ActorID, audit.Action, audit.ResourceType, audit.ResourceID, jsonText(audit.Metadata), timeText(audit.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	return mapSQLError(tx.Commit())
}
