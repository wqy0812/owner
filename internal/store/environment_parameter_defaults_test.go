package store

import (
	"context"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestOptionalDefaultTablePreservesExistingDefinitionsOnStartup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "existing.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.User{ID: "defaults-admin", Name: "Platform Owner", Role: domain.RolePlatformAdmin, CreatedAt: testNow}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	definition := domain.EnvironmentParameterDefinition{ID: "defaults-definition", Key: "defaults_key", Label: "Enabled", Description: "Switch", Type: domain.ParameterTypeBoolean, CreatedBy: owner.ID, CreatedAt: testNow}
	if err := db.UpsertEnvironmentParameterDefinition(ctx, definition); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, "DROP TABLE environment_parameter_defaults"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := db.GetEnvironmentParameterDefinition(ctx, definition.ID)
	if err != nil || original.DefaultValue != nil || original.Label != definition.Label {
		t.Fatalf("retained definition=%#v err=%v", original, err)
	}
	audit := domain.AuditEvent{ID: "defaults-audit", ActorID: owner.ID, Action: "default_updated", ResourceType: "environment_parameter_definition", ResourceID: definition.ID, CreatedAt: testNow}
	if _, err := db.UpdateEnvironmentParameterDefault(ctx, definition.ID, false, audit); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restored, err := db.GetEnvironmentParameterDefinition(ctx, definition.ID)
	if err != nil || restored.DefaultValue != false {
		t.Fatalf("persistent false=%#v err=%v", restored, err)
	}
}
