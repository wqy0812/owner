package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) ListComponents(ctx context.Context, user domain.User) ([]domain.Component, error) {
	return p.store.ListComponents(ctx, user)
}

func (p *Platform) GetComponent(ctx context.Context, user domain.User, id string) (domain.Component, error) {
	component, err := p.store.GetComponent(ctx, id, true)
	if err != nil {
		return component, err
	}
	if user.Role == domain.RoleComponentOwner && user.ID == component.OwnerID {
		return component, nil
	}
	filtered := component.Releases[:0]
	for _, release := range component.Releases {
		if release.Status == domain.ReleaseReleased {
			filtered = append(filtered, release)
		}
	}
	component.Releases = filtered
	if len(filtered) == 0 {
		return domain.Component{}, domain.ErrNotFound
	}
	return component, nil
}

func (p *Platform) CreateComponent(ctx context.Context, user domain.User, component domain.Component) (domain.Component, error) {
	if err := domain.ValidateRole(user, domain.RoleComponentOwner); err != nil {
		return component, err
	}
	if err := validateSlug(component.Slug); err != nil {
		return component, err
	}
	if component.Name == "" {
		return component, fmt.Errorf("%w: component name is required", domain.ErrInvalid)
	}
	if err := domain.ValidateComponentClassification(component); err != nil {
		return component, err
	}
	now := time.Now().UTC()
	component.ID = newID("component")
	component.OwnerID = user.ID
	component.CreatedAt, component.UpdatedAt = now, now
	if err := p.store.CreateComponent(ctx, component); err != nil {
		return component, err
	}
	p.audit(ctx, user, "component.created", "component", component.ID, map[string]any{
		"slug": component.Slug, "layer": component.Layer, "category": component.Category,
		"kind": component.Kind, "requiredness": component.Requiredness,
	})
	return component, nil
}

func (p *Platform) UpdateComponent(ctx context.Context, user domain.User, id string, patch domain.Component) (domain.Component, error) {
	component, err := p.store.GetComponent(ctx, id, true)
	if err != nil {
		return component, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return component, err
	}
	if err := domain.ValidateComponentClassification(patch); err != nil {
		return component, err
	}
	if patch.Name != "" {
		component.Name = patch.Name
	}
	if patch.Slug != "" {
		if err := validateSlug(patch.Slug); err != nil {
			return component, err
		}
		component.Slug = patch.Slug
	}
	component.Description = patch.Description
	component.Layer = patch.Layer
	component.Category = patch.Category
	component.Kind = patch.Kind
	component.Requiredness = patch.Requiredness
	component.UpdatedAt = time.Now().UTC()
	if err := p.store.UpdateComponent(ctx, component); err != nil {
		return component, err
	}
	p.audit(ctx, user, "component.updated", "component", component.ID, map[string]any{
		"slug": component.Slug, "layer": component.Layer, "category": component.Category,
		"kind": component.Kind, "requiredness": component.Requiredness,
	})
	return component, nil
}

func (p *Platform) CreateRelease(ctx context.Context, user domain.User, componentID string, release domain.ComponentRelease) (domain.ComponentRelease, error) {
	component, err := p.store.GetComponent(ctx, componentID, false)
	if err != nil {
		return release, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, err
	}
	release.ID = newID("release")
	release.ComponentID = componentID
	release.Status = domain.ReleaseDraft
	release.Verified = false
	if release.Type == "" {
		release.Type = domain.ReleaseAtomic
	}
	if release.RiskLevel == "" {
		release.RiskLevel = domain.RiskLow
	}
	release.CreatedAt = time.Now().UTC()
	rewriteReleaseChildren(&release)
	if err := validateRelease(release); err != nil {
		return release, err
	}
	if err := p.store.CreateComponentRelease(ctx, release); err != nil {
		return release, err
	}
	p.audit(ctx, user, "component_release.created", "component_release", release.ID, map[string]any{"version": release.Version})
	return release, nil
}

func rewriteReleaseChildren(release *domain.ComponentRelease) {
	for i := range release.Dependencies {
		release.Dependencies[i].ID = newID("dependency")
		release.Dependencies[i].ReleaseID = release.ID
	}
	for i := range release.Actions {
		release.Actions[i].ID = newID("action")
		release.Actions[i].ReleaseID = release.ID
		if release.Actions[i].TimeoutSeconds <= 0 {
			release.Actions[i].TimeoutSeconds = 1800
		}
		if release.Actions[i].RiskLevel == "" {
			release.Actions[i].RiskLevel = domain.RiskLow
		}
	}
}

