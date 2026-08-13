PRAGMA defer_foreign_keys = ON;

-- Replace the fixed Kubernetes 1.17.5 demo contract. This is intentionally a
-- first-version cutover: runs and audit records locked to the four legacy
-- delivery stages are removed instead of being dual-read or aliased.
DROP TRIGGER IF EXISTS audit_events_no_update;
DROP TRIGGER IF EXISTS audit_events_no_delete;

DELETE FROM audit_events
WHERE resource_id IN (
  'component-k8s-1.17.5-cert','component-k8s-1.17.5-etcd',
  'component-k8s-1.17.5-master','component-k8s-1.17.5-node',
  'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
  'release-k8s-1.17.5-master','release-k8s-1.17.5-node',
  'scenario-k8s-1.17.5','scenario-k8s-1.17.5-r1',
  'environment-k8s-1.17.5-template','environment-k8s-1.17.5-template-r1'
)
OR resource_id IN (
  SELECT id FROM runs
  WHERE component_release_id IN (
    'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
    'release-k8s-1.17.5-master','release-k8s-1.17.5-node'
  ) OR scenario_revision_id='scenario-k8s-1.17.5-r1'
)
OR metadata_json LIKE '%k8s-1.17.5-%';

DELETE FROM notifications
WHERE resource_url LIKE '%k8s-1.17.5%'
   OR payload_json LIKE '%k8s-1.17.5%';

DELETE FROM runs
WHERE component_release_id IN (
  'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
  'release-k8s-1.17.5-master','release-k8s-1.17.5-node'
) OR scenario_revision_id='scenario-k8s-1.17.5-r1';

DELETE FROM scenario_revisions
WHERE id='scenario-k8s-1.17.5-r1' OR scenario_id='scenario-k8s-1.17.5';
DELETE FROM scenarios WHERE id='scenario-k8s-1.17.5';
DELETE FROM environment_revisions
WHERE id='environment-k8s-1.17.5-template-r1'
   OR environment_id='environment-k8s-1.17.5-template';
DELETE FROM environments WHERE id='environment-k8s-1.17.5-template';

DELETE FROM component_dependencies
WHERE release_id IN (
  'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
  'release-k8s-1.17.5-master','release-k8s-1.17.5-node'
) OR upstream_component_id IN (
  'component-k8s-1.17.5-cert','component-k8s-1.17.5-etcd',
  'component-k8s-1.17.5-master','component-k8s-1.17.5-node'
) OR upstream_release_id IN (
  'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
  'release-k8s-1.17.5-master','release-k8s-1.17.5-node'
);
DELETE FROM action_definitions
WHERE release_id IN (
  'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
  'release-k8s-1.17.5-master','release-k8s-1.17.5-node'
);
DELETE FROM component_releases
WHERE id IN (
  'release-k8s-1.17.5-cert','release-k8s-1.17.5-etcd',
  'release-k8s-1.17.5-master','release-k8s-1.17.5-node'
);
DELETE FROM components
WHERE id IN (
  'component-k8s-1.17.5-cert','component-k8s-1.17.5-etcd',
  'component-k8s-1.17.5-master','component-k8s-1.17.5-node'
);

-- SQLite cannot widen an existing CHECK constraint in place. Rebuild only the
-- catalog parent table while deferred foreign keys keep child references valid.
CREATE TABLE components_v4 (
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

INSERT INTO components_v4(
  id,slug,name,description,owner_id,created_at,updated_at,
  layer,category,component_kind,requiredness
)
SELECT id,slug,name,description,owner_id,created_at,updated_at,
       layer,category,component_kind,requiredness
FROM components;

DROP TABLE components;
ALTER TABLE components_v4 RENAME TO components;

CREATE TRIGGER audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
  SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TRIGGER audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
  SELECT RAISE(ABORT, 'audit events are append-only');
END;

