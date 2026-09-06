package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

type ScenarioForkRequest struct {
	EnvironmentConstraints map[string]any `json:"environmentConstraints"`
	SourceRevisionID       string         `json:"sourceRevisionId"`
	Name                   string         `json:"name"`
	Slug                   string         `json:"slug"`
	Description            string         `json:"description"`
	ExpectedPlanDigest     string         `json:"expectedPlanDigest,omitempty"`
}
type ScenarioForkPlan struct {
	EnvironmentConstraints       map[string]any `json:"environmentConstraints"`
	SourceEnvironmentConstraints map[string]any `json:"sourceEnvironmentConstraints"`
	SourceScenarioID             string         `json:"sourceScenarioId"`
	SourceRevisionID             string         `json:"sourceRevisionId"`
	SourceRevision               int            `json:"sourceRevision"`
	SourceDigest                 string         `json:"sourceDigest"`
	NodeCount                    int            `json:"nodeCount"`
	AcceptanceJobCount           int            `json:"acceptanceJobCount"`
	PlanDigest                   string         `json:"planDigest"`
}

func (p *ScenarioService) PreviewFork(ctx context.Context, user domain.User, input ScenarioForkRequest) (ScenarioForkPlan, error) {
	if err := domain.ValidateRole(user, domain.RoleScenarioOwner); err != nil {
		return ScenarioForkPlan{}, err
	}
	if strings.TrimSpace(input.Name) == "" {
		return ScenarioForkPlan{}, fmt.Errorf("%w: scenario name is required", domain.ErrInvalid)
	}
	if err := validateSlug(input.Slug); err != nil {
		return ScenarioForkPlan{}, err
	}
	source, err := p.store.GetScenarioRevision(ctx, input.SourceRevisionID)
	if err != nil {
		return ScenarioForkPlan{}, err
	}
	visible, err := p.Get(ctx, user, source.ScenarioID)
	if err != nil {
		return ScenarioForkPlan{}, err
	}
	found := false
	for _, revision := range visible.Revisions {
		if revision.ID == source.ID {
			found = true
			break
		}
	}
	if !found {
		return ScenarioForkPlan{}, domain.ErrNotFound
	}
	if source.Status != domain.RevisionReleased {
		return ScenarioForkPlan{}, fmt.Errorf("%w: branch source must be published", domain.ErrConflict)
	}
	plan := ScenarioForkPlan{SourceScenarioID: source.ScenarioID, SourceRevisionID: source.ID, SourceRevision: source.Revision, SourceDigest: domain.ScenarioRevisionSpecDigest(source), NodeCount: len(source.Graph.Nodes), AcceptanceJobCount: len(source.AcceptanceJobs)}
	if input.EnvironmentConstraints == nil {
		input.EnvironmentConstraints = source.EnvironmentConstraints
	}
	if err := p.catalogRules.validateEnvironmentConstraintRetiredReferences(ctx, input.EnvironmentConstraints, nil); err != nil {
		return ScenarioForkPlan{}, err
	}
	plan.EnvironmentConstraints = domain.NormalizeEnvironmentConstraints(input.EnvironmentConstraints)
	plan.SourceEnvironmentConstraints = source.EnvironmentConstraints
	plan.PlanDigest = digestValue(struct {
		SourceDigest, SourceRevisionID, UserID, Name, Slug, Description string
		Scope                                                           map[string]any
	}{plan.SourceDigest, source.ID, user.ID, input.Name, input.Slug, input.Description, plan.EnvironmentConstraints})
	return plan, nil
}

