package store

import "embed"

// schemaContract is the only database contract accepted by this V1 build.
// Populated databases with any other contract fail closed.
const schemaContract = "clusterforge-v1-20260828-publication-guards"

//go:embed schema.sql
var schemaFiles embed.FS
