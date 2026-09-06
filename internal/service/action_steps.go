package service

import (
	"context"
	"fmt"
	"strings"

	"codex/platform-demo/internal/domain"
)

func (p *ActionPlanner) expandActionSteps(ctx context.Context, steps []lockedStep) ([]lockedStep, error) {
	expanded := []lockedStep{}
	for _, main := range steps {
		if main.Phase != "" {
			expanded = append(expanded, main)
			continue
		}
		release, err := p.store.GetComponentRelease(ctx, main.ReleaseID)
		if err != nil {
			return nil, err
		}
		action, ok := release.ActionByID(main.ActionID)
		if !ok {
			return nil, domain.ErrNotFound
		}
		if err := domain.ValidateActionBindings(release, action.Kind != domain.ActionCheck); err != nil {
			return nil, err
		}
		if main.SourceNodeID == "" {
			main.SourceNodeID = main.NodeID
		}
		main.ParentActionID = main.ActionID
		if action.Kind == domain.ActionCheck {
			main.Phase = "check"
			expanded = append(expanded, main)
			continue
		}
		component, err := p.store.GetComponent(ctx, main.ComponentID, false)
		if err != nil {
			return nil, err
		}
		preID, postID := action.PreCheckActionID, action.PostCheckActionID
		if action.Kind == domain.ActionRollback {
			sourceID := main.RollbackSourceActionID
			if sourceID == "" && main.Backup != nil {
				sourceID = main.Backup.ActionID
			}
			check, err := domain.RollbackPostCheck(release, action, sourceID)
			if err != nil {
				return nil, err
			}
			postID = check.ID
			main.RollbackSourceActionID = sourceID
		}
		main.PreCheckRequired = preID != ""
		for i, id := range []string{preID, postID} {
			if i == 1 {
				main.Phase = "execute"
				expanded = append(expanded, main)
			}
			if id == "" {
				continue
			}
			check, ok := release.ActionByID(id)
			if !ok {
				return nil, fmt.Errorf("%w: bound check not found", domain.ErrInvalid)
			}
			check.HostGroup = main.Limit
			variables := cloneMap(main.Variables)
			defaultRollbackPost := action.Kind == domain.ActionRollback && action.PostCheckActionID == "" && i == 1
			if defaultRollbackPost && main.RollbackSourceVariables != nil {
				variables = cloneMap(main.RollbackSourceVariables)
			}

			phase := []string{"pre", "post"}[i]
			step, err := p.lockAction(component, main.NodeID+"-"+phase+"-"+check.ID, release, check, variables)
			if err != nil {
				return nil, err
			}
			step.Phase, step.ParentActionID, step.SourceNodeID = phase, main.ActionID, main.SourceNodeID
			step.Stage, step.SourceType = main.Stage, main.SourceType
			step.SourceParametersFrozen = main.SourceParametersFrozen
			if defaultRollbackPost {
				step.RollbackSourceActionID = main.RollbackSourceActionID
				step.SourceParametersFrozen = main.RollbackSourceFrozen
			}
			expanded = append(expanded, step)
		}
	}
	return expanded, nil
}

// Backup context is bound to the executable action once and shared by its
// checks. A retry must retain the source baseline, never capture partial state.
func syncActionCheckContext(steps []lockedStep) {
	parents := map[string]lockedStep{}
	for _, step := range steps {
		if step.Phase == "execute" {
			parents[step.SourceNodeID+"\x00"+step.ActionID] = step
		}
	}
	for i := range steps {
		step := &steps[i]
		if step.Phase != "pre" && step.Phase != "post" {
			continue
		}
		parent, ok := parents[step.SourceNodeID+"\x00"+step.ParentActionID]
		if !ok {
			continue
		}
		if step.Phase == "post" && step.RollbackSourceActionID != "" {
			step.Variables = cloneMap(step.Variables)
			for name, value := range parent.Variables {
				if strings.HasPrefix(name, "clusterforge_backup_") || strings.HasPrefix(name, "clusterforge_replaced_") {
					step.Variables[name] = value
				}
			}
		} else {
			step.Variables = cloneMap(parent.Variables)
		}
		step.BackupRef = parent.BackupRef
		step.Backup = parent.Backup
	}
}

func refreshParentSteps(plan *lockedPlan) {
	syncActionCheckContext(plan.Steps)
	for i := range plan.ParentSteps {
		for _, current := range plan.Steps {
			if current.ID == plan.ParentSteps[i].ID {
				plan.ParentSteps[i] = current
				break
			}
		}
	}
}

func (b *ActionPlanner) lockAction(component domain.Component, nodeID string, release domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) (lockedStep, error) {
	if err := domain.ValidateReleaseYAMLAuthoring(release); err != nil {
		return lockedStep{}, err
	}
	step := lockedStep{ResourceContract: action.ResourceContract,
		ID: "locked-" + digestValue([]string{nodeID, release.ID, action.ID})[:24], NodeID: nodeID, Name: component.Name + " · " + actionDisplayName(action),
		ComponentID: component.ID, ComponentName: component.Name, ReleaseID: release.ID, ReleaseVersion: release.Version,
		ReleaseSpecDigest: componentReleaseSpecDigest(release), ActionID: action.ID,
		Action: action.Kind, FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		Playbook: action.Playbook, Tags: append([]string(nil), action.Tags...),
		Limit: action.HostGroup, Variables: cloneMap(variables),
		RequiredCredentials: append([]string(nil), action.RequiredCredentials...), TimeoutSeconds: action.TimeoutSeconds,
		NeedsApproval: action.NeedsApproval(),
		RetrySafe:     action.Kind == domain.ActionCheck || action.Idempotent, Become: action.Become,
	}
	return step, nil
}

func actionDisplayName(action domain.ActionDefinition) string {
	if strings.TrimSpace(action.Name) != "" {
		return action.Name
	}
	return string(action.Kind)
}