func (p *ScenarioService) Fork(ctx context.Context, user domain.User, input ScenarioForkRequest) (domain.Scenario, error) {
	plan, err := p.PreviewFork(ctx, user, input)
	if err != nil {
		return domain.Scenario{}, err
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != plan.PlanDigest {
		return domain.Scenario{}, fmt.Errorf("%w: branch preview changed; preview again", domain.ErrConflict)
	}
	source, err := p.store.GetScenarioRevision(ctx, input.SourceRevisionID)
	if err != nil {
		return domain.Scenario{}, err
	}
	now := time.Now().UTC()
	scenario := domain.Scenario{ID: newID("scenario"), Slug: input.Slug, Name: strings.TrimSpace(input.Name), Description: input.Description, OwnerID: user.ID, CreatedAt: now, UpdatedAt: now, ForkedFromScenarioID: plan.SourceScenarioID, ForkedFromRevisionID: plan.SourceRevisionID, ForkedFromDigest: plan.SourceDigest}
	scenario.EnvironmentConstraints = plan.EnvironmentConstraints
	revision := source
	revision.EnvironmentConstraints = plan.EnvironmentConstraints
	revision.ID, revision.ScenarioID, revision.Revision, revision.Status = newID("scenario-revision"), scenario.ID, 1, domain.RevisionDraft
	revision.SourceRevisionID, revision.SourceRunID = "", ""
	revision.UpgradeConstraints = nil
	revision.DigestVersion = domain.ScenarioDigestVersion
	revision.PublicationGeneration = 0
	revision.CreatedAt, revision.TestPassedAt, revision.ReleasedAt, revision.DeprecatedAt, revision.AbandonedAt = now, nil, nil, nil, nil
	cleanup, err := p.CloneScenarioAcceptanceWorkspace(ctx, source, &revision)
	if err != nil {
		return domain.Scenario{}, err
	}
	scenario.CurrentRevisionID = revision.ID
	if err = p.store.CreateScenario(ctx, scenario, revision); err != nil {
		cleanup()
		return domain.Scenario{}, err
	}
	scenario.Revisions = []domain.ScenarioRevision{revision}
	p.audit.Record(ctx, user, "scenario.forked", "scenario", scenario.ID, map[string]any{"sourceScenarioId": source.ScenarioID, "sourceRevisionId": source.ID, "sourceDigest": plan.SourceDigest, "planDigest": plan.PlanDigest})
	p.hub.Publish("scenario.created", map[string]any{"scenarioId": scenario.ID})
	return scenario, nil
}

func (p *ScenarioService) ReopenRevision(ctx context.Context, user domain.User, revisionID, expectedDigest string) (domain.ScenarioRevision, error) {
	revision, _, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if err = p.store.SaveScenarioRevisionDefinition(ctx, revision, expectedDigest); err != nil {
		return revision, err
	}
	p.audit.Record(ctx, user, "scenario_revision.reopened", "scenario_revision", revision.ID, nil)
	return p.store.GetScenarioRevision(ctx, revisionID)
}

func (p *ScenarioService) SaveUpgradeConstraints(ctx context.Context, user domain.User, revisionID string, edges []domain.ScenarioEdge, expectedDigest string) (domain.ScenarioRevision, error) {
	revision, _, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if revision.SourceRevisionID == "" && len(edges) > 0 {
		return revision, fmt.Errorf("%w: only derived versions have upgrade order constraints", domain.ErrInvalid)
	}
	known := map[string]bool{}
	for _, node := range revision.Graph.Nodes {
		known[node.ID] = true
	}
	if revision.SourceRevisionID != "" {
		source, e := p.store.GetScenarioRevision(ctx, revision.SourceRevisionID)
		if e != nil {
			return revision, e
		}
		for _, node := range source.Graph.Nodes {
			known[node.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, edge := range edges {
		if edge.ID == "" || seen[edge.ID] || edge.Kind != domain.ScenarioEdgeSequence || edge.DependencyID != "" || !known[edge.Source] || !known[edge.Target] || edge.Source == edge.Target {
			return revision, fmt.Errorf("%w: invalid upgrade order constraint", domain.ErrInvalid)
		}
		seen[edge.ID] = true
	}
	// Dependencies and cycles involving component migration are evaluated by the
	// environment-specific upgrade planner; the editor never invents graph edges.
	revision.UpgradeConstraints = edges
	if err = p.store.SaveScenarioRevisionDefinition(ctx, revision, expectedDigest); err != nil {
		return revision, err
	}
	return p.store.GetScenarioRevision(ctx, revisionID)
}
