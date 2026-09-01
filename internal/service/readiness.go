package service

import (
	"context"
	"fmt"
	"strings"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) releaseReadiness(ctx context.Context, release domain.ComponentRelease) (domain.ReleaseReadiness, error) {
	return p.releaseReadinessWithin(ctx, release, map[string]bool{}, map[string]domain.ReleaseReadiness{})
}

func (p *Platform) releaseReadinessWithin(ctx context.Context, release domain.ComponentRelease, visiting map[string]bool, memo map[string]domain.ReleaseReadiness) (domain.ReleaseReadiness, error) {
	if cached, ok := memo[release.ID]; ok {
		return cached, nil
	}
	componentURL := fmt.Sprintf("/components?selected=%s&release=%s", release.ComponentID, release.ID)
	result := domain.ReleaseReadiness{Status: domain.ReadinessReady, Blockers: []domain.ReadinessBlocker{}}
	add := func(code, message, action string) {
		result.Blockers = append(result.Blockers, domain.ReadinessBlocker{Code: code, Message: message, ActionURL: componentURL + "&action=" + action})
	}

	configured := map[domain.ActionKind]bool{}
	for _, action := range release.Actions {
		configured[action.Kind] = true
	}
	requiredActions := []struct {
		kind  domain.ActionKind
		label string
	}{{domain.ActionInstall, "Install"}, {domain.ActionVerify, "Verify"}, {domain.ActionRollback, "Rollback"}}
	if release.ParentReleaseID != "" {
		requiredActions = []struct {
			kind  domain.ActionKind
			label string
		}{{domain.ActionVerify, "Verify"}, {domain.ActionRollback, "Rollback"}}
		if _, err := actionFor(release, domain.ActionUpgrade); err != nil {
			add("lifecycle_action_missing", "缺少 Upgrade 生命周期能力（显式 Upgrade 或幂等 Install）", "lifecycle")
		}
	}
	for _, required := range requiredActions {
		if !configured[required.kind] {
			add("lifecycle_action_missing", "缺少 "+required.label+" 生命周期动作", "lifecycle")
		}
	}

	if err := p.validateReleaseContract(ctx, release, true); err != nil {
		add("release_contract_invalid", err.Error(), "contract")
	} else if err := p.validateReleaseTransitionContracts(ctx, release); err != nil {
		add("release_contract_invalid", err.Error(), "contract")
	}

	digest := componentReleaseSpecDigest(release)
	if release.ParentReleaseID != "" {
		transitionID, err := p.store.SuccessfulComponentEvolutionEvidenceRunID(ctx, release.ID, digest)
		if err != nil {
			return result, err
		}
		result.TransitionEvidenceRunID = transitionID
		if transitionID == "" {
			add("evolution_evidence_missing", "当前合同缺少父版本安装、升级、验证和回退闭环证据", "validate")
		}
	} else {
		installID, rollbackID, err := p.store.SuccessfulComponentEvidenceRunIDs(ctx, release.ID, digest)
		if err != nil {
			return result, err
		}
		result.InstallEvidenceRunID = installID
		result.RollbackEvidenceRunID = rollbackID
		if installID == "" {
			add("install_evidence_missing", "当前合同缺少安装及 Verify 成功证据", "validate")
		}
		if rollbackID == "" {
			add("rollback_evidence_missing", "当前合同缺少回滚及回滚后验证证据", "validate")
		}
	}

	if visiting[release.ID] {
		add("candidate_dependency_cycle", "候选依赖闭包存在循环", "contract")
	} else {
		visiting[release.ID] = true
		for _, dependency := range release.Dependencies {
			upstream, getErr := p.store.GetComponentRelease(ctx, dependency.UpstreamReleaseID)
			if getErr != nil {
				add("dependency_unavailable", fmt.Sprintf("依赖 Release %s 不可用", dependency.UpstreamReleaseID), "contract")
				continue
			}
			if upstream.Status == domain.ReleaseReleased {
				continue
			}
			if upstream.Status != domain.ReleaseDraft || !upstream.Candidate {
				add("candidate_dependency_not_shared", fmt.Sprintf("依赖 %s 不是 Released 或共享候选", dependency.UpstreamReleaseID), "contract")
				continue
			}
			upstreamReadiness, readyErr := p.releaseReadinessWithin(ctx, upstream, visiting, memo)
			if readyErr != nil {
				delete(visiting, release.ID)
				return result, readyErr
			}
			if upstreamReadiness.Status == domain.ReadinessBlocked {
				add("candidate_dependency_blocked", fmt.Sprintf("候选依赖 %s 尚未就绪", dependency.UpstreamReleaseID), "contract")
			}
		}
		delete(visiting, release.ID)
	}

	if len(result.Blockers) > 0 {
		result.Status = domain.ReadinessBlocked
	} else if release.Compatibility == domain.CompatibilityBreaking || release.RiskLevel == domain.RiskHigh || release.RiskLevel == domain.RiskDestructive {
		result.Status = domain.ReadinessRisky
	}
	memo[release.ID] = result
	return result, nil
}

