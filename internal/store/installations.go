package store

import (
	"context"
	"database/sql"

	"codex/platform-demo/internal/domain"
)

func (s *Store) GetEnvironmentComponentInstallation(ctx context.Context, environmentID, componentID string) (domain.EnvironmentComponentInstallation, error) {
	var installation domain.EnvironmentComponentInstallation
	var metadata, installedAt string
	var testOnly int
	err := s.db.QueryRowContext(ctx, `
SELECT source_node_id,environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at
FROM environment_component_installations
WHERE environment_id=? AND component_id=? ORDER BY installed_at DESC LIMIT 1`, environmentID, componentID).Scan(
		&installation.NodeID, &installation.EnvironmentID, &installation.ComponentID, &installation.ReleaseID,
		&installation.InstallRunID, &installation.BackupRef, &metadata, &testOnly, &installedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return installation, domain.ErrNotFound
		}
		return installation, err
	}
	installation.Backup = decodeJSON(metadata, domain.BackupMetadata{})
	installation.TestOnly = testOnly != 0
	installation.InstalledAt = parseTime(installedAt)
	return installation, nil
}

func (s *Store) ListEnvironmentComponentInstallations(ctx context.Context, environmentID string) ([]domain.EnvironmentComponentInstallation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT source_node_id,environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at
FROM environment_component_installations
WHERE environment_id=?
ORDER BY installed_at,component_id`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.EnvironmentComponentInstallation, 0)
	for rows.Next() {
		var installation domain.EnvironmentComponentInstallation
		var metadata, installedAt string
		var testOnly int
		if err := rows.Scan(
			&installation.NodeID, &installation.EnvironmentID, &installation.ComponentID, &installation.ReleaseID,
			&installation.InstallRunID, &installation.BackupRef, &metadata, &testOnly, &installedAt,
		); err != nil {
			return nil, err
		}
		installation.Backup = decodeJSON(metadata, domain.BackupMetadata{})
		installation.TestOnly = testOnly != 0
		installation.InstalledAt = parseTime(installedAt)
		out = append(out, installation)
	}
	return out, rows.Err()
}

func (s *Store) UpsertEnvironmentComponentInstallation(ctx context.Context, installation domain.EnvironmentComponentInstallation) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateNewRunReferences(ctx, tx, domain.Run{RetryOfRunID: installation.InstallRunID, InputSnapshot: map[string]any{"backup": installation.Backup}}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO environment_component_installations(
  source_node_id,environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at
) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(environment_id,component_id,source_node_id) DO UPDATE SET
  release_id=excluded.release_id,
  install_run_id=excluded.install_run_id,
  backup_ref=excluded.backup_ref,
  backup_metadata_json=excluded.backup_metadata_json,
  test_only=excluded.test_only,
  installed_at=excluded.installed_at`,
		installation.NodeID, installation.EnvironmentID, installation.ComponentID, installation.ReleaseID,
		installation.InstallRunID, installation.BackupRef, jsonText(installation.Backup),
		installation.TestOnly, timeText(installation.InstalledAt),
	)
	if err != nil {
		return mapSQLError(err)
	}
	return tx.Commit()
}

func (s *Store) DeleteEnvironmentComponentInstallation(ctx context.Context, environmentID, componentID, installRunID string) error {
	result, err := s.execWithBusyRetry(ctx, `
DELETE FROM environment_component_installations
WHERE environment_id=? AND component_id=? AND install_run_id=?`, environmentID, componentID, installRunID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetEnvironmentComponentInstallationForNode(ctx context.Context, environmentID, componentID, nodeID string) (domain.EnvironmentComponentInstallation, error) {
	all, err := s.ListEnvironmentComponentInstallations(ctx, environmentID)
	if err != nil {
		return domain.EnvironmentComponentInstallation{}, err
	}
	for _, item := range all {
		if item.ComponentID == componentID && item.NodeID == nodeID {
			return item, nil
		}
	}
	return domain.EnvironmentComponentInstallation{}, domain.ErrNotFound
}
func (s *Store) DeleteInstallationBaseline(ctx context.Context, environmentID, componentID, backupRef string) error {
	result, err := s.execWithBusyRetry(ctx, `DELETE FROM environment_component_installations WHERE environment_id=? AND component_id=? AND backup_ref=?`, environmentID, componentID, backupRef)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}
