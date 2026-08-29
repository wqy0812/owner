PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_contract (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_contract(id, version)
VALUES(1, 'clusterforge-v1-20260829-ssh-connectivity');

CREATE TABLE IF NOT EXISTS publication_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0)
);

INSERT OR IGNORE INTO publication_state(id, generation) VALUES(1, 1);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('component_owner','scenario_owner','environment_owner')),
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS components (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  layer TEXT NOT NULL DEFAULT 'platform_extension'
    CHECK (layer IN ('host_foundation','runtime_state','orchestration_core','cluster_service','observability_management','platform_extension')),
  tags_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags_json))
);

CREATE TABLE IF NOT EXISTS component_releases (
  id TEXT PRIMARY KEY,
  component_id TEXT NOT NULL REFERENCES components(id) ON DELETE CASCADE,
  version TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft','released','deprecated')),
  release_notes TEXT NOT NULL DEFAULT '',
  breaking INTEGER NOT NULL DEFAULT 0,
  candidate INTEGER NOT NULL DEFAULT 0 CHECK (candidate IN (0,1)),
  publication_generation INTEGER NOT NULL DEFAULT 1 CHECK (publication_generation > 0),
  risk_level TEXT NOT NULL DEFAULT 'low',
  environment_constraints_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(environment_constraints_json)),
  parameters_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(parameters_json)),
  created_at TEXT NOT NULL,
  released_at TEXT,
  deprecated_at TEXT,
  UNIQUE(component_id, version)
);

CREATE TABLE IF NOT EXISTS component_dependencies (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  upstream_component_id TEXT NOT NULL REFERENCES components(id),
  upstream_release_id TEXT NOT NULL REFERENCES component_releases(id),
  purpose TEXT NOT NULL DEFAULT '',
  parameter_mappings_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(parameter_mappings_json)),
  UNIQUE(release_id, upstream_component_id)
);

CREATE TABLE IF NOT EXISTS action_definitions (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  playbook TEXT NOT NULL,
  playbook_sha256 TEXT NOT NULL DEFAULT '',
  tags_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags_json)),
  limit_pattern TEXT NOT NULL DEFAULT '',
  host_group TEXT NOT NULL DEFAULT '',
  allowed_parameters_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(allowed_parameters_json)),
  required_credentials_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(required_credentials_json)),
  timeout_seconds INTEGER NOT NULL DEFAULT 1800,
  risk_level TEXT NOT NULL DEFAULT 'low',
  destructive INTEGER NOT NULL DEFAULT 0,
  idempotent INTEGER NOT NULL DEFAULT 0 CHECK (idempotent IN (0,1)),
  from_release_id TEXT,
  to_release_id TEXT
);

