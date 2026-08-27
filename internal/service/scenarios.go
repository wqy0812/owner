package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

type CandidateReleaseSetItem struct {
	ReleaseID     string `json:"releaseId"`
	ComponentID   string `json:"componentId"`
	ComponentName string `json:"componentName"`
	Version       string `json:"version"`
}

type CandidateReleaseSet struct {
	ScenarioRevisionID string                    `json:"scenarioRevisionId"`
	Ready              bool                      `json:"ready"`
	Releases           []CandidateReleaseSetItem `json:"releases"`
	Issues             []domain.ValidationIssue  `json:"issues"`
}

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

func (p *Platform) DeleteScenario(ctx context.Context, user domain.User, scenarioID string) error {
	scenario, err := p.store.GetScenario(ctx, scenarioID, true)
	if err != nil {
		return err
	}
	if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
		return err
	}
	impact, err := p.store.ScenarioDeletionImpact(ctx, scenarioID)
	if err != nil {
		return err
	}
	if impact.RunCount > 0 {
		base := fmt.Errorf("%w: scenario is retained by %d run(s)", domain.ErrConflict, impact.RunCount)
		return actionableExistingError(base, "scenario.run_history", "该场景已有运行记录，必须保留场景与 Revision 快照", "查看运行记录", "/runs")
	}
	if impact.PublishedRevisionCount > 0 {
		base := fmt.Errorf("%w: scenario has %d published revision(s)", domain.ErrConflict, impact.PublishedRevisionCount)
		return actionableExistingError(base, "scenario.published_history", "该场景已有已发布或已废弃 Revision，不能物理删除", "废弃已发布 Revision", "/scenarios?selected="+scenario.ID)
	}
	audit := newAuditEvent(user, "scenario.deleted", "scenario", scenario.ID, map[string]any{
		"slug": scenario.Slug, "name": scenario.Name, "revisionCount": impact.RevisionCount,
	})
	if err := p.store.DeleteScenario(ctx, scenario.ID, audit); err != nil {
		return err
	}
	p.hub.Publish("scenario.deleted", map[string]any{"scenarioId": scenario.ID})
	return nil
}

