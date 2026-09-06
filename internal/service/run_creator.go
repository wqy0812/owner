package service

import (
	"context"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func (c *RunCreator) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, prepared lockedRunPreparation, resolvedParametersByNode map[string]map[string]resolvedParameter, expectedScenarioDigest ...string) (domain.Run, error) {
	runID, now, plan, destructive := prepared.ID, prepared.CapturedAt, prepared.Plan, prepared.Destructive

	snapshot := structToMap(plan)
	if kind == domain.RunComponentTest {
		snapshot["componentTestEvidence"] = componentTestEvidence(action, plan.Steps)
	}
	if len(resolvedParametersByNode) > 0 {
		snapshot["resolvedParametersByNode"] = provenanceSnapshot(resolvedParametersByNode)
	}
	if releaseID != "" {
		release, err := c.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return domain.Run{}, err
		}
		snapshot["componentReleaseSpecDigest"] = componentReleaseSpecDigest(release)
	}
	if revisionID != "" {
		revision, err := c.store.GetScenarioRevision(ctx, revisionID)
		if err != nil {
			return domain.Run{}, err
		}
		if len(expectedScenarioDigest) > 0 && expectedScenarioDigest[0] != scenarioRevisionSpecDigest(revision) {
			return domain.Run{}, fmt.Errorf("%w: 场景在预览后变化，请重新预览", domain.ErrConflict)
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
	if err := c.store.CreateRun(ctx, run, approval); err != nil {
		return run, err
	}
	c.audit.Record(ctx, user, "run.created", "run", run.ID, map[string]any{
		"kind": kind, "environmentId": environment.ID, "environmentRevisionId": environment.CurrentRevisionID,
		"componentReleaseId": releaseID, "scenarioRevisionId": revisionID, "artifactDigest": plan.TreeDigest, "destructive": destructive,
	})
	c.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	if !destructive {
		c.scheduler.schedule(environment.ID)
	}
	return run, nil
}

func componentTestEvidence(action domain.ActionKind, steps []lockedStep) string {
	complete := map[domain.ActionKind]bool{}
	for i, step := range steps {
		if step.Phase != "execute" {
			continue
		}
		if i+1 >= len(steps) || steps[i+1].Phase != "post" || steps[i+1].ParentActionID != step.ActionID || steps[i+1].SourceNodeID != step.SourceNodeID {
			return "incomplete"
		}
		if (step.Action != domain.ActionRollback || step.PreCheckRequired) && (i == 0 || steps[i-1].Phase != "pre" || steps[i-1].ParentActionID != step.ActionID || steps[i-1].SourceNodeID != step.SourceNodeID) {
			return "incomplete"
		}
		complete[step.Action] = true
	}
	if action == domain.ActionUpgrade && complete[domain.ActionInstall] && complete[domain.ActionUpgrade] && complete[domain.ActionRollback] {
		return "evolution_round_trip"
	}
	if action == domain.ActionRollback && complete[domain.ActionRollback] {
		return "rollback_verify"
	}
	if complete[domain.ActionInstall] {
		return "install_verify"
	}
	if action == domain.ActionCheck {
		return "check"
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
	for name := range revision.Variables {
		if _, exists := credentialNames[name]; exists {
			return fmt.Errorf("%w: environment variable %q conflicts with a CredentialRef", domain.ErrInvalid, name)
		}
	}
	for index := range steps {
		if steps[index].Variables == nil {
			steps[index].Variables = map[string]any{}
		}
		// Scenario component stages already contain the complete effective
		// environment values. Source checks and uninstall must retain those
		// frozen values rather than inject the target environment's new values.
		if steps[index].SourceParametersFrozen || (steps[index].SourceType == "component_action" && steps[index].Stage != "") {
			continue
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
