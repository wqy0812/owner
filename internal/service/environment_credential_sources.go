package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"fmt"
	"sort"
)

type CredentialDeclarationSource struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	ComponentID   string `json:"componentId,omitempty"`
	ComponentName string `json:"componentName,omitempty"`
	ReleaseID     string `json:"releaseId,omitempty"`
	Version       string `json:"version,omitempty"`
	LineName      string `json:"lineName,omitempty"`
	ActionID      string `json:"actionId,omitempty"`
	ActionName    string `json:"actionName,omitempty"`
	ScenarioID    string `json:"scenarioId,omitempty"`
	ScenarioName  string `json:"scenarioName,omitempty"`
	RevisionID    string `json:"revisionId,omitempty"`
	Revision      int    `json:"revision,omitempty"`
	Description   string `json:"description,omitempty"`
}

func configurationSourceVisible(viewer domain.User, component domain.Component, release domain.ComponentRelease) bool {
	return viewer.Role == domain.RolePlatformAdmin || (viewer.Role == domain.RoleComponentOwner && component.OwnerID == viewer.ID) || release.Status == domain.ReleaseReleased || release.IsApprovedCandidate()
}

// Declaration provenance is catalog metadata, not proof that an environment ran
// an action or that a referenced secret can be resolved.
func (p *EnvironmentService) CredentialSources(ctx context.Context, viewer domain.User, environmentID, revisionID string) ([]CredentialDeclarationSource, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return nil, err
	}
	if environment.ArchivedAt != nil && viewer.Role != domain.RolePlatformAdmin && !(viewer.Role == domain.RoleEnvironmentOwner && viewer.ID == environment.OwnerID) {
		return nil, domain.ErrForbidden
	}
	if revisionID != "" {
		revision, err := p.store.GetEnvironmentRevision(ctx, revisionID)
		if err != nil {
			return nil, err
		}
		if revision.EnvironmentID != environmentID {
			return nil, domain.ErrNotFound
		}
	}
	components, err := p.store.ListComponents(ctx, viewer)
	if err != nil {
		return nil, err
	}
	out := []CredentialDeclarationSource{
		{Name: "ssh_private_key", Kind: "platform_ssh", Description: "平台 SSH 连接与主机连通性检查"},
		{Name: "ssh_password", Kind: "platform_ssh", Description: "平台 SSH 密码认证与主机连通性检查"},
	}
	seen := map[string]bool{}
	for _, component := range components {
		for _, release := range component.Releases {
			if !configurationSourceVisible(viewer, component, release) || (release.Status == domain.ReleaseDeprecated && release.ReleasedAt == nil) {
				continue
			}
			for _, action := range release.Actions {
				for _, name := range action.RequiredCredentials {
					key := release.ID + ":" + action.ID + ":" + name
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, CredentialDeclarationSource{Name: name, Kind: "component_action", ComponentID: component.ID, ComponentName: component.Name, ReleaseID: release.ID, Version: release.Version, LineName: release.LineName, ActionID: action.ID, ActionName: action.Name})
				}
			}
		}
	}
	scenarios, err := p.store.ListScenarios(ctx, viewer)
	if err != nil {
		return nil, err
	}
	for _, scenario := range scenarios {
		for _, revision := range scenario.Revisions {
			if revision.AbandonedAt != nil {
				continue
			}
			for _, job := range revision.AcceptanceJobs {
				for _, name := range job.RequiredCredentials {
					key := fmt.Sprintf("%s:%s:%s", revision.ID, job.ID, name)
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, CredentialDeclarationSource{Name: name, Kind: "scenario_acceptance", ScenarioID: scenario.ID, ScenarioName: scenario.Name, RevisionID: revision.ID, Revision: revision.Revision, ActionID: job.ID, ActionName: job.Name})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Name+a.Kind+a.ComponentName+a.ReleaseID+a.RevisionID+a.ActionID < b.Name+b.Kind+b.ComponentName+b.ReleaseID+b.RevisionID+b.ActionID
	})
	return out, nil
}