func (p *Platform) CloneScenarioRevision(ctx context.Context, user domain.User, scenarioID string, input ScenarioCloneRequest) (domain.ScenarioRevision, error) {
	plan, err := p.PreviewScenarioClone(ctx, user, scenarioID, input)
	if err != nil {
		return domain.ScenarioRevision{}, err
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != plan.PlanDigest {
		return domain.ScenarioRevision{}, fmt.Errorf("%w: scenario clone plan changed; preview again", domain.ErrConflict)
	}
	source, err := p.store.GetScenarioRevision(ctx, input.SourceRevisionID)
	if err != nil {
		return source, err
	}
	next := plan.NextRevision
	source.ID, source.Revision, source.Status = newID("scenario-revision"), next, domain.RevisionDraft
	source.CreatedAt, source.TestPassedAt, source.ReleasedAt, source.DeprecatedAt, source.AbandonedAt = time.Now().UTC(), nil, nil, nil, nil
	if err := p.store.CreateScenarioRevision(ctx, source); err != nil {
		return source, err
	}
	p.audit(ctx, user, "scenario_revision.cloned", "scenario_revision", source.ID, map[string]any{"scenarioId": scenarioID, "revision": next, "sourceRevisionId": input.SourceRevisionID, "planDigest": plan.PlanDigest})
	return source, nil
}

func (p *Platform) AbandonScenarioRevision(ctx context.Context, user domain.User, revisionID string) (domain.Scenario, error) {
	revision, scenario, err := p.ownedScenarioRevision(ctx, user, revisionID)
	if err != nil {
		return scenario, err
	}
	if scenario.CurrentRevisionID != revisionID || revision.Status != domain.RevisionDraft {
		return scenario, fmt.Errorf("%w: only the current draft revision can be abandoned", domain.ErrConflict)
	}
	restoredID, err := p.store.AbandonScenarioRevision(ctx, revisionID, time.Now().UTC())
	if err != nil {
		return scenario, err
	}
	p.audit(ctx, user, "scenario_revision.abandoned", "scenario_revision", revisionID, map[string]any{
		"scenarioId": scenario.ID, "revision": revision.Revision, "restoredRevisionId": restoredID,
	})
	return p.store.GetScenario(ctx, scenario.ID, true)
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
	filtered := make([]domain.ScenarioRevision, 0, len(scenario.Revisions))
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
	if issues := domain.ValidateGraph(graph); len(issues) > 0 {
		return revision, &domain.ValidationError{Message: "scenario graph is invalid", Details: issues}
	}
	if err := rejectSensitiveMap(policy, "scenario execution policy"); err != nil {
		return revision, err
	}
	for _, node := range graph.Nodes {
		if err := rejectSensitiveMap(node.Values, "scenario node value"); err != nil {
			return revision, err
		}
		for _, parameter := range node.RunInputs {
			if isSensitiveKey(parameter) {
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

	nodesByRelease := map[string][]string{}
	releaseByNode := map[string]domain.ComponentRelease{}
	for _, node := range revision.Graph.Nodes {
		release, releaseErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil {
			issues = append(issues, domain.ValidationIssue{Code: "release_not_found", Message: "locked component release does not exist", NodeID: node.ID})
			continue
		}
		candidateDraft := revision.Status != domain.RevisionReleased && release.Status == domain.ReleaseDraft && release.Candidate && release.Verified
		if release.Status != domain.ReleaseReleased && !candidateDraft && !(revision.Status == domain.RevisionReleased && release.Status == domain.ReleaseDeprecated) {
			issues = append(issues, domain.ValidationIssue{Code: "release_not_released", Message: "scenario nodes may only use released versions or verified shared candidates", NodeID: node.ID})
		}
		if _, actionErr := actionFor(release, node.Action); actionErr != nil {
			issues = append(issues, domain.ValidationIssue{Code: "action_missing", Message: actionErr.Error(), NodeID: node.ID})
		}
		validateRequiredParameters(release, node, &issues)
		if conflicts := nodeOverridesMappedParameter(node, release); len(conflicts) > 0 {
			issues = append(issues, domain.ValidationIssue{
				Code: "mapped_parameter_overridden", Message: fmt.Sprintf("mapped parameters cannot also be set locally: %s", strings.Join(conflicts, ", ")), NodeID: node.ID,
			})
		}
		nodesByRelease[release.ID] = append(nodesByRelease[release.ID], node.ID)
		releaseByNode[node.ID] = release
	}

	reachable := graphReachability(revision.Graph)
	if revision.Status != domain.RevisionReleased {
		for _, edge := range revision.Graph.Edges {
			source, sourceOK := releaseByNode[edge.Source]
			target, targetOK := releaseByNode[edge.Target]
			if !sourceOK || !targetOK || source.ID == target.ID || !target.Candidate {
				continue
			}
			targetNode := findScenarioNode(revision.Graph.Nodes, edge.Target)
			if targetNode.Action == domain.ActionVerify {
				continue
			}
			declared := false
			for _, dependency := range target.Dependencies {
				if dependency.UpstreamReleaseID == source.ID {
					declared = true
					break
				}
			}
			if !declared {
				issues = append(issues, domain.ValidationIssue{Code: "undeclared_dependency_edge", Message: fmt.Sprintf("hard edge from %s must also be declared in the target Release contract", edge.Source), NodeID: edge.Target})
			}
		}
	}
	for _, node := range revision.Graph.Nodes {
		release, ok := releaseByNode[node.ID]
		if !ok {
			continue
		}
		// A verify node observes a release that is expected to exist already. Its
		// install-time dependencies do not need to be rebuilt unless this node
		// actually imports mapped parameters.
		skipInstallDependencies := node.Action == domain.ActionVerify && !releaseNeedsImportedParameters(release)
		if skipInstallDependencies {
			continue
		}
		for _, dependency := range release.Dependencies {
			upstreamNodes := nodesByRelease[dependency.UpstreamReleaseID]
			if len(upstreamNodes) == 0 {
				issues = append(issues, domain.ValidationIssue{
					Code: "missing_dependency_node", Message: "locked upstream component release is absent from the graph", NodeID: node.ID,
				})
				continue
			}
			if !anyUpstreamNodeReachable(upstreamNodes, node.ID, reachable) {
				issues = append(issues, domain.ValidationIssue{
					Code: "dependency_order", Message: "upstream component must precede the dependent node", NodeID: node.ID,
				})
			}
			if len(dependency.ParameterMappings) == 0 {
				continue
			}
			if _, sourceErr := selectDependencySource(node, dependency, revision.Graph, releaseByNode, reachable); sourceErr != nil {
				issues = append(issues, domain.ValidationIssue{Code: sourceIssueCode(sourceErr), Message: sourceErr.Error(), NodeID: node.ID})
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

func findScenarioNode(nodes []domain.ScenarioNode, id string) domain.ScenarioNode {
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	return domain.ScenarioNode{}
}

func anyUpstreamNodeReachable(upstreamNodes []string, downstreamNode string, reachable map[string]map[string]bool) bool {
	for _, upstreamNode := range upstreamNodes {
		if reachable[upstreamNode][downstreamNode] {
			return true
		}
	}
	return false
}

func validateRequiredParameters(release domain.ComponentRelease, node domain.ScenarioNode, issues *[]domain.ValidationIssue) {
	mapped := domain.MappedTargets(release.Dependencies)
	for _, parameter := range release.Parameters {
		if !parameter.Required || parameter.HasDefault() {
			continue
		}
		if _, imported := mapped[parameter.Name]; imported {
			continue
		}
		validateRequiredKey(parameter.Name, node, issues)
	}
}

func sourceIssueCode(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "absent from the graph"):
		return "missing_dependency_node"
	case strings.Contains(message, "must choose a source"):
		return "dependency_source_required"
	case strings.Contains(message, "not a reachable"), strings.Contains(message, "does not lock"):
		return "dependency_source_invalid"
	default:
		return "dependency_source"
	}
}

func validateRequiredKey(key string, node domain.ScenarioNode, issues *[]domain.ValidationIssue) {
	_, inValues := node.Values[key]
	if !inValues && !contains(node.RunInputs, key) {
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
		base := fmt.Errorf("%w: only the current scenario revision can be published", domain.ErrConflict)
		return revision, actionableExistingError(base, "scenario.not_current", "历史 Revision 保持不可变，只有当前 Revision 可以发布", "查看当前 Revision", "/scenarios?selected="+scenario.ID)
	}
	if revision.Status != domain.RevisionTestPassed || revision.TestPassedAt == nil {
		base := fmt.Errorf("%w: the current scenario revision must pass a complete test before publishing", domain.ErrConflict)
		return revision, actionableExistingError(base, "scenario.test_required", "发布规则要求当前 Revision 通过完整环境测试", "前往场景测试", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=test", scenario.ID, revision.ID))
	}
	issues, err := p.ValidateScenario(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if len(issues) > 0 {
		base := &domain.ValidationError{Message: "scenario validation failed", Details: issues}
		return revision, actionableExistingError(base, "scenario.graph_invalid", "当前 DAG 或锁定 Release 未通过校验", "检查场景问题", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
	}
	set, err := p.CandidateReleaseSet(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if !set.Ready {
		base := &domain.ValidationError{Message: "candidate release set is not ready", Details: set.Issues}
		return revision, actionableExistingError(base, "scenario.candidate_set_blocked", "候选 Release 状态或证据在发布前复核时发生变化", "检查候选集", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
	}
	now := time.Now().UTC()
	releaseIDs := make([]string, 0, len(set.Releases))
	for _, item := range set.Releases {
		releaseIDs = append(releaseIDs, item.ReleaseID)
	}
	if err := p.store.PublishCandidateReleaseSet(ctx, revisionID, releaseIDs, now); err != nil {
		return revision, err
	}
	revision.Status, revision.ReleasedAt = domain.RevisionReleased, &now
	for _, item := range set.Releases {
		p.audit(ctx, user, "component_release.published_with_scenario", "component_release", item.ReleaseID, map[string]any{"scenarioRevisionId": revisionID, "componentId": item.ComponentID, "version": item.Version})
		p.hub.Publish("release.published", map[string]any{"releaseId": item.ReleaseID, "componentId": item.ComponentID})
	}
	p.audit(ctx, user, "scenario_revision.published", "scenario_revision", revisionID, map[string]any{"scenarioId": scenario.ID, "revision": revision.Revision, "candidateReleaseCount": len(set.Releases)})
	p.hub.Publish("scenario.published", map[string]any{"scenarioId": scenario.ID, "revisionId": revisionID})
	return revision, nil
}

func (p *Platform) CandidateReleaseSet(ctx context.Context, user domain.User, revisionID string) (CandidateReleaseSet, error) {
	revision, _, err := p.ownedScenarioRevision(ctx, user, revisionID)
	set := CandidateReleaseSet{ScenarioRevisionID: revisionID, Releases: []CandidateReleaseSetItem{}, Issues: []domain.ValidationIssue{}}
	if err != nil {
		return set, err
	}
	seen := map[string]bool{}
	for _, node := range revision.Graph.Nodes {
		if seen[node.ReleaseID] {
			continue
		}
		seen[node.ReleaseID] = true
		release, getErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if getErr != nil {
			set.Issues = append(set.Issues, domain.ValidationIssue{Code: "release_not_found", Message: "locked component release does not exist", NodeID: node.ID})
			continue
		}
		if release.Status == domain.ReleaseReleased {
			continue
		}
		if release.Status != domain.ReleaseDraft || !release.Candidate || !release.Verified {
			set.Issues = append(set.Issues, domain.ValidationIssue{Code: "candidate_not_ready", Message: "draft release must be verified and shared by its component owner", NodeID: node.ID})
			continue
		}
		if validateErr := p.validateReleaseForCandidate(ctx, release); validateErr != nil {
			set.Issues = append(set.Issues, domain.ValidationIssue{Code: "candidate_invalid", Message: validateErr.Error(), NodeID: node.ID})
			continue
		}
		component, componentErr := p.store.GetComponent(ctx, release.ComponentID, false)
		if componentErr != nil {
			return set, componentErr
		}
		set.Releases = append(set.Releases, CandidateReleaseSetItem{ReleaseID: release.ID, ComponentID: component.ID, ComponentName: component.Name, Version: release.Version})
	}
	sort.Slice(set.Releases, func(i, j int) bool { return set.Releases[i].ComponentName < set.Releases[j].ComponentName })
	set.Ready = len(set.Issues) == 0
	return set, nil
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
