package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func digestValue(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

type ScenarioCloneRequest struct {
	SourceRevisionID   string `json:"sourceRevisionId"`
	SourceRunID        string `json:"sourceRunId,omitempty"`
	ExpectedPlanDigest string `json:"expectedPlanDigest,omitempty"`
}

type ScenarioClonePlan struct {
	ScenarioID       string `json:"scenarioId"`
	SourceRevisionID string `json:"sourceRevisionId"`
	SourceRunID      string `json:"sourceRunId"`
	SourceRevision   int    `json:"sourceRevision"`
	NextRevision     int    `json:"nextRevision"`
	NodeCount        int    `json:"nodeCount"`
	EdgeCount        int    `json:"edgeCount"`
	PlanDigest       string `json:"planDigest"`
}

func (p *ScenarioService) PreviewClone(ctx context.Context, user domain.User, scenarioID string, input ScenarioCloneRequest) (ScenarioClonePlan, error) {
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
	sourceRun, err := p.store.SuccessfulScenarioSourceRun(ctx, source, input.SourceRunID)
	if err != nil {
		return ScenarioClonePlan{}, err
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
	plan := ScenarioClonePlan{ScenarioID: scenarioID, SourceRevisionID: source.ID, SourceRunID: sourceRun.ID, SourceRevision: source.Revision, NextRevision: next, NodeCount: len(source.Graph.Nodes), EdgeCount: len(source.Graph.Edges)}
	plan.PlanDigest = digestValue(struct {
		ScenarioID   string
		Next         int
		SourceDigest string
		SourceRunID  string
	}{scenarioID, next, scenarioRevisionSpecDigest(source), sourceRun.ID})
	return plan, nil
}
