package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type ScenarioExecutionRequest struct {
	EnvironmentID      string                       `json:"environmentId"`
	ExecutionMode      domain.ScenarioExecutionMode `json:"executionMode"`
	ExpectedPlanDigest string                       `json:"expectedPlanDigest"`
	IdempotencyKey     string                       `json:"idempotencyKey"`
	TestOnly           bool                         `json:"testOnly"`
}

func (s *ScenarioService) TestEvidence(ctx context.Context, id string) (map[string]string, error) {
	return s.store.ScenarioTestEvidence(ctx, id)
}

type ScenarioOperation struct {
	NodeID        string `json:"nodeId"`
	Name          string `json:"name"`
	Change        string `json:"change"`
	FromReleaseID string `json:"fromReleaseId,omitempty"`
	ToReleaseID   string `json:"toReleaseId,omitempty"`
}
type scenarioTargetNode struct {
	NodeID      string         `json:"nodeId"`
	ComponentID string         `json:"componentId"`
	ReleaseID   string         `json:"releaseId"`
	Variables   map[string]any `json:"variables"`
}
type ScenarioExecutionPreview struct {
	ScenarioRevisionID string                       `json:"scenarioRevisionId"`
	EnvironmentID      string                       `json:"environmentId"`
	ExecutionMode      domain.ScenarioExecutionMode `json:"executionMode"`
	PlanDigest         string                       `json:"planDigest"`
	SourceRevisionID   string                       `json:"sourceRevisionId,omitempty"`
	BaselineRunID      string                       `json:"baselineRunId,omitempty"`
	Operations         []ScenarioOperation          `json:"operations"`
	Steps              []lockedStep                 `json:"steps"`
	Issues             []domain.ValidationIssue     `json:"issues"`
	Ready              bool                         `json:"ready"`
	NeedsApproval      bool                         `json:"needsApproval"`
}
type scenarioExecution struct {
	preview            ScenarioExecutionPreview
	prepared           preparedScenario
	plan               lockedPlan
	target             []scenarioTargetNode
	baseline           domain.ScenarioInstallation
	installationDigest string
	historical         *store.ScenarioHistoricalBaseline
	recoveredReceipts  []store.ActionExecutionReceipt
}

func normalizeScenarioMode(mode domain.ScenarioExecutionMode) (domain.ScenarioExecutionMode, error) {
	if mode == "" {
		return domain.ScenarioExecutionInstall, nil
	}
	switch mode {
	case domain.ScenarioExecutionInstall, domain.ScenarioExecutionUpgrade, domain.ScenarioExecutionBaselineVerify:
		return mode, nil
	}
	return mode, fmt.Errorf("%w: unsupported scenario execution mode", domain.ErrInvalid)
}

func (p *ExecutionService) PreviewScenarioExecution(ctx context.Context, user domain.User, id string, input ScenarioExecutionRequest, kind domain.RunKind) (ScenarioExecutionPreview, error) {
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	x, err := p.planner.prepareScenarioLifecycle(ctx, user, id, input, kind, "preview", time.Unix(0, 0).UTC())
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
			return x.preview, err
		}
		var validation *domain.ValidationError
		if errors.As(err, &validation) {
			if issues, ok := validation.Details.([]domain.ValidationIssue); ok {
				x.preview.Issues = append(x.preview.Issues, issues...)
			}
		}
		if len(x.preview.Issues) == 0 {
			x.preview.Issues = append(x.preview.Issues, domain.ValidationIssue{Code: "scenario.execution_blocked", Message: err.Error()})
		}
		x.preview.Ready = false
		return x.preview, nil
	}
	return x.preview, nil
}

