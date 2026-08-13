ALTER TABLE components ADD COLUMN layer TEXT NOT NULL DEFAULT 'platform_extension'
  CHECK (layer IN ('host_foundation','runtime_state','orchestration_core','cluster_service','observability_management','platform_extension'));
ALTER TABLE components ADD COLUMN category TEXT NOT NULL DEFAULT 'platform'
  CHECK (
    (layer='host_foundation' AND category IN ('preflight','bootstrap','security')) OR
    (layer='runtime_state' AND category IN ('runtime','state_store')) OR
    (layer='orchestration_core' AND category IN ('control_plane','worker','network')) OR
    (layer='cluster_service' AND category IN ('network','dns','ingress','storage')) OR
    (layer='observability_management' AND category IN ('observability','node_management')) OR
    (layer='platform_extension' AND category IN ('platform','autoscaling'))
  );
ALTER TABLE components ADD COLUMN component_kind TEXT NOT NULL DEFAULT 'software'
  CHECK (component_kind IN ('software','software_bundle','delivery_stage'));
ALTER TABLE components ADD COLUMN requiredness TEXT NOT NULL DEFAULT 'optional'
  CHECK (requiredness IN ('core_required','profile_required','optional'));

-- The localhost Demo Agent and Policy Bundle were non-product fixtures. Remove
-- their complete persisted lifecycle before deleting the catalog resources.
DROP TRIGGER IF EXISTS audit_events_no_update;
DROP TRIGGER IF EXISTS audit_events_no_delete;

DELETE FROM audit_events
WHERE resource_id IN (
  'component-demo-agent','component-demo-policy-bundle',
  'release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0',
  'scenario-demo-agent','scenario-demo-agent-r1','scenario-demo-agent-r2',
  'environment-local','environment-local-r1','newplatform-demo'
)
OR resource_id IN (
  SELECT id FROM runs
  WHERE component_release_id IN ('release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0')
     OR scenario_revision_id IN ('scenario-demo-agent-r1','scenario-demo-agent-r2')
)
OR metadata_json LIKE '%component-demo-agent%'
OR metadata_json LIKE '%demo-node-agent%'
OR metadata_json LIKE '%scenario-demo-agent%';

DELETE FROM notifications
WHERE resource_url LIKE '%component-demo-agent%'
   OR resource_url LIKE '%scenario-demo-agent%'
   OR payload_json LIKE '%component-demo-agent%'
   OR payload_json LIKE '%component-demo-policy-bundle%'
   OR payload_json LIKE '%scenario-demo-agent%';

DELETE FROM runs
WHERE component_release_id IN ('release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0')
   OR scenario_revision_id IN ('scenario-demo-agent-r1','scenario-demo-agent-r2');

DELETE FROM scenario_revisions WHERE id IN ('scenario-demo-agent-r1','scenario-demo-agent-r2') OR scenario_id='scenario-demo-agent';
DELETE FROM scenarios WHERE id='scenario-demo-agent';
DELETE FROM environment_revisions WHERE id='environment-local-r1' OR environment_id='environment-local';
DELETE FROM environments WHERE id='environment-local';

DELETE FROM component_dependencies
WHERE release_id IN ('release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0')
   OR upstream_component_id IN ('component-demo-agent','component-demo-policy-bundle')
   OR upstream_release_id IN ('release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0');
DELETE FROM action_definitions WHERE release_id IN ('release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0');
DELETE FROM component_releases WHERE id IN ('release-demo-agent-1.0.0','release-demo-agent-1.1.0','release-demo-policy-bundle-1.0');
DELETE FROM components WHERE id IN ('component-demo-agent','component-demo-policy-bundle');

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

UPDATE components SET layer='host_foundation', category='security', component_kind='delivery_stage', requiredness='profile_required'
WHERE id IN ('component-bke-cert','component-k8s-1.17.5-cert');
UPDATE components SET layer='host_foundation', category='bootstrap', component_kind='delivery_stage', requiredness='profile_required'
WHERE id='component-bke-bootstrap';

UPDATE components SET layer='runtime_state', category='runtime', component_kind='software', requiredness='profile_required'
WHERE id='component-containerd';
UPDATE components SET layer='runtime_state', category='state_store', component_kind='software', requiredness='core_required'
WHERE id='component-etcd';
UPDATE components SET layer='runtime_state', category='state_store', component_kind='delivery_stage', requiredness='profile_required'
WHERE id='component-k8s-1.17.5-etcd';

UPDATE components SET layer='orchestration_core', category='control_plane', component_kind='software_bundle', requiredness='core_required'
WHERE id='component-kubernetes';
UPDATE components SET layer='orchestration_core', category='network', component_kind='software', requiredness='profile_required'
WHERE id='component-kube-proxy';
UPDATE components SET layer='orchestration_core', category='control_plane', component_kind='delivery_stage', requiredness='profile_required'
WHERE id='component-k8s-1.17.5-master';
UPDATE components SET layer='orchestration_core', category='worker', component_kind='delivery_stage', requiredness='profile_required'
WHERE id='component-k8s-1.17.5-node';

UPDATE components SET layer='cluster_service', category='network', component_kind='software', requiredness='profile_required'
WHERE id='component-calico';
UPDATE components SET layer='cluster_service', category='dns', component_kind='software', requiredness='core_required'
WHERE id='component-coredns';

UPDATE components SET layer='platform_extension', category='platform', component_kind='delivery_stage', requiredness='profile_required'
WHERE id IN ('component-bke-common','component-bke-master');
UPDATE components SET layer='platform_extension', category='platform', component_kind='software_bundle', requiredness='profile_required'
WHERE id='component-bke-addon';

UPDATE component_releases SET release_type='bundle'
WHERE component_id IN ('component-kubernetes','component-bke-addon');
