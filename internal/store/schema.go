package store

import "embed"

const (
	// schemaContract is the current database contract accepted by this V1 build.
	schemaContract = "clusterforge-v1-20260905-adaptation-run-archive"
	// CurrentSchemaContract is exposed for offline tools which must emit data for
	// exactly the schema accepted by this build. Runtime databases must already
	// use this exact contract.
	CurrentSchemaContract = schemaContract
)

//go:embed schema.sql
var schemaFiles embed.FS