func (p *PlanBuilder) prepareScenarioLifecycle(ctx context.Context, user domain.User, id string, input ScenarioExecutionRequest, kind domain.RunKind, runID string, at time.Time) (scenarioExecution, error) {
	mode, err := normalizeScenarioMode(input.ExecutionMode)
	x := scenarioExecution{preview: ScenarioExecutionPreview{ScenarioRevisionID: id, EnvironmentID: input.EnvironmentID, ExecutionMode: mode, Operations: []ScenarioOperation{}, Steps: []lockedStep{}, Issues: []domain.ValidationIssue{}}}
	if err != nil {
		return x, err
	}
	if kind != domain.RunScenario && kind != domain.RunScenarioTest {
		return x, domain.ErrInvalid
	}
	if mode == domain.ScenarioExecutionBaselineVerify && kind != domain.RunScenario {
		return x, fmt.Errorf("%w: baseline verification is not a test", domain.ErrInvalid)
	}
	prepared, err := p.prepareScenarioExecution(ctx, user, id, input.EnvironmentID, kind, mode == domain.ScenarioExecutionBaselineVerify)
	if err != nil {
		return x, err
	}
	x.prepared = prepared
	revision := prepared.revision
	acceptanceEnvironment := *prepared.environment.Revision
	if len(revision.AcceptanceJobs) == 0 && mode != domain.ScenarioExecutionBaselineVerify {
		return x, fmt.Errorf("%w: 场景必须至少配置一项业务验收作业", domain.ErrConflict)
	}
	if user.Role == domain.RoleEnvironmentOwner && prepared.environment.OwnerID != user.ID {
		return x, domain.ErrForbidden
	}
	baseline, err := p.store.GetScenarioInstallation(ctx, input.EnvironmentID, revision.ScenarioID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return x, err
	}
	x.baseline = baseline
	installed, err := p.store.ListEnvironmentComponentInstallations(ctx, input.EnvironmentID)
	if err != nil {
		return x, err
	}
	x.installationDigest = store.ScenarioComponentInstallationDigest(installed)
	if mode == domain.ScenarioExecutionBaselineVerify || mode == domain.ScenarioExecutionInstall {
		receipts, e := p.store.LatestEnvironmentActionReceipts(ctx, input.EnvironmentID)
		if e != nil {
			return x, e
		}
		for _, receipt := range receipts {
			if receipt.Status != "verified" {
				x.recoveredReceipts = append(x.recoveredReceipts, receipt)
			}
		}
		if mode == domain.ScenarioExecutionInstall && len(x.recoveredReceipts) > 0 {
			return x, fmt.Errorf("%w: 安装要求干净环境；存在未恢复的组件变更", domain.ErrConflict)
		}
	}
	releases := map[string]domain.ComponentRelease{}
	for _, node := range revision.Graph.Nodes {
		release, e := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if e != nil {
			return x, e
		}
		releases[node.ID] = release
	}
	resolved, _, err := resolveScenarioParameters(revision.Graph, releases, *prepared.environment.Revision)
	if err != nil {
		return x, err
	}
	seenComponents := map[string]bool{}
	for _, node := range revision.Graph.Nodes {
		release := releases[node.ID]
		if seenComponents[release.ComponentID] {
			return x, fmt.Errorf("%w: 同组件多实例暂不支持场景版本演进", domain.ErrConflict)
		}
		seenComponents[release.ComponentID] = true
		values := cloneMap(resolved[node.ID])
		if values == nil {
			values = map[string]any{}
		}
		for key, value := range prepared.environment.Revision.Variables {
			if _, exists := values[key]; exists {
				return x, fmt.Errorf("%w: environment variable %q conflicts with a component parameter", domain.ErrInvalid, key)
			}
			values[key] = value
		}
		resolved[node.ID] = values
		x.target = append(x.target, scenarioTargetNode{NodeID: node.ID, ComponentID: release.ComponentID, ReleaseID: release.ID, Variables: values})
	}
	var steps []lockedStep
	if mode == domain.ScenarioExecutionInstall {
		count, e := p.store.ScenarioEnvironmentStateCount(ctx, input.EnvironmentID)
		if e != nil {
			return x, e
		}
		if len(installed) > 0 || count > 0 {
			return x, fmt.Errorf("%w: 安装要求干净环境；已有安装或待恢复的基线", domain.ErrConflict)
		}
		ordered, e := topologicalNodes(revision.Graph)
		if e != nil {
			return x, e
		}
		for _, node := range ordered {
			step, e := p.scenarioActionStep(ctx, node, releases[node.ID], domain.ActionInstall, resolved[node.ID], "change")
			if e != nil {
				return x, e
			}
			steps = append(steps, step)
			x.preview.Operations = append(x.preview.Operations, ScenarioOperation{NodeID: node.ID, Name: node.Name, Change: "install", ToReleaseID: node.ReleaseID})
		}
	} else {
		if mode == domain.ScenarioExecutionBaselineVerify {
			needsHistorical := baseline.RunID == "" || baseline.RevisionID == ""
			if !needsHistorical {
				prior, e := p.store.GetRun(ctx, baseline.RunID)
				if e != nil {
					return x, e
				}
				_, e = scenarioSnapshotNodes(prior.InputSnapshot)
				needsHistorical = e != nil
			}
			if needsHistorical {
				candidate, e := p.store.FindScenarioHistoricalBaseline(ctx, input.EnvironmentID, revision.ScenarioID, revision.ID)
				if e != nil {
					return x, e
				}
				x.historical = &candidate
			}
		}
		if x.historical == nil && (baseline.RunID == "" || baseline.RevisionID == "") {
			return x, fmt.Errorf("%w: 环境没有可核实的场景基线", domain.ErrConflict)
		}
		sourceID := revision.SourceRevisionID
		if mode == domain.ScenarioExecutionBaselineVerify {
			sourceID = revision.ID
		} else if sourceID == "" {
			return x, fmt.Errorf("%w: 首版或分支首版没有升级来源", domain.ErrConflict)
		}
		if x.historical == nil && baseline.RevisionID != sourceID {
			return x, fmt.Errorf("%w: 环境实际版本与直接来源版本不匹配", domain.ErrConflict)
		}
		if mode == domain.ScenarioExecutionUpgrade && (baseline.State != "complete" || baseline.TestOnly) {
			return x, fmt.Errorf("%w: 升级要求完整的正式来源基线；请先恢复并复核", domain.ErrConflict)
		}
		source, err := p.store.GetScenarioRevision(ctx, sourceID)
		if err != nil {
			return x, err
		}
		if source.ScenarioID != revision.ScenarioID || (len(source.AcceptanceJobs) == 0 && mode != domain.ScenarioExecutionBaselineVerify) {
			return x, fmt.Errorf("%w: 来源必须属于同一场景且包含业务验收", domain.ErrConflict)
		}
		if mode == domain.ScenarioExecutionUpgrade {
			if _, e := p.store.SuccessfulScenarioSourceRun(ctx, source, revision.SourceRunID); e != nil {
				return x, e
			}
			if _, e := p.store.SuccessfulScenarioSourceRun(ctx, source, baseline.RunID); e != nil {
				return x, e
			}
		}
		var baseRun domain.Run
		if x.historical != nil {
			baseRun = x.historical.Run
		} else {
			baseRun, err = p.store.GetRun(ctx, baseline.RunID)
			if err != nil {
				return x, err
			}
		}
		if baseRun.Status != domain.RunSucceeded || baseRun.ScenarioRevisionID != source.ID || baseRun.EnvironmentID != input.EnvironmentID {
			return x, fmt.Errorf("%w: 基线正式运行身份不匹配", domain.ErrConflict)
		}
		if baseRun.InputSnapshot["scenarioRevisionSpecDigest"] != scenarioRevisionSpecDigest(source) {
			return x, fmt.Errorf("%w: 原始基线的版本内容已变化，不能把编辑后的版本视作已恢复", domain.ErrConflict)
		}
		if mode == domain.ScenarioExecutionUpgrade && (baseRun.Kind != domain.RunScenario || (baseRun.InputSnapshot["executionMode"] != "install" && baseRun.InputSnapshot["executionMode"] != "upgrade")) {
			return x, fmt.Errorf("%w: 基线不属于正式安装或升级", domain.ErrConflict)
		}
		baseEnvironment, err := p.store.GetEnvironmentRevision(ctx, baseRun.EnvironmentRevisionID)
		if err != nil {
			return x, err
		}
		if !sameScenarioHostInventory(baseEnvironment.Inventory, prepared.environment.Revision.Inventory) {
			return x, fmt.Errorf("%w: 主机迁移、主机组变更及扩缩容不在场景升级和基线复核范围内", domain.ErrConflict)
		}
		if mode == domain.ScenarioExecutionBaselineVerify {
			acceptanceEnvironment = baseEnvironment
		}
		var old []scenarioTargetNode
		if x.historical != nil {
			for _, node := range x.historical.Nodes {
				old = append(old, scenarioTargetNode{NodeID: node.NodeID, ComponentID: node.ComponentID, ReleaseID: node.ReleaseID, Variables: cloneMap(node.Variables)})
			}
		} else {
			old, err = scenarioSnapshotNodes(baseRun.InputSnapshot)
			if err != nil {
				return x, err
			}
		}
		if err := matchScenarioInstallations(old, installed); err != nil {
			return x, err
		}
		frozen, e := scenarioFrozenExecutionNodes(baseRun, old)
		if e != nil {
			return x, e
		}
		if mode == domain.ScenarioExecutionUpgrade && baseline.InstallationDigest != x.installationDigest {
			return x, fmt.Errorf("%w: 组件安装基线发生漂移，请重新复核", domain.ErrConflict)
		}
		x.preview.SourceRevisionID = source.ID
		x.preview.BaselineRunID = baseRun.ID
		if mode == domain.ScenarioExecutionBaselineVerify {
			x.target = frozen
			for _, receipt := range x.recoveredReceipts {
				covered := false
				for _, node := range old {
					if receipt.SourceNodeID == node.NodeID && receipt.ComponentID == node.ComponentID {
						covered = true
						break
					}
				}
				if !covered {
					return x, fmt.Errorf("%w: 节点 %s 的未完成变更不属于待复核基线，须先证明其卸载或清理结果", domain.ErrConflict, receipt.SourceNodeID)
				}
			}
			for _, n := range old {
				resolved[n.NodeID] = cloneMap(n.Variables)
			}
		} else {
			before, e := p.scenarioStateChecks(ctx, source, frozen, "source_verify")
			if e != nil {
				return x, e
			}
			steps = append(steps, before...)
			changes, operations, e := p.scenarioUpgradeSteps(ctx, source, revision, old, x.target, frozen)
			if e != nil {
				return x, e
			}
			steps = append(steps, changes...)
			x.preview.Operations = operations
		}
	}
	checks, err := p.scenarioStateChecks(ctx, revision, x.target, "target_verify")
	if err != nil {
		return x, err
	}
	if mode == domain.ScenarioExecutionBaselineVerify {
		for i := range checks {
			checks[i].SourceParametersFrozen = true
		}
	}
	steps = append(steps, checks...)
	if len(revision.AcceptanceJobs) > 0 {
		acceptance, err := p.lockScenarioAcceptanceSteps(ctx, revision, acceptanceEnvironment, resolved)
		if err != nil {
			return x, err
		}
		steps = append(steps, acceptance...)
	}
	plan, _, approval, err := p.prepareLockedPlan(ctx, prepared.environment, kind, runID, at, steps)
	if err != nil {
		return x, err
	}
	if mode == domain.ScenarioExecutionBaselineVerify && (len(plan.DeliveryRequirements) > 0 || len(plan.ImageTransfers) > 0 || len(plan.ArtifactTransfers) > 0) {
		return x, fmt.Errorf("%w: 基线复核不能新交付镜像或介质，请先完成环境恢复", domain.ErrConflict)
	}
	bindScenarioStateCheckBackups(plan.Steps, installed)
	if mode == domain.ScenarioExecutionUpgrade {
		for i := range plan.Steps {
			if plan.Steps[i].Backup != nil {
				plan.Steps[i].Variables["clusterforge_backup_cleanup_on_success"] = false
			}
		}
		refreshParentSteps(&plan)
	}
	x.plan = plan
	x.preview.Steps = plan.Steps
	x.preview.NeedsApproval = approval
	x.preview.PlanDigest = digestValue(struct {
		Contract                                     int
		Mode                                         domain.ScenarioExecutionMode
		Revision, Plan, Installations, HistoricalRun string
		Baseline                                     domain.ScenarioInstallation
		RecoveredReceipts                            []store.ActionExecutionReceipt
	}{2, mode, scenarioRevisionSpecDigest(revision), componentTestPlanDigest(prepared.environment.CurrentRevisionID, plan), x.installationDigest, scenarioHistoricalRunID(x.historical), baseline, x.recoveredReceipts})
	x.preview.Ready = true
	return x, nil
}

