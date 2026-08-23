CREATE TABLE component_release_artifacts (
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

CREATE INDEX idx_component_release_artifacts_release
ON component_release_artifacts(release_id, alias);
