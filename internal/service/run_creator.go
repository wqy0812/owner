package service

import (
	"context"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (c *RunCreator) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, steps []lockedStep, resolvedParametersByNode map[string]map[string]resolvedParameter, expectedPlanDigest string) (domain.Run, error) {
	p := c.platform
	now := time.Now().UTC()
	runID := newID("run")
	plan, planDigest, destructive, err := p.prepareLockedPlan(ctx, environment, kind, runID, now, steps)
	if err != nil {
		return domain.Run{}, err
	}
	if expectedPlanDigest != "" && expectedPlanDigest != planDigest {
		base := fmt.Errorf("%w: execution plan changed after preview; refresh the plan before submitting", domain.ErrConflict)
		return domain.Run{}, actionableExistingError(base, "execution.plan_changed", "环境 Revision、输入、Release 定义或可执行内容在预览后发生变化", "重新预览执行计划", p.planRefreshHref(ctx, kind, releaseID, revisionID, environment.ID))
	}
	snapshot := structToMap(plan)
	if kind == domain.RunComponentTest {
		snapshot["componentTestEvidence"] = componentTestEvidence(action, plan.Steps)
	}
	if len(resolvedParametersByNode) > 0 {
		snapshot["resolvedParametersByNode"] = provenanceSnapshot(resolvedParametersByNode)
	}
	if releaseID != "" {
		release, err := p.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return domain.Run{}, err
		}
		snapshot["componentReleaseSpecDigest"] = componentReleaseSpecDigest(release)
	}
	if revisionID != "" {
		revision, err := p.store.GetScenarioRevision(ctx, revisionID)
		if err != nil {
			return domain.Run{}, err
		}
		snapshot["scenarioRevisionSpecDigest"] = scenarioRevisionSpecDigest(revision)
	}
	redactedRefs := make([]domain.CredentialRef, 0)
	if environment.Revision != nil {
		redactedRefs = domain.RedactCredentialRefs(environment.Revision.CredentialRefs, false)
	}
	snapshot["environmentRevisionId"] = environment.CurrentRevisionID
	snapshot["credentialRefs"] = redactedRefs
	status := domain.RunQueued
	var approval *domain.Approval
	run := domain.Run{
		ID: runID, Kind: kind, Status: status, RequestedBy: user.ID,
		EnvironmentID: environment.ID, EnvironmentRevisionID: environment.CurrentRevisionID,
		ComponentReleaseID: releaseID, ScenarioRevisionID: revisionID, Action: action,
		Destructive: destructive, InputSnapshot: snapshot, ArtifactDigest: plan.TreeDigest, CreatedAt: now,
	}
	if destructive {
		run.Status = domain.RunAwaitingApproval
		approval = &domain.Approval{ID: newID("approval"), RunID: run.ID, Status: "pending", RequestedAt: now}
		run.Approval = approval
	}
	if err := p.store.CreateRun(ctx, run, approval); err != nil {
		return run, err
	}
	p.audit(ctx, user, "run.created", "run", run.ID, map[string]any{
		"kind": kind, "environmentId": environment.ID, "environmentRevisionId": environment.CurrentRevisionID,
		"componentReleaseId": releaseID, "scenarioRevisionId": revisionID, "artifactDigest": plan.TreeDigest, "destructive": destructive,
	})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	if !destructive {
		p.schedule(environment.ID)
	}
	return run, nil
}

func componentTestEvidence(action domain.ActionKind, steps []lockedStep) string {
	upgradeSeen, targetVerified, rollbackSeen, parentVerified := false, false, false, false
	for _, step := range steps {
		switch step.Action {
		case domain.ActionUpgrade:
			upgradeSeen = true
		case domain.ActionRollback:
			if upgradeSeen && targetVerified {
				rollbackSeen = true
			}
		case domain.ActionVerify:
			if rollbackSeen {
				parentVerified = true
			} else if upgradeSeen {
				targetVerified = true
			}
		}
	}
	if upgradeSeen && targetVerified && rollbackSeen && parentVerified {
		return "evolution_round_trip"
	}
	if action == domain.ActionRollback {
		rollbackSeen := false
		rollbackSelfVerifies := false
		for _, step := range steps {
			if step.Action == domain.ActionRollback {
				rollbackSeen = true
				rollbackSelfVerifies = step.FromReleaseID == "" && step.ToReleaseID == "" && containsString(step.Tags, rollbackSelfVerifyTag)
				continue
			}
			if rollbackSeen && step.Action == domain.ActionVerify {
				return "rollback_verify"
			}
		}
		if rollbackSeen && rollbackSelfVerifies {
			return "rollback_self_verify"
		}
		return "rollback_only"
	}
	primarySeen := false
	for _, step := range steps {
		if step.Action == domain.ActionUpgrade || step.Action == domain.ActionInstall || step.Action == domain.ActionConfigure || step.Action == domain.ActionPreflight || step.Action == domain.ActionInspect {
			primarySeen = true
			continue
		}
		if primarySeen && step.Action == domain.ActionVerify {
			return "install_verify"
		}
	}
	return "incomplete"
}

const rollbackSelfVerifyTag = "clusterforge.rollback-self-verifies"

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func injectEnvironmentVariables(revision domain.EnvironmentRevision, steps []lockedStep) error {
	credentialNames := make(map[string]struct{}, len(revision.CredentialRefs))
	for _, ref := range revision.CredentialRefs {
		credentialNames[ref.Name] = struct{}{}
	}
	for index := range steps {
		if steps[index].Variables == nil {
			steps[index].Variables = map[string]any{}
		}
		for name, value := range revision.Variables {
			if _, exists := steps[index].Variables[name]; exists {
				return fmt.Errorf("%w: environment variable %q conflicts with a component parameter", domain.ErrInvalid, name)
			}
			if _, exists := credentialNames[name]; exists {
				return fmt.Errorf("%w: environment variable %q conflicts with a CredentialRef", domain.ErrInvalid, name)
			}
			steps[index].Variables[name] = value
		}
	}
	return nil
}
