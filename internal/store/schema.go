package store

import "embed"

// schemaContract identifies the current database contract. Additive migrations
// may name an exact predecessor; unknown or non-additive historical contracts
// remain unsupported and fail closed.
const (
	legacySchemaContract     = "first-version-20260824"
	idempotentSchemaContract = "first-version-20260824-idempotent-actions"
	candidateSchemaContract  = "first-version-20260824-candidate-releases"
	evidenceSchemaContract   = "first-version-20260825-candidate-evidence"
	schemaContract           = "first-version-20260825-safety-fences"
)

//go:embed schema.sql
var schemaFiles embed.FS
