package store

import "embed"

const (
	// schemaContract is the current database contract accepted by this V1 build.
	schemaContract = "clusterforge-v1-20260829-ssh-connectivity"
	// previousSchemaContract is the single exact predecessor supported by the
	// additive SSH-check migration. Every other populated contract fails closed.
	previousSchemaContract = "clusterforge-v1-20260828-environment-lifecycle"
)

//go:embed schema.sql
var schemaFiles embed.FS