func (p *Platform) UpdateRelease(ctx context.Context, user domain.User, id string, patch domain.ComponentRelease) (domain.ComponentRelease, error) {
	release, err := p.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, err
	}
	if release.Status != domain.ReleaseDraft {
		return release, fmt.Errorf("%w: released versions are immutable; clone a new version", domain.ErrConflict)
	}
	if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
		return release, activeErr
	} else if active {
		return release, fmt.Errorf("%w: wait for the active component test before editing this draft", domain.ErrConflict)
	}
	patch.ID, patch.ComponentID, patch.Status, patch.CreatedAt = release.ID, release.ComponentID, release.Status, release.CreatedAt
	// Any change to actions, dependencies, constraints or parameters invalidates
	// the evidence produced by an earlier component test.
	patch.Verified = false
	if patch.Type == "" {
		patch.Type = release.Type
	}
	if patch.RiskLevel == "" {
		patch.RiskLevel = release.RiskLevel
	}
	rewriteReleaseChildren(&patch)
	if err := validateRelease(patch); err != nil {
		return release, err
	}
	if err := p.store.UpdateDraftRelease(ctx, patch); err != nil {
		return release, err
	}
	p.audit(ctx, user, "component_release.updated", "component_release", id, map[string]any{"version": patch.Version})
	return p.store.GetComponentRelease(ctx, id)
}

func (p *Platform) CloneRelease(ctx context.Context, user domain.User, sourceID, version, notes string, breaking bool) (domain.ComponentRelease, error) {
	source, err := p.store.GetComponentRelease(ctx, sourceID)
	if err != nil {
		return source, err
	}
	component, err := p.store.GetComponent(ctx, source.ComponentID, false)
	if err != nil {
		return source, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return source, err
	}
	source.ID = newID("release")
	source.Version = version
	source.ReleaseNotes = notes
	source.Breaking = breaking
	source.Status = domain.ReleaseDraft
	source.Verified = false
	source.CreatedAt = time.Now().UTC()
	source.ReleasedAt, source.DeprecatedAt = nil, nil
	rewriteReleaseChildren(&source)
	if err := validateRelease(source); err != nil {
		return source, err
	}
	if err := p.store.CreateComponentRelease(ctx, source); err != nil {
		return source, err
	}
	p.audit(ctx, user, "component_release.cloned", "component_release", source.ID, map[string]any{"sourceReleaseId": sourceID, "version": version})
	return source, nil
}

func (p *Platform) Impact(ctx context.Context, user domain.User, releaseID string) (domain.ImpactReport, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return domain.ImpactReport{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return domain.ImpactReport{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return domain.ImpactReport{}, err
	}
	components, err := p.store.ListComponents(ctx, user)
	if err != nil {
		return domain.ImpactReport{}, err
	}
	var releases []domain.ComponentRelease
	for _, item := range components {
		for _, candidate := range item.Releases {
			if candidate.Status == domain.ReleaseReleased {
				releases = append(releases, candidate)
			}
		}
	}
	scenarios, err := p.store.ListScenariosForImpact(ctx)
	if err != nil {
		return domain.ImpactReport{}, err
	}
	return BuildImpactReport(release.ComponentID, components, releases, scenarios), nil
}

func (p *Platform) PublishRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, domain.ImpactReport, error) {
	release, err := p.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, domain.ImpactReport{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, domain.ImpactReport{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, domain.ImpactReport{}, err
	}
	if release.Status != domain.ReleaseDraft {
		return release, domain.ImpactReport{}, fmt.Errorf("%w: only a draft can be published", domain.ErrConflict)
	}
	if err := p.validateReleaseForPublish(ctx, release); err != nil {
		return release, domain.ImpactReport{}, err
	}
	oldVersion, oldErr := p.store.LatestReleasedVersion(ctx, release.ComponentID)
	if oldErr != nil && !errors.Is(oldErr, domain.ErrNotFound) {
		return release, domain.ImpactReport{}, oldErr
	}
	report, err := p.Impact(ctx, user, id)
	if err != nil {
		return release, report, err
	}
	now := time.Now().UTC()
	if err := p.store.PublishComponentRelease(ctx, id, now); err != nil {
		return release, report, err
	}
	release.Status, release.ReleasedAt = domain.ReleaseReleased, &now
	notifications := make([]domain.Notification, 0, len(report.Recipients))
	for _, recipient := range report.Recipients {
		pathNames := make([][]string, 0, len(recipient.Paths))
		for _, path := range recipient.Paths {
			pathNames = append(pathNames, path.ComponentNames)
		}
		notifications = append(notifications, domain.Notification{
			ID: newID("notification"), UserID: recipient.UserID, Type: "component_release_impact",
			Title:       fmt.Sprintf("%s 发布 %s", component.Name, release.Version),
			Body:        fmt.Sprintf("上游组件从 %s 更新为 %s；请评估锁定版本和兼容性。", valueOr(oldVersion, "首次发布"), release.Version),
			ResourceURL: "/components?selected=" + component.ID,
			Payload: map[string]any{
				"componentId": component.ID, "componentName": component.Name,
				"oldVersion": oldVersion, "newVersion": release.Version,
				"releaseNotes": release.ReleaseNotes, "breaking": release.Breaking,
				"impactPaths": pathNames, "scenarioIds": recipient.ScenarioIDs,
			}, CreatedAt: now,
		})
	}
	if err := p.store.CreateNotifications(ctx, notifications); err != nil {
		return release, report, err
	}
	p.audit(ctx, user, "component_release.published", "component_release", id, map[string]any{
		"componentId": component.ID, "oldVersion": oldVersion, "newVersion": release.Version,
		"breaking": release.Breaking, "verified": release.Verified, "recipientCount": len(notifications),
	})
	p.hub.Publish("release.published", map[string]any{"releaseId": id, "componentId": component.ID})
	if len(notifications) > 0 {
		p.hub.Publish("notification", map[string]any{"releaseId": id, "count": len(notifications)})
	}
	return release, report, nil
}

func (p *Platform) DeprecateRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
	release, err := p.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, err
	}
	now := time.Now().UTC()
	if err := p.store.DeprecateComponentRelease(ctx, id, now); err != nil {
		return release, err
	}
	release.Status, release.DeprecatedAt = domain.ReleaseDeprecated, &now
	p.audit(ctx, user, "component_release.deprecated", "component_release", id, map[string]any{"componentId": component.ID, "version": release.Version})
	return release, nil
}

