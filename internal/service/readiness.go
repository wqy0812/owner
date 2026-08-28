package service

import (
	"context"
	"fmt"
	"strings"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) releaseReadiness(ctx context.Context, release domain.ComponentRelease) (domain.ReleaseReadiness, error) {
	return p.releaseReadinessWithin(ctx, release, map[string]bool{})
}

func (p *Platform) releaseReadinessWithin(ctx context.Context, release domain.ComponentRelease, visiting map[string]bool) (domain.ReleaseReadiness, error) {
	componentURL := fmt.Sprintf("/components?selected=%s&release=%s", release.ComponentID, release.ID)
	result := domain.ReleaseReadiness{Status: domain.ReadinessReady, Blockers: []domain.ReadinessBlocker{}}
	add := func(code, message, action string) {
		result.Blockers = append(result.Blockers, domain.ReadinessBlocker{Code: code, Message: message, ActionURL: componentURL + "&action=" + action})
	}

	configured := map[domain.ActionKind]bool{}
	for _, action := range release.Actions {
		configured[action.Kind] = true
	}
	for _, required := range []struct {
		kind  domain.ActionKind
		label string
	}{{domain.ActionInstall, "Install"}, {domain.ActionVerify, "Verify"}, {domain.ActionRollback, "Rollback"}} {
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
			upstreamReadiness, readyErr := p.releaseReadinessWithin(ctx, upstream, visiting)
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
	} else if release.Breaking || release.RiskLevel == domain.RiskHigh || release.RiskLevel == domain.RiskDestructive {
		result.Status = domain.ReadinessRisky
	}
	return result, nil
}

func (p *Platform) validateReleaseTransitionContracts(ctx context.Context, release domain.ComponentRelease) error {
	for _, action := range release.Actions {
		switch action.Kind {
		case domain.ActionUpgrade:
			if action.ToReleaseID != release.ID || action.FromReleaseID == release.ID {
				return fmt.Errorf("%w: upgrade action must point from an earlier release to this release", domain.ErrInvalid)
			}
			from, err := p.store.GetComponentRelease(ctx, action.FromReleaseID)
			if err != nil || from.ComponentID != release.ComponentID || from.Status != domain.ReleaseReleased {
				return fmt.Errorf("%w: upgrade fromReleaseId must lock a released version of the same component", domain.ErrInvalid)
			}
		case domain.ActionRollback:
			if action.FromReleaseID == "" && action.ToReleaseID == "" {
				continue
			}
			if action.FromReleaseID != release.ID || action.ToReleaseID == release.ID {
				return fmt.Errorf("%w: rollback action must point from this release to an earlier release", domain.ErrInvalid)
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
