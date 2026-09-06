package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

var activeWorkRunStatuses = map[domain.RunStatus]bool{
	domain.RunAwaitingApproval: true,
	domain.RunQueued:           true,
	domain.RunRunning:          true,
}

func (p *ReadModelService) Workbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	request := *p
	request.store = newWorkbenchReadStore(p.store)
	return request.workbench(ctx, user)
}

func (p *ReadModelService) workbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	if user.Role == domain.RolePlatformAdmin {
		return p.platformAdminWorkbench(ctx, user)
	}
	subjects, err := p.store.WorkbenchSubjects(ctx, user)
	if err != nil {
		return domain.Workbench{}, err
	}
	components, scenarios, environments := subjects.Components, subjects.Scenarios, subjects.Environments
	if user.Role == domain.RoleComponentOwner {
		components, err = p.catalog.WorkbenchReadiness(ctx, components)
		if err != nil {
			return domain.Workbench{}, err
		}
	}
	runs, err := p.workbenchRuns(ctx, user, components, scenarios)
	if err != nil {
		return domain.Workbench{}, err
	}
	notifications, err := p.store.ListNotifications(ctx, user.ID, true)
	if err != nil {
		return domain.Workbench{}, err
	}

	workbench := domain.Workbench{GeneratedAt: time.Now().UTC(), Role: user.Role, Items: []domain.WorkItem{}}
	componentByID := map[string]domain.Component{}
	releaseByID := map[string]domain.ComponentRelease{}
	for _, component := range components {
		componentByID[component.ID] = component
		if component.OwnerID == user.ID {
			workbench.Assets.Components++
		}
		for _, release := range component.Releases {
			releaseByID[release.ID] = release
		}
	}
	scenarioByID := map[string]domain.Scenario{}
	revisionByID := map[string]domain.ScenarioRevision{}
	for _, scenario := range scenarios {
		scenarioByID[scenario.ID] = scenario
		if scenario.OwnerID == user.ID {
			workbench.Assets.Scenarios++
		}
		for _, revision := range scenario.Revisions {
			revisionByID[revision.ID] = revision
		}
	}
	environmentByID := map[string]domain.Environment{}
	for _, environment := range environments {
		environmentByID[environment.ID] = environment
		if environment.OwnerID == user.ID {
			workbench.Assets.Environments++
		}
	}
	metadata, err := p.store.WorkbenchMetadata(ctx, user, runs)
	if err != nil {
		return domain.Workbench{}, err
	}
	for id, c := range metadata.Components {
		if _, exists := componentByID[id]; !exists {
			componentByID[id] = c
		}
	}
	for id, r := range metadata.Releases {
		if _, exists := releaseByID[id]; !exists {
			releaseByID[id] = r
		}
	}
	for id, s := range metadata.Scenarios {
		if _, exists := scenarioByID[id]; !exists {
			scenarioByID[id] = s
		}
	}
	for id, r := range metadata.Revisions {
		if _, exists := revisionByID[id]; !exists {
			revisionByID[id] = r
		}
	}

	embeddedRuns := map[string]bool{}
	switch user.Role {
	case domain.RoleComponentOwner:
		items, embedded, itemErr := componentOwnerWork(user, components, runs)
		if itemErr != nil {
			return domain.Workbench{}, itemErr
		}
		workbench.Items = append(workbench.Items, items...)
		mergeRunSet(embeddedRuns, embedded)
	case domain.RoleScenarioOwner:
		items, embedded, itemErr := p.scenarioOwnerWork(ctx, user, scenarios, runs)
		if itemErr != nil {
			return domain.Workbench{}, itemErr
		}
		workbench.Items = append(workbench.Items, items...)
		mergeRunSet(embeddedRuns, embedded)
	case domain.RoleEnvironmentOwner:
		workbench.Items = append(workbench.Items, environmentOwnerWork(user, environments)...)
		if p.publicationBackupHealth != nil {
			health, healthErr := p.publicationBackupHealth.CatalogBackupHealth(ctx)
			workbench.Items = append(workbench.Items, catalogBackupWorkItem(health, healthErr)...)
		}
	}

	workbench.Items = append(workbench.Items, impactWork(user, notifications, scenarioByID)...)
	runItems, runErr := p.runWork(ctx, user, runs, embeddedRuns, componentByID, releaseByID, scenarioByID, revisionByID, environmentByID)
	if runErr != nil {
		return domain.Workbench{}, runErr
	}
	workbench.Items = append(workbench.Items, runItems...)
	sortWorkItems(workbench.Items)
	for _, item := range workbench.Items {
		if item.Priority == domain.WorkPriorityCritical {
			workbench.Summary.Critical++
			continue
		}
		switch item.Status {
		case domain.WorkStatusActionRequired, domain.WorkStatusBlocked:
			workbench.Summary.ActionRequired++
		case domain.WorkStatusInProgress:
			workbench.Summary.InProgress++
		case domain.WorkStatusAttention:
			workbench.Summary.Informational++
		}
	}
	return workbench, nil
}

func (p *ReadModelService) platformAdminWorkbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	subjects, err := p.store.WorkbenchSubjects(ctx, user)
	if err != nil {
		return domain.Workbench{}, err
	}
	workbench := domain.Workbench{GeneratedAt: time.Now().UTC(), Role: user.Role, Items: []domain.WorkItem{}}
	workbench.Items = platformAdminComponentWork(subjects.Components)
	workbench.Summary.ActionRequired = len(workbench.Items)
	return workbench, nil
}