CREATE INDEX IF NOT EXISTS idx_releases_component ON component_releases(component_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dependencies_upstream ON component_dependencies(upstream_component_id);

CREATE TABLE IF NOT EXISTS scenarios (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL REFERENCES users(id),
  current_revision_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scenario_revisions (
  id TEXT PRIMARY KEY,
  scenario_id TEXT NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft','testing','test_passed','released','deprecated')),
  publication_generation INTEGER NOT NULL DEFAULT 1 CHECK (publication_generation > 0),
  graph_json TEXT NOT NULL DEFAULT '{"nodes":[],"edges":[]}' CHECK (json_valid(graph_json)),
  created_at TEXT NOT NULL,
  test_passed_at TEXT,
  released_at TEXT,
  deprecated_at TEXT,
  abandoned_at TEXT,
  UNIQUE(scenario_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_scenario_revisions_scenario ON scenario_revisions(scenario_id, revision DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scenario_revisions_one_active
ON scenario_revisions(scenario_id)
WHERE status IN ('draft','testing','test_passed');

CREATE TABLE IF NOT EXISTS environments (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL REFERENCES users(id),
  current_revision_id TEXT,
  archived_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS environment_revisions (
  id TEXT PRIMARY KEY,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL,
  facts_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(facts_json)),
  inventory_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(inventory_json)),
  variables_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(variables_json)),
  credential_refs_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(credential_refs_json)),
  created_by TEXT NOT NULL DEFAULT '',
  change_reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(environment_id, revision)
);

CREATE TRIGGER IF NOT EXISTS environment_revisions_active_environment_insert
BEFORE INSERT ON environment_revisions
WHEN EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot create revisions');
END;

CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  requested_by TEXT NOT NULL REFERENCES users(id),
  environment_id TEXT NOT NULL REFERENCES environments(id),
  environment_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
  component_release_id TEXT REFERENCES component_releases(id),
  scenario_revision_id TEXT REFERENCES scenario_revisions(id),
  action_kind TEXT NOT NULL DEFAULT '',
  destructive INTEGER NOT NULL DEFAULT 0,
  input_snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(input_snapshot_json)),
  artifact_digest TEXT NOT NULL DEFAULT '',
  retry_of_run_id TEXT REFERENCES runs(id),
  retry_root_run_id TEXT REFERENCES runs(id),
  retry_attempt INTEGER NOT NULL DEFAULT 0,
  retry_start_step INTEGER NOT NULL DEFAULT 0,
  error_text TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_runs_environment_status ON runs(environment_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_runs_requester ON runs(requested_by, created_at DESC);

CREATE TRIGGER IF NOT EXISTS runs_active_environment_insert
BEFORE INSERT ON runs
WHEN EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot create runs');
END;

CREATE TRIGGER IF NOT EXISTS runs_active_environment_update
BEFORE UPDATE OF status,environment_id ON runs
WHEN NEW.status IN ('running','awaiting_approval','queued')
AND EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot receive runs');
END;
CREATE INDEX IF NOT EXISTS idx_runs_retry_root ON runs(retry_root_run_id, retry_attempt);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_retry_attempt ON runs(retry_root_run_id, retry_attempt) WHERE retry_root_run_id IS NOT NULL;
DROP INDEX IF EXISTS idx_runs_one_active_retry;
CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_one_active_retry_root ON runs(retry_root_run_id) WHERE retry_root_run_id IS NOT NULL AND status IN ('awaiting_approval','queued','running');

CREATE TABLE IF NOT EXISTS run_input_presets (
  id TEXT PRIMARY KEY,
  created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('component_release','scenario_revision')),
  resource_id TEXT NOT NULL,
  context TEXT NOT NULL CHECK (context IN ('component_install_verify','component_rollback','scenario_test','scenario_run')),
  name TEXT NOT NULL,
  values_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(values_json)),
  definition_digest TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(created_by,resource_type,resource_id,context,name)
);

CREATE INDEX IF NOT EXISTS idx_run_input_presets_lookup ON run_input_presets(created_by,resource_type,resource_id,context,name);

CREATE TRIGGER IF NOT EXISTS runs_environment_rollback_fence_insert
BEFORE INSERT ON runs
WHEN NEW.status IN ('awaiting_approval','queued','running') AND (
  (NEW.kind='environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.environment_id=NEW.environment_id
      AND r.status IN ('awaiting_approval','queued','running')
  ))
  OR
  (NEW.kind<>'environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.environment_id=NEW.environment_id
      AND r.kind='environment_rollback'
      AND r.status IN ('awaiting_approval','queued','running')
  ))
)
BEGIN
  SELECT RAISE(ABORT, 'environment rollback fence conflict');
END;

CREATE TRIGGER IF NOT EXISTS runs_environment_rollback_fence_update
BEFORE UPDATE OF status,environment_id,kind ON runs
WHEN NEW.status IN ('awaiting_approval','queued','running') AND (
  (NEW.kind='environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.id<>NEW.id
      AND r.environment_id=NEW.environment_id
      AND r.status IN ('awaiting_approval','queued','running')
  ))
  OR
  (NEW.kind<>'environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.id<>NEW.id
      AND r.environment_id=NEW.environment_id
      AND r.kind='environment_rollback'
      AND r.status IN ('awaiting_approval','queued','running')
  ))
)
BEGIN
  SELECT RAISE(ABORT, 'environment rollback fence conflict');