func (p *Platform) validateReleaseForPublish(ctx context.Context, release domain.ComponentRelease) error {
	if err := validateRelease(release); err != nil {
		return err
	}
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
			if action.FromReleaseID != release.ID || action.ToReleaseID == release.ID {
				return fmt.Errorf("%w: rollback action must point from this release to an earlier release", domain.ErrInvalid)
			}
			to, err := p.store.GetComponentRelease(ctx, action.ToReleaseID)
			if err != nil || to.ComponentID != release.ComponentID || to.Status != domain.ReleaseReleased {
				return fmt.Errorf("%w: rollback toReleaseId must lock a released version of the same component", domain.ErrInvalid)
			}
		}
	}
	links, err := p.store.ListReleasedDependencyLinks(ctx)
	if err != nil {
		return err
	}
	forward := map[string][]string{}
	for _, link := range links {
		forward[link.DownstreamComponentID] = appendUnique(forward[link.DownstreamComponentID], link.UpstreamComponentID)
	}
	for _, dependency := range release.Dependencies {
		upstream, getErr := p.store.GetComponentRelease(ctx, dependency.UpstreamReleaseID)
		if getErr != nil {
			return fmt.Errorf("%w: locked upstream release %s does not exist", domain.ErrInvalid, dependency.UpstreamReleaseID)
		}
		if upstream.ComponentID != dependency.UpstreamComponentID || upstream.Status != domain.ReleaseReleased {
			return fmt.Errorf("%w: upstream dependency must lock a released version of component %s", domain.ErrInvalid, dependency.UpstreamComponentID)
		}
		if dependencyPathExists(forward, dependency.UpstreamComponentID, release.ComponentID) {
			return fmt.Errorf("%w: dependency would create a component cycle", domain.ErrInvalid)
		}
	}
	if digester, ok := p.runner.(digestRunner); ok {
		for _, action := range release.Actions {
			if _, _, digestErr := digester.Digest(action.Playbook); digestErr != nil {
				return fmt.Errorf("%w: action %s playbook is not executable: %v", domain.ErrInvalid, action.Name, digestErr)
			}
		}
	}
	return nil
}

func dependencyPathExists(graph map[string][]string, start, target string) bool {
	queue := []string{start}
	seen := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, graph[current]...)
	}
	return false
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func sortImpactReport(report *domain.ImpactReport) {
	sort.Slice(report.Recipients, func(i, j int) bool { return report.Recipients[i].UserID < report.Recipients[j].UserID })
}