func (p *Platform) validateReleaseTransitionContracts(ctx context.Context, release domain.ComponentRelease) error {
	var parent domain.ComponentRelease
	if release.ParentReleaseID == "" {
		for _, action := range release.Actions {
			if action.Kind == domain.ActionUpgrade || (action.Kind == domain.ActionRollback && (action.FromReleaseID != "" || action.ToReleaseID != "")) {
				return fmt.Errorf("%w: a new-line baseline cannot declare cross-release upgrade or rollback transitions", domain.ErrInvalid)
			}
		}
	} else {
		var err error
		parent, err = p.store.GetComponentRelease(ctx, release.ParentReleaseID)
		if err != nil || parent.ComponentID != release.ComponentID || parent.LineID != release.LineID || parent.Status != domain.ReleaseReleased {
			return fmt.Errorf("%w: evolution parent must be a released version in the same line", domain.ErrInvalid)
		}
		if release.Status == domain.ReleaseDraft {
			latest, latestErr := p.store.LatestPublishedInLine(ctx, release.LineID)
			if latestErr != nil || latest.ID != parent.ID {
				return fmt.Errorf("%w: evolution parent is no longer the latest published version in the line", domain.ErrConflict)
			}
		}
		if _, err := actionFor(release, domain.ActionUpgrade); err != nil {
			return fmt.Errorf("%w: evolution requires an explicit Upgrade or idempotent Install", domain.ErrInvalid)
		}
		rollback, ok := findAction(release, domain.ActionRollback)
		if !ok || rollback.FromReleaseID != release.ID || rollback.ToReleaseID != parent.ID {
			return fmt.Errorf("%w: evolution rollback must point from this release to its parent", domain.ErrInvalid)
		}
	}
	for _, action := range release.Actions {
		switch action.Kind {
		case domain.ActionUpgrade:
			if release.ParentReleaseID == "" || action.ToReleaseID != release.ID || action.FromReleaseID != release.ParentReleaseID {
				return fmt.Errorf("%w: upgrade action must point from the evolution parent to this release", domain.ErrInvalid)
			}
			from, err := p.store.GetComponentRelease(ctx, action.FromReleaseID)
			if err != nil || from.ComponentID != release.ComponentID || from.LineID != release.LineID || from.Status != domain.ReleaseReleased {
				return fmt.Errorf("%w: upgrade fromReleaseId must lock a released version of the same component", domain.ErrInvalid)
			}
		case domain.ActionRollback:
			if action.FromReleaseID == "" && action.ToReleaseID == "" {
				if release.ParentReleaseID != "" {
					return fmt.Errorf("%w: evolution rollback must be bound to its parent", domain.ErrInvalid)
				}
				continue
			}
			if action.FromReleaseID != release.ID || action.ToReleaseID != release.ParentReleaseID {
				return fmt.Errorf("%w: rollback action must point from this release to its evolution parent", domain.ErrInvalid)
			}
			to, err := p.store.GetComponentRelease(ctx, action.ToReleaseID)
			if err != nil || to.ComponentID != release.ComponentID || to.Status != domain.ReleaseReleased {
				return fmt.Errorf("%w: rollback toReleaseId must lock an earlier released version of the same component", domain.ErrInvalid)
			}
		}
	}
	if digester, ok := p.runner.(digestRunner); ok {
		for _, action := range release.Actions {
			if _, _, err := digester.Digest(action.Playbook); err != nil {
				return fmt.Errorf("%w: action %s playbook is not executable: %v", domain.ErrInvalid, action.Name, err)
			}
		}
	}
	return nil
}

func readinessError(readiness domain.ReleaseReadiness) error {
	if readiness.Status != domain.ReadinessBlocked {
		return nil
	}
	messages := make([]string, 0, len(readiness.Blockers))
	for _, blocker := range readiness.Blockers {
		messages = append(messages, blocker.Message)
	}
	return fmt.Errorf("%w: release readiness blocked: %s", domain.ErrConflict, strings.Join(messages, "; "))
}

func (p *Platform) decorateReleaseReadiness(ctx context.Context, release domain.ComponentRelease) (domain.ComponentRelease, error) {
	readiness, err := p.releaseReadiness(ctx, release)
	if err != nil {
		return release, err
	}
	release.Readiness = readiness
	return release, nil
}

func (p *Platform) decorateComponentReadiness(ctx context.Context, component domain.Component) (domain.Component, error) {
	for i := range component.Releases {
		release, err := p.decorateReleaseReadiness(ctx, component.Releases[i])
		if err != nil {
			return component, err
		}
		component.Releases[i] = release
	}
	return component, nil
}
