CREATE TABLE component_image_mirrors (
  target_registry TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  target_ref TEXT NOT NULL,
  target_digest TEXT NOT NULL,
  mirrored_at TEXT NOT NULL,
  PRIMARY KEY(target_registry, source_digest)
);
