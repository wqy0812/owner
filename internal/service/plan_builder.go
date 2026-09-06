package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func (p *PlanBuilder) prepareComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (preparedComponentTest, error) {
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
	if user.Role == domain.RoleEnvironmentOwner && environment.OwnerID != user.ID {
		return preparedComponentTest{}, domain.ErrForbidden
	}
	if input.RollbackVerification.Kind != "" || input.RollbackVerification.ReleaseID != "" {
		return preparedComponentTest{}, fmt.Errorf("%w: rollbackVerification is not supported; rollback uses its bound checks", domain.ErrInvalid)
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

	if input.ActionID != "" {
		action, ok := release.ActionByID(input.ActionID)
		if !ok {
			return preparedComponentTest{}, domain.ErrNotFound
		}
		variables, provenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, *environment.Revision, true)
		if err != nil {
			return preparedComponentTest{}, err
		}
		if err := validateResolvedParameters(release.Parameters, variables); err != nil {
			return preparedComponentTest{}, err
		}
		if action.HostGroup == "" {
			action.HostGroup = "all"
		}
		step, err := p.actions.lockAction(component, "component-"+component.ID, release, action, variables)
		if err != nil {
			return preparedComponentTest{}, err
		}
		return preparedComponentTest{release: release, environment: environment, action: action.Kind, steps: []lockedStep{step}, provenance: map[string]map[string]resolvedParameter{step.NodeID: provenance}}, nil
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
	step, err := p.actions.lockAction(component, nodeID, release, selected, variables)
	if err != nil {
		return preparedComponentTest{}, err
	}
	steps = append(steps, step)

	return preparedComponentTest{
		release: release, environment: environment, action: selected.Kind, steps: steps,
		provenance: map[string]map[string]resolvedParameter{nodeID: provenance},
	}, nil
}

func (p *PlanBuilder) prepareEvolutionRoundTrip(ctx context.Context, component domain.Component, release domain.ComponentRelease, environment domain.Environment, input ComponentTestRequest) (preparedComponentTest, error) {
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
	upgrade, err := actionFor(release, domain.ActionUpgrade)
	if err != nil {
		return preparedComponentTest{}, err
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
		step, stepErr := p.actions.lockAction(component, nodeID, item, action, variables)
		if stepErr == nil {
			step.SourceNodeID = "component-" + component.ID
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
		{targetNodeID, release, upgrade, targetVariables},
		{targetNodeID + "-rollback", release, rollback, targetVariables},
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
	for _, kind := range []domain.ActionKind{domain.ActionUpgrade, domain.ActionInstall, domain.ActionConfigure} {
		if action, found := findAction(release, kind); found {
			return action, true
		}
	}
	return domain.ActionDefinition{}, false
}

type preparedScenario struct {
	environment domain.Environment
	revision    domain.ScenarioRevision
	steps       []lockedStep
	provenance  map[string]map[string]resolvedParameter
}

func (p *PlanBuilder) prepareScenarioExecution(ctx context.Context, user domain.User, revisionID, environmentID string, kind domain.RunKind, baselineVerification ...bool) (preparedScenario, error) {
	verifyBaseline := len(baselineVerification) > 0 && baselineVerification[0]
	revision, err := p.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return preparedScenario{}, err
	}
	scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
	if err != nil {
		return preparedScenario{}, err
	}
	if user.Role != domain.RoleEnvironmentOwner {
		if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
			return preparedScenario{}, err
		}
	}
	if kind == domain.RunScenario && !verifyBaseline && revision.Status != domain.RevisionReleased {
		return preparedScenario{}, fmt.Errorf("%w: only a released scenario can run outside testing", domain.ErrConflict)
	}
	if kind == domain.RunScenarioTest && revision.Status == domain.RevisionReleased {
		return preparedScenario{}, fmt.Errorf("%w: released scenario revisions are immutable; test a draft revision", domain.ErrConflict)
	}
	if kind == domain.RunScenarioTest && scenario.CurrentRevisionID != revisionID {
		return preparedScenario{}, fmt.Errorf("%w: only the current scenario revision can be tested", domain.ErrConflict)
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return preparedScenario{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return preparedScenario{}, err
	}
	if environment.Revision == nil {
		return preparedScenario{}, fmt.Errorf("%w: environment has no revision", domain.ErrConflict)
	}
	validator := user
	if user.Role == domain.RoleEnvironmentOwner {
		validator = domain.User{ID: scenario.OwnerID, Role: domain.RoleScenarioOwner}
	}
	issues, validationErr := p.scenarios.Validate(ctx, validator, revisionID)
	if validationErr != nil {
		return preparedScenario{}, validationErr
	}
	if len(issues) > 0 {
		base := &domain.ValidationError{Message: "scenario validation failed", Details: issues}
		return preparedScenario{}, actionableExistingError(base, "scenario.graph_invalid", "当前 DAG 或锁定 Release 未通过校验", "检查场景问题", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
	}
	releaseByNode := map[string]domain.ComponentRelease{}
	for _, node := range revision.Graph.Nodes {
		release, releaseErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil {
			return preparedScenario{}, releaseErr
		}
		releaseByNode[node.ID] = release
	}
	planGraph, dependencyIssues := normalizeScenarioGraph(revision.Graph, releaseByNode)
	if len(dependencyIssues) > 0 {
		return preparedScenario{}, &domain.ValidationError{Message: "scenario dependency graph is incomplete", Details: dependencyIssues}
	}
	if issues := scenarioExecutionOrderIssues(planGraph); len(issues) > 0 {
		return preparedScenario{}, &domain.ValidationError{Message: "scenario execution order is undetermined", Details: issues}
	}
	ordered, err := topologicalNodes(planGraph)
	if err != nil {
		return preparedScenario{}, err
	}
	if err := domain.MatchEnvironment(revision.EnvironmentConstraints, environment.Revision.Facts); err != nil {
		return preparedScenario{}, fmt.Errorf("场景 %s: %w", scenario.Name, err)
	}
	resolvedByNode, provenanceByNode, err := resolveScenarioParameters(planGraph, releaseByNode, *environment.Revision)
	if err != nil {
		return preparedScenario{}, err
	}
	steps := make([]lockedStep, 0, len(ordered))
	for _, node := range ordered {
		release := releaseByNode[node.ID]
		if constraintErr := validateEnvironmentConstraints(release.EnvironmentConstraints, environment.Revision.Facts); constraintErr != nil {
			return preparedScenario{}, fmt.Errorf("node %s: %w", node.ID, constraintErr)
		}
		component, componentErr := p.store.GetComponent(ctx, release.ComponentID, false)
		if componentErr != nil {
			return preparedScenario{}, componentErr
		}
		action, actionErr := actionFor(release, node.Action)
		if actionErr != nil {
			return preparedScenario{}, actionErr
		}
		variables := resolvedByNode[node.ID]
		action.HostGroup = node.HostGroup
		step, stepErr := p.actions.lockAction(component, node.ID, release, action, variables)
		if stepErr != nil {
			return preparedScenario{}, stepErr
		}
		steps = append(steps, step)

	}
	return preparedScenario{environment: environment, revision: revision, steps: steps, provenance: provenanceByNode}, nil
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
	return domain.MatchEnvironment(constraints, facts)
}

func (b *PlanBuilder) prepareLockedPlan(ctx context.Context, environment domain.Environment, kind domain.RunKind, runID string, capturedAt time.Time, steps []lockedStep) (lockedPlan, string, bool, error) {
	if environment.Revision == nil {
		return lockedPlan{}, "", false, fmt.Errorf("%w: environment revision is required", domain.ErrInvalid)
	}
	if err := b.catalogRules.validateEnvironmentFactsCatalog(ctx, environment.Revision.Facts, true); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := b.catalogRules.validateEnvironmentInventoryCatalog(ctx, environment.Revision.Inventory); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := b.rollback.bindRollbackCheckSources(ctx, environment.ID, steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	expanded, err := b.actions.expandActionSteps(ctx, steps)
	if err != nil {
		return lockedPlan{}, "", false, err
	}
	plan := lockedPlan{Steps: expanded}
	plan.Runtime, err = b.runtime.RuntimeIdentity(ctx)
	if err == nil {
		err = b.runtime.CheckRuntime(ctx)
	}
	if err != nil {
		reference := newID("executor-check")
		log.Printf("executor check %s failed: %s", reference, safePreparationError(err))
		return lockedPlan{}, "", false, &domain.CodedError{Code: "ansible.runtime_unavailable", Message: "无法识别或启动 Ansible 运行时：" + safePreparationError(err) + "；请检查平台控制机运行时与插件。关联编号：" + reference, Cause: domain.ErrConflict}
	}

	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.Phase == "execute" && step.RetrySafe {
			verified, err := b.store.HasSuccessfulActionTest(ctx, step.ReleaseID, step.ReleaseSpecDigest, step.ActionID, plan.Runtime.AnsibleCore, plan.Runtime.Python)
			if err != nil {
				return lockedPlan{}, "", false, err
			}
			step.RetrySafe = verified
		}
	}
	for index := range plan.Steps {
		plan.Steps[index].Variables = cloneMap(plan.Steps[index].Variables)
	}
	if err := b.delivery.bindComponentArtifacts(ctx, *environment.Revision, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := b.delivery.bindComponentImages(ctx, *environment.Revision, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := injectEnvironmentVariables(*environment.Revision, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	if kind != domain.RunEnvironmentRollback {
		allFrozen := len(plan.Steps) > 0
		for _, step := range plan.Steps {
			if !step.SourceParametersFrozen && step.SourceType != "scenario_acceptance" {
				allFrozen = false
			}
		}
		if !allFrozen {
			if err := validateResourcePlan(ctx, b.store, environment, &plan); err != nil {
				return lockedPlan{}, "", false, err
			}
		}
	}
	if err := observePreparationPlan(ctx, environment, plan); err != nil {
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

	if err := b.workspaceVerifier.bindVerifiedWorkspaceDigests(ctx, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	tree := sha256.New()
	releaseTrees := map[string]string{}
	for _, step := range plan.Steps {
		identity := step.ReleaseID
		if step.SourceType == "scenario_acceptance" {
			identity = "scenario:" + step.ScenarioRevisionID
		}
		releaseTrees[identity] = step.WorkspaceDigest
	}
	releaseIDs := make([]string, 0, len(releaseTrees))
	for releaseID := range releaseTrees {
		releaseIDs = append(releaseIDs, releaseID)
	}
	sort.Strings(releaseIDs)
	for _, releaseID := range releaseIDs {
		_, _ = tree.Write([]byte(releaseID + "\x00" + releaseTrees[releaseID] + "\x00"))
	}
	plan.TreeDigest = fmt.Sprintf("%x", tree.Sum(nil))
	if err := b.rollback.bindBackupPlan(ctx, environment.ID, runID, kind, capturedAt, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	syncActionCheckContext(plan.Steps)
	for _, step := range plan.Steps {
		if step.Phase == "execute" {
			plan.ParentSteps = append(plan.ParentSteps, step)
		}
	}
	if kind == domain.RunEnvironmentRollback {
		current, err := b.rollback.recoveryBaselines(ctx, environment.ID)
		if err != nil {
			return lockedPlan{}, "", false, err
		}
		plan.RecoveryEnvironmentDigest = digestValue(current)
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

// bindVerifiedWorkspaceDigests joins the immutable Release contract, its
// persisted manifest, and the current executable bytes. It deliberately groups
// steps by Release so a plan scans each workspace once.

func componentTestPlanDigest(environmentRevisionID string, plan lockedPlan) string {
	type digestStep struct {
		SourceParametersFrozen         bool   `json:",omitempty"`
		RollbackSourceActionID         string `json:",omitempty"`
		PreCheckRequired               bool   `json:",omitempty"`
		NodeID, ParentActionID, Phase  string
		Become, GatherFacts, RetrySafe bool
		ComponentID                    string
		ReleaseID                      string
		ReleaseVersion                 string
		ReleaseSpecDigest              string
		ActionID                       string
		Action                         domain.ActionKind
		FromReleaseID                  string
		ToReleaseID                    string
		Playbook                       string
		PlaybookDigest                 string
		WorkspaceDigest                string
		Tags                           []string
		Limit                          string
		Variables                      map[string]any
		RequiredCredentials            []string
		TimeoutSeconds                 int
		NeedsApproval                  bool
		BackupRef                      string
		BackupInstallRunID             string
		BackupPlaybookSHA              string
		BackupCapturedAt               string
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
			SourceParametersFrozen: step.SourceParametersFrozen, RollbackSourceActionID: step.RollbackSourceActionID, PreCheckRequired: step.PreCheckRequired,
			NodeID: step.SourceNodeID, ParentActionID: step.ParentActionID, Phase: step.Phase, Become: step.Become, GatherFacts: step.GatherFacts, RetrySafe: step.RetrySafe,
			ComponentID: step.ComponentID, ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, ReleaseSpecDigest: step.ReleaseSpecDigest,
			ActionID: step.ActionID, Action: step.Action, FromReleaseID: step.FromReleaseID, ToReleaseID: step.ToReleaseID,
			Playbook: step.Playbook, PlaybookDigest: step.PlaybookDigest, WorkspaceDigest: step.WorkspaceDigest, Tags: step.Tags, Limit: step.Limit,
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
		Runtime                    ansiblerunner.JobRuntime
		EnvironmentRevisionID      string
		TreeDigest                 string
		Steps                      []digestStep
		ArtifactTransfers          []lockedArtifactTransfer
		ImageTransfers             []lockedImageTransfer
		DeliveryRequirements       []DeliveryRequirement
		InstallationBaselineDigest string
		RecoveryEnvironmentDigest  string
	}{Runtime: plan.Runtime, EnvironmentRevisionID: environmentRevisionID, TreeDigest: plan.TreeDigest, Steps: digestSteps, ArtifactTransfers: plan.ArtifactTransfers, ImageTransfers: plan.ImageTransfers, DeliveryRequirements: digestRequirements, InstallationBaselineDigest: plan.InstallationBaselineDigest, RecoveryEnvironmentDigest: plan.RecoveryEnvironmentDigest})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func (b *PlanBuilder) componentTestPlanDTO(ctx context.Context, environment domain.Environment, plan lockedPlan, digest string, destructive bool) ComponentTestPlan {
	versions := map[string]string{}
	version := func(releaseID string) string {
		if releaseID == "" {
			return ""
		}
		if cached, ok := versions[releaseID]; ok {
			return cached
		}
		release, err := b.store.GetComponentRelease(ctx, releaseID)
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
			Name: step.Name, Stage: step.Stage, SourceType: step.SourceType,
			RollbackSourceActionID: step.RollbackSourceActionID, Phase: step.Phase, ParentActionID: step.ParentActionID, ActionID: step.ActionID, NodeID: step.SourceNodeID, Order: index + 1, ComponentID: step.ComponentID, ComponentName: step.ComponentName,
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
