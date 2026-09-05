PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_contract (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_contract(id, version)
VALUES(1, 'clusterforge-v1-20260905-adaptation-run-archive');

CREATE TABLE IF NOT EXISTS publication_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0)
);

INSERT OR IGNORE INTO publication_state(id, generation) VALUES(1, 1);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('component_owner','scenario_owner','environment_owner','platform_admin')),
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS platform_option_categories (
  id TEXT PRIMARY KEY,
  parent_category_id TEXT REFERENCES platform_option_categories(id),
  technical_key TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL COLLATE NOCASE UNIQUE,
  category_type TEXT NOT NULL CHECK (category_type IN ('environment_dimension','host_group')),
  environment_required INTEGER NOT NULL DEFAULT 0 CHECK (environment_required IN (0,1)),
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  retired_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_option_categories_host_group
  ON platform_option_categories(category_type) WHERE category_type='host_group';

CREATE TABLE IF NOT EXISTS platform_options (
  id TEXT PRIMARY KEY,
  category_id TEXT NOT NULL REFERENCES platform_option_categories(id) ON DELETE CASCADE,
  parent_option_id TEXT REFERENCES platform_options(id),
  technical_value TEXT NOT NULL,
  label TEXT NOT NULL COLLATE NOCASE,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  retired_at TEXT,
  UNIQUE(category_id, technical_value),
  UNIQUE(category_id, label)
);

CREATE INDEX IF NOT EXISTS idx_platform_options_category
  ON platform_options(category_id, sort_order, created_at);

CREATE TABLE IF NOT EXISTS environment_parameter_definitions (
  id TEXT PRIMARY KEY,
  technical_key TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL COLLATE NOCASE UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  parameter_type TEXT NOT NULL CHECK (parameter_type IN ('string','boolean','integer','number','object','array')),
  enum_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(enum_json)),
  min_length INTEGER NOT NULL DEFAULT 0 CHECK (min_length >= 0),
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL
);

-- Platform defaults are separate from immutable component contracts and revisions.
CREATE TABLE IF NOT EXISTS environment_parameter_defaults (
  definition_id TEXT PRIMARY KEY REFERENCES environment_parameter_definitions(id) ON DELETE CASCADE,
  value_json TEXT NOT NULL CHECK (json_valid(value_json) AND json_type(value_json) != 'null')
);

CREATE TABLE IF NOT EXISTS environment_variable_definitions (
  id TEXT PRIMARY KEY,
  variable_name TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL COLLATE NOCASE UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL REFERENCES users(id),
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

CREATE TABLE IF NOT EXISTS component_release_lines (
  id TEXT PRIMARY KEY,
  component_id TEXT NOT NULL REFERENCES components(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(component_id, name)
);

CREATE TABLE IF NOT EXISTS component_releases (
  id TEXT PRIMARY KEY,
  component_id TEXT NOT NULL REFERENCES components(id) ON DELETE CASCADE,
  line_id TEXT NOT NULL REFERENCES component_release_lines(id) ON DELETE CASCADE,
  parent_release_id TEXT REFERENCES component_releases(id),
  template_source_release_id TEXT REFERENCES component_releases(id),
  version TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft','released','deprecated')),
  release_notes TEXT NOT NULL DEFAULT '',
  compatibility TEXT NOT NULL CHECK (compatibility IN ('not_applicable','compatible','breaking')),
  candidate INTEGER NOT NULL DEFAULT 0 CHECK (candidate IN (0,1)),
  review_status TEXT NOT NULL DEFAULT 'not_submitted' CHECK (review_status IN ('not_submitted','pending','approved','rejected')),
  review_contract_digest TEXT NOT NULL DEFAULT '',
  review_submitted_at TEXT,
  reviewed_by TEXT REFERENCES users(id),
  reviewed_at TEXT,
  review_comment TEXT NOT NULL DEFAULT '',
  publication_generation INTEGER NOT NULL DEFAULT 1 CHECK (publication_generation > 0),
  risk_level TEXT NOT NULL DEFAULT 'low',
  environment_constraints_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(environment_constraints_json)),
  parameters_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(parameters_json)),
  playbook_tree_sha256 TEXT NOT NULL DEFAULT '',
  playbook_workspace_root TEXT NOT NULL DEFAULT '',
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
  kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'configuration')),
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
  host_group TEXT NOT NULL DEFAULT '',
  required_credentials_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(required_credentials_json)),
  timeout_seconds INTEGER NOT NULL DEFAULT 1800,
  risk_level TEXT NOT NULL DEFAULT 'low',
  destructive INTEGER NOT NULL DEFAULT 0,
  idempotent INTEGER NOT NULL DEFAULT 0 CHECK (idempotent IN (0,1)),
  from_release_id TEXT,
  to_release_id TEXT
);

CREATE TABLE IF NOT EXISTS component_playbook_files (
  release_id TEXT NOT NULL REFERENCES component_releases(id) ON DELETE CASCADE,
  relative_path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  media_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(release_id, relative_path)
);

