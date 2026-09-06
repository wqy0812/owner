package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"time"
)

// Test-only reference retained to verify read-model behavior and measure the old full aggregation.
func (p *ReadModelService) legacyWorkbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	if user.Role == domain.RolePlatformAdmin {
		return p.legacyPlatformAdminWorkbench(ctx, user)
	}
	components, err := p.catalog.ListComponents(ctx, user)
	if err != nil {
		return domain.Workbench{}, err
	}
	scenarios, err := p.scenarios.List(ctx, user)
	if err != nil {
		return domain.Workbench{}, err
	}
	environments, err := p.environments.List(ctx, user)
	if err != nil {
		return domain.Workbench{}, err
	}
	runs, err := p.store.ListRuns(ctx, user)
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

func (p *ReadModelService) legacyPlatformAdminWorkbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	components, err := p.store.ListComponents(ctx, user)
	if err != nil {
		return domain.Workbench{}, err
	}
	workbench := domain.Workbench{GeneratedAt: time.Now().UTC(), Role: user.Role, Items: []domain.WorkItem{}}
	workbench.Items = platformAdminComponentWork(components)
	workbench.Summary.ActionRequired = len(workbench.Items)
	return workbench, nil
}