END;

CREATE TABLE IF NOT EXISTS run_steps (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  node_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER,
  summary TEXT NOT NULL DEFAULT '',
  started_at TEXT,
  finished_at TEXT
);

CREATE TABLE IF NOT EXISTS run_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  step_id TEXT NOT NULL DEFAULT '',
  stream TEXT NOT NULL DEFAULT 'stdout',
  message TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_logs_run ON run_logs(run_id, id);

CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending',
  requested_at TEXT NOT NULL,
  decided_by TEXT REFERENCES users(id),
  decision TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  decided_at TEXT
);

CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  resource_url TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
  read_at TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  actor_id TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json)),
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_resource_created ON audit_events(resource_type, resource_id, created_at);

CREATE TRIGGER IF NOT EXISTS audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
  SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TRIGGER IF NOT EXISTS audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
  SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TABLE IF NOT EXISTS component_image_builds (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  environment_id TEXT REFERENCES environments(id),
  environment_revision_id TEXT REFERENCES environment_revisions(id),
  requested_by TEXT NOT NULL REFERENCES users(id),
  status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','cancelled','interrupted')),
  dockerfile_sha256 TEXT NOT NULL,
  image_tag TEXT NOT NULL,
  image_ref TEXT NOT NULL,
  image_digest TEXT NOT NULL DEFAULT '',
  error_text TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_component_image_builds_release_created
ON component_image_builds(release_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_component_image_builds_environment_created
ON component_image_builds(environment_id, created_at DESC);

CREATE TRIGGER IF NOT EXISTS component_image_builds_active_environment_insert
BEFORE INSERT ON component_image_builds
WHEN NEW.environment_id IS NOT NULL
AND EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot create image builds');
END;

CREATE TRIGGER IF NOT EXISTS component_image_builds_active_environment_update
BEFORE UPDATE OF status,environment_id ON component_image_builds
WHEN NEW.status IN ('queued','running')
AND NEW.environment_id IS NOT NULL
AND EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot activate image builds');
END;

CREATE TABLE IF NOT EXISTS component_image_build_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  build_id TEXT NOT NULL REFERENCES component_image_builds(id) ON DELETE CASCADE,
  stream TEXT NOT NULL CHECK (stream IN ('stdout','stderr','system')),
  message TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_component_image_build_logs_build_id
ON component_image_build_logs(build_id, id);

CREATE TABLE IF NOT EXISTS environment_component_installations (
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  component_id TEXT NOT NULL REFERENCES components(id) ON DELETE CASCADE,
  release_id TEXT NOT NULL REFERENCES component_releases(id),
  install_run_id TEXT NOT NULL REFERENCES runs(id),
  backup_ref TEXT NOT NULL,
  backup_metadata_json TEXT NOT NULL CHECK (json_valid(backup_metadata_json)),
  test_only INTEGER NOT NULL DEFAULT 0,
  installed_at TEXT NOT NULL,
  PRIMARY KEY(environment_id, component_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_environment_component_install_backup_ref
ON environment_component_installations(backup_ref);

CREATE TABLE IF NOT EXISTS component_release_artifacts (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  alias TEXT NOT NULL,
  filename TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  source_url TEXT NOT NULL,
  source_updated_by TEXT NOT NULL REFERENCES users(id),
  source_updated_at TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  UNIQUE(release_id, alias)
);

CREATE INDEX IF NOT EXISTS idx_component_release_artifacts_release
ON component_release_artifacts(release_id, alias);

CREATE TABLE IF NOT EXISTS component_release_images (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  logical_name TEXT NOT NULL,
  digest TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  source_updated_by TEXT NOT NULL REFERENCES users(id),
  source_updated_at TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  UNIQUE(release_id, logical_name)
);

CREATE INDEX IF NOT EXISTS idx_component_release_images_release
ON component_release_images(release_id, logical_name);

CREATE TABLE IF NOT EXISTS component_artifact_mirrors (
  target_file_station TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  source_file_station TEXT NOT NULL,
  mirrored_at TEXT NOT NULL,
  PRIMARY KEY(target_file_station, relative_path, sha256)
);

CREATE TABLE IF NOT EXISTS component_image_mirrors (
  target_registry TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  target_ref TEXT NOT NULL,
  target_digest TEXT NOT NULL,
  mirrored_at TEXT NOT NULL,
  PRIMARY KEY(target_registry, source_digest)
);

CREATE TABLE IF NOT EXISTS environment_health_checks (
  id TEXT PRIMARY KEY,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  environment_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
  status TEXT NOT NULL CHECK (status IN ('healthy','degraded')),
  results_json TEXT NOT NULL CHECK (json_valid(results_json)),
  checked_at TEXT NOT NULL
);

CREATE TRIGGER IF NOT EXISTS environment_health_checks_active_environment_insert
BEFORE INSERT ON environment_health_checks
WHEN EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot record health checks');
END;

CREATE INDEX IF NOT EXISTS idx_environment_health_checks_latest
ON environment_health_checks(environment_id, checked_at DESC);

CREATE TABLE IF NOT EXISTS environment_ssh_checks (
  id TEXT PRIMARY KEY,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  environment_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
  status TEXT NOT NULL CHECK (status IN ('healthy','degraded')),
  duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
  results_json TEXT NOT NULL CHECK (json_valid(results_json)),
  checked_at TEXT NOT NULL
);

CREATE TRIGGER IF NOT EXISTS environment_ssh_checks_active_environment_insert
BEFORE INSERT ON environment_ssh_checks
WHEN EXISTS (SELECT 1 FROM environments WHERE id=NEW.environment_id AND archived_at IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT, 'archived environment cannot record SSH checks');
END;

CREATE INDEX IF NOT EXISTS idx_environment_ssh_checks_latest
ON environment_ssh_checks(environment_id, checked_at DESC);

-- Publication generations are optimistic-concurrency fences. They advance for
-- every definition or candidate-intent change, but deliberately ignore mutable
-- artifact/image source locations.
CREATE TRIGGER IF NOT EXISTS component_dependencies_publication_insert
AFTER INSERT ON component_dependencies
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_dependencies_publication_update
AFTER UPDATE ON component_dependencies
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_dependencies_publication_delete
AFTER DELETE ON component_dependencies
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS action_definitions_publication_insert
AFTER INSERT ON action_definitions
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS action_definitions_publication_update
AFTER UPDATE ON action_definitions
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS action_definitions_publication_delete
AFTER DELETE ON action_definitions
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_artifacts_publication_insert
AFTER INSERT ON component_release_artifacts
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_artifacts_publication_update
AFTER UPDATE OF release_id,alias,filename,sha256 ON component_release_artifacts
WHEN OLD.release_id<>NEW.release_id OR OLD.alias<>NEW.alias OR OLD.filename<>NEW.filename OR OLD.sha256<>NEW.sha256
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_artifacts_publication_delete
AFTER DELETE ON component_release_artifacts
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_images_publication_insert
AFTER INSERT ON component_release_images
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_images_publication_update
AFTER UPDATE OF release_id,logical_name,digest ON component_release_images
WHEN OLD.release_id<>NEW.release_id OR OLD.logical_name<>NEW.logical_name OR OLD.digest<>NEW.digest
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_images_publication_delete
AFTER DELETE ON component_release_images
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1 WHERE id=OLD.release_id;
END;