CREATE TABLE IF NOT EXISTS playbook_action_mutations (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES component_releases(id),
  workspace_root TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  before_exists INTEGER NOT NULL CHECK (before_exists IN (0,1)),
  before_content BLOB NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(release_id)
);

CREATE INDEX IF NOT EXISTS idx_releases_component ON component_releases(component_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_releases_line ON component_releases(line_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_releases_one_draft_per_line
  ON component_releases(line_id) WHERE status = 'draft';
CREATE UNIQUE INDEX IF NOT EXISTS idx_releases_one_successor
  ON component_releases(parent_release_id)
  WHERE parent_release_id IS NOT NULL
    AND (status IN ('draft', 'released') OR released_at IS NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS idx_releases_playbook_workspace_root
  ON component_releases(playbook_workspace_root) WHERE playbook_workspace_root <> '';
CREATE INDEX IF NOT EXISTS idx_dependencies_upstream ON component_dependencies(upstream_component_id);
CREATE INDEX IF NOT EXISTS idx_actions_release ON action_definitions(release_id, kind, name);
CREATE INDEX IF NOT EXISTS idx_playbook_files_release ON component_playbook_files(release_id, relative_path);

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
  environment_constraints_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(environment_constraints_json)),
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
  parameters_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(parameters_json)),
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
  finished_at TEXT,
  -- Query projections are derived from the locked snapshot, never independently
  -- written. Keep missing or non-text metadata NULL rather than coercing it.
  component_spec_digest TEXT GENERATED ALWAYS AS (
    CASE WHEN json_type(input_snapshot_json,'$.componentReleaseSpecDigest')='text'
      THEN json_extract(input_snapshot_json,'$.componentReleaseSpecDigest') END
  ) VIRTUAL,
  component_evidence_kind TEXT GENERATED ALWAYS AS (
    CASE WHEN json_type(input_snapshot_json,'$.componentTestEvidence')='text'
      THEN json_extract(input_snapshot_json,'$.componentTestEvidence') END
  ) VIRTUAL,
  evidence_at TEXT GENERATED ALWAYS AS (COALESCE(finished_at,created_at)) VIRTUAL
);

