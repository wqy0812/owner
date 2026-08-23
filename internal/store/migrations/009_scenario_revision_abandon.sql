ALTER TABLE scenario_revisions
ADD COLUMN abandoned_at TEXT;

UPDATE scenario_revisions
SET status='deprecated',
    deprecated_at=COALESCE(deprecated_at, strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    abandoned_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE status IN ('draft','testing','test_passed')
  AND id NOT IN (
    SELECT COALESCE(
      NULLIF(scenarios.current_revision_id, ''),
      (SELECT candidate.id FROM scenario_revisions AS candidate WHERE candidate.scenario_id=scenarios.id ORDER BY candidate.revision DESC LIMIT 1)
    )
    FROM scenarios
  );

CREATE UNIQUE INDEX idx_scenario_revisions_one_active
ON scenario_revisions(scenario_id)
WHERE status IN ('draft','testing','test_passed');
