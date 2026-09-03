package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) PreviewComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (ComponentTestPlan, error) {
	prepared, err := p.prepareComponentTest(ctx, user, releaseID, input)
	if err != nil {
		return ComponentTestPlan{}, err
	}
	plan, digest, destructive, err := p.prepareLockedPlan(ctx, prepared.environment, domain.RunComponentTest, "preview", time.Time{}, prepared.steps)
	if err != nil {
		return ComponentTestPlan{}, err
	}
	return p.componentTestPlanDTO(ctx, prepared.environment, plan, digest, destructive), nil
}

func (p *Platform) StartComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (domain.Run, error) {
	prepared, err := p.prepareComponentTest(ctx, user, releaseID, input)
	if err != nil {
		return domain.Run{}, err
	}
	return p.createRun(ctx, user, prepared.environment, domain.RunComponentTest, prepared.release.ID, "", prepared.action, prepared.steps, prepared.provenance, input.ExpectedPlanDigest)
}

func (p *Platform) prepareComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (preparedComponentTest, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return preparedComponentTest{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if user.Role != domain.RoleEnvironmentOwner {
		if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
			return preparedComponentTest{}, err
		}
	}
	environment, err := p.store.GetEnvironment(ctx, input.EnvironmentID, false)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return preparedComponentTest{}, err
	}
	if environment.Revision == nil {
		return preparedComponentTest{}, fmt.Errorf("%w: environment has no revision", domain.ErrConflict)
	}
	if err := validateEnvironmentConstraints(release.EnvironmentConstraints, environment.Revision.Facts); err != nil {
		return preparedComponentTest{}, err
	}

	if input.Mode == "" {
		if release.ParentReleaseID != "" {
			input.Mode = ComponentTestEvolutionRoundTrip
		} else {
			input.Mode = ComponentTestInstallVerify
		}
	}
	if input.Mode == ComponentTestEvolutionRoundTrip {
		return p.prepareEvolutionRoundTrip(ctx, component, release, environment, input)
	}
	var selected domain.ActionDefinition
	switch input.Mode {
	case ComponentTestInstallVerify:
		if input.RollbackVerification.Kind != "" || input.RollbackVerification.ReleaseID != "" {
			return preparedComponentTest{}, fmt.Errorf("%w: rollbackVerification is only valid for rollback tests", domain.ErrInvalid)
		}
		selected, _ = primaryActionForComponentTest(release)
		if selected.Kind == "" {
			return preparedComponentTest{}, fmt.Errorf("%w: install verification requires an executable primary action", domain.ErrInvalid)
		}
	case ComponentTestRollback:
		selected, _ = findAction(release, domain.ActionRollback)
		if selected.Kind == "" {
			return preparedComponentTest{}, fmt.Errorf("%w: rollback verification requires a rollback action", domain.ErrInvalid)
		}
	default:
		return preparedComponentTest{}, fmt.Errorf("%w: unsupported component test mode %q", domain.ErrInvalid, input.Mode)
	}
	variables, provenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, *environment.Revision, true)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if err := validateResolvedParameters(release.Parameters, variables); err != nil {
		return preparedComponentTest{}, err
	}
	steps := make([]lockedStep, 0, 2)
	nodeID := "component-" + component.ID
	step, err := p.lockAction(component, nodeID, release, selected, variables)
	if err != nil {
		return preparedComponentTest{}, err
	}
	steps = append(steps, step)
	if input.Mode == ComponentTestInstallVerify {
		if verify, ok := findAction(release, domain.ActionVerify); ok {
			verifyStep, stepErr := p.lockAction(component, nodeID+"-verify", release, verify, variables)
			if stepErr != nil {
				return preparedComponentTest{}, stepErr
			}
			steps = append(steps, verifyStep)
		}
	} else {
		if (selected.FromReleaseID == "") != (selected.ToReleaseID == "") {
			return preparedComponentTest{}, fmt.Errorf("%w: rollback action must define both fromReleaseId and toReleaseId, or neither", domain.ErrInvalid)
		}
		if selected.FromReleaseID != "" && (selected.FromReleaseID != release.ID || selected.ToReleaseID == release.ID) {
			return preparedComponentTest{}, fmt.Errorf("%w: rollback action must point from this draft to an earlier release", domain.ErrInvalid)
		}
		verification := input.RollbackVerification
		if verification.Kind == "" {
			if selected.ToReleaseID == "" {
				verification.Kind = RollbackVerificationOnly
			} else {
				verification.Kind, verification.ReleaseID = RollbackVerificationTargetRelease, selected.ToReleaseID
			}
		}
		switch verification.Kind {
		case RollbackVerificationOnly:
			if verification.ReleaseID != "" {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback_only must not include a releaseId", domain.ErrInvalid)
			}
			if selected.FromReleaseID != "" || selected.ToReleaseID != "" {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback_only is only valid for a clean-state rollback without fromReleaseId/toReleaseId", domain.ErrInvalid)
			}
		case RollbackVerificationTargetRelease:
			if verification.ReleaseID == "" {
				return preparedComponentTest{}, fmt.Errorf("%w: target_release requires a releaseId", domain.ErrInvalid)
			}
			target, targetErr := p.store.GetComponentRelease(ctx, verification.ReleaseID)
			if targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback verify target must be a retained release of the same component", domain.ErrInvalid)
			}
			if target.ComponentID != release.ComponentID || (target.Status != domain.ReleaseReleased && target.Status != domain.ReleaseDeprecated) {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback verify target must be a retained release of the same component", domain.ErrInvalid)
			}
			verify, ok := findAction(target, domain.ActionVerify)
			if !ok {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback verify target must define a verify action", domain.ErrInvalid)
			}
			targetVariables, _, targetErr := resolveOwnParameters(target, domain.ScenarioNode{}, *environment.Revision, true)
			if targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("rollback target: %w", targetErr)
			}
			if targetErr = validateResolvedParameters(target.Parameters, targetVariables); targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("rollback target: %w", targetErr)
			}
			verifyStep, stepErr := p.lockAction(component, nodeID+"-verify", target, verify, targetVariables)
			if stepErr != nil {
				return preparedComponentTest{}, stepErr
			}
			steps = append(steps, verifyStep)
		default:
			return preparedComponentTest{}, fmt.Errorf("%w: unsupported rollback verification kind %q", domain.ErrInvalid, verification.Kind)
		}
	}
	return preparedComponentTest{
		release: release, environment: environment, action: selected.Kind, steps: steps,
		provenance: map[string]map[string]resolvedParameter{nodeID: provenance},
	}, nil
}