func scenarioHistoricalRunID(baseline *store.ScenarioHistoricalBaseline) string {
	if baseline == nil {
		return ""
	}
	return baseline.Run.ID
}

func sameScenarioHostInventory(left, right json.RawMessage) bool {
	var a, b InventoryDocument
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	canonical := func(value InventoryDocument) string {
		for i := range value.Hosts {
			sort.Strings(value.Hosts[i].Groups)
		}
		sort.Slice(value.Hosts, func(i, j int) bool { return value.Hosts[i].Name < value.Hosts[j].Name })
		return digestValue(value.Hosts)
	}
	return canonical(a) == canonical(b)
}

// Whole-cluster checks receive the same recovery context as the corresponding
// installed state. Source checks must never observe the target capture.
func bindScenarioStateCheckBackups(steps []lockedStep, installed []domain.EnvironmentComponentInstallation) {
	for i := range steps {
		step := &steps[i]
		if step.Stage != "source_verify" && step.Stage != "target_verify" {
			continue
		}
		if step.Stage == "target_verify" {
			for j := i - 1; j >= 0; j-- {
				parent := steps[j]
				if parent.Phase == "execute" && parent.SourceNodeID == step.SourceNodeID && parent.ReleaseID == step.ReleaseID && parent.Backup != nil {
					step.BackupRef, step.Backup = parent.BackupRef, parent.Backup
					break
				}
			}
		}
		if step.Backup == nil {
			for _, item := range installed {
				if item.NodeID == step.SourceNodeID && item.ComponentID == step.ComponentID && item.ReleaseID == step.ReleaseID {
					metadata := item.Backup
					step.BackupRef, step.Backup = item.BackupRef, &metadata
					break
				}
			}
		}
		if step.Backup != nil {
			bindBackupVariables(step, "verify", false)
		}
	}
}

