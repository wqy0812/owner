package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) CreateScenario(ctx context.Context, user domain.User, scenario domain.Scenario) (domain.Scenario, error) {
	if err := domain.ValidateRole(user, domain.RoleScenarioOwner); err != nil {
		return scenario, err
	}
	if scenario.Name == "" {
		return scenario, fmt.Errorf("%w: scenario name is required", domain.ErrInvalid)
	}
	if err := validateSlug(scenario.Slug); err != nil {
		return scenario, err
	}
	now := time.Now().UTC()
	scenario.ID, scenario.OwnerID, scenario.CreatedAt, scenario.UpdatedAt = newID("scenario"), user.ID, now, now
	revision := domain.ScenarioRevision{
		ID: newID("scenario-revision"), ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph:           domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}},
		ExecutionPolicy: map[string]any{"maxUnavailableNodes": 1, "failurePolicy": "manual_intervention"}, CreatedAt: now,
	}
	scenario.CurrentRevisionID = revision.ID
	if err := p.store.CreateScenario(ctx, scenario, revision); err != nil {
		return scenario, err
	}
	scenario.Revisions = []domain.ScenarioRevision{revision}
	p.audit(ctx, user, "scenario.created", "scenario", scenario.ID, map[string]any{"slug": scenario.Slug, "revisionId": revision.ID})
	return scenario, nil
}

func (p *Platform) CloneScenarioRevision(ctx context.Context, user domain.User, scenarioID string) (domain.ScenarioRevision, error) {
	scenario, err := p.store.GetScenario(ctx, scenarioID, true)
	if err != nil {
		return domain.ScenarioRevision{}, err
	}
	if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
		return domain.ScenarioRevision{}, err
	}
	var source domain.ScenarioRevision
	for _, revision := range scenario.Revisions {
		if revision.ID == scenario.CurrentRevisionID || source.ID == "" || revision.Revision > source.Revision {
			source = revision
			if revision.ID == scenario.CurrentRevisionID {
				break
			}
		}
	}
	if source.ID == "" {
		return source, fmt.Errorf("%w: scenario has no source revision", domain.ErrConflict)
	}
	next, err := p.store.NextScenarioRevision(ctx, scenarioID)
	if err != nil {
		return source, err
	}
	source.ID, source.Revision, source.Status = newID("scenario-revision"), next, domain.RevisionDraft
	source.CreatedAt, source.TestPassedAt, source.ReleasedAt, source.DeprecatedAt = time.Now().UTC(), nil, nil, nil
	if err := p.store.CreateScenarioRevision(ctx, source); err != nil {
		return source, err
	}
	p.audit(ctx, user, "scenario_revision.cloned", "scenario_revision", source.ID, map[string]any{"scenarioId": scenarioID, "revision": next})
	return source, nil
}

func (p *Platform) ListScenarios(ctx context.Context, user domain.User) ([]domain.Scenario, error) {
	return p.store.ListScenarios(ctx, user)
}

func (p *Platform) GetScenario(ctx context.Context, user domain.User, id string) (domain.Scenario, error) {
	scenario, err := p.store.GetScenario(ctx, id, true)
	if err != nil {
		return scenario, err
	}
	if user.Role == domain.RoleScenarioOwner && scenario.OwnerID == user.ID {
		return scenario, nil
	}
	filtered := scenario.Revisions[:0]
	for _, revision := range scenario.Revisions {
		if revision.Status == domain.RevisionReleased {
			filtered = append(filtered, revision)
		}
	}
	scenario.Revisions = filtered
	if len(filtered) == 0 {
		return domain.Scenario{}, domain.ErrNotFound
	}
	return scenario, nil
}

