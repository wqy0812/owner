package service

import (
	"context"
	"errors"
	"testing"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type creationAudit struct{ actions []string }

func (a *creationAudit) Record(_ context.Context, _ domain.User, action, _, _ string, _ map[string]any) {
	a.actions = append(a.actions, action)
}

type componentCreationStore struct {
	catalogStore
	saved domain.Component
	err   error
}

func (s *componentCreationStore) CreateComponent(_ context.Context, c domain.Component) error {
	s.saved = c
	return s.err
}

type scenarioCreationStore struct {
	scenariosStore
	saved    domain.Scenario
	revision domain.ScenarioRevision
}

func (s *scenarioCreationStore) CreateScenario(_ context.Context, value domain.Scenario, revision domain.ScenarioRevision) error {
	s.saved, s.revision = value, revision
	return nil
}

func TestCatalogCreateWithOnlyItsStoreAndAudit(t *testing.T) {
	ctx := context.Background()
	data, audit := &componentCreationStore{}, &creationAudit{}
	catalog := &CatalogService{store: data, audit: audit}
	input := domain.Component{ID: "caller-id", OwnerID: "caller-owner", Slug: "component-one", Name: "Component One", Layer: domain.LayerHostFoundation}
	if _, err := catalog.Create(ctx, domain.User{Role: domain.RoleScenarioOwner}, input); !errors.Is(err, domain.ErrForbidden) || data.saved.ID != "" {
		t.Fatalf("role gate failed: %v", err)
	}
	created, err := catalog.Create(ctx, domain.User{ID: "owner-one", Role: domain.RoleComponentOwner}, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == input.ID || created.OwnerID != "owner-one" || created.CreatedAt.IsZero() || data.saved.ID != created.ID || len(audit.actions) != 1 {
		t.Fatalf("invalid ownership or persistence: %+v", created)
	}
	data.err = errors.New("write failed")
	if _, err := catalog.Create(ctx, domain.User{Role: domain.RoleComponentOwner}, input); !errors.Is(err, data.err) || len(audit.actions) != 1 {
		t.Fatalf("failed write emitted audit: %v %v", err, audit.actions)
	}
}

func TestScenarioCreateOwnsItsInitialRevision(t *testing.T) {
	data, audit := &scenarioCreationStore{}, &creationAudit{}
	scenarios := &ScenarioService{store: data, audit: audit, catalogRules: creationCatalogRules{}}
	created, err := scenarios.Create(context.Background(), domain.User{ID: "owner-one", Role: domain.RoleScenarioOwner}, domain.Scenario{Slug: "scenario-one", Name: "Scenario One", ForkedFromScenarioID: "untrusted-source"})
	if err != nil {
		t.Fatal(err)
	}
	if created.OwnerID != "owner-one" || created.ForkedFromScenarioID != "" || created.CurrentRevisionID != data.revision.ID || data.revision.ScenarioID != created.ID || data.revision.Status != domain.RevisionDraft || len(data.revision.Graph.Nodes) != 0 || len(audit.actions) != 1 {
		t.Fatalf("invalid initial revision: %+v %+v", created, data.revision)
	}
}

type archiveGuardStore struct {
	environmentsStore
	impact store.EnvironmentLifecycleImpact
}

func (*archiveGuardStore) GetEnvironment(context.Context, string, bool) (domain.Environment, error) {
	return domain.Environment{ID: "environment-one", OwnerID: "owner-one"}, nil
}
func (s *archiveGuardStore) EnvironmentLifecycleImpact(context.Context, string) (store.EnvironmentLifecycleImpact, error) {
	return s.impact, nil
}

func TestEnvironmentArchiveGuardsNeedNoExecutionService(t *testing.T) {
	for _, impact := range []store.EnvironmentLifecycleImpact{{ActiveRunCount: 1}, {ActiveImageBuildCount: 1}, {InstallationCount: 1}} {
		environments := &EnvironmentService{store: &archiveGuardStore{impact: impact}}
		if _, err := environments.Archive(context.Background(), domain.User{ID: "owner-one", Role: domain.RoleEnvironmentOwner}, "environment-one"); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("archive passed active state %+v: %v", impact, err)
		}
	}
}

type componentPlanReads struct {
	plannerStore
	release     domain.ComponentRelease
	environment domain.Environment
}

func (s *componentPlanReads) GetComponentRelease(context.Context, string) (domain.ComponentRelease, error) {
	return s.release, nil
}
func (s *componentPlanReads) GetComponent(context.Context, string, bool) (domain.Component, error) {
	return domain.Component{ID: "component-one", OwnerID: "owner-one", Name: "Component One"}, nil
}
func (s *componentPlanReads) GetEnvironment(context.Context, string, bool) (domain.Environment, error) {
	return s.environment, nil
}

func TestPlanBuilderLocksSelectedActionWithOnlyReadDependencies(t *testing.T) {
	data := &componentPlanReads{
		release:     domain.ComponentRelease{ID: "release-one", ComponentID: "component-one", Version: "1.0.0", Actions: []domain.ActionDefinition{{ID: "check-one", Kind: domain.ActionCheck, Playbook: "tasks/check.yml", HostGroup: "workers", TimeoutSeconds: 60}}},
		environment: domain.Environment{ID: "environment-one", CurrentRevisionID: "revision-one", Revision: &domain.EnvironmentRevision{ID: "revision-one"}},
	}
	planner := &PlanBuilder{store: data, actions: &ActionPlanner{}}
	input := ComponentTestRequest{EnvironmentID: data.environment.ID, ActionID: "check-one"}
	if _, err := planner.prepareComponentTest(context.Background(), domain.User{ID: "different-owner", Role: domain.RoleComponentOwner}, data.release.ID, input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner gate: %v", err)
	}
	prepared, err := planner.prepareComponentTest(context.Background(), domain.User{ID: "owner-one", Role: domain.RoleComponentOwner}, data.release.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.steps) != 1 {
		t.Fatalf("selected action expanded unexpectedly: %+v", prepared.steps)
	}
	step := prepared.steps[0]
	if step.ReleaseID != data.release.ID || step.ActionID != input.ActionID || step.Limit != "workers" || step.Playbook != "tasks/check.yml" || step.ReleaseSpecDigest != componentReleaseSpecDigest(data.release) || step.NeedsApproval || !step.RetrySafe {
		t.Fatalf("wrong locked action: %+v", step)
	}
}

type creationCatalogRules struct{ catalogRulesPort }

func (creationCatalogRules) validateEnvironmentConstraintRetiredReferences(context.Context, map[string]any, map[string]any) error {
	return nil
}