func platformAdminComponentWork(components []domain.Component) []domain.WorkItem {
	items := []domain.WorkItem{}
	for _, component := range components {
		for _, release := range component.Releases {
			if release.Status != domain.ReleaseDraft || release.Review.Status != domain.ReleaseReviewPending {
				continue
			}
			updatedAt := release.CreatedAt
			if release.Review.SubmittedAt != nil {
				updatedAt = *release.Review.SubmittedAt
			}
			digest := release.Review.ContractDigest
			if len(digest) > 16 {
				digest = digest[:16] + "…"
			}
			items = append(items, domain.WorkItem{
				ID: "component_review:" + release.ID, Kind: "component_review", Priority: domain.WorkPriorityHigh,
				Status: domain.WorkStatusActionRequired, Title: component.Name + " " + release.Version + " 等待合同审核",
				Subject: domain.WorkSubject{Type: "component_release", ID: release.ID, ParentID: component.ID, Name: component.Name, Version: release.Version},
				Reasons: []domain.WorkReason{{
					Code: "component_release.review_pending", Message: "组件 Owner 已提交当前 Release 合同",
					Cause: &domain.WorkCause{Kind: "review_submission", Summary: release.LineName + " · 合同摘要 " + digest, At: &updatedAt},
				}},
				PrimaryAction: domain.WorkAction{Label: "预览并审核", Href: "/?review=" + release.ID}, SecondaryActions: []domain.WorkAction{}, UpdatedAt: updatedAt,
			})
		}
	}
	sortWorkItems(items)
	return items
}

func catalogBackupWorkItem(health domain.CatalogBackupHealth, healthErr error) []domain.WorkItem {
	now := time.Now().UTC()
	item := domain.WorkItem{
		ID: "catalog_backup:health", Kind: "catalog_backup", Priority: domain.WorkPriorityHigh,
		Status: domain.WorkStatusActionRequired, Title: "发布目录灾备需要处理",
		Subject:          domain.WorkSubject{Type: "catalog_repository", ID: "selected", Name: "发布目录灾备"},
		PrimaryAction:    domain.WorkAction{Label: "检查灾备配置", Href: "/environments#catalog-repository"},
		SecondaryActions: []domain.WorkAction{}, UpdatedAt: now,
	}
	if healthErr != nil {
		item.Priority, item.Status = domain.WorkPriorityCritical, domain.WorkStatusBlocked
		item.Reasons = []domain.WorkReason{{Code: "catalog_backup.health_failed", Message: "无法检查发布目录灾备状态", Cause: &domain.WorkCause{Kind: "backup_health", Summary: healthErr.Error(), At: &now}, NextAction: workAction("检查灾备配置", item.PrimaryAction.Href)}}
		return []domain.WorkItem{item}
	}
	if !health.Configured {
		item.Reasons = []domain.WorkReason{{Code: "catalog_backup.repository_unconfigured", Message: "尚未通过前台录入私有仓库，无法创建发布目录恢复点", Cause: ruleCause("自动备份和立即备份都只使用环境 Owner 明确选择的仓库"), NextAction: workAction("录入私有仓库", item.PrimaryAction.Href)}}
		return []domain.WorkItem{item}
	}
	if health.LastError != "" {
		item.Priority, item.Status = domain.WorkPriorityCritical, domain.WorkStatusBlocked
		at := valueOrTime(health.LastErrorAt, now)
		item.UpdatedAt = at
		item.Reasons = append(item.Reasons, domain.WorkReason{Code: "catalog_backup.failed", Message: "最近一次发布目录备份失败", Cause: &domain.WorkCause{Kind: "backup_failure", Summary: health.LastError, At: &at}, NextAction: workAction("检查恢复点", item.PrimaryAction.Href)})
	}
	if health.Behind {
		item.Status = domain.WorkStatusBlocked
		item.Reasons = append(item.Reasons, domain.WorkReason{Code: "catalog_backup.behind", Message: fmt.Sprintf("最近恢复点落后：当前发布代次 %d，已备份代次 %d", health.CurrentGeneration, health.BackedUpGeneration), Cause: ruleCause("发布目录发生变化后尚未形成成功恢复点"), NextAction: workAction("检查恢复点", item.PrimaryAction.Href)})
	}
	if len(item.Reasons) == 0 {
		return nil
	}
	return []domain.WorkItem{item}
}

func valueOrTime(value *time.Time, fallback time.Time) time.Time {
	if value == nil {
		return fallback
	}
	return *value
}

