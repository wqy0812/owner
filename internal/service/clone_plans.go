package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"codex/platform-demo/internal/domain"
)

type ReleaseCloneRequest struct {
	Version                string           `json:"version"`
	ReleaseNotes           string           `json:"releaseNotes"`
	Breaking               bool             `json:"breaking"`
	RiskLevel              domain.RiskLevel `json:"riskLevel,omitempty"`
	EnvironmentConstraints map[string]any   `json:"environmentConstraints"`
	ExpectedPlanDigest     string           `json:"expectedPlanDigest,omitempty"`
}

type ReleaseClonePlan struct {
	SourceReleaseID string              `json:"sourceReleaseId"`
	SourceVersion   string              `json:"sourceVersion"`
	TargetVersion   string              `json:"targetVersion"`
	PlanDigest      string              `json:"planDigest"`
	Actions         []domain.ActionKind `json:"actions"`
	Playbooks       []string            `json:"playbooks"`
	ArtifactCount   int                 `json:"artifactCount"`
}

func digestValue(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func (p *Platform) PreviewReleaseClone(ctx context.Context, user domain.User, sourceID string, input ReleaseCloneRequest) (ReleaseClonePlan, error) {
	source, err := p.store.GetComponentRelease(ctx, sourceID)
	if err != nil {
		return ReleaseClonePlan{}, err
	}
	component, err := p.store.GetComponent(ctx, source.ComponentID, false)
	if err != nil {
		return ReleaseClonePlan{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return ReleaseClonePlan{}, err
	}
	if input.Version == "" {
		return ReleaseClonePlan{}, fmt.Errorf("%w: target version is required", domain.ErrInvalid)
	}
	plan := ReleaseClonePlan{SourceReleaseID: sourceID, SourceVersion: source.Version, TargetVersion: input.Version, ArtifactCount: len(source.Artifacts)}
	executableDigests := map[string]string{}
	for _, action := range source.Actions {
		plan.Actions = append(plan.Actions, action.Kind)
		plan.Playbooks = append(plan.Playbooks, action.Playbook)
		if digester, ok := p.runner.(digestRunner); ok {
			playbookDigest, treeDigest, digestErr := digester.Digest(action.Playbook)
			if digestErr != nil {
				return ReleaseClonePlan{}, digestErr
			}
			executableDigests[action.Playbook] = playbookDigest + ":" + treeDigest
		}
	}
	plan.PlanDigest = digestValue(struct {
		SourceDigest      string
		ExecutableDigests map[string]string
		Input             ReleaseCloneRequest
	}{componentReleaseSpecDigest(source), executableDigests, ReleaseCloneRequest{Version: input.Version, ReleaseNotes: input.ReleaseNotes, Breaking: input.Breaking, RiskLevel: input.RiskLevel, EnvironmentConstraints: input.EnvironmentConstraints}})
	return plan, nil
}

type ScenarioCloneRequest struct {
	SourceRevisionID   string `json:"sourceRevisionId"`
	ExpectedPlanDigest string `json:"expectedPlanDigest,omitempty"`
}

type ScenarioClonePlan struct {
	ScenarioID       string `json:"scenarioId"`
	SourceRevisionID string `json:"sourceRevisionId"`
	SourceRevision   int    `json:"sourceRevision"`
	NextRevision     int    `json:"nextRevision"`
	NodeCount        int    `json:"nodeCount"`
	EdgeCount        int    `json:"edgeCount"`
	PlanDigest       string `json:"planDigest"`
}

func (p *Platform) PreviewScenarioClone(ctx context.Context, user domain.User, scenarioID string, input ScenarioCloneRequest) (ScenarioClonePlan, error) {
	scenario, err := p.store.GetScenario(ctx, scenarioID, true)
	if err != nil {
		return ScenarioClonePlan{}, err
	}
	if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
		return ScenarioClonePlan{}, err
	}
	var source domain.ScenarioRevision
	for _, revision := range scenario.Revisions {
		if revision.ID == input.SourceRevisionID {
			source = revision
			break
		}
	}
	if source.ID == "" {
		return ScenarioClonePlan{}, fmt.Errorf("%w: source revision does not belong to scenario", domain.ErrInvalid)
	}
	if source.Status != domain.RevisionReleased && source.Status != domain.RevisionDeprecated && source.Status != domain.RevisionTestPassed {
		return ScenarioClonePlan{}, fmt.Errorf("%w: source revision must be test passed, released, or deprecated", domain.ErrConflict)
	}
	for _, revision := range scenario.Revisions {
		if revision.ID == source.ID {
			continue
		}
		if revision.Status == domain.RevisionDraft || revision.Status == domain.RevisionTesting || revision.Status == domain.RevisionTestPassed {
			return ScenarioClonePlan{}, fmt.Errorf("%w: scenario already has an active draft revision", domain.ErrConflict)
		}
	}
	next, err := p.store.NextScenarioRevision(ctx, scenarioID)
	if err != nil {
		return ScenarioClonePlan{}, err
	}
	plan := ScenarioClonePlan{ScenarioID: scenarioID, SourceRevisionID: source.ID, SourceRevision: source.Revision, NextRevision: next, NodeCount: len(source.Graph.Nodes), EdgeCount: len(source.Graph.Edges)}
	plan.PlanDigest = digestValue(struct {
		ScenarioID   string
		Next         int
		SourceDigest string
	}{scenarioID, next, scenarioRevisionSpecDigest(source)})
	return plan, nil
}