CREATE INDEX IF NOT EXISTS idx_runs_environment_status ON runs(environment_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_runs_requester ON runs(requested_by, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_component_evidence
ON runs(component_release_id, component_spec_digest, component_evidence_kind, evidence_at DESC, created_at DESC, id DESC)
WHERE kind='component_test' AND status='succeeded';
CREATE INDEX IF NOT EXISTS idx_runs_active_component
ON runs(component_release_id)
WHERE kind='component_test' AND status IN ('awaiting_approval','queued','running');

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
CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_one_active_retry_root ON runs(retry_root_run_id) WHERE retry_root_run_id IS NOT NULL AND status IN ('awaiting_approval','queued','running');

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

-- The index retains rowid order within each Run, matching execution order.
CREATE INDEX IF NOT EXISTS idx_run_steps_run ON run_steps(run_id);

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
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_dependencies_publication_update
AFTER UPDATE ON component_dependencies
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_dependencies_publication_delete
AFTER DELETE ON component_dependencies
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS action_definitions_publication_insert
AFTER INSERT ON action_definitions
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS action_definitions_publication_update
AFTER UPDATE ON action_definitions
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS action_definitions_publication_delete
AFTER DELETE ON action_definitions
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_artifacts_publication_insert
AFTER INSERT ON component_release_artifacts
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_artifacts_publication_update
AFTER UPDATE OF release_id,alias,filename,sha256 ON component_release_artifacts
WHEN OLD.release_id<>NEW.release_id OR OLD.alias<>NEW.alias OR OLD.filename<>NEW.filename OR OLD.sha256<>NEW.sha256
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_artifacts_publication_delete
AFTER DELETE ON component_release_artifacts
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_images_publication_insert
AFTER INSERT ON component_release_images
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_images_publication_update
AFTER UPDATE OF release_id,logical_name,digest ON component_release_images
WHEN OLD.release_id<>NEW.release_id OR OLD.logical_name<>NEW.logical_name OR OLD.digest<>NEW.digest
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=NEW.release_id AND NEW.release_id<>OLD.release_id;
END;

CREATE TRIGGER IF NOT EXISTS component_images_publication_delete
AFTER DELETE ON component_release_images
BEGIN
  UPDATE component_releases SET publication_generation=publication_generation+1,review_status=CASE WHEN status='draft' THEN 'not_submitted' ELSE review_status END,review_contract_digest=CASE WHEN status='draft' THEN '' ELSE review_contract_digest END,candidate=CASE WHEN status='draft' THEN 0 ELSE candidate END WHERE id=OLD.release_id;
END;

CREATE TABLE IF NOT EXISTS run_retention_policy (
 id INTEGER PRIMARY KEY CHECK(id=1),auto_archive INTEGER NOT NULL DEFAULT 0 CHECK(auto_archive IN (0,1)),auto_cleanup INTEGER NOT NULL DEFAULT 0 CHECK(auto_cleanup IN (0,1)),archive_days INTEGER NOT NULL DEFAULT 90 CHECK(archive_days BETWEEN 1 AND 36500),cleanup_days INTEGER NOT NULL DEFAULT 90 CHECK(cleanup_days BETWEEN 1 AND 36500),last_scan_at TEXT,last_scan_result TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO run_retention_policy(id) VALUES(1);
CREATE TABLE IF NOT EXISTS run_archive_tasks (
 run_id TEXT PRIMARY KEY REFERENCES runs(id),status TEXT NOT NULL CHECK(status IN ('queued','running','archived','failed')),source TEXT NOT NULL CHECK(source IN ('manual','automatic')),actor_id TEXT NOT NULL,error_text TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL,lease_until TEXT,lease_token TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS run_archive_files (
 run_id TEXT PRIMARY KEY REFERENCES runs(id),relative_path TEXT NOT NULL UNIQUE,format_version TEXT NOT NULL,size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),sha256 TEXT NOT NULL,source_digest TEXT NOT NULL,log_count INTEGER NOT NULL,archived_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_archive_tasks_state ON run_archive_tasks(status,updated_at);
CREATE INDEX IF NOT EXISTS idx_runs_retention_scan ON runs(status,finished_at,id) WHERE finished_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_cleanup_results ON audit_events(created_at DESC) WHERE action IN ('run.cleaned','run.cleanup_skipped','run.cleanup_failed');
CREATE TABLE IF NOT EXISTS run_cleanup_history (
 id TEXT PRIMARY KEY,kind TEXT NOT NULL,status TEXT NOT NULL CHECK(status='failed'),requested_by TEXT NOT NULL,environment_id TEXT NOT NULL REFERENCES environments(id),environment_revision_id TEXT NOT NULL REFERENCES environment_revisions(id),component_release_id TEXT REFERENCES component_releases(id),scenario_revision_id TEXT REFERENCES scenario_revisions(id),action_kind TEXT NOT NULL,created_at TEXT NOT NULL,finished_at TEXT NOT NULL,cleaned_at TEXT NOT NULL,actor_id TEXT NOT NULL,reason TEXT NOT NULL,identity_json TEXT NOT NULL CHECK(json_valid(identity_json))
);
CREATE TRIGGER IF NOT EXISTS run_cleanup_history_no_update BEFORE UPDATE ON run_cleanup_history BEGIN SELECT RAISE(ABORT,'cleanup history is immutable'); END;
CREATE TRIGGER IF NOT EXISTS run_cleanup_history_no_delete BEFORE DELETE ON run_cleanup_history BEGIN SELECT RAISE(ABORT,'cleanup history is immutable'); END;
CREATE VIEW IF NOT EXISTS retained_run_history AS
 SELECT id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,retry_of_run_id,retry_root_run_id,retry_attempt,retry_start_step,error_text,created_at,started_at,finished_at FROM runs
 UNION ALL SELECT id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,0,identity_json,'',NULL,NULL,0,0,'记录已按保留策略清理',created_at,NULL,finished_at FROM run_cleanup_history;
CREATE TABLE IF NOT EXISTS run_retention_cursors(status TEXT PRIMARY KEY,finished_at TEXT NOT NULL,run_id TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS archived_run_no_log_insert BEFORE INSERT ON run_logs WHEN EXISTS(SELECT 1 FROM run_archive_files WHERE run_id=NEW.run_id) BEGIN SELECT RAISE(ABORT,'Run logs have been archived'); END;
CREATE TRIGGER IF NOT EXISTS archived_run_no_update BEFORE UPDATE ON runs WHEN EXISTS(SELECT 1 FROM run_archive_files WHERE run_id=OLD.id) BEGIN SELECT RAISE(ABORT,'archived Run core is immutable'); END;
CREATE TRIGGER IF NOT EXISTS archived_run_no_step_update BEFORE UPDATE ON run_steps WHEN EXISTS(SELECT 1 FROM run_archive_files WHERE run_id=OLD.run_id) BEGIN SELECT RAISE(ABORT,'archived Run steps are immutable'); END;
CREATE TRIGGER IF NOT EXISTS archived_run_no_step_insert BEFORE INSERT ON run_steps WHEN EXISTS(SELECT 1 FROM run_archive_files WHERE run_id=NEW.run_id) BEGIN SELECT RAISE(ABORT,'archived Run steps are immutable'); END;
CREATE TRIGGER IF NOT EXISTS archived_run_no_approval_update BEFORE UPDATE ON approvals WHEN EXISTS(SELECT 1 FROM run_archive_files WHERE run_id=OLD.run_id) BEGIN SELECT RAISE(ABORT,'archived Run approvals are immutable'); END;