func (p *Platform) prepareEvolutionRoundTrip(ctx context.Context, component domain.Component, release domain.ComponentRelease, environment domain.Environment, input ComponentTestRequest) (preparedComponentTest, error) {
	if release.ParentReleaseID == "" {
		return preparedComponentTest{}, fmt.Errorf("%w: evolution_round_trip requires an evolution release", domain.ErrInvalid)
	}
	if input.RollbackVerification.Kind != "" || input.RollbackVerification.ReleaseID != "" {
		return preparedComponentTest{}, fmt.Errorf("%w: rollbackVerification is implicit for evolution_round_trip", domain.ErrInvalid)
	}
	parent, err := p.store.GetComponentRelease(ctx, release.ParentReleaseID)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if parent.ComponentID != release.ComponentID || parent.LineID != release.LineID || parent.Status != domain.ReleaseReleased {
		return preparedComponentTest{}, fmt.Errorf("%w: evolution parent must be a released version in the same line", domain.ErrInvalid)
	}
	if err := validateEnvironmentConstraints(parent.EnvironmentConstraints, environment.Revision.Facts); err != nil {
		return preparedComponentTest{}, fmt.Errorf("parent release: %w", err)
	}
	parentInstall, err := actionFor(parent, domain.ActionInstall)
	if err != nil {
		return preparedComponentTest{}, fmt.Errorf("parent release: %w", err)
	}
	parentVerify, ok := findAction(parent, domain.ActionVerify)
	if !ok {
		return preparedComponentTest{}, fmt.Errorf("%w: parent release requires Verify for evolution evidence", domain.ErrInvalid)
	}
	upgrade, err := actionFor(release, domain.ActionUpgrade)
	if err != nil {
		return preparedComponentTest{}, err
	}
	targetVerify, ok := findAction(release, domain.ActionVerify)
	if !ok {
		return preparedComponentTest{}, fmt.Errorf("%w: evolution release requires Verify", domain.ErrInvalid)
	}
	rollback, ok := findAction(release, domain.ActionRollback)
	if !ok || rollback.FromReleaseID != release.ID || rollback.ToReleaseID != parent.ID {
		return preparedComponentTest{}, fmt.Errorf("%w: evolution rollback must point from the Draft to its parent", domain.ErrInvalid)
	}

	parentVariables, parentProvenance, err := resolveOwnParameters(parent, domain.ScenarioNode{}, *environment.Revision, true)
	if err != nil {
		return preparedComponentTest{}, fmt.Errorf("parent release: %w", err)
	}
	if err := validateResolvedParameters(parent.Parameters, parentVariables); err != nil {
		return preparedComponentTest{}, fmt.Errorf("parent release: %w", err)
	}
	targetVariables, targetProvenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, *environment.Revision, true)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if err := validateResolvedParameters(release.Parameters, targetVariables); err != nil {
		return preparedComponentTest{}, err
	}

	parentNodeID, targetNodeID := "component-"+component.ID+"-parent", "component-"+component.ID+"-target"
	steps := make([]lockedStep, 0, 6)
	appendStep := func(nodeID string, item domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) error {
		step, stepErr := p.lockAction(component, nodeID, item, action, variables)
		if stepErr == nil {
			steps = append(steps, step)
		}
		return stepErr
	}
	for _, step := range []struct {
		nodeID    string
		release   domain.ComponentRelease
		action    domain.ActionDefinition
		variables map[string]any
	}{
		{parentNodeID, parent, parentInstall, parentVariables},
		{parentNodeID + "-verify", parent, parentVerify, parentVariables},
		{targetNodeID, release, upgrade, targetVariables},
		{targetNodeID + "-verify", release, targetVerify, targetVariables},
		{targetNodeID + "-rollback", release, rollback, targetVariables},
		{parentNodeID + "-verify-restored", parent, parentVerify, parentVariables},
	} {
		if err := appendStep(step.nodeID, step.release, step.action, step.variables); err != nil {
			return preparedComponentTest{}, err
		}
	}
	return preparedComponentTest{
		release: release, environment: environment, action: domain.ActionUpgrade, steps: steps,
		provenance: map[string]map[string]resolvedParameter{parentNodeID: parentProvenance, targetNodeID: targetProvenance},
	}, nil
}