func (p *PlanBuilder) scenarioActionStep(ctx context.Context, node domain.ScenarioNode, release domain.ComponentRelease, kind domain.ActionKind, values map[string]any, stage string) (lockedStep, error) {
	action, ok := findAction(release, kind)
	if !ok {
		return lockedStep{}, fmt.Errorf("%w: %s 缺少 %s 动作", domain.ErrConflict, node.Name, kind)
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return lockedStep{}, err
	}
	step, err := p.actions.lockAction(component, node.ID, release, action, values)
	step.SourceNodeID = node.ID
	step.SourceType = "component_action"
	step.Stage = stage
	step.MayMutate = kind != domain.ActionCheck
	if kind == domain.ActionUninstall {
		step.NeedsApproval = true
	}
	return step, err
}

func (p *PlanBuilder) scenarioStateChecks(ctx context.Context, revision domain.ScenarioRevision, nodes []scenarioTargetNode, stage string) ([]lockedStep, error) {
	byID := map[string]scenarioTargetNode{}
	for _, node := range nodes {
		byID[node.NodeID] = node
	}
	ordered, err := topologicalNodes(revision.Graph)
	if err != nil {
		return nil, err
	}
	steps := []lockedStep{}
	for _, node := range ordered {
		snapshot, ok := byID[node.ID]
		if !ok || snapshot.ReleaseID != node.ReleaseID {
			return nil, fmt.Errorf("%w: 版本节点快照不完整", domain.ErrConflict)
		}
		release, err := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if err != nil {
			return nil, err
		}
		install, ok := findAction(release, domain.ActionInstall)
		if !ok {
			return nil, fmt.Errorf("%w: %s 缺少已安装状态检查", domain.ErrConflict, node.Name)
		}
		check, ok := release.ActionByID(install.PostCheckActionID)
		if !ok || check.Kind != domain.ActionCheck {
			return nil, fmt.Errorf("%w: %s 缺少已安装状态检查", domain.ErrConflict, node.Name)
		}
		component, err := p.store.GetComponent(ctx, release.ComponentID, false)
		if err != nil {
			return nil, err
		}
		step, err := p.actions.lockAction(component, stage+"-"+node.ID, release, check, snapshot.Variables)
		if err != nil {
			return nil, err
		}
		step.SourceNodeID = node.ID
		step.SourceType = "component_action"
		step.Stage = stage
		step.Phase = "check"
		step.SourceParametersFrozen = stage == "source_verify"
		step.Name = node.Name + " · " + stage
		steps = append(steps, step)
	}
	return steps, nil
}

