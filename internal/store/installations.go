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
SELECT environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at
FROM environment_component_installations
WHERE environment_id=? AND component_id=?`, environmentID, componentID).Scan(
		&installation.EnvironmentID, &installation.ComponentID, &installation.ReleaseID,
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
SELECT environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at
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
			&installation.EnvironmentID, &installation.ComponentID, &installation.ReleaseID,
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
	_, err := s.execWithBusyRetry(ctx, `
INSERT INTO environment_component_installations(
  environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at
) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(environment_id,component_id) DO UPDATE SET
  release_id=excluded.release_id,
  install_run_id=excluded.install_run_id,
  backup_ref=excluded.backup_ref,
  backup_metadata_json=excluded.backup_metadata_json,
  test_only=excluded.test_only,
  installed_at=excluded.installed_at`,
		installation.EnvironmentID, installation.ComponentID, installation.ReleaseID,
		installation.InstallRunID, installation.BackupRef, jsonText(installation.Backup),
		installation.TestOnly, timeText(installation.InstalledAt),
	)
	return mapSQLError(err)
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