func primaryActionForComponentTest(release domain.ComponentRelease) (domain.ActionDefinition, bool) {
	for _, kind := range []domain.ActionKind{domain.ActionUpgrade, domain.ActionInstall, domain.ActionConfigure, domain.ActionPreflight, domain.ActionInspect} {
		if action, found := findAction(release, kind); found {
			return action, true
		}
	}
	return domain.ActionDefinition{}, false
}

func (p *Platform) StartScenarioTest(ctx context.Context, user domain.User, revisionID, environmentID string) (domain.Run, error) {
	return p.startScenario(ctx, user, revisionID, environmentID, domain.RunScenarioTest)
}

func (p *Platform) StartScenarioRun(ctx context.Context, user domain.User, revisionID, environmentID string) (domain.Run, error) {
	return p.startScenario(ctx, user, revisionID, environmentID, domain.RunScenario)
}

func (p *Platform) startScenario(ctx context.Context, user domain.User, revisionID, environmentID string, kind domain.RunKind) (domain.Run, error) {
	revision, err := p.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return domain.Run{}, err
	}
	scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
	if err != nil {
		return domain.Run{}, err
	}
	if user.Role != domain.RoleEnvironmentOwner {
		if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
			return domain.Run{}, err
		}
	}
	if kind == domain.RunScenario && revision.Status != domain.RevisionReleased {
		return domain.Run{}, fmt.Errorf("%w: only a released scenario can run outside testing", domain.ErrConflict)
	}
	if kind == domain.RunScenarioTest && revision.Status == domain.RevisionReleased {
		return domain.Run{}, fmt.Errorf("%w: released scenario revisions are immutable; test a draft revision", domain.ErrConflict)
	}
	if kind == domain.RunScenarioTest && scenario.CurrentRevisionID != revisionID {
		return domain.Run{}, fmt.Errorf("%w: only the current scenario revision can be tested", domain.ErrConflict)
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return domain.Run{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return domain.Run{}, err
	}
	if environment.Revision == nil {
		return domain.Run{}, fmt.Errorf("%w: environment has no revision", domain.ErrConflict)
	}
	validator := user
	if user.Role == domain.RoleEnvironmentOwner {
		validator = domain.User{ID: scenario.OwnerID, Role: domain.RoleScenarioOwner}
	}
	issues, validationErr := p.ValidateScenario(ctx, validator, revisionID)
	if validationErr != nil {
		return domain.Run{}, validationErr
	}
	if len(issues) > 0 {
		base := &domain.ValidationError{Message: "scenario validation failed", Details: issues}
		return domain.Run{}, actionableExistingError(base, "scenario.graph_invalid", "当前 DAG 或锁定 Release 未通过校验", "检查场景问题", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
	}
	ordered, err := topologicalNodes(revision.Graph)
	if err != nil {
		return domain.Run{}, err
	}
	steps := make([]lockedStep, 0, len(ordered))
	releaseByNode := map[string]domain.ComponentRelease{}
	resolvedByNode := map[string]map[string]any{}
	provenanceByNode := map[string]map[string]resolvedParameter{}
	for _, node := range ordered {
		release, releaseErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil {
			return domain.Run{}, releaseErr
		}
		releaseByNode[node.ID] = release
	}
	for _, node := range ordered {
		release := releaseByNode[node.ID]
		if constraintErr := validateEnvironmentConstraints(release.EnvironmentConstraints, environment.Revision.Facts); constraintErr != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, constraintErr)
		}
		component, componentErr := p.store.GetComponent(ctx, release.ComponentID, false)
		if componentErr != nil {
			return domain.Run{}, componentErr
		}
		action, actionErr := actionFor(release, node.Action)
		if actionErr != nil {
			return domain.Run{}, actionErr
		}
		variables, provenance, resolveErr := resolveOwnParameters(release, node, *environment.Revision, false)
		if resolveErr != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, resolveErr)
		}
		if mapErr := applyParameterMappings(release, node, revision.Graph, releaseByNode, resolvedByNode, variables, provenance); mapErr != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, mapErr)
		}
		if err := validateResolvedParameters(release.Parameters, variables); err != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, err)
		}
		resolvedByNode[node.ID] = variables
		provenanceByNode[node.ID] = provenance
		action.HostGroup = node.HostGroup
		step, stepErr := p.lockAction(component, node.ID, release, action, variables)
		if stepErr != nil {
			return domain.Run{}, stepErr
		}
		steps = append(steps, step)
		if node.Action != domain.ActionVerify {
			verifyRelease, verifyVariables := release, variables
			if node.Action == domain.ActionRollback {
				// A rollback without version endpoints is the compensating job for
				// this release's installation. It verifies its own postcondition and
				// intentionally does not append the install-state verify action.
				if action.FromReleaseID == "" && action.ToReleaseID == "" {
					continue
				}
				target, targetErr := p.store.GetComponentRelease(ctx, action.ToReleaseID)
				if targetErr != nil || target.ComponentID != release.ComponentID || (target.Status != domain.ReleaseReleased && target.Status != domain.ReleaseDeprecated) {
					return domain.Run{}, fmt.Errorf("%w: rollback target must be a retained release of the same component", domain.ErrInvalid)
				}
				verifyRelease = target
				verifyVariables, _, targetErr = resolveOwnParameters(target, node, *environment.Revision, false)
				if targetErr != nil {
					return domain.Run{}, fmt.Errorf("node %s rollback target: %w", node.ID, targetErr)
				}
				if mapErr := applyParameterMappings(target, node, revision.Graph, releaseByNode, resolvedByNode, verifyVariables, map[string]resolvedParameter{}); mapErr != nil {
					return domain.Run{}, fmt.Errorf("node %s rollback target: %w", node.ID, mapErr)
				}
				if err := validateResolvedParameters(target.Parameters, verifyVariables); err != nil {
					return domain.Run{}, fmt.Errorf("node %s rollback target: %w", node.ID, err)
				}
			}
			if verify, ok := findAction(verifyRelease, domain.ActionVerify); ok {
				verifyStep, verifyErr := p.lockAction(component, node.ID+"-verify", verifyRelease, verify, verifyVariables)
				if verifyErr != nil {
					return domain.Run{}, verifyErr
				}
				steps = append(steps, verifyStep)
			}
		}
	}
	if kind == domain.RunScenarioTest {
		now := time.Now().UTC()
		if err := p.store.SetScenarioRevisionStatus(ctx, revisionID, []domain.RevisionStatus{domain.RevisionDraft, domain.RevisionTestPassed}, domain.RevisionTesting, now); err != nil {
			return domain.Run{}, err
		}
	}
	run, err := p.createRun(ctx, user, environment, kind, "", revision.ID, "", steps, provenanceByNode, "")
	if err != nil && kind == domain.RunScenarioTest {
		_ = p.store.SetScenarioRevisionStatus(ctx, revisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
	}
	return run, err
}

