PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_contract (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_contract(id, version)
VALUES(1, 'first-version-20260826-reuse-workflows');

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
  category TEXT NOT NULL DEFAULT 'platform'
    CHECK (
      (layer='host_foundation' AND category IN ('preflight','bootstrap','security')) OR
      (layer='runtime_state' AND category IN ('runtime','state_store')) OR
      (layer='orchestration_core' AND category IN ('control_plane','worker','network')) OR
      (layer='cluster_service' AND category IN ('network','dns','ingress','storage')) OR
      (layer='observability_management' AND category IN ('observability','node_management')) OR
      (layer='platform_extension' AND category IN ('platform','autoscaling'))
    ),
  component_kind TEXT NOT NULL DEFAULT 'software'
    CHECK (component_kind IN ('software','software_bundle','delivery_stage','configuration','artifact_set')),
  requiredness TEXT NOT NULL DEFAULT 'optional'
    CHECK (requiredness IN ('core_required','profile_required','optional'))
);

CREATE TABLE IF NOT EXISTS component_releases (
  id TEXT PRIMARY KEY,
  component_id TEXT NOT NULL REFERENCES components(id) ON DELETE CASCADE,
  version TEXT NOT NULL,
  release_type TEXT NOT NULL CHECK (release_type IN ('atomic','bundle')),
  status TEXT NOT NULL CHECK (status IN ('draft','released','deprecated')),
  release_notes TEXT NOT NULL DEFAULT '',
  breaking INTEGER NOT NULL DEFAULT 0,
  verified INTEGER NOT NULL DEFAULT 0,
  candidate INTEGER NOT NULL DEFAULT 0 CHECK (candidate IN (0,1)),
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

CREATE TRIGGER IF NOT EXISTS component_releases_candidate_requires_verified_insert
BEFORE INSERT ON component_releases
WHEN NEW.candidate=1 AND NEW.verified<>1
BEGIN
  SELECT RAISE(ABORT, 'candidate release must be verified');
END;

CREATE TRIGGER IF NOT EXISTS component_releases_candidate_requires_verified_update
BEFORE UPDATE OF candidate,verified ON component_releases
WHEN NEW.candidate=1 AND NEW.verified<>1
BEGIN
  SELECT RAISE(ABORT, 'candidate release must be verified');
END;

CREATE TABLE IF NOT EXISTS action_definitions (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  playbook TEXT NOT NULL,
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
  graph_json TEXT NOT NULL DEFAULT '{"nodes":[],"edges":[]}' CHECK (json_valid(graph_json)),
  execution_policy_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(execution_policy_json)),
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
  max_concurrent INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL DEFAULT '',
  change_reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(environment_id, revision)
);

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
  file_station TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  filename TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  source_mode TEXT NOT NULL CHECK (source_mode IN ('upload','register')),
  environment_id TEXT NOT NULL REFERENCES environments(id),
  environment_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  UNIQUE(release_id, alias)
);

CREATE INDEX IF NOT EXISTS idx_component_release_artifacts_release
ON component_release_artifacts(release_id, alias);

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

CREATE INDEX IF NOT EXISTS idx_environment_health_checks_latest
ON environment_health_checks(environment_id, checked_at DESC);
