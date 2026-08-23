ALTER TABLE environment_revisions
ADD COLUMN variables_json TEXT NOT NULL DEFAULT '{}';

ALTER TABLE component_image_builds
ADD COLUMN environment_id TEXT REFERENCES environments(id);

ALTER TABLE component_image_builds
ADD COLUMN environment_revision_id TEXT REFERENCES environment_revisions(id);

CREATE INDEX idx_component_image_builds_environment_created
ON component_image_builds(environment_id, created_at DESC);
