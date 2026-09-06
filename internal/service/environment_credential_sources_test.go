package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCredentialSourcesAggregateDeclarationsWithoutLeakingHiddenDrafts(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	publicID := revision.Graph.Nodes[0].ReleaseID
	if _, err := db.DB().ExecContext(ctx, `UPDATE action_definitions SET required_credentials_json='["shared_token","shared_token"]' WHERE release_id=?`, publicID); err != nil {
		t.Fatal(err)
	}
	// Acceptance definitions are owner-visible drafts until publication.
	revision, _ = db.GetScenarioRevision(ctx, revision.ID)
	expected := domain.ScenarioRevisionSpecDigest(revision)
	revision.AcceptanceJobs[0].RequiredCredentials = []string{"shared_token"}
	if err := db.SaveScenarioRevisionDefinition(ctx, revision, expected); err != nil {
		t.Fatal(err)
	}
	hidden := domain.ComponentRelease{ID: "hidden-credential-draft", ComponentID: "component-1", Version: "private", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: time.Now(), Actions: []domain.ActionDefinition{{ID: "hidden-action", Name: "Private action title", Kind: domain.ActionInstall, HostGroup: "test_nodes", RequiredCredentials: []string{"private_token"}, TimeoutSeconds: 60, RiskLevel: domain.RiskLow}}}
	if err := db.CreateComponentRelease(ctx, hidden); err != nil {
		t.Fatal(err)
	}
	sources, err := p.Environments().CredentialSources(ctx, owner, env.ID, env.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	componentCount, acceptanceCount := 0, 0
	for _, source := range sources {
		if source.Name == "private_token" {
			t.Fatal("hidden draft credential leaked")
		}
		key := source.Kind + source.ReleaseID + source.RevisionID + source.ActionID + source.Name
		if keys[key] {
			t.Fatalf("duplicate source: %+v", source)
		}
		keys[key] = true
		if source.Name == "shared_token" {
			if source.Kind == "component_action" {
				componentCount++
			}
			if source.Kind == "scenario_acceptance" {
				acceptanceCount++
			}
		}
	}
	if componentCount < 2 || acceptanceCount != 1 {
		t.Fatalf("lost action or acceptance provenance: %+v", sources)
	}
	encoded, _ := json.Marshal(sources)
	if len(encoded) == 0 {
		t.Fatal("empty declaration response")
	}
	outsider := domain.User{ID: "viewer", Role: domain.RoleEnvironmentOwner}
	sources, err = p.Environments().CredentialSources(ctx, outsider, env.ID, env.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Kind == "scenario_acceptance" {
			t.Fatal("unpublished acceptance leaked")
		}
	}
	other := copyScenarioTestEnvironment(t, p, env, "another-source-env")
	if _, err = p.Environments().CredentialSources(ctx, owner, env.ID, other.CurrentRevisionID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-environment revision accepted: %v", err)
	}
}
