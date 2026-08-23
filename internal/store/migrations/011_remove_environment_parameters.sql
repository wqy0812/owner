UPDATE scenario_revisions
SET status = 'deprecated',
    abandoned_at = CASE WHEN status IN ('draft','testing','test_passed') THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') ELSE abandoned_at END,
    deprecated_at = COALESCE(deprecated_at, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
WHERE EXISTS (
  SELECT 1 FROM json_each(json_extract(scenario_revisions.graph_json, '$.nodes')) AS node
  WHERE EXISTS (SELECT 1 FROM json_each(json_extract(node.value, '$.bindings')))
     OR json_extract(node.value, '$.releaseId') IN (
       SELECT id FROM component_releases
       WHERE EXISTS (
         SELECT 1 FROM json_each(component_releases.parameters_json) AS parameter
         WHERE COALESCE(json_extract(parameter.value, '$.environmentPath'), '') <> ''
       )
     )
);

UPDATE component_releases
SET status='deprecated', deprecated_at=COALESCE(deprecated_at, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
WHERE EXISTS (
  SELECT 1 FROM json_each(component_releases.parameters_json) AS parameter
  WHERE COALESCE(json_extract(parameter.value, '$.environmentPath'), '') <> ''
);

UPDATE component_releases
SET parameters_json = COALESCE((SELECT json_group_array(json_remove(parameter.value, '$.environmentPath')) FROM json_each(component_releases.parameters_json) AS parameter), '[]');

UPDATE scenario_revisions
SET graph_json = json_set(graph_json, '$.nodes', COALESCE((SELECT json_group_array(json_remove(node.value, '$.bindings')) FROM json_each(json_extract(scenario_revisions.graph_json, '$.nodes')) AS node), json('[]')));

ALTER TABLE environment_revisions DROP COLUMN parameters_json;
