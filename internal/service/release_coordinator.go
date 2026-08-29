package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

// ReleaseCoordinator owns standalone and joint publication so both paths use
// the same current-contract readiness rules.
type ReleaseCoordinator struct {
	platform *Platform
	store    releaseCoordinatorStore
}

type releaseCoordinatorStore interface {
	GetComponentRelease(context.Context, string) (domain.ComponentRelease, error)
	GetComponent(context.Context, string, bool) (domain.Component, error)
	GetPublicationEpoch(context.Context) (int64, error)
	LatestSuccessfulScenarioTestRun(context.Context, string) (domain.Run, error)
	PublishCandidateReleaseSet(context.Context, store.ScenarioPublicationGuard, []string, []store.ReleasePublicationGuard, string, int64, time.Time) error
	PublishComponentRelease(context.Context, string, int64, []store.ReleasePublicationGuard, time.Time) error
	LatestReleasedVersion(context.Context, string) (string, error)
	CreateNotifications(context.Context, []domain.Notification) error
}

func (c *ReleaseCoordinator) PublishRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, domain.ImpactReport, error) {
	p := c.platform
	publicationEpoch, err := c.store.GetPublicationEpoch(ctx)
	if err != nil {
		return domain.ComponentRelease{}, domain.ImpactReport{}, err
	}
	captured, publicationGuards, err := c.captureReleasePublicationState(ctx, []string{id})
	if err != nil {
		return domain.ComponentRelease{}, domain.ImpactReport{}, err
	}
	release := captured[id]
	component, err := c.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, domain.ImpactReport{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, domain.ImpactReport{}, err
	}
	if release.Status != domain.ReleaseDraft {
		base := fmt.Errorf("%w: only a draft can be published", domain.ErrConflict)
		return release, domain.ImpactReport{}, actionableExistingError(base, "resource.immutable", "只有 Draft Release 可以发布", "查看 Release", fmt.Sprintf("/components?selected=%s&release=%s", component.ID, release.ID))
	}
	if err := p.validateReleaseForPublish(ctx, release); err != nil {
		return release, domain.ImpactReport{}, actionableExistingError(err, "release.contract_invalid", "Release 合同未满足发布规则", "编辑合同", fmt.Sprintf("/components?selected=%s&release=%s&action=contract", component.ID, release.ID))
	}
	if err := p.validateReleaseEvidence(ctx, release); err != nil {
		return release, domain.ImpactReport{}, actionableExistingError(err, "release.evidence_missing", "当前合同缺少安装或回滚成功证据", "前往环境验证", fmt.Sprintf("/components?selected=%s&release=%s&action=validate", component.ID, release.ID))
	}
	oldVersion, oldErr := c.store.LatestReleasedVersion(ctx, release.ComponentID)
	if oldErr != nil && !errors.Is(oldErr, domain.ErrNotFound) {
		return release, domain.ImpactReport{}, oldErr
	}
	report, err := p.Impact(ctx, user, id)
	if err != nil {
		return release, report, err
	}
	now := time.Now().UTC()
	if err := c.store.PublishComponentRelease(ctx, id, publicationEpoch, publicationGuards, now); err != nil {
		return release, report, err
	}
	p.requestPublicationBackup("component-release:" + id)
	release.Status, release.ReleasedAt = domain.ReleaseReleased, &now
	notifications := make([]domain.Notification, 0, len(report.Recipients))
	for _, recipient := range report.Recipients {
		pathNames := make([][]string, 0, len(recipient.Paths))
		for _, path := range recipient.Paths {
			pathNames = append(pathNames, path.ComponentNames)
		}
		notifications = append(notifications, domain.Notification{
			ID: newID("notification"), UserID: recipient.UserID, Type: "component_release_impact",
			Title:       fmt.Sprintf("%s 发布 %s", component.Name, release.Version),
			Body:        fmt.Sprintf("上游组件从 %s 更新为 %s；请评估锁定版本和兼容性。", valueOr(oldVersion, "首次发布"), release.Version),
			ResourceURL: "/components?selected=" + component.ID,
			Payload: map[string]any{
				"componentId": component.ID, "componentName": component.Name,
				"oldVersion": oldVersion, "newVersion": release.Version,
				"releaseNotes": release.ReleaseNotes, "breaking": release.Breaking,
				"impactPaths": pathNames, "scenarioIds": recipient.ScenarioIDs,
			}, CreatedAt: now,
		})
	}
	if err := c.store.CreateNotifications(ctx, notifications); err != nil {
		return release, report, err
	}
	p.audit(ctx, user, "component_release.published", "component_release", id, map[string]any{
		"componentId": component.ID, "oldVersion": oldVersion, "newVersion": release.Version,
		"breaking": release.Breaking, "recipientCount": len(notifications),
	})
	p.hub.Publish("release.published", map[string]any{"releaseId": id, "componentId": component.ID})
	if len(notifications) > 0 {
		p.hub.Publish("notification", map[string]any{"releaseId": id, "count": len(notifications)})
	}
	release, err = p.decorateReleaseReadiness(ctx, release)
	if err != nil {
		return release, report, err
	}
	return release, report, nil
}

