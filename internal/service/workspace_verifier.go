package service

import (
	"context"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func (p *WorkspaceVerifier) bindVerifiedWorkspaceDigests(ctx context.Context, steps []lockedStep) error {
	byRelease := map[string][]int{}
	for index := range steps {
		if steps[index].SourceType == "scenario_acceptance" {
			if err := p.verifyScenarioAcceptanceStep(ctx, &steps[index]); err != nil {
				return err
			}
			continue
		}
		byRelease[steps[index].ReleaseID] = append(byRelease[steps[index].ReleaseID], index)
	}
	for releaseID, indexes := range byRelease {
		release, err := p.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return err
		}
		if err := p.releaseRules.validateWorkspaceManifest(ctx, release); err != nil {
			return err
		}
		playbooks := make([]string, 0, len(indexes))
		actions := make(map[string]domain.ActionDefinition, len(release.Actions))
		for _, action := range release.Actions {
			actions[action.ID] = action
		}
		for _, index := range indexes {
			step := &steps[index]
			if current := componentReleaseSpecDigest(release); current != step.ReleaseSpecDigest {
				return fmt.Errorf("%w: release %s changed after the plan was assembled", domain.ErrConflict, releaseID)
			}
			action, found := actions[step.ActionID]
			if !found || action.Kind != step.Action || action.Playbook != step.Playbook {
				return fmt.Errorf("%w: release %s action changed after the plan was assembled", domain.ErrConflict, releaseID)
			}
			if action.PlaybookSHA256 == "" {
				return fmt.Errorf("%w: action %s has no persisted Playbook digest", domain.ErrConflict, action.Kind)
			}
			playbooks = append(playbooks, step.Playbook)
		}
		digests, treeDigest, err := p.inspector.DigestPlan(playbooks)
		if err != nil {
			return err
		}

		for _, index := range indexes {
			step := &steps[index]
			action := actions[step.ActionID]
			if digests[step.Playbook] != action.PlaybookSHA256 {
				return fmt.Errorf("%w: action %s bytes differ from the Release manifest", domain.ErrConflict, action.Kind)
			}
			step.PlaybookDigest = digests[step.Playbook]
			step.WorkspaceDigest = treeDigest
		}
	}
	return nil
}

func (p *WorkspaceVerifier) verifyLockedWorkspaceDigests(ctx context.Context, locked []lockedStep) error {
	current := append([]lockedStep(nil), locked...)
	if err := p.bindVerifiedWorkspaceDigests(ctx, current); err != nil {
		return err
	}
	for index := range locked {
		if current[index].PlaybookDigest != locked[index].PlaybookDigest || current[index].WorkspaceDigest != locked[index].WorkspaceDigest {
			return fmt.Errorf("%w: release workspace changed after the run was queued", domain.ErrConflict)
		}
	}
	return nil
}