func findAction(release domain.ComponentRelease, kind domain.ActionKind) (domain.ActionDefinition, bool) {
	for _, action := range release.Actions {
		if action.Kind == kind {
			return action, true
		}
	}
	return domain.ActionDefinition{}, false
}

func matchesParameterType(value any, expected string) bool {
	return domain.MatchesParameterType(value, expected)
}
func containsParameterValue(values []any, value any) bool {
	return domain.ContainsParameterValue(values, value)
}
func parameterValuesEqual(left, right any) bool { return domain.ParameterValuesEqual(left, right) }

func validateEnvironmentConstraints(constraints, facts map[string]any) error {
	for key, expected := range constraints {
		actual, found := facts[key]
		if !found {
			return fmt.Errorf("%w: environment fact %q is required by the component", domain.ErrInvalid, key)
		}
		if !constraintMatches(expected, actual) {
			return fmt.Errorf("%w: environment fact %q=%v does not satisfy %v", domain.ErrInvalid, key, actual, expected)
		}
	}
	return nil
}

func constraintMatches(expected, actual any) bool {
	switch values := expected.(type) {
	case []any:
		for _, value := range values {
			if reflect.DeepEqual(value, actual) {
				return true
			}
		}
		return false
	case []string:
		for _, value := range values {
			if reflect.DeepEqual(value, actual) {
				return true
			}
		}
		return false
	default:
		return reflect.DeepEqual(expected, actual)
	}
}

