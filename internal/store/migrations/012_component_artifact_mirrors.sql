CREATE TABLE component_artifact_mirrors (
  target_file_station TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  source_file_station TEXT NOT NULL,
  mirrored_at TEXT NOT NULL,
  PRIMARY KEY(target_file_station, relative_path, sha256)
);
