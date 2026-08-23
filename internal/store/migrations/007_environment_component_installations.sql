CREATE TABLE environment_component_installations (
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  component_id TEXT NOT NULL REFERENCES components(id) ON DELETE CASCADE,
  release_id TEXT NOT NULL REFERENCES component_releases(id),
  install_run_id TEXT NOT NULL REFERENCES runs(id),
  backup_ref TEXT NOT NULL,
  backup_metadata_json TEXT NOT NULL,
  test_only INTEGER NOT NULL DEFAULT 0,
  installed_at TEXT NOT NULL,
  PRIMARY KEY(environment_id, component_id)
);

CREATE UNIQUE INDEX idx_environment_component_install_backup_ref
ON environment_component_installations(backup_ref);
