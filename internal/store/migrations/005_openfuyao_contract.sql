PRAGMA defer_foreign_keys = ON;

ALTER TABLE action_definitions
ADD COLUMN required_credentials_json TEXT NOT NULL DEFAULT '[]';

-- OpenFuyao is seeded demo data. Replace the incomplete contract directly;
-- do not retain aliases, dual reads, or runs locked to the broken releases.
DROP TRIGGER IF EXISTS audit_events_no_update;
DROP TRIGGER IF EXISTS audit_events_no_delete;

DELETE FROM audit_events
WHERE resource_id IN (
  'component-bke-cert','component-bke-bootstrap','component-bke-common',
  'component-bke-addon','component-bke-master','component-bke-nodes',
  'release-bke-cert-25.12','release-bke-bootstrap-25.12',
  'release-bke-common-25.12','release-bke-addon-25.12',
  'release-bke-master-25.12','release-bke-nodes-25.12',
  'scenario-openfuyao','scenario-openfuyao-r1',
  'scenario-openfuyao-work-cluster','scenario-openfuyao-work-cluster-r1',
  'scenario-openfuyao-work-nodes','scenario-openfuyao-work-nodes-r1',
  'environment-openfuyao-template','environment-openfuyao-template-r1'
)
OR resource_id IN (
  SELECT id FROM runs
  WHERE environment_id='environment-openfuyao-template'
     OR scenario_revision_id IN (
       'scenario-openfuyao-r1','scenario-openfuyao-work-cluster-r1',
       'scenario-openfuyao-work-nodes-r1'
     )
     OR component_release_id IN (
       'release-bke-cert-25.12','release-bke-bootstrap-25.12',
       'release-bke-common-25.12','release-bke-addon-25.12',
       'release-bke-master-25.12','release-bke-nodes-25.12'
     )
)
OR metadata_json LIKE '%scenario-openfuyao%'
OR metadata_json LIKE '%component-bke-cert%'
OR metadata_json LIKE '%component-bke-bootstrap%'
OR metadata_json LIKE '%component-bke-common%'
OR metadata_json LIKE '%component-bke-addon%'
OR metadata_json LIKE '%component-bke-master%'
OR metadata_json LIKE '%component-bke-nodes%'
OR metadata_json LIKE '%release-bke-cert-25.12%'
OR metadata_json LIKE '%release-bke-bootstrap-25.12%'
OR metadata_json LIKE '%release-bke-common-25.12%'
OR metadata_json LIKE '%release-bke-addon-25.12%'
OR metadata_json LIKE '%release-bke-master-25.12%'
OR metadata_json LIKE '%release-bke-nodes-25.12%';

DELETE FROM notifications
WHERE resource_url LIKE '%scenario-openfuyao%'
   OR payload_json LIKE '%scenario-openfuyao%'
   OR payload_json LIKE '%component-bke-cert%'
   OR payload_json LIKE '%component-bke-bootstrap%'
   OR payload_json LIKE '%component-bke-common%'
   OR payload_json LIKE '%component-bke-addon%'
   OR payload_json LIKE '%component-bke-master%'
   OR payload_json LIKE '%component-bke-nodes%'
   OR payload_json LIKE '%release-bke-cert-25.12%'
   OR payload_json LIKE '%release-bke-bootstrap-25.12%'
   OR payload_json LIKE '%release-bke-common-25.12%'
   OR payload_json LIKE '%release-bke-addon-25.12%'
   OR payload_json LIKE '%release-bke-master-25.12%'
   OR payload_json LIKE '%release-bke-nodes-25.12%';

DELETE FROM runs
WHERE environment_id='environment-openfuyao-template'
   OR scenario_revision_id IN (
     'scenario-openfuyao-r1','scenario-openfuyao-work-cluster-r1',
     'scenario-openfuyao-work-nodes-r1'
   )
   OR component_release_id IN (
     'release-bke-cert-25.12','release-bke-bootstrap-25.12',
     'release-bke-common-25.12','release-bke-addon-25.12',
     'release-bke-master-25.12','release-bke-nodes-25.12'
   );

DELETE FROM scenario_revisions
WHERE scenario_id IN (
  'scenario-openfuyao','scenario-openfuyao-work-cluster',
  'scenario-openfuyao-work-nodes'
);
DELETE FROM scenarios
WHERE id IN (
  'scenario-openfuyao','scenario-openfuyao-work-cluster',
  'scenario-openfuyao-work-nodes'
);

DELETE FROM environment_revisions
WHERE environment_id='environment-openfuyao-template';
DELETE FROM environments WHERE id='environment-openfuyao-template';

DELETE FROM component_dependencies
WHERE release_id IN (
     'release-bke-cert-25.12','release-bke-bootstrap-25.12',
     'release-bke-common-25.12','release-bke-addon-25.12',
     'release-bke-master-25.12','release-bke-nodes-25.12'
   )
   OR upstream_component_id IN (
     'component-bke-cert','component-bke-bootstrap','component-bke-common',
     'component-bke-addon','component-bke-master','component-bke-nodes'
   )
   OR upstream_release_id IN (
     'release-bke-cert-25.12','release-bke-bootstrap-25.12',
     'release-bke-common-25.12','release-bke-addon-25.12',
     'release-bke-master-25.12','release-bke-nodes-25.12'
   );
DELETE FROM action_definitions WHERE release_id IN (
  'release-bke-cert-25.12','release-bke-bootstrap-25.12',
  'release-bke-common-25.12','release-bke-addon-25.12',
  'release-bke-master-25.12','release-bke-nodes-25.12'
);
DELETE FROM component_releases WHERE id IN (
  'release-bke-cert-25.12','release-bke-bootstrap-25.12',
  'release-bke-common-25.12','release-bke-addon-25.12',
  'release-bke-master-25.12','release-bke-nodes-25.12'
);
DELETE FROM components
WHERE id IN (
  'component-bke-cert','component-bke-bootstrap','component-bke-common',
  'component-bke-addon','component-bke-master','component-bke-nodes'
);

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