func (c *ReleaseCoordinator) PublishScenario(ctx context.Context, user domain.User, revisionID string) (domain.ScenarioRevision, error) {
	p := c.platform
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
	publicationEpoch, err := c.store.GetPublicationEpoch(ctx)
	if err != nil {
		return revision, err
	}
	rootReleaseIDs := make([]string, 0, len(revision.Graph.Nodes))
	for _, node := range revision.Graph.Nodes {
		rootReleaseIDs = append(rootReleaseIDs, node.ReleaseID)
	}
	_, publicationGuards, err := c.captureReleasePublicationState(ctx, rootReleaseIDs)
	if err != nil {
		return revision, err
	}
	issues, err := p.ValidateScenario(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if len(issues) > 0 {
		base := &domain.ValidationError{Message: "scenario validation failed", Details: issues}
		return revision, actionableExistingError(base, "scenario.graph_invalid", "当前 DAG 或锁定 Release 未通过校验", "检查场景问题", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
	}
	set, err := c.CandidateReleaseSet(ctx, user, revisionID)
	if err != nil {
		return revision, err
	}
	if !set.Ready {
		base := &domain.ValidationError{Message: "candidate release set is not ready", Details: set.Issues}
		return revision, actionableExistingError(base, "scenario.candidate_set_blocked", "候选 Release 状态或证据在发布前复核时发生变化", "检查候选集", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
	}
	evidence, err := c.store.LatestSuccessfulScenarioTestRun(ctx, revisionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			base := fmt.Errorf("%w: no successful complete test evidence exists for the current scenario revision", domain.ErrConflict)
			return revision, actionableExistingError(base, "scenario.test_required", "当前 Revision 缺少可用于发布的完整测试证据", "重新运行场景测试", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=test", scenario.ID, revision.ID))
		}
		return revision, err
	}
	currentEvidence, err := c.scenarioTestEvidenceCurrent(ctx, evidence, revision)
	if err != nil {
		return revision, err
	}
	if !currentEvidence {
		base := fmt.Errorf("%w: successful scenario test evidence is stale for the current release definitions", domain.ErrConflict)
		return revision, actionableExistingError(base, "scenario.test_evidence_stale", "完整测试证据对应的组件 Release 定义已变化", "重新运行场景测试", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=test", scenario.ID, revision.ID))
	}
	now := time.Now().UTC()
	releaseIDs := make([]string, 0, len(set.Releases))
	for _, item := range set.Releases {
		releaseIDs = append(releaseIDs, item.ReleaseID)
	}
	revisionGuard := store.ScenarioPublicationGuard{
		RevisionID: revision.ID, PublicationGeneration: revision.PublicationGeneration,
		SpecDigest: scenarioRevisionSpecDigest(revision),
	}
	if err := c.store.PublishCandidateReleaseSet(ctx, revisionGuard, releaseIDs, publicationGuards, evidence.ID, publicationEpoch, now); err != nil {
		return revision, err
	}
	p.requestPublicationBackup("scenario-revision:" + revisionID)
	revision.Status, revision.ReleasedAt = domain.RevisionReleased, &now
	for _, item := range set.Releases {
		p.audit(ctx, user, "component_release.published_with_scenario", "component_release", item.ReleaseID, map[string]any{"scenarioRevisionId": revisionID, "componentId": item.ComponentID, "version": item.Version})
		p.hub.Publish("release.published", map[string]any{"releaseId": item.ReleaseID, "componentId": item.ComponentID})
	}
	p.audit(ctx, user, "scenario_revision.published", "scenario_revision", revisionID, map[string]any{"scenarioId": scenario.ID, "revision": revision.Revision, "candidateReleaseCount": len(set.Releases)})
	p.hub.Publish("scenario.published", map[string]any{"scenarioId": scenario.ID, "revisionId": revisionID})
	return revision, nil
}

func (c *ReleaseCoordinator) captureReleasePublicationState(ctx context.Context, rootReleaseIDs []string) (map[string]domain.ComponentRelease, []store.ReleasePublicationGuard, error) {
	captured := map[string]domain.ComponentRelease{}
	missing := map[string]bool{}
	roots := map[string]bool{}
	for _, releaseID := range rootReleaseIDs {
		roots[releaseID] = true
	}
	queue := append([]string(nil), rootReleaseIDs...)
	for len(queue) > 0 {
		releaseID := queue[0]
		queue = queue[1:]
		if releaseID == "" {
			return nil, nil, fmt.Errorf("%w: publication references an empty release id", domain.ErrInvalid)
		}
		if _, seen := captured[releaseID]; seen || missing[releaseID] {
			continue
		}
		release, err := c.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) && !roots[releaseID] {
				missing[releaseID] = true
				continue
			}
			return nil, nil, err
		}
		captured[releaseID] = release
		for _, dependency := range release.Dependencies {
			queue = append(queue, dependency.UpstreamReleaseID)
		}
		for _, action := range release.Actions {
			if action.FromReleaseID != "" && action.FromReleaseID != release.ID {
				queue = append(queue, action.FromReleaseID)
			}
			if action.ToReleaseID != "" && action.ToReleaseID != release.ID {
				queue = append(queue, action.ToReleaseID)
			}
		}
	}
	guards := make([]store.ReleasePublicationGuard, 0, len(captured))
	for _, release := range captured {
		guards = append(guards, store.ReleasePublicationGuard{
			ReleaseID: release.ID, PublicationGeneration: release.PublicationGeneration,
			SpecDigest: componentReleaseSpecDigest(release), Status: release.Status, Candidate: release.Candidate,
		})
	}
	for releaseID := range missing {
		guards = append(guards, store.ReleasePublicationGuard{ReleaseID: releaseID, Missing: true})
	}
	sort.Slice(guards, func(i, j int) bool { return guards[i].ReleaseID < guards[j].ReleaseID })
	return captured, guards, nil
}

func (c *ReleaseCoordinator) scenarioTestEvidenceCurrent(ctx context.Context, run domain.Run, revision domain.ScenarioRevision) (bool, error) {
	if run.Kind != domain.RunScenarioTest || run.Status != domain.RunSucceeded {
		return false, nil
	}
	return c.scenarioRunDefinitionCurrent(ctx, run, revision)
}

func (c *ReleaseCoordinator) scenarioRunDefinitionCurrent(ctx context.Context, run domain.Run, revision domain.ScenarioRevision) (bool, error) {
	if run.ScenarioRevisionID != revision.ID || snapshotString(run, "scenarioRevisionSpecDigest") != scenarioRevisionSpecDigest(revision) {
		return false, nil
	}
	plan, err := mapToPlan(run.InputSnapshot)
	if err != nil {
		return false, nil
	}
	locks := map[string]string{}
	for _, step := range plan.Steps {
		if step.ReleaseID == "" || step.ReleaseSpecDigest == "" {
			return false, nil
		}
		if existing, duplicate := locks[step.ReleaseID]; duplicate && existing != step.ReleaseSpecDigest {
			return false, nil
		}
		locks[step.ReleaseID] = step.ReleaseSpecDigest
	}
	current := map[string]domain.ComponentRelease{}
	for releaseID, digest := range locks {
		release, getErr := c.store.GetComponentRelease(ctx, releaseID)
		if getErr != nil {
			if errors.Is(getErr, domain.ErrNotFound) {
				return false, nil
			}
			return false, getErr
		}
		if componentReleaseSpecDigest(release) != digest {
			return false, nil
		}
		current[releaseID] = release
	}
	for _, node := range revision.Graph.Nodes {
		release, covered := current[node.ReleaseID]
		retainedHistorical := (revision.Status == domain.RevisionReleased || revision.Status == domain.RevisionDeprecated) && release.Status == domain.ReleaseDeprecated
		if !covered || (release.Status != domain.ReleaseReleased && (release.Status != domain.ReleaseDraft || !release.Candidate) && !retainedHistorical) {
			return false, nil
		}
	}
	return true, nil
}

func (c *ReleaseCoordinator) CandidateReleaseSet(ctx context.Context, user domain.User, revisionID string) (CandidateReleaseSet, error) {
	p := c.platform
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
		release, getErr := c.store.GetComponentRelease(ctx, node.ReleaseID)
		if getErr != nil {
			set.Issues = append(set.Issues, domain.ValidationIssue{Code: "release_not_found", Message: "locked component release does not exist", NodeID: node.ID})
			continue
		}
		if release.Status == domain.ReleaseReleased {
			continue
		}
		if release.Status != domain.ReleaseDraft || !release.Candidate {
			set.Issues = append(set.Issues, domain.ValidationIssue{Code: "candidate_not_ready", Message: "draft release must be shared by its component owner", NodeID: node.ID})
			continue
		}
		if validateErr := p.validateReleaseForCandidate(ctx, release); validateErr != nil {
			set.Issues = append(set.Issues, domain.ValidationIssue{Code: "candidate_invalid", Message: validateErr.Error(), NodeID: node.ID})
			continue
		}
		component, componentErr := c.store.GetComponent(ctx, release.ComponentID, false)
		if componentErr != nil {
			return set, componentErr
		}
		set.Releases = append(set.Releases, CandidateReleaseSetItem{ReleaseID: release.ID, ComponentID: component.ID, ComponentName: component.Name, Version: release.Version})
	}
	sort.Slice(set.Releases, func(i, j int) bool { return set.Releases[i].ComponentName < set.Releases[j].ComponentName })
	set.Ready = len(set.Issues) == 0
	return set, nil
}

func (p *Platform) PublishRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, domain.ImpactReport, error) {
	return p.releaseCoordinator.PublishRelease(ctx, user, id)
}
func (p *Platform) PublishScenario(ctx context.Context, user domain.User, revisionID string) (domain.ScenarioRevision, error) {
	return p.releaseCoordinator.PublishScenario(ctx, user, revisionID)
}
func (p *Platform) CandidateReleaseSet(ctx context.Context, user domain.User, revisionID string) (CandidateReleaseSet, error) {
	return p.releaseCoordinator.CandidateReleaseSet(ctx, user, revisionID)
}
