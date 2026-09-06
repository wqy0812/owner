package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

type PendingActionFileMutation struct {
	ID             string
	ReleaseID      string
	WorkspaceRoot  string
	RelativePath   string
	BeforeExists   bool
	BeforeContents []byte
	CreatedAt      time.Time
}

func (s *Store) CreatePendingActionFileMutation(ctx context.Context, mutation PendingActionFileMutation) error {
	contents := mutation.BeforeContents
	if contents == nil {
		contents = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO playbook_action_mutations(id,release_id,workspace_root,relative_path,before_exists,before_content,created_at) VALUES(?,?,?,?,?,?,?)`, mutation.ID, mutation.ReleaseID, mutation.WorkspaceRoot, mutation.RelativePath, mutation.BeforeExists, contents, timeText(mutation.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) DeletePendingActionFileMutation(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM playbook_action_mutations WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: pending Action file mutation no longer exists", domain.ErrConflict)
	}
	return nil
}

func (s *Store) ListPendingActionFileMutations(ctx context.Context) ([]PendingActionFileMutation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,release_id,workspace_root,relative_path,before_exists,before_content,created_at FROM playbook_action_mutations ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mutations := []PendingActionFileMutation{}
	for rows.Next() {
		var mutation PendingActionFileMutation
		var beforeExists int
		var createdAt string
		if err := rows.Scan(&mutation.ID, &mutation.ReleaseID, &mutation.WorkspaceRoot, &mutation.RelativePath, &beforeExists, &mutation.BeforeContents, &createdAt); err != nil {
			return nil, err
		}
		mutation.BeforeExists = beforeExists != 0
		mutation.CreatedAt = parseTime(createdAt)
		mutations = append(mutations, mutation)
	}
	return mutations, rows.Err()
}

func listComponentPlaybookFiles(ctx context.Context, q queryer, releaseIDs ...string) ([]domain.ComponentPlaybookFile, error) {
	placeholders, args := releaseIDPlaceholders(releaseIDs)
	rows, err := q.QueryContext(ctx, `SELECT release_id,relative_path,sha256,size_bytes,media_type,updated_at FROM component_playbook_files WHERE release_id IN (`+placeholders+`) ORDER BY release_id,relative_path`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []domain.ComponentPlaybookFile{}
	for rows.Next() {
		var file domain.ComponentPlaybookFile
		var updated string
		if err := rows.Scan(&file.ReleaseID, &file.Path, &file.SHA256, &file.SizeBytes, &file.MediaType, &updated); err != nil {
			return nil, err
		}
		file.UpdatedAt = parseTime(updated)
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s *Store) ListComponentPlaybookFiles(ctx context.Context, releaseID string) ([]domain.ComponentPlaybookFile, error) {
	return listComponentPlaybookFiles(ctx, s.db, releaseID)
}

// ReplaceDraftPlaybookFilesAndInvalidate atomically publishes the metadata for
// one staged workspace. The caller publishes the staged directory afterwards;
// an interruption therefore leaves digest mismatch and fails closed.
func (s *Store) ReplaceDraftPlaybookFilesAndInvalidate(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string) error {
	return s.replaceDraftPlaybookFilesAndAction(ctx, releaseID, workspaceRoot, treeSHA, files, actionDigests, nil, nil, "")
}

// ReplaceDraftPlaybookFilesAndUpsertAction commits the workspace manifest and
// one complete Action definition and removal of its durable filesystem
// recovery marker in the same SQLite transaction.
func (s *Store) ReplaceDraftPlaybookFilesAndUpsertAction(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string, action domain.ActionDefinition, mutationID string) error {
	return s.replaceDraftPlaybookFilesAndAction(ctx, releaseID, workspaceRoot, treeSHA, files, actionDigests, &action, nil, mutationID)
}

// ReplaceDraftPlaybookFilesAndDeleteAction commits entrypoint removal and its
// Action metadata together, so readers cannot observe a persisted Action that
// is absent from the new manifest.
func (s *Store) ReplaceDraftPlaybookFilesAndDeleteAction(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string, actionID string, mutationID string) error {
	return s.replaceDraftPlaybookFilesAndAction(ctx, releaseID, workspaceRoot, treeSHA, files, actionDigests, nil, &actionID, mutationID)
}

func (s *Store) replaceDraftPlaybookFilesAndAction(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string, upsert *domain.ActionDefinition, deleteID *string, mutationID string) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateDraftReleaseDeliveryTx(ctx, tx, releaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM component_playbook_files WHERE release_id=?`, releaseID); err != nil {
		return err
	}
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO component_playbook_files(release_id,relative_path,sha256,size_bytes,media_type,updated_at) VALUES(?,?,?,?,?,?)`, releaseID, file.Path, file.SHA256, file.SizeBytes, file.MediaType, timeText(file.UpdatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE component_releases SET playbook_tree_sha256=?,playbook_workspace_root=? WHERE id=?`, treeSHA, workspaceRoot, releaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE action_definitions SET playbook_sha256='' WHERE release_id=?`, releaseID); err != nil {
		return err
	}
	for playbook, digest := range actionDigests {
		if _, err := tx.ExecContext(ctx, `UPDATE action_definitions SET playbook_sha256=? WHERE release_id=? AND playbook=?`, digest, releaseID, playbook); err != nil {
			return err
		}
	}
	if deleteID != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM action_definitions WHERE release_id=? AND id=?`, releaseID, *deleteID); err != nil {
			return err
		}
	}
	if upsert != nil {
		var conflictingID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM action_definitions WHERE release_id=? AND kind=? AND kind <> 'check' AND id<>? LIMIT 1`, releaseID, upsert.Kind, upsert.ID).Scan(&conflictingID)
		if err == nil {
			return fmt.Errorf("%w: action kind %s already exists as %s", domain.ErrConflict, upsert.Kind, conflictingID)
		}
		if err != sql.ErrNoRows {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE action_definitions SET name=?,playbook=?,playbook_sha256=?,tags_json=?,host_group=?,required_credentials_json=?,timeout_seconds=?,risk_level=?,destructive=?,idempotent=?,from_release_id=?,to_release_id=?,pre_check_action_id=?,post_check_action_id=?,become=?,gather_facts=?,resource_contract_json=? WHERE id=? AND release_id=? AND kind=?`, upsert.Name, upsert.Playbook, upsert.PlaybookSHA256, jsonText(nonNilStrings(upsert.Tags)), upsert.HostGroup, jsonText(nonNilStrings(upsert.RequiredCredentials)), upsert.TimeoutSeconds, upsert.RiskLevel, upsert.Destructive, upsert.Idempotent, nullString(upsert.FromReleaseID), nullString(upsert.ToReleaseID), upsert.PreCheckActionID, upsert.PostCheckActionID, upsert.Become, upsert.GatherFacts, resourceContractJSON(upsert.ResourceContract), upsert.ID, releaseID, upsert.Kind)
		if err != nil {
			return mapSQLError(err)
		}
		updated, _ := result.RowsAffected()
		if updated == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO action_definitions(id,release_id,name,kind,playbook,playbook_sha256,tags_json,host_group,required_credentials_json,timeout_seconds,risk_level,destructive,idempotent,from_release_id,to_release_id,pre_check_action_id,post_check_action_id,become,gather_facts,resource_contract_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, upsert.ID, releaseID, upsert.Name, upsert.Kind, upsert.Playbook, upsert.PlaybookSHA256, jsonText(nonNilStrings(upsert.Tags)), upsert.HostGroup, jsonText(nonNilStrings(upsert.RequiredCredentials)), upsert.TimeoutSeconds, upsert.RiskLevel, upsert.Destructive, upsert.Idempotent, nullString(upsert.FromReleaseID), nullString(upsert.ToReleaseID), upsert.PreCheckActionID, upsert.PostCheckActionID, upsert.Become, upsert.GatherFacts, resourceContractJSON(upsert.ResourceContract)); err != nil {
				return mapSQLError(err)
			}
		}
	}
	if mutationID != "" {
		result, err := tx.ExecContext(ctx, `DELETE FROM playbook_action_mutations WHERE id=? AND release_id=?`, mutationID, releaseID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return fmt.Errorf("%w: pending Action file mutation no longer exists", domain.ErrConflict)
		}
	}
	if err := tx.Commit(); err != nil {
		if mutationID != "" {
			checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var pending int
			if checkErr := s.db.QueryRowContext(checkCtx, `SELECT COUNT(*) FROM playbook_action_mutations WHERE id=?`, mutationID).Scan(&pending); checkErr == nil && pending == 0 {
				return nil
			}
		}
		return err
	}
	return nil
}

func insertComponentPlaybookFiles(ctx context.Context, tx *sql.Tx, releaseID string, files []domain.ComponentPlaybookFile) error {
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO component_playbook_files(release_id,relative_path,sha256,size_bytes,media_type,updated_at) VALUES(?,?,?,?,?,?)`, releaseID, file.Path, file.SHA256, file.SizeBytes, file.MediaType, timeText(file.UpdatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	return nil
}