func (p *Platform) SaveScenarioGraph(ctx context.Context, user domain.User, revisionID string, graph domain.ScenarioGraph, policy map[string]any) (domain.ScenarioRevision, error) {
	revision, scenario, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if scenario.CurrentRevisionID != revisionID {
		return revision, fmt.Errorf("%w: only the current scenario revision can be edited", domain.ErrConflict)
	}
	if len(domain.ValidateGraph(graph)) > 0 {
		return revision, &domain.ValidationError{Message: "scenario graph is invalid", Details: domain.ValidateGraph(graph)}
	}
	if err := rejectSensitiveMap(policy, "scenario execution policy"); err != nil {
		return revision, err
	}
	for _, node := range graph.Nodes {
		if err := rejectSensitiveMap(node.Values, "scenario node value"); err != nil {
			return revision, err
		}
		for parameter, environmentKey := range node.Bindings {
			if sensitiveKey.MatchString(parameter) || sensitiveKey.MatchString(environmentKey) {
				return revision, fmt.Errorf("%w: sensitive scenario binding %q must use a CredentialRef", domain.ErrInvalid, parameter)
			}
		}
		for _, parameter := range node.RunInputs {
			if sensitiveKey.MatchString(parameter) {
				return revision, fmt.Errorf("%w: sensitive run input %q must use a CredentialRef", domain.ErrInvalid, parameter)
			}
		}
	}
	if err := p.store.SaveScenarioGraph(ctx, revisionID, graph, policy); err != nil {
		return revision, err
	}
	revision.Graph, revision.ExecutionPolicy, revision.Status, revision.TestPassedAt = graph, policy, domain.RevisionDraft, nil
	p.audit(ctx, user, "scenario_revision.graph_updated", "scenario_revision", revisionID, map[string]any{
		"scenarioId": scenario.ID, "nodes": len(graph.Nodes), "edges": len(graph.Edges),
	})
	return revision, nil
}

func (p *Platform) ValidateScenario(ctx context.Context, user domain.User, revisionID string) ([]domain.ValidationIssue, error) {
	revision, _, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return nil, err
	}
	issues := append([]domain.ValidationIssue(nil), domain.ValidateGraph(revision.Graph)...)
	if len(revision.Graph.Nodes) == 0 {
		return issues, nil
	}

	nodeByRelease := map[string]string{}
	releaseByNode := map[string]domain.ComponentRelease{}
	for _, node := range revision.Graph.Nodes {
		release, releaseErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil {
			issues = append(issues, domain.ValidationIssue{Code: "release_not_found", Message: "locked component release does not exist", NodeID: node.ID})
			continue
		}
		if release.Status != domain.ReleaseReleased && !(revision.Status == domain.RevisionReleased && release.Status == domain.ReleaseDeprecated) {
			issues = append(issues, domain.ValidationIssue{Code: "release_not_released", Message: "scenario nodes may only use released component versions", NodeID: node.ID})
		}
		if _, actionErr := actionFor(release, node.Action); actionErr != nil {
			issues = append(issues, domain.ValidationIssue{Code: "action_missing", Message: actionErr.Error(), NodeID: node.ID})
		}
		validateRequiredParameters(release.ParameterSchema, node, &issues)
		nodeByRelease[release.ID] = node.ID
		releaseByNode[node.ID] = release
	}

	reachable := graphReachability(revision.Graph)
	for _, node := range revision.Graph.Nodes {
		release, ok := releaseByNode[node.ID]
		if !ok {
			continue
		}
		for _, dependency := range release.Dependencies {
			upstreamNode, exists := nodeByRelease[dependency.UpstreamReleaseID]
			if !exists {
				issues = append(issues, domain.ValidationIssue{
					Code: "missing_dependency_node", Message: "locked upstream component release is absent from the graph", NodeID: node.ID,
				})
				continue
			}
			if !reachable[upstreamNode][node.ID] {
				issues = append(issues, domain.ValidationIssue{
					Code: "dependency_order", Message: "upstream component must precede the dependent node", NodeID: node.ID,
				})
			}
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].NodeID == issues[j].NodeID {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].NodeID < issues[j].NodeID
	})
	return issues, nil
}

