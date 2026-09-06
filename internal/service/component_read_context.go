package service

import (
	"context"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
)

type EvidenceSummary struct {
	ID              string           `json:"id"`
	Status          domain.RunStatus `json:"status"`
	EnvironmentID   string           `json:"environmentId"`
	EnvironmentName string           `json:"environmentName"`
	CreatedAt       time.Time        `json:"createdAt"`
	FinishedAt      *time.Time       `json:"finishedAt,omitempty"`
	MatchesContract bool             `json:"matchesContract"`
}
type ReleaseEvidenceSummary struct {
	CurrentInstall       *EvidenceSummary           `json:"currentInstall,omitempty"`
	CurrentRollback      *EvidenceSummary           `json:"currentRollback,omitempty"`
	CurrentTransition    *EvidenceSummary           `json:"currentTransition,omitempty"`
	HistoricalInstall    *EvidenceSummary           `json:"historicalInstall,omitempty"`
	HistoricalRollback   *EvidenceSummary           `json:"historicalRollback,omitempty"`
	HistoricalTransition *EvidenceSummary           `json:"historicalTransition,omitempty"`
	CurrentByID          map[string]EvidenceSummary `json:"currentById"`
}
type ParameterConsumer struct {
	ComponentName     string `json:"componentName"`
	Version           string `json:"version"`
	UpstreamParameter string `json:"upstreamParameter"`
	TargetParameter   string `json:"targetParameter"`
	Label             string `json:"label"`
}
type ComponentReadContext struct {
	Evidence           map[string]ReleaseEvidenceSummary `json:"evidence"`
	WorkItems          []domain.WorkItem                 `json:"workItems"`
	ParameterConsumers []ParameterConsumer               `json:"parameterConsumers"`
}

func (s *CatalogService) ReadContext(ctx context.Context, user domain.User, component domain.Component) (ComponentReadContext, error) {
	result := ComponentReadContext{Evidence: map[string]ReleaseEvidenceSummary{}, WorkItems: []domain.WorkItem{}, ParameterConsumers: []ParameterConsumer{}}
	runs, names, err := s.store.ComponentEvidenceReads(ctx, user, component.ID)
	if err != nil {
		return result, err
	}
	byID := map[string]domain.Run{}
	for _, run := range runs {
		byID[run.ID] = run
	}
	for _, release := range component.Releases {
		digest := componentReleaseSpecDigest(release)
		summary := func(id string) *EvidenceSummary {
			run, ok := byID[id]
			if !ok {
				return nil
			}
			return &EvidenceSummary{ID: run.ID, Status: run.Status, EnvironmentID: run.EnvironmentID, EnvironmentName: names[run.EnvironmentID], CreatedAt: run.CreatedAt, FinishedAt: run.FinishedAt, MatchesContract: snapshotString(run, "componentReleaseSpecDigest") == digest}
		}
		evidence := ReleaseEvidenceSummary{CurrentInstall: summary(release.Readiness.InstallEvidenceRunID), CurrentRollback: summary(release.Readiness.RollbackEvidenceRunID), CurrentTransition: summary(release.Readiness.TransitionEvidenceRunID), CurrentByID: map[string]EvidenceSummary{}}
		for _, id := range []string{release.Readiness.InstallEvidenceRunID, release.Readiness.RollbackEvidenceRunID, release.Readiness.TransitionEvidenceRunID} {
			if item := summary(id); item != nil {
				evidence.CurrentByID[id] = *item
			}
		}
		for _, run := range runs {
			if run.ComponentReleaseID != release.ID {
				continue
			}
			install, rollback := orderedEvidenceActions(run)
			if install && evidence.HistoricalInstall == nil {
				evidence.HistoricalInstall = summary(run.ID)
			}
			if rollback && evidence.HistoricalRollback == nil {
				evidence.HistoricalRollback = summary(run.ID)
			}
			if run.Action == domain.ActionUpgrade && evidence.HistoricalTransition == nil {
				evidence.HistoricalTransition = summary(run.ID)
			}
		}
		result.Evidence[release.ID] = evidence
	}
	if user.Role == domain.RoleComponentOwner && component.OwnerID == user.ID {
		result.WorkItems, _, err = componentOwnerWork(user, []domain.Component{component}, runs)
		if err != nil {
			return result, err
		}
	}
	if user.Role == domain.RolePlatformAdmin {
		result.WorkItems = platformAdminComponentWork([]domain.Component{component})
	}
	// Visibility includes digest-bound candidate checks; no Readiness recomputation
	// is needed to format incoming parameter references.
	visible, err := s.store.ListComponents(ctx, user)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, c := range visible {
		sort.SliceStable(c.Releases, func(i, j int) bool {
			return c.Releases[i].Status == domain.ReleaseReleased && c.Releases[j].Status != domain.ReleaseReleased
		})
		for _, release := range c.Releases {
			for _, dep := range release.Dependencies {
				if dep.UpstreamComponentID != component.ID {
					continue
				}
				for _, mapping := range dep.ParameterMappings {
					key := c.ID + ":" + mapping.UpstreamParameter + ":" + mapping.TargetParameter
					if seen[key] {
						continue
					}
					seen[key] = true
					result.ParameterConsumers = append(result.ParameterConsumers, ParameterConsumer{ComponentName: c.Name, Version: release.Version, UpstreamParameter: mapping.UpstreamParameter, TargetParameter: mapping.TargetParameter, Label: c.Name + " " + release.Version + " 的 " + mapping.TargetParameter + " 引用本组件公开参数 " + mapping.UpstreamParameter})
				}
			}
		}
	}
	return result, nil
}

func orderedEvidenceActions(run domain.Run) (bool, bool) {
	// Evidence is classified by the locked parent actions and explicit postchecks.
	plan, err := mapToPlan(run.InputSnapshot)
	if err != nil {
		return false, false
	}
	install, rollback := false, false
	for _, step := range plan.Steps {
		if step.Phase != "post" {
			continue
		}
		parent := parentExecutionStep(plan.ParentSteps, step)
		if parent == nil {
			continue
		}
		rollback = rollback || parent.Action == domain.ActionRollback
		install = install || parent.Action == domain.ActionInstall || parent.Action == domain.ActionUpgrade || parent.Action == domain.ActionConfigure
	}
	return install, rollback
}