// Components carry the Readiness evaluated by ListComponents in this same
// request. Formatting work items must not repeat filesystem or evidence reads.
func componentOwnerWork(user domain.User, components []domain.Component, runs []domain.Run) ([]domain.WorkItem, map[string]bool, error) {
	items := []domain.WorkItem{}
	embedded := map[string]bool{}
	for _, component := range components {
		if component.OwnerID != user.ID {
			continue
		}
		for _, release := range component.Releases {
			if release.Status != domain.ReleaseDraft {
				continue
			}
			digest := componentReleaseSpecDigest(release)
			componentHref := fmt.Sprintf("/components?selected=%s&release=%s", component.ID, release.ID)
			latest := latestMatchingRun(runs, func(run domain.Run) bool {
				return run.Kind == domain.RunComponentTest && run.ComponentReleaseID == release.ID && snapshotString(run, "componentReleaseSpecDigest") == digest
			})
			reasons := []domain.WorkReason{}
			readiness := release.Readiness
			if readiness.Status != domain.ReadinessReady && readiness.Status != domain.ReadinessRisky && readiness.Status != domain.ReadinessBlocked {
				return nil, nil, fmt.Errorf("component work requires evaluated Readiness for release %s", release.ID)
			}
			for _, blocker := range readiness.Blockers {
				evidenceRunID := ""
				if blocker.Code == "install_evidence_missing" {
					evidenceRunID = readiness.InstallEvidenceRunID
				}
				if blocker.Code == "rollback_evidence_missing" {
					evidenceRunID = readiness.RollbackEvidenceRunID
				}
				reasons = append(reasons, domain.WorkReason{
					Code: blocker.Code, Message: blocker.Message, EvidenceRunID: evidenceRunID,
					Cause: ruleCause("Release Readiness 统一校验未通过"), NextAction: workAction("处理阻断", blocker.ActionURL),
				})
			}

			priority, status := domain.WorkPriorityHigh, domain.WorkStatusBlocked
			title := fmt.Sprintf("%s %s 尚不可发布", component.Name, release.Version)
			action := domain.WorkAction{Label: "前往环境验证", Href: fmt.Sprintf("/components?selected=%s&release=%s&action=validate", component.ID, release.ID)}
			if latest != nil && activeWorkRunStatuses[latest.Status] {
				embedded[latest.ID] = true
				status, priority = domain.WorkStatusInProgress, domain.WorkPriorityNormal
				reasons = append(reasons, domain.WorkReason{Code: "release.validation_in_progress", Message: runStatusMessage(latest.Status), EvidenceRunID: latest.ID, Cause: runCause(*latest), NextAction: workAction("查看运行", "/runs?selected="+latest.ID)})
				title = fmt.Sprintf("%s %s 正在验证", component.Name, release.Version)
				action = domain.WorkAction{Label: "查看运行", Href: "/runs?selected=" + latest.ID}
			} else if latest != nil && latest.InputSnapshot["cleaned"] != true && (latest.Status == domain.RunFailed || latest.Status == domain.RunInterrupted) {
				embedded[latest.ID] = true
				priority = domain.WorkPriorityCritical
				reasons = append(reasons, domain.WorkReason{Code: "release.validation_failed", Message: "当前合同最近一次环境验证失败", EvidenceRunID: latest.ID, Cause: runCause(*latest), NextAction: workAction("查看失败运行", "/runs?selected="+latest.ID)})
				action = domain.WorkAction{Label: "查看失败运行", Href: "/runs?selected=" + latest.ID}
			}
			if len(reasons) == 0 {
				status, priority = domain.WorkStatusActionRequired, domain.WorkPriorityNormal
				title = fmt.Sprintf("%s %s 已满足发布条件", component.Name, release.Version)
				reasons = append(reasons, domain.WorkReason{Code: "release.ready_to_publish", Message: "合同、生命周期、安装和回滚证据均已满足", Cause: ruleCause("平台已按当前合同重新核对全部发布门禁"), NextAction: workAction("预览影响并发布", componentHref+"&action=publish")})
				action = domain.WorkAction{Label: "预览影响并发布", Href: fmt.Sprintf("/components?selected=%s&release=%s&action=publish", component.ID, release.ID)}
			}
			items = append(items, domain.WorkItem{
				ID: "component_draft:" + release.ID, Kind: "component_draft", Priority: priority, Status: status, Title: title,
				Subject: domain.WorkSubject{Type: "component_release", ID: release.ID, ParentID: component.ID, Name: component.Name, Version: release.Version},
				Reasons: reasons, PrimaryAction: action, SecondaryActions: []domain.WorkAction{}, UpdatedAt: component.UpdatedAt,
			})
		}
	}
	return items, embedded, nil
}

