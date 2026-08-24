package store

import "embed"

// schemaContract identifies the only database contract supported by the first
// project version. Change it whenever the first-version schema changes; test
// databases created with an older contract must be recreated, not upgraded.
const schemaContract = "first-version-20260824"

//go:embed schema.sql
var schemaFiles embed.FS
