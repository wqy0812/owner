package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func importGovernanceFixture(t *testing.T) (*Platform, domain.User, domain.Environment, EnvironmentExportDocument, domain.ComponentRelease) {
	t.Helper()
	p, owner, environment := maintenanceTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	c := domain.Component{ID: "import-contract", Slug: "import-contract", Name: "Import contract", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now}
	if err := p.store.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	definition := domain.EnvironmentParameterDefinition{ID: "import-count", Key: "count", Label: "Count", Type: domain.ParameterTypeInteger, Enum: []any{2, 4}, CreatedBy: seed.PlatformAdminID, CreatedAt: now}
	if err := p.store.UpsertEnvironmentParameterDefinition(ctx, definition); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "import-contract-r1", ComponentID: c.ID, LineID: "import-line", LineName: "Import", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{
		{Name: "count", Description: "Instance count", Type: domain.ParameterTypeInteger, Enum: definition.Enum, Required: true, Modifiable: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderEnvironmentOwner, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingGlobal, DefinitionID: definition.ID}},
		{Name: "region", Description: "Placement region", Type: domain.ParameterTypeString, Enum: []any{"north", "south"}, MinLength: 3, Required: true, Modifiable: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderEnvironmentOwner, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}},
	}}
	if err := p.store.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	doc, err := p.ExportEnvironmentRevision(ctx, owner, environment.ID, environment.CurrentRevisionID, false)
	if err != nil {
		t.Fatal(err)
	}
	doc.Snapshot.Parameters = map[string]any{domain.EnvironmentParameterValueKey(r.ID, r.Parameters[0]): 2, domain.EnvironmentParameterValueKey(r.ID, r.Parameters[1]): "north"}
	return p, owner, environment, doc, r
}

func TestEnvironmentImportGovernance(t *testing.T) {
	for _, kind := range []string{"new", "existing"} {
		for _, name := range []string{"valid", "partial", "unknown_parameter", "wrong_type", "fractional_integer", "wrong_enum", "short_string", "unknown_variable", "sensitive_variable", "credential_collision", "nested_sensitive"} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				p, owner, environment, doc, r := importGovernanceFixture(t)
				countKey := domain.EnvironmentParameterValueKey(r.ID, r.Parameters[0])
				regionKey := domain.EnvironmentParameterValueKey(r.ID, r.Parameters[1])
				switch name {
				case "partial":
					doc.Snapshot.Parameters = map[string]any{}
				case "unknown_parameter":
					doc.Snapshot.Parameters["unmanaged"] = "value"
				case "wrong_type":
					doc.Snapshot.Parameters[countKey] = "2"
				case "fractional_integer":
					doc.Snapshot.Parameters[countKey] = 2.5
				case "wrong_enum":
					doc.Snapshot.Parameters[countKey] = 3
				case "short_string":
					doc.Snapshot.Parameters[regionKey] = "n"
				case "unknown_variable":
					doc.Snapshot.Variables["UNMANAGED"] = "value"
				case "sensitive_variable":
					doc.Snapshot.Variables["PRIVATE_KEY"] = "value"
				case "credential_collision":
					doc.Snapshot.CredentialRefs = []domain.CredentialRef{{Name: "IMAGE_REGISTRY", Kind: "envVarRef", Reference: "TEST_REGISTRY"}}
				case "nested_sensitive":
					doc.Snapshot.Parameters["unmanaged"] = map[string]any{"password": "value"}
				}
				input := EnvironmentImportRequest{Document: doc, Target: EnvironmentImportTarget{Kind: kind, Name: "Imported", EnvironmentID: environment.ID}, ChangeReason: "Import validated settings"}
				ctx := context.Background()
				before, _ := p.store.ListEnvironments(ctx)
				plan, err := p.PreviewEnvironmentImport(ctx, owner, input)
				valid := name == "valid" || name == "partial"
				if !valid {
					if !errors.Is(err, domain.ErrInvalid) {
						t.Fatalf("preview error=%v", err)
					}
					input.ExpectedPlanDigest = "invalid-plan"
					if _, err := p.ImportEnvironment(ctx, owner, input); !errors.Is(err, domain.ErrInvalid) {
						t.Fatalf("commit error=%v", err)
					}
					after, _ := p.store.ListEnvironments(ctx)
					current, _ := p.store.GetEnvironment(ctx, environment.ID, true)
					if len(after) != len(before) || len(current.Revisions) != 1 {
						t.Fatal("invalid import left partial records")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if plan.ParameterCount != len(doc.Snapshot.Parameters) {
					t.Fatalf("parameter count=%d", plan.ParameterCount)
				}
				input.ExpectedPlanDigest = plan.PlanDigest
				imported, err := p.ImportEnvironment(ctx, owner, input)
				if err != nil {
					t.Fatal(err)
				}
				exported, err := p.ExportEnvironmentRevision(ctx, owner, imported.ID, imported.CurrentRevisionID, false)
				if err != nil {
					t.Fatal(err)
				}
				if len(exported.Snapshot.Parameters) != len(doc.Snapshot.Parameters) {
					t.Fatalf("parameters lost: %+v", exported.Snapshot.Parameters)
				}
				if name == "valid" && (exported.Snapshot.Parameters[regionKey] != "north" || !domain.ParameterValuesEqual(exported.Snapshot.Parameters[countKey], 2)) {
					t.Fatalf("round trip values=%+v", exported.Snapshot.Parameters)
				}
			})
		}
	}
}

func TestEnvironmentImportRevalidatesChangedCatalog(t *testing.T) {
	for _, kind := range []string{"new", "existing"} {
		for _, change := range []string{"removed_field", "removed_variable"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				p, owner, environment, doc, r := importGovernanceFixture(t)
				ctx := context.Background()
				if change == "removed_variable" {
					if err := p.store.UpsertEnvironmentVariableDefinition(ctx, domain.EnvironmentVariableDefinition{ID: "import-variable", Name: "IMPORT_ZONE", Label: "Zone", CreatedBy: seed.PlatformAdminID, CreatedAt: time.Now().UTC()}); err != nil {
						t.Fatal(err)
					}
					doc.Snapshot.Variables["IMPORT_ZONE"] = "north"
				}
				input := EnvironmentImportRequest{Document: doc, Target: EnvironmentImportTarget{Kind: kind, Name: "Imported", EnvironmentID: environment.ID}, ChangeReason: "Import"}
				plan, err := p.PreviewEnvironmentImport(ctx, owner, input)
				if err != nil {
					t.Fatal(err)
				}
				input.ExpectedPlanDigest = plan.PlanDigest
				if change == "removed_field" {
					r.Parameters = nil
					if err := p.store.UpdateDraftRelease(ctx, r); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := p.store.DeleteEnvironmentVariableDefinition(ctx, "import-variable", domain.AuditEvent{ID: "remove-import-variable", ActorID: seed.PlatformAdminID, Action: "variable.deleted", ResourceType: "variable", ResourceID: "import-variable", CreatedAt: time.Now().UTC()}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := p.ImportEnvironment(ctx, owner, input); err == nil {
					t.Fatal("stale import accepted")
				}
				current, err := p.store.GetEnvironment(ctx, environment.ID, true)
				if err != nil || len(current.Revisions) != 1 {
					t.Fatalf("partial import=%+v err=%v", current, err)
				}
			})
		}
	}
}