func scenarioSnapshotNodes(snapshot map[string]any) ([]scenarioTargetNode, error) {
	data, err := json.Marshal(snapshot["targetNodes"])
	if err != nil {
		return nil, err
	}
	var nodes []scenarioTargetNode
	if err = json.Unmarshal(data, &nodes); err != nil || len(nodes) == 0 {
		return nil, fmt.Errorf("%w: 来源 Run 缺少完整的目标组件和参数快照", domain.ErrConflict)
	}
	return nodes, nil
}

// A baseline's final checks contain the actual approved delivery locations and
// complete execution values. Keep these for source checks and deletion; compare
// governed targetNodes separately so incidental recovery metadata is not a change.
func scenarioFrozenExecutionNodes(run domain.Run, nodes []scenarioTargetNode) ([]scenarioTargetNode, error) {
	plan, err := mapToPlan(run.InputSnapshot)
	if err != nil {
		return nil, err
	}
	frozen := append([]scenarioTargetNode(nil), nodes...)
	for i := range frozen {
		found := false
		for _, step := range plan.Steps {
			if step.Stage == "target_verify" && step.SourceNodeID == frozen[i].NodeID && step.ReleaseID == frozen[i].ReleaseID && step.ComponentID == frozen[i].ComponentID {
				if found || step.Variables == nil {
					return nil, fmt.Errorf("%w: 来源节点执行参数快照不完整", domain.ErrConflict)
				}
				frozen[i].Variables = cloneMap(step.Variables)
				found = true
			}
		}
		if !found && run.InputSnapshot["targetNodes"] != nil {
			return nil, fmt.Errorf("%w: 来源节点缺少最终执行参数", domain.ErrConflict)
		}
	}
	return frozen, nil
}
func matchScenarioInstallations(nodes []scenarioTargetNode, installed []domain.EnvironmentComponentInstallation) error {
	if len(nodes) != len(installed) {
		return fmt.Errorf("%w: 当前组件数量与场景基线不符", domain.ErrConflict)
	}
	for _, node := range nodes {
		matches := 0
		for _, item := range installed {
			if item.ComponentID == node.ComponentID && item.ReleaseID == node.ReleaseID && item.NodeID == node.NodeID {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%w: 节点 %s 的当前安装与基线不符", domain.ErrConflict, node.NodeID)
		}
	}
	return nil
}

func (p *PlanBuilder) scenarioUpgradeSteps(ctx context.Context, source, target domain.ScenarioRevision, old, next []scenarioTargetNode, frozenSource ...[]scenarioTargetNode) ([]lockedStep, []ScenarioOperation, error) {
	oldByID := map[string]scenarioTargetNode{}
	nextByID := map[string]scenarioTargetNode{}
	sourceNodes := map[string]domain.ScenarioNode{}
	targetNodes := map[string]domain.ScenarioNode{}
	for _, n := range old {
		oldByID[n.NodeID] = n
	}
	for _, n := range next {
		nextByID[n.NodeID] = n
	}
	for _, n := range source.Graph.Nodes {
		sourceNodes[n.ID] = n
	}
	for _, n := range target.Graph.Nodes {
		targetNodes[n.ID] = n
	}
	operations := []ScenarioOperation{}
	actions := map[string]lockedStep{}
	for _, n := range target.Graph.Nodes {
		t := nextByID[n.ID]
		before, exists := oldByID[n.ID]
		kind := domain.ActionInstall
		change := "install"
		if exists {
			if before.ComponentID != t.ComponentID {
				return nil, nil, fmt.Errorf("%w: 节点 %s 不能更换为其他组件", domain.ErrInvalid, n.ID)
			}
			if before.ReleaseID != t.ReleaseID {
				kind = domain.ActionUpgrade
				change = "upgrade"
			} else if !parameterValuesEqual(before.Variables, t.Variables) {
				kind = domain.ActionConfigure
				change = "configure"
			} else {
				change = "unchanged"
			}
		}
		op := ScenarioOperation{NodeID: n.ID, Name: n.Name, Change: change, FromReleaseID: before.ReleaseID, ToReleaseID: t.ReleaseID}
		operations = append(operations, op)
		if change == "unchanged" {
			continue
		}
		release, err := p.store.GetComponentRelease(ctx, t.ReleaseID)
		if err != nil {
			return nil, nil, err
		}
		if kind == domain.ActionUpgrade {
			from, err := p.store.GetComponentRelease(ctx, before.ReleaseID)
			if err != nil {
				return nil, nil, err
			}
			if from.ComponentID != release.ComponentID || from.LineID != release.LineID || release.ParentReleaseID != from.ID {
				return nil, nil, fmt.Errorf("%w: %s 缺少精确直接升级路径", domain.ErrConflict, n.Name)
			}
			if a, ok := findAction(release, domain.ActionUpgrade); ok {
				if a.FromReleaseID != from.ID || a.ToReleaseID != release.ID {
					return nil, nil, fmt.Errorf("%w: %s 升级动作来源或目标不匹配", domain.ErrConflict, n.Name)
				}
			} else {
				// The existing idempotent-install evolution contract also declares
				// its exact reverse path. Execute that persisted install action so
				// its workspace and bound checks remain auditable.
				install, hasInstall := findAction(release, domain.ActionInstall)
				rollback, hasRollback := findAction(release, domain.ActionRollback)
				if !hasInstall || !install.Idempotent || !hasRollback || rollback.FromReleaseID != release.ID || rollback.ToReleaseID != from.ID {
					return nil, nil, fmt.Errorf("%w: %s 缺少精确直接升级合同", domain.ErrConflict, n.Name)
				}
				kind = domain.ActionInstall
			}
		}
		step, err := p.scenarioActionStep(ctx, n, release, kind, t.Variables, "change")
		if err != nil {
			return nil, nil, err
		}
		if change == "upgrade" {
			step.FromReleaseID, step.ToReleaseID = before.ReleaseID, t.ReleaseID
		}
		actions[n.ID] = step
	}
	for _, n := range source.Graph.Nodes {
		if _, exists := nextByID[n.ID]; exists {
			continue
		}
		before := oldByID[n.ID]
		for _, frozen := range frozenSource {
			for _, item := range frozen {
				if item.NodeID == n.ID {
					before.Variables = item.Variables
				}
			}
		}
		release, err := p.store.GetComponentRelease(ctx, before.ReleaseID)
		if err != nil {
			return nil, nil, err
		}
		uninstall, ok := findAction(release, domain.ActionUninstall)
		if !ok || uninstall.PostCheckActionID == "" {
			return nil, nil, fmt.Errorf("%w: %s 缺少卸载及卸载后检查", domain.ErrConflict, n.Name)
		}
		if install, ok := findAction(release, domain.ActionInstall); ok && install.PostCheckActionID == uninstall.PostCheckActionID {
			return nil, nil, fmt.Errorf("%w: 卸载后检查不能复用已安装状态检查", domain.ErrConflict)
		}
		step, err := p.scenarioActionStep(ctx, n, release, domain.ActionUninstall, before.Variables, "change")
		if err != nil {
			return nil, nil, err
		}
		step.SourceParametersFrozen = true
		actions[n.ID] = step
		operations = append(operations, ScenarioOperation{NodeID: n.ID, Name: n.Name, Change: "uninstall", FromReleaseID: before.ReleaseID})
	}
	// Retain unchanged nodes as ordering barriers; they never execute an action.
	nodes := map[string]bool{}
	edges := map[string]map[string]bool{}
	for _, n := range target.Graph.Nodes {
		nodes[n.ID] = true
	}
	for _, n := range source.Graph.Nodes {
		nodes[n.ID] = true
	}
	add := func(a, b string) {
		if edges[a] == nil {
			edges[a] = map[string]bool{}
		}
		edges[a][b] = true
	}
	for _, edge := range target.Graph.Edges {
		add(edge.Source, edge.Target)
	}
	for _, edge := range source.Graph.Edges {
		if _, deleted := nextByID[edge.Source]; !deleted {
			add(edge.Target, edge.Source)
		}
	}
	for _, edge := range target.UpgradeConstraints {
		if !nodes[edge.Source] || !nodes[edge.Target] || edge.Source == edge.Target {
			return nil, nil, fmt.Errorf("%w: 升级顺序约束引用无效节点", domain.ErrInvalid)
		}
		add(edge.Source, edge.Target)
	}
	order, err := uniqueScenarioOperationOrder(nodes, edges, actions)
	if err != nil {
		return nil, nil, err
	}
	steps := []lockedStep{}
	for _, id := range order {
		if step, ok := actions[id]; ok {
			steps = append(steps, step)
		}
	}
	return steps, operations, nil
}

func uniqueScenarioOperationOrder(nodes map[string]bool, edges map[string]map[string]bool, actions map[string]lockedStep) ([]string, error) {
	degree := map[string]int{}
	for id := range nodes {
		degree[id] = 0
	}
	for _, targets := range edges {
		for target := range targets {
			degree[target]++
		}
	}
	order := []string{}
	for len(degree) > 0 {
		ready := []string{}
		for id, d := range degree {
			if d == 0 {
				ready = append(ready, id)
			}
		}
		sort.Strings(ready)
		if len(ready) == 0 {
			return nil, fmt.Errorf("%w: 升级依赖与顺序约束形成环", domain.ErrConflict)
		}
		// Drain unchanged barriers before deciding the next mutating operation.
		chosen := ""
		for _, id := range ready {
			if _, ok := actions[id]; !ok {
				chosen = id
				break
			}
		}
		if chosen == "" {
			if len(ready) > 1 {
				return nil, fmt.Errorf("%w: 请补充升级顺序约束：%s", domain.ErrConflict, strings.Join(ready, ", "))
			}
			chosen = ready[0]
		}
		order = append(order, chosen)
		delete(degree, chosen)
		for id := range edges[chosen] {
			degree[id]--
		}
	}
	return order, nil
}

func (p *ExecutionService) StartScenarioExecution(ctx context.Context, user domain.User, id string, input ScenarioExecutionRequest, kind domain.RunKind) (domain.Run, error) {
	// Serialize workspace edits with the final fingerprint check and queue
	// insertion; a failed edit must not expose transient bytes to a queued job.
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	mode, err := normalizeScenarioMode(input.ExecutionMode)
	if err != nil {
		return domain.Run{}, err
	}
	input.ExecutionMode = mode
	if input.ExpectedPlanDigest == "" || strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 200 {
		return domain.Run{}, fmt.Errorf("%w: 预览摘要和幂等提交标识必填", domain.ErrInvalid)
	}
	requestDigest := digestValue(struct {
		ID    string
		Kind  domain.RunKind
		Input ScenarioExecutionRequest
	}{id, kind, input})
	if run, err := p.store.GetScenarioSubmission(ctx, user.ID, input.IdempotencyKey, requestDigest); err == nil {
		return run, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return run, err
	}
	at := time.Now().UTC()
	runID := newID("run")
	x, err := p.planner.prepareScenarioLifecycle(ctx, user, id, input, kind, runID, at)
	if err != nil {
		return domain.Run{}, err
	}
	if x.preview.PlanDigest != input.ExpectedPlanDigest {
		return domain.Run{}, fmt.Errorf("%w: 执行计划已变化，请重新预览", domain.ErrConflict)
	}
	return p.creator.createScenarioRun(ctx, user, id, input, kind, runID, at, x, requestDigest)
}