func (p *ReadModelService) scenarioOwnerWork(ctx context.Context, user domain.User, scenarios []domain.Scenario, runs []domain.Run) ([]domain.WorkItem, map[string]bool, error) {
	items := []domain.WorkItem{}
	embedded := map[string]bool{}
	for _, scenario := range scenarios {
		if scenario.OwnerID != user.ID {
			continue
		}
		revision, ok := currentScenarioRevision(scenario)
		if !ok || (revision.Status != domain.RevisionDraft && revision.Status != domain.RevisionTesting && revision.Status != domain.RevisionTestPassed) {
			continue
		}
		scenarioHref := fmt.Sprintf("/scenarios?selected=%s&revision=%s", scenario.ID, revision.ID)
		graphAction := workAction("检查场景问题", scenarioHref+"&action=inspect")
		testAction := workAction("前往场景测试", scenarioHref+"&action=test")
		matchesCurrentDefinition := map[string]bool{}
		for _, run := range runs {
			if run.Kind != domain.RunScenarioTest || run.ScenarioRevisionID != revision.ID {
				continue
			}
			matches, matchErr := scenarioRunDefinitionCurrent(ctx, run, revision, p.store.GetComponentRelease)
			if matchErr != nil {
				return nil, nil, matchErr
			}
			matchesCurrentDefinition[run.ID] = matches
		}
		latest := latestMatchingRun(runs, func(run domain.Run) bool {
			return matchesCurrentDefinition[run.ID]
		})
		latestSuccessfulCurrent := latestMatchingRun(runs, func(run domain.Run) bool {
			return matchesCurrentDefinition[run.ID] && run.Status == domain.RunSucceeded
		})
		latestSuccessfulTest := latestMatchingRun(runs, func(run domain.Run) bool {
			return run.Kind == domain.RunScenarioTest && run.ScenarioRevisionID == revision.ID && run.Status == domain.RunSucceeded
		})
		reasons := []domain.WorkReason{}
		issues, validationErr := p.scenarios.Validate(ctx, user, revision.ID)
		if validationErr != nil {
			return nil, nil, validationErr
		}
		if len(issues) > 0 {
			message := issues[0].Message
			if len(issues) > 1 {
				message = fmt.Sprintf("%s；另有 %d 项问题", message, len(issues)-1)
			}
			reasons = append(reasons, domain.WorkReason{Code: "scenario.graph_invalid", Message: message, Cause: ruleCause("当前场景图未通过发布前校验"), NextAction: graphAction})
		}
		priority, status := domain.WorkPriorityHigh, domain.WorkStatusBlocked
		title := fmt.Sprintf("%s r%d 需要完整测试", scenario.Name, revision.Revision)
		action := domain.WorkAction{Label: "前往场景测试", Href: fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=test", scenario.ID, revision.ID)}
		switch revision.Status {
		case domain.RevisionDraft:
			if latestSuccessfulTest != nil && !matchesCurrentDefinition[latestSuccessfulTest.ID] {
				reasons = append(reasons, domain.WorkReason{Code: "scenario.test_evidence_stale", Message: "完整测试证据来自旧场景或旧 Release 定义，需要重新测试当前 Revision", EvidenceRunID: latestSuccessfulTest.ID, Cause: p.evidenceInvalidationCause(ctx, "scenario_revision", revision.ID, latestSuccessfulTest), NextAction: testAction})
			} else {
				reasons = append(reasons, domain.WorkReason{Code: "scenario.test_required", Message: "当前 Revision 尚未通过完整环境测试", Cause: ruleCause("场景发布规则要求当前 Revision 完成一次完整环境测试"), NextAction: testAction})
			}
		case domain.RevisionTesting:
			if len(issues) == 0 {
				status, priority = domain.WorkStatusInProgress, domain.WorkPriorityNormal
				title = fmt.Sprintf("%s r%d 正在测试", scenario.Name, revision.Revision)
				reason := domain.WorkReason{Code: "scenario.test_in_progress", Message: "完整场景测试正在等待审批、排队或执行", Cause: ruleCause("当前 Revision 已由活动测试 Run 锁定"), NextAction: testAction}
				if latest != nil {
					reason.EvidenceRunID = latest.ID
					reason.Cause = runCause(*latest)
					reason.NextAction = workAction("查看运行", "/runs?selected="+latest.ID)
				}
				reasons = append(reasons, reason)
			} else {
				title = fmt.Sprintf("%s r%d 存在测试阻塞", scenario.Name, revision.Revision)
				action = domain.WorkAction{Label: "检查场景问题", Href: fmt.Sprintf("/scenarios?selected=%s&revision=%s", scenario.ID, revision.ID)}
			}
		case domain.RevisionTestPassed:
			if len(issues) == 0 && latestSuccessfulCurrent != nil {
				status, priority = domain.WorkStatusActionRequired, domain.WorkPriorityNormal
				title = fmt.Sprintf("%s r%d 已测试通过", scenario.Name, revision.Revision)
				reasons = append(reasons, domain.WorkReason{Code: "scenario.ready_to_publish", Message: "当前 Revision 可以预览候选集并发布", Cause: ruleCause("DAG 校验和当前定义的完整测试证据均已满足"), NextAction: workAction("预览候选集并发布", scenarioHref+"&action=publish")})
				action = domain.WorkAction{Label: "预览候选集并发布", Href: fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=publish", scenario.ID, revision.ID)}
			} else if len(issues) == 0 {
				reason := domain.WorkReason{Code: "scenario.test_evidence_stale", Message: "完整测试证据对应的组件 Release 定义已变化，需要重新测试", Cause: ruleCause("场景测试证据必须同时匹配 DAG 和每个锁定 Release 的当前定义"), NextAction: testAction}
				if latestSuccessfulTest != nil {
					reason.EvidenceRunID = latestSuccessfulTest.ID
					reason.Cause = p.evidenceInvalidationCause(ctx, "scenario_revision", revision.ID, latestSuccessfulTest)
				}
				reasons = append(reasons, reason)
			} else {
				title = fmt.Sprintf("%s r%d 存在发布阻塞", scenario.Name, revision.Revision)
				action = domain.WorkAction{Label: "检查场景问题", Href: fmt.Sprintf("/scenarios?selected=%s&revision=%s", scenario.ID, revision.ID)}
			}
		}
		if latest != nil && activeWorkRunStatuses[latest.Status] {
			embedded[latest.ID] = true
			action = domain.WorkAction{Label: "查看运行", Href: "/runs?selected=" + latest.ID}
		} else if latest != nil && latest.InputSnapshot["cleaned"] != true && (latest.Status == domain.RunFailed || latest.Status == domain.RunInterrupted) {
			embedded[latest.ID] = true
			priority, status = domain.WorkPriorityCritical, domain.WorkStatusBlocked
			reasons = append(reasons, domain.WorkReason{Code: "scenario.test_failed", Message: "当前 Revision 最近一次完整测试失败", EvidenceRunID: latest.ID, Cause: runCause(*latest), NextAction: workAction("查看失败运行", "/runs?selected="+latest.ID)})
			action = domain.WorkAction{Label: "查看失败运行", Href: "/runs?selected=" + latest.ID}
		}
		items = append(items, domain.WorkItem{
			ID: "scenario_revision:" + revision.ID, Kind: "scenario_revision", Priority: priority, Status: status, Title: title,
			Subject: domain.WorkSubject{Type: "scenario_revision", ID: revision.ID, ParentID: scenario.ID, Name: scenario.Name, Revision: revision.Revision},
			Reasons: reasons, PrimaryAction: action, SecondaryActions: []domain.WorkAction{}, UpdatedAt: scenario.UpdatedAt,
		})
	}
	return items, embedded, nil
}

func environmentOwnerWork(user domain.User, environments []domain.Environment) []domain.WorkItem {
	items := []domain.WorkItem{}
	for _, environment := range environments {
		if environment.OwnerID != user.ID || environment.Revision == nil {
			continue
		}
		reasons := []domain.WorkReason{}
		environmentHref := "/environments?selected=" + environment.ID
		var inventory InventoryDocument
		if err := json.Unmarshal(environment.Revision.Inventory, &inventory); err != nil || len(inventory.Hosts) == 0 {
			reasons = append(reasons, domain.WorkReason{Code: "environment.inventory_empty", Message: "当前 Revision 尚未配置 Inventory 主机", Cause: ruleCause("交付计划必须解析到至少一台 Inventory 主机"), NextAction: workAction("配置 Inventory", environmentHref+"&tab=inventory")})
		}
		if strings.TrimSpace(environment.Revision.Variables["IMAGE_REGISTRY"]) == "" {
			reasons = append(reasons, domain.WorkReason{Code: "environment.registry_missing", Message: "缺少 IMAGE_REGISTRY", Cause: ruleCause("镜像交付需要环境声明 IMAGE_REGISTRY"), NextAction: workAction("配置镜像仓库", environmentHref+"&tab=variables&focus=IMAGE_REGISTRY")})
		}
		if strings.TrimSpace(environment.Revision.Variables["FILE_STATION"]) == "" {
			reasons = append(reasons, domain.WorkReason{Code: "environment.file_station_missing", Message: "缺少 FILE_STATION", Cause: ruleCause("组件介质交付需要环境声明 FILE_STATION"), NextAction: workAction("配置 File Station", environmentHref+"&tab=variables&focus=FILE_STATION")})
		}
		if environment.HealthCheck == nil {
			reasons = append(reasons, domain.WorkReason{Code: "environment.health_missing", Message: "当前 Revision 尚未执行 TCP 端点检查", Cause: ruleCause("TCP 连通性证据必须绑定当前 Environment Revision"), NextAction: workAction("检查环境连通性", environmentHref+"&focus=health")})
		} else if environment.HealthCheck.EnvironmentRevisionID != environment.CurrentRevisionID {
			at := environment.Revision.CreatedAt
			cause := &domain.WorkCause{Kind: "revision_change", Summary: valueOr(environment.Revision.ChangeReason, "环境配置已创建新的 Revision"), ActorID: environment.Revision.CreatedBy, At: &at}
			if environment.Revision.CreatedBy == user.ID {
				cause.ActorName = user.Name
			}
			reasons = append(reasons, domain.WorkReason{Code: "environment.health_stale", Message: "最近 TCP 检查来自旧 Environment Revision，需要重新检查", Cause: cause, NextAction: workAction("重新检查环境连通性", environmentHref+"&focus=health")})
		} else if environment.HealthCheck.Status == "degraded" {
			failed := 0
			for _, result := range environment.HealthCheck.Results {
				if !result.Reachable {
					failed++
				}
			}
			checkedAt := environment.HealthCheck.CheckedAt
			reasons = append(reasons, domain.WorkReason{Code: "environment.health_degraded", Message: fmt.Sprintf("当前检查有 %d 个端点不可达", failed), Cause: &domain.WorkCause{Kind: "health_check", Summary: "当前 Revision 的只读 TCP 检查未全部通过", At: &checkedAt}, NextAction: workAction("查看异常端点", environmentHref+"&focus=health")})
		}
		if environment.SSHCheck == nil {
			reasons = append(reasons, domain.WorkReason{Code: "environment.ssh_missing", Message: "当前 Revision 尚未执行 SSH 检查", Cause: ruleCause("SSH 认证与远端 true 执行证据必须绑定当前 Environment Revision"), NextAction: workAction("检查环境连通性", environmentHref+"&focus=health")})
		} else if environment.SSHCheck.EnvironmentRevisionID != environment.CurrentRevisionID {
			at := environment.Revision.CreatedAt
			cause := &domain.WorkCause{Kind: "revision_change", Summary: valueOr(environment.Revision.ChangeReason, "环境配置已创建新的 Revision"), ActorID: environment.Revision.CreatedBy, At: &at}
			if environment.Revision.CreatedBy == user.ID {
				cause.ActorName = user.Name
			}
			reasons = append(reasons, domain.WorkReason{Code: "environment.ssh_stale", Message: "最近 SSH 检查来自旧 Environment Revision，需要重新检查", Cause: cause, NextAction: workAction("重新检查环境连通性", environmentHref+"&focus=health")})
		} else if environment.SSHCheck.Status == "degraded" {
			failed := 0
			for _, result := range environment.SSHCheck.Results {
				if result.Status != "passed" && result.Status != "skipped" {
					failed++
				}
			}
			checkedAt := environment.SSHCheck.CheckedAt
			reasons = append(reasons, domain.WorkReason{Code: "environment.ssh_degraded", Message: fmt.Sprintf("当前 SSH 检查有 %d 项失败", failed), Cause: &domain.WorkCause{Kind: "ssh_check", Summary: "当前 Revision 的 SSH 认证或远端 true 执行未全部通过", At: &checkedAt}, NextAction: workAction("查看 SSH 错误", environmentHref+"&focus=health")})
		}
		if len(reasons) == 0 {
			continue
		}
		items = append(items, domain.WorkItem{
			ID: "environment:" + environment.ID, Kind: "environment", Priority: domain.WorkPriorityHigh, Status: domain.WorkStatusBlocked,
			Title: environment.Name + " 需要维护", Subject: domain.WorkSubject{Type: "environment", ID: environment.ID, Name: environment.Name, Revision: environment.Revision.Revision},
			Reasons: reasons, PrimaryAction: domain.WorkAction{Label: "检查环境", Href: environmentHref}, SecondaryActions: []domain.WorkAction{}, UpdatedAt: environment.UpdatedAt,
		})
	}
	return items
}

func impactWork(user domain.User, notifications []domain.Notification, scenarios map[string]domain.Scenario) []domain.WorkItem {
	items := []domain.WorkItem{}
	if user.Role == domain.RoleScenarioOwner {
		grouped := map[string][]domain.Notification{}
		for _, notification := range notifications {
			for _, scenarioID := range payloadStrings(notification.Payload, "scenarioIds") {
				if scenario, ok := scenarios[scenarioID]; ok && scenario.OwnerID == user.ID {
					grouped[scenarioID] = append(grouped[scenarioID], notification)
				}
			}
		}
		for scenarioID, notices := range grouped {
			scenario := scenarios[scenarioID]
			latest := notices[0]
			for _, notice := range notices[1:] {
				if notice.CreatedAt.After(latest.CreatedAt) {
					latest = notice
				}
			}
			items = append(items, domain.WorkItem{
				ID: "upstream_impact:" + scenarioID, Kind: "upstream_impact", Priority: domain.WorkPriorityInfo, Status: domain.WorkStatusAttention,
				Title: scenario.Name + " 有上游变化待评估", Subject: domain.WorkSubject{Type: "scenario", ID: scenario.ID, Name: scenario.Name},
				Reasons:       []domain.WorkReason{{Code: "scenario.upstream_change_review", Message: fmt.Sprintf("有 %d 条未读上游发布影响；不会自动使当前 Revision 失效", len(notices)), Cause: ruleCause("上游发布只产生影响评估，不自动改变锁定 Release"), NextAction: workAction("评估影响", "/notifications?scenario="+scenarioID)}},
				PrimaryAction: domain.WorkAction{Label: "评估影响", Href: "/notifications?scenario=" + scenarioID}, SecondaryActions: []domain.WorkAction{}, UpdatedAt: latest.CreatedAt,
			})
		}
		return items
	}
	if user.Role == domain.RoleComponentOwner {
		for _, notice := range notifications {
			componentID, _ := notice.Payload["componentId"].(string)
			componentName, _ := notice.Payload["componentName"].(string)
			items = append(items, domain.WorkItem{
				ID: "upstream_impact:" + notice.ID, Kind: "upstream_impact", Priority: domain.WorkPriorityInfo, Status: domain.WorkStatusAttention,
				Title: notice.Title, Subject: domain.WorkSubject{Type: "component", ID: componentID, Name: valueOr(componentName, "上游组件")},
				Reasons:       []domain.WorkReason{{Code: "component.upstream_change_review", Message: notice.Body, Cause: ruleCause("上游组件发布了新的不可变 Release"), NextAction: workAction("评估影响", "/notifications?selected="+notice.ID)}},
				PrimaryAction: domain.WorkAction{Label: "评估影响", Href: "/notifications?selected=" + notice.ID}, SecondaryActions: []domain.WorkAction{}, UpdatedAt: notice.CreatedAt,
			})
		}
	}
	return items
}

func (p *ReadModelService) runWork(ctx context.Context, user domain.User, runs []domain.Run, embedded map[string]bool, components map[string]domain.Component, releases map[string]domain.ComponentRelease, scenarios map[string]domain.Scenario, revisions map[string]domain.ScenarioRevision, environments map[string]domain.Environment) ([]domain.WorkItem, error) {
	latestByKey := map[string]domain.Run{}
	for _, run := range runs {
		key := runWorkloadKey(run)
		if current, ok := latestByKey[key]; !ok || run.CreatedAt.After(current.CreatedAt) {
			latestByKey[key] = run
		}
	}
	items := []domain.WorkItem{}
	for _, run := range runs {
		if run.InputSnapshot["cleaned"] == true || embedded[run.ID] {
			continue
		}
		active := activeWorkRunStatuses[run.Status]
		failed := run.Status == domain.RunFailed || run.Status == domain.RunInterrupted
		if !active && (!failed || latestByKey[runWorkloadKey(run)].ID != run.ID) {
			continue
		}
		if failed {
			current, currentErr := p.runMatchesCurrentDefinition(ctx, run)
			if currentErr != nil {
				return nil, currentErr
			}
			if !current {
				continue
			}
			if user.Role == domain.RoleComponentOwner {
				owned, ownedErr := p.failedStepOwnedBy(ctx, run, user.ID, components)
				if ownedErr != nil {
					return nil, ownedErr
				}
				if !owned {
					continue
				}
			}
		}
		name := runDisplayName(run, components, releases, scenarios, revisions, environments)
		priority, status := domain.WorkPriorityNormal, domain.WorkStatusInProgress
		title := name + " " + runStatusMessage(run.Status)
		runHref := "/runs?selected=" + run.ID
		reason := domain.WorkReason{Code: "run.in_progress", Message: runStatusMessage(run.Status), EvidenceRunID: run.ID, Cause: runCause(run), NextAction: workAction("查看运行", runHref)}
		if run.Status == domain.RunAwaitingApproval {
			canApprove := user.Role == domain.RoleEnvironmentOwner && environments[run.EnvironmentID].OwnerID == user.ID
			priority, status = domain.WorkPriorityCritical, domain.WorkStatusActionRequired
			reason = domain.WorkReason{Code: "run.awaiting_approval", Message: "危险作业等待环境 Owner 审批", EvidenceRunID: run.ID, Cause: &domain.WorkCause{Kind: "approval_rule", Summary: "锁定计划包含 destructive 动作或跨站传输"}, NextAction: workAction(runActionLabel(run.Status, canApprove), runHref)}
		} else if failed {
			priority, status = domain.WorkPriorityCritical, domain.WorkStatusBlocked
			reason = domain.WorkReason{Code: "run.failed", Message: valueOr(run.Error, "最近一次有效运行失败"), EvidenceRunID: run.ID, Cause: runCause(run), NextAction: workAction("查看失败运行", runHref)}
		}
		canApprove := user.Role == domain.RoleEnvironmentOwner && environments[run.EnvironmentID].OwnerID == user.ID
		items = append(items, domain.WorkItem{
			ID: "run:" + run.ID, Kind: "run", Priority: priority, Status: status, Title: title,
			Subject: domain.WorkSubject{Type: "run", ID: run.ID, ParentID: run.EnvironmentID, Name: name, Environment: environmentName(run.EnvironmentID, environments)},
			Reasons: []domain.WorkReason{reason}, PrimaryAction: domain.WorkAction{Label: runActionLabel(run.Status, canApprove), Href: "/runs?selected=" + run.ID}, SecondaryActions: []domain.WorkAction{}, UpdatedAt: run.CreatedAt,
		})
	}
	return items, nil
}

func (p *ReadModelService) runMatchesCurrentDefinition(ctx context.Context, run domain.Run) (bool, error) {
	if run.ComponentReleaseID != "" {
		release, err := p.store.GetComponentRelease(ctx, run.ComponentReleaseID)
		if err != nil {
			return false, nil
		}
		return snapshotString(run, "componentReleaseSpecDigest") == componentReleaseSpecDigest(release), nil
	}
	if run.ScenarioRevisionID != "" {
		revision, err := p.store.GetScenarioRevision(ctx, run.ScenarioRevisionID)
		if err != nil {
			return false, nil
		}
		return scenarioRunDefinitionCurrent(ctx, run, revision, p.store.GetComponentRelease)
	}
	return true, nil
}

func (p *ReadModelService) failedStepOwnedBy(ctx context.Context, run domain.Run, ownerID string, components map[string]domain.Component) (bool, error) {
	steps, err := p.store.ListRunSteps(ctx, run.ID)
	if err != nil {
		return false, err
	}
	failedNodeID := ""
	for _, step := range steps {
		if step.Status == domain.RunFailed {
			failedNodeID = step.NodeID
			break
		}
	}
	lockedSteps, _ := run.InputSnapshot["steps"].([]any)
	for _, raw := range lockedSteps {
		locked, _ := raw.(map[string]any)
		if nodeID, _ := locked["nodeId"].(string); nodeID != failedNodeID {
			continue
		}
		componentID, _ := locked["componentId"].(string)
		component, ok := components[componentID]
		if !ok {
			loaded, loadErr := p.store.GetComponent(ctx, componentID, false)
			if loadErr != nil {
				return false, nil
			}
			component = loaded
		}
		return component.OwnerID == ownerID, nil
	}
	return run.RequestedBy == ownerID, nil
}

func currentScenarioRevision(scenario domain.Scenario) (domain.ScenarioRevision, bool) {
	for _, revision := range scenario.Revisions {
		if revision.ID == scenario.CurrentRevisionID {
			return revision, true
		}
	}
	return domain.ScenarioRevision{}, false
}

func latestMatchingRun(runs []domain.Run, predicate func(domain.Run) bool) *domain.Run {
	var latest *domain.Run
	for i := range runs {
		if predicate(runs[i]) && (latest == nil || runs[i].CreatedAt.After(latest.CreatedAt)) {
			candidate := runs[i]
			latest = &candidate
		}
	}
	return latest
}

func snapshotString(run domain.Run, key string) string {
	value, _ := run.InputSnapshot[key].(string)
	return value
}

func payloadStrings(payload map[string]any, key string) []string {
	raw, _ := payload[key].([]any)
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}

func runWorkloadKey(run domain.Run) string {
	if run.ComponentReleaseID != "" {
		return fmt.Sprintf("%s:%s:%s:%s", run.Kind, run.ComponentReleaseID, run.Action, run.EnvironmentID)
	}
	if run.ScenarioRevisionID != "" {
		return fmt.Sprintf("%s:%s:%s", run.Kind, run.ScenarioRevisionID, run.EnvironmentID)
	}
	return fmt.Sprintf("%s:%s", run.Kind, run.EnvironmentID)
}

func runDisplayName(run domain.Run, components map[string]domain.Component, releases map[string]domain.ComponentRelease, scenarios map[string]domain.Scenario, revisions map[string]domain.ScenarioRevision, environments map[string]domain.Environment) string {
	if release, ok := releases[run.ComponentReleaseID]; ok {
		if component, found := components[release.ComponentID]; found {
			return component.Name + " " + release.Version
		}
	}
	if revision, ok := revisions[run.ScenarioRevisionID]; ok {
		if scenario, found := scenarios[revision.ScenarioID]; found {
			return scenario.Name + fmt.Sprintf(" r%d", revision.Revision)
		}
	}
	if run.Kind == domain.RunEnvironmentRollback {
		return environmentName(run.EnvironmentID, environments) + " 整集群回滚"
	}
	return "Run " + run.ID[:min(12, len(run.ID))]
}

func environmentName(id string, environments map[string]domain.Environment) string {
	if environment, ok := environments[id]; ok {
		return environment.Name
	}
	return id
}

func runStatusMessage(status domain.RunStatus) string {
	switch status {
	case domain.RunAwaitingApproval:
		return "等待审批"
	case domain.RunQueued:
		return "正在排队"
	case domain.RunRunning:
		return "正在执行"
	case domain.RunInterrupted:
		return "运行被中断"
	case domain.RunFailed:
		return "运行失败"
	default:
		return string(status)
	}
}

func runActionLabel(status domain.RunStatus, canApprove bool) string {
	if status == domain.RunAwaitingApproval {
		if canApprove {
			return "复核并审批"
		}
		return "查看审批状态"
	}
	if status == domain.RunFailed || status == domain.RunInterrupted {
		return "查看失败诊断"
	}
	return "查看运行"
}

func workAction(label, href string) *domain.WorkAction {
	return &domain.WorkAction{Label: label, Href: href}
}

func ruleCause(summary string) *domain.WorkCause {
	return &domain.WorkCause{Kind: "platform_rule", Summary: summary}
}

func runCause(run domain.Run) *domain.WorkCause {
	at := run.CreatedAt
	if run.FinishedAt != nil {
		at = *run.FinishedAt
	} else if run.StartedAt != nil {
		at = *run.StartedAt
	}
	return &domain.WorkCause{Kind: "run", Summary: runStatusMessage(run.Status), At: &at}
}

func (p *ReadModelService) evidenceInvalidationCause(ctx context.Context, resourceType, resourceID string, evidence *domain.Run) *domain.WorkCause {
	after := evidence.CreatedAt
	if evidence.FinishedAt != nil {
		after = *evidence.FinishedAt
	}
	allowed := map[string]string{
		"component_release.updated":       "Release 合同或动作定义在证据产生后发生修改",
		"component_playbook.saved":        "Playbook 在证据产生后发生修改",
		"component.artifact_saved":        "组件介质在证据产生后发生修改",
		"component.artifact_detached":     "组件介质引用在证据产生后被移除",
		"scenario_revision.graph_updated": "场景图或执行策略在证据产生后发生修改",
	}
	actions := make([]string, 0, len(allowed))
	for action := range allowed {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	event, err := p.store.FirstAuditForResourceAfter(ctx, resourceType, resourceID, after, actions)
	if err != nil {
		return ruleCause("当前定义与证据 Run 锁定的定义摘要不一致")
	}
	summary := allowed[event.Action]
	cause := &domain.WorkCause{Kind: "audit_event", Summary: summary, ActorID: event.ActorID, Action: event.Action, At: &event.CreatedAt}
	if actor, actorErr := p.store.GetUser(ctx, event.ActorID); actorErr == nil {
		cause.ActorName = actor.Name
	}
	return cause
}

func mergeRunSet(target, source map[string]bool) {
	for id := range source {
		target[id] = true
	}
}

func sortWorkItems(items []domain.WorkItem) {
	priority := map[domain.WorkPriority]int{domain.WorkPriorityCritical: 0, domain.WorkPriorityHigh: 1, domain.WorkPriorityNormal: 2, domain.WorkPriorityInfo: 3}
	status := map[domain.WorkStatus]int{domain.WorkStatusActionRequired: 0, domain.WorkStatusBlocked: 1, domain.WorkStatusInProgress: 2, domain.WorkStatusAttention: 3}
	sort.SliceStable(items, func(i, j int) bool {
		if priority[items[i].Priority] != priority[items[j].Priority] {
			return priority[items[i].Priority] < priority[items[j].Priority]
		}
		if status[items[i].Status] != status[items[j].Status] {
			return status[items[i].Status] < status[items[j].Status]
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func (s *ReadModelService) ListNotifications(ctx context.Context, userID string, unreadOnly bool) ([]domain.Notification, error) {
	return s.store.ListNotifications(ctx, userID, unreadOnly)
}

func (s *ReadModelService) MarkNotificationRead(ctx context.Context, id, userID string) error {
	return s.store.MarkNotificationRead(ctx, id, userID)
}

func (s *ReadModelService) GetNotification(ctx context.Context, id string) (domain.Notification, error) {
	return s.store.GetNotification(ctx, id)
}