func validateRequiredParameters(schema map[string]any, node domain.ScenarioNode, issues *[]domain.ValidationIssue) {
	for _, key := range requiredParameterKeys(schema) {
		if parameterHasDefault(schema, key) {
			continue
		}
		validateRequiredKey(key, node, issues)
	}
}

func parameterHasDefault(schema map[string]any, key string) bool {
	properties, _ := schema["properties"].(map[string]any)
	definition, _ := properties[key].(map[string]any)
	_, exists := definition["default"]
	return exists
}

func requiredParameterKeys(schema map[string]any) []string {
	var keys []string
	switch required := schema["required"].(type) {
	case []string:
		keys = append(keys, required...)
	case []any:
		for _, value := range required {
			if key, ok := value.(string); ok {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func validateRequiredKey(key string, node domain.ScenarioNode, issues *[]domain.ValidationIssue) {
	_, inValues := node.Values[key]
	_, inBindings := node.Bindings[key]
	if !inValues && !inBindings && !contains(node.RunInputs, key) {
		*issues = append(*issues, domain.ValidationIssue{Code: "required_parameter", Message: fmt.Sprintf("required parameter %q is not bound", key), NodeID: node.ID})
	}
}

func graphReachability(graph domain.ScenarioGraph) map[string]map[string]bool {
	adjacency := map[string][]string{}
	for _, edge := range graph.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	result := map[string]map[string]bool{}
	for _, node := range graph.Nodes {
		seen := map[string]bool{}
		queue := append([]string(nil), adjacency[node.ID]...)
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if seen[current] {
				continue
			}
			seen[current] = true
			queue = append(queue, adjacency[current]...)
		}
		result[node.ID] = seen
	}
	return result
}

func (p *Platform) PublishScenario(ctx context.Context, user domain.User, revisionID string) (domain.ScenarioRevision, error) {
	revision, scenario, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if scenario.CurrentRevisionID != revisionID {
		return revision, fmt.Errorf("%w: only the current scenario revision can be published", domain.ErrConflict)
	}
	if revision.Status != domain.RevisionTestPassed || revision.TestPassedAt == nil {
		return revision, fmt.Errorf("%w: the current scenario revision must pass a complete test before publishing", domain.ErrConflict)
	}
	issues, err := p.ValidateScenario(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if len(issues) > 0 {
		return revision, &domain.ValidationError{Message: "scenario validation failed", Details: issues}
	}
	now := time.Now().UTC()
	if err := p.store.SetScenarioRevisionStatus(ctx, revisionID, []domain.RevisionStatus{domain.RevisionTestPassed}, domain.RevisionReleased, now); err != nil {
		return revision, err
	}
	revision.Status, revision.ReleasedAt = domain.RevisionReleased, &now
	p.audit(ctx, user, "scenario_revision.published", "scenario_revision", revisionID, map[string]any{"scenarioId": scenario.ID, "revision": revision.Revision})
	p.hub.Publish("scenario.published", map[string]any{"scenarioId": scenario.ID, "revisionId": revisionID})
	return revision, nil
}

func (p *Platform) DeprecateScenario(ctx context.Context, user domain.User, revisionID string) (domain.ScenarioRevision, error) {
	revision, scenario, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	now := time.Now().UTC()
	if err := p.store.DeprecateScenarioRevision(ctx, revisionID, now); err != nil {
		return revision, err
	}
	revision.Status, revision.DeprecatedAt = domain.RevisionDeprecated, &now
	p.audit(ctx, user, "scenario_revision.deprecated", "scenario_revision", revisionID, map[string]any{"scenarioId": scenario.ID, "revision": revision.Revision})
	return revision, nil
}

func (p *Platform) ownedScenarioRevision(ctx context.Context, user domain.User, revisionID string) (domain.ScenarioRevision, domain.Scenario, error) {
	revision, err := p.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return revision, domain.Scenario{}, err
	}
	scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
	if err != nil {
		return revision, scenario, err
	}
	if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
		return revision, scenario, err
	}
	return revision, scenario, nil
}