func (b *PlanBuilder) lockAction(component domain.Component, nodeID string, release domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) (lockedStep, error) {
	step := lockedStep{
		ID: newID("locked-step"), NodeID: nodeID, Name: component.Name + " · " + string(action.Kind),
		ComponentID: component.ID, ComponentName: component.Name, ReleaseID: release.ID, ReleaseVersion: release.Version,
		ReleaseSpecDigest: componentReleaseSpecDigest(release), ActionID: action.ID,
		Action: action.Kind, FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		Playbook: action.Playbook, Tags: append([]string(nil), action.Tags...),
		Limit: action.HostGroup, Variables: cloneMap(variables),
		RequiredCredentials: append([]string(nil), action.RequiredCredentials...), TimeoutSeconds: action.TimeoutSeconds,
		NeedsApproval: action.NeedsApproval(),
		RetrySafe:     action.Kind == domain.ActionInspect || action.Kind == domain.ActionPreflight || action.Kind == domain.ActionVerify || (action.Kind == domain.ActionInstall && action.Idempotent),
	}
	return step, nil
}

func (b *PlanBuilder) prepareLockedPlan(ctx context.Context, environment domain.Environment, kind domain.RunKind, runID string, capturedAt time.Time, steps []lockedStep) (lockedPlan, string, bool, error) {
	p := b.platform
	if p.runner == nil {
		return lockedPlan{}, "", false, fmt.Errorf("%w: Ansible runner is not configured", domain.ErrConflict)
	}
	if environment.Revision == nil {
		return lockedPlan{}, "", false, fmt.Errorf("%w: environment revision is required", domain.ErrInvalid)
	}
	if err := p.validateEnvironmentFactsCatalog(ctx, environment.Revision.Facts, true); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := p.validateEnvironmentInventoryCatalog(ctx, environment.Revision.Inventory); err != nil {
		return lockedPlan{}, "", false, err
	}
	plan := lockedPlan{Steps: append([]lockedStep(nil), steps...)}
	for index := range plan.Steps {
		plan.Steps[index].Variables = cloneMap(plan.Steps[index].Variables)
	}
	if err := p.bindComponentArtifacts(ctx, *environment.Revision, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := p.bindComponentImages(ctx, *environment.Revision, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := injectEnvironmentVariables(*environment.Revision, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	for _, step := range plan.Steps {
		if err := rejectSensitiveMap(step.Variables, "run parameter"); err != nil {
			return lockedPlan{}, "", false, err
		}
	}
	if err := validateRequiredCredentials(environment.Revision.CredentialRefs, plan.Steps); err != nil {
		return lockedPlan{}, "", false, actionableExistingError(
			err,
			"environment.credentials_missing",
			"目标 Environment Revision 缺少执行计划要求的 CredentialRef",
			"查看目标环境凭据",
			fmt.Sprintf("/environments?selected=%s&tab=credentials", environment.ID),
		)
	}
	if err := validatePlanHostGroups(environment.Revision.Inventory, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	if digester, ok := p.runner.(planDigestRunner); ok {
		playbooks := make([]string, 0, len(plan.Steps))
		for _, step := range plan.Steps {
			playbooks = append(playbooks, step.Playbook)
		}
		digests, treeDigest, err := digester.DigestPlan(playbooks)
		if err != nil {
			return lockedPlan{}, "", false, err
		}
		plan.TreeDigest = treeDigest
		for i := range plan.Steps {
			plan.Steps[i].PlaybookDigest = digests[plan.Steps[i].Playbook]
		}
	} else if digester, ok := p.runner.(digestRunner); ok {
		for i := range plan.Steps {
			playbookDigest, treeDigest, err := digester.Digest(plan.Steps[i].Playbook)
			if err != nil {
				return lockedPlan{}, "", false, err
			}
			if plan.TreeDigest != "" && plan.TreeDigest != treeDigest {
				return lockedPlan{}, "", false, fmt.Errorf("%w: playbooks resolved to different executable trees", domain.ErrConflict)
			}
			plan.Steps[i].PlaybookDigest = playbookDigest
			plan.TreeDigest = treeDigest
		}
	}
	if err := p.bindBackupPlan(ctx, environment.ID, runID, kind, capturedAt, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if kind == domain.RunEnvironmentRollback {
		plan.InstallationBaseline = installationBaselineFromSteps(plan.Steps)
		plan.InstallationBaselineDigest = installationBaselineDigest(plan.InstallationBaseline)
	}
	planDigest := componentTestPlanDigest(environment.CurrentRevisionID, plan)
	destructive := len(plan.DeliveryRequirements) > 0 || len(plan.ArtifactTransfers) > 0 || len(plan.ImageTransfers) > 0
	for _, step := range plan.Steps {
		if step.NeedsApproval {
			destructive = true
			break
		}
	}
	return plan, planDigest, destructive, nil
}

func componentTestPlanDigest(environmentRevisionID string, plan lockedPlan) string {
	type digestStep struct {
		ComponentID         string
		ReleaseID           string
		ReleaseVersion      string
		ReleaseSpecDigest   string
		ActionID            string
		Action              domain.ActionKind
		FromReleaseID       string
		ToReleaseID         string
		Playbook            string
		PlaybookDigest      string
		Tags                []string
		Limit               string
		Variables           map[string]any
		RequiredCredentials []string
		TimeoutSeconds      int
		NeedsApproval       bool
		BackupRef           string
		BackupInstallRunID  string
		BackupPlaybookSHA   string
		BackupCapturedAt    string
	}
	digestSteps := make([]digestStep, 0, len(plan.Steps))
	sameRunBackupRefs := map[string]bool{}
	for _, step := range plan.Steps {
		switch step.Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			if step.BackupRef != "" {
				sameRunBackupRefs[step.BackupRef] = true
			}
		}
	}
	for _, step := range plan.Steps {
		variables := cloneMap(step.Variables)
		for _, name := range []string{
			"clusterforge_backup_ref", "clusterforge_backup_marker", "clusterforge_backup_operation",
			"clusterforge_backup_cleanup_on_success", "clusterforge_backup_metadata",
		} {
			delete(variables, name)
		}
		backupRef, backupInstallRunID, backupPlaybookSHA, backupCapturedAt := "", "", "", ""
		if (step.Action == domain.ActionRollback || step.Action == domain.ActionUninstall) && step.Backup != nil {
			backupRef, backupInstallRunID, backupPlaybookSHA = step.BackupRef, step.Backup.InstallRunID, step.Backup.PlaybookSHA256
			backupCapturedAt = step.Backup.CapturedAt.UTC().Format(time.RFC3339Nano)
			if sameRunBackupRefs[step.BackupRef] {
				backupRef, backupInstallRunID, backupCapturedAt = "same-run", "same-run", "same-run"
			}
		}
		digestSteps = append(digestSteps, digestStep{
			ComponentID: step.ComponentID, ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, ReleaseSpecDigest: step.ReleaseSpecDigest,
			ActionID: step.ActionID, Action: step.Action, FromReleaseID: step.FromReleaseID, ToReleaseID: step.ToReleaseID,
			Playbook: step.Playbook, PlaybookDigest: step.PlaybookDigest, Tags: step.Tags, Limit: step.Limit,
			Variables: variables, RequiredCredentials: step.RequiredCredentials,
			TimeoutSeconds: step.TimeoutSeconds, NeedsApproval: step.NeedsApproval,
			BackupRef: backupRef, BackupInstallRunID: backupInstallRunID,
			BackupPlaybookSHA: backupPlaybookSHA, BackupCapturedAt: backupCapturedAt,
		})
	}
	digestRequirements := append([]DeliveryRequirement(nil), plan.DeliveryRequirements...)
	for index := range digestRequirements {
		digestRequirements[index].StepIDs = nil
	}
	encoded, _ := json.Marshal(struct {
		EnvironmentRevisionID      string
		TreeDigest                 string
		Steps                      []digestStep
		ArtifactTransfers          []lockedArtifactTransfer
		ImageTransfers             []lockedImageTransfer
		DeliveryRequirements       []DeliveryRequirement
		InstallationBaselineDigest string
	}{EnvironmentRevisionID: environmentRevisionID, TreeDigest: plan.TreeDigest, Steps: digestSteps, ArtifactTransfers: plan.ArtifactTransfers, ImageTransfers: plan.ImageTransfers, DeliveryRequirements: digestRequirements, InstallationBaselineDigest: plan.InstallationBaselineDigest})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func (b *PlanBuilder) componentTestPlanDTO(ctx context.Context, environment domain.Environment, plan lockedPlan, digest string, destructive bool) ComponentTestPlan {
	p := b.platform
	versions := map[string]string{}
	version := func(releaseID string) string {
		if releaseID == "" {
			return ""
		}
		if cached, ok := versions[releaseID]; ok {
			return cached
		}
		release, err := p.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return ""
		}
		versions[releaseID] = release.Version
		return release.Version
	}
	steps := make([]ComponentTestPlanStep, 0, len(plan.Steps))
	for index, step := range plan.Steps {
		var backupInstallRunID, backupPlaybookSHA string
		var backupCapturedAt *time.Time
		if (step.Action == domain.ActionRollback || step.Action == domain.ActionUninstall) && step.Backup != nil {
			backupInstallRunID, backupPlaybookSHA = step.Backup.InstallRunID, step.Backup.PlaybookSHA256
			capturedAt := step.Backup.CapturedAt
			backupCapturedAt = &capturedAt
		}
		steps = append(steps, ComponentTestPlanStep{
			Order: index + 1, ComponentID: step.ComponentID, ComponentName: step.ComponentName,
			ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, Action: step.Action,
			Playbook: step.Playbook, Limit: step.Limit, NeedsApproval: step.NeedsApproval,
			FromReleaseID: step.FromReleaseID, FromReleaseVersion: version(step.FromReleaseID),
			ToReleaseID: step.ToReleaseID, ToReleaseVersion: version(step.ToReleaseID),
			BackupRef: step.BackupRef, BackupInstallRunID: backupInstallRunID,
			BackupCapturedAt: backupCapturedAt, BackupPlaybookSHA: backupPlaybookSHA,
		})
	}
	return ComponentTestPlan{
		EnvironmentID: environment.ID, EnvironmentRevisionID: environment.CurrentRevisionID,
		Destructive: destructive, RequiresApproval: destructive, PlanDigest: digest, Steps: steps,
		DeliveryRequirements: append([]DeliveryRequirement{}, plan.DeliveryRequirements...),
	}
}
