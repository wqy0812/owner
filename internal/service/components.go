package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) ListComponents(ctx context.Context, user domain.User) ([]domain.Component, error) {
	components, err := p.store.ListComponents(ctx, user)
	if err != nil {
		return nil, err
	}
	for i := range components {
		components[i], err = p.decorateComponentReadiness(ctx, components[i])
		if err != nil {
			return nil, err
		}
	}
	return components, nil
}

func (p *Platform) GetComponent(ctx context.Context, user domain.User, id string) (domain.Component, error) {
	component, err := p.store.GetComponent(ctx, id, true)
	if err != nil {
		return component, err
	}
	if user.Role == domain.RoleComponentOwner && user.ID == component.OwnerID {
		return p.decorateComponentReadiness(ctx, component)
	}
	filtered := make([]domain.ComponentRelease, 0, len(component.Releases))
	for _, release := range component.Releases {
		if release.Status == domain.ReleaseReleased || (release.Status == domain.ReleaseDraft && release.Candidate) {
			filtered = append(filtered, release)
		}
	}
	component.Releases = filtered
	if len(filtered) == 0 {
		return domain.Component{}, domain.ErrNotFound
	}
	return p.decorateComponentReadiness(ctx, component)
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
		"slug": component.Slug, "layer": component.Layer, "tags": component.Tags,
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
	component.Tags = append([]string(nil), patch.Tags...)
	component.UpdatedAt = time.Now().UTC()
	if err := p.store.UpdateComponent(ctx, component); err != nil {
		return component, err
	}
	p.audit(ctx, user, "component.updated", "component", component.ID, map[string]any{
		"slug": component.Slug, "layer": component.Layer, "tags": component.Tags,
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
	if release.RiskLevel == "" {
		release.RiskLevel = domain.RiskLow
	}
	release.CreatedAt = time.Now().UTC()
	p.populatePlaybookDigests(component, &release, nil)
	rewriteReleaseChildren(&release)
	if err := p.validateReleaseContract(ctx, release, false); err != nil {
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

func (p *Platform) populatePlaybookDigests(component domain.Component, release *domain.ComponentRelease, previous []domain.ActionDefinition) {
	known := make(map[string]string, len(previous))
	for _, action := range previous {
		if action.PlaybookSHA256 != "" {
			known[action.Playbook] = action.PlaybookSHA256
		}
	}
	for index := range release.Actions {
		action := &release.Actions[index]
		if action.PlaybookSHA256 = known[action.Playbook]; action.PlaybookSHA256 != "" {
			continue
		}
		_, resolved, err := p.resolveManagedPlaybookPath(component, *release, action.Playbook, false)
		if err != nil {
			continue
		}
		contents, err := os.ReadFile(resolved)
		if err != nil {
			continue
		}
		digest := sha256.Sum256(contents)
		action.PlaybookSHA256 = hex.EncodeToString(digest[:])
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
		base := fmt.Errorf("%w: released versions are immutable; clone a new version", domain.ErrConflict)
		return release, actionableExistingError(base, "resource.immutable", "Released Release 不允许原地修改", "创建新 Draft", "/components?selected="+component.ID+"&release="+release.ID+"&action=contract")
	}
	if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
		return release, activeErr
	} else if active {
		base := fmt.Errorf("%w: wait for the active component test before editing this draft", domain.ErrConflict)
		return release, actionableExistingError(base, "release.validation_in_progress", "活动测试 Run 已锁定当前 Draft 定义", "查看运行", "/runs")
	}
	patch.ID, patch.ComponentID, patch.Status, patch.CreatedAt = release.ID, release.ComponentID, release.Status, release.CreatedAt
	// Evidence is derived from the current spec digest. Definition changes make
	// old runs inapplicable without mutating the owner's candidate intent.
	patch.Candidate = release.Candidate
	if patch.RiskLevel == "" {
		patch.RiskLevel = release.RiskLevel
	}
	p.populatePlaybookDigests(component, &patch, release.Actions)
	rewriteReleaseChildren(&patch)
	if err := p.validateReleaseContract(ctx, patch, false); err != nil {
		return release, err
	}
	if err := p.store.UpdateDraftRelease(ctx, patch); err != nil {
		return release, err
	}
	p.audit(ctx, user, "component_release.updated", "component_release", id, map[string]any{"version": patch.Version})
	return p.store.GetComponentRelease(ctx, id)
}

// UpdateReleaseContract replaces only a Draft release's dependency and
// parameter contract. The remaining release definition is loaded from the
// store so a focused UI edit cannot accidentally erase action metadata.
func (p *Platform) UpdateReleaseContract(ctx context.Context, user domain.User, id string, parameters []domain.ParameterDefinition, dependencies []domain.ComponentDependency) (domain.ComponentRelease, error) {
	release, err := p.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, err
	}
	release.Parameters = parameters
	release.Dependencies = dependencies
	return p.UpdateRelease(ctx, user, id, release)
}

func (p *Platform) CloneRelease(ctx context.Context, user domain.User, sourceID string, input ReleaseCloneRequest) (domain.ComponentRelease, error) {
	plan, err := p.PreviewReleaseClone(ctx, user, sourceID, input)
	if err != nil {
		return domain.ComponentRelease{}, err
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != plan.PlanDigest {
		return domain.ComponentRelease{}, fmt.Errorf("%w: release clone plan changed; preview again", domain.ErrConflict)
	}
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
	newReleaseID := newID("release")
	source.ID = newReleaseID
	source.Version = input.Version
	source.ReleaseNotes = input.ReleaseNotes
	source.Breaking = input.Breaking
	if input.EnvironmentConstraints != nil {
		source.EnvironmentConstraints = input.EnvironmentConstraints
	}
	source.Status = domain.ReleaseDraft
	source.Candidate = false
	source.CreatedAt = time.Now().UTC()
	source.ReleasedAt, source.DeprecatedAt = nil, nil
	for index := range source.Artifacts {
		source.Artifacts[index].ID = newID("artifact")
		source.Artifacts[index].ReleaseID = newReleaseID
		source.Artifacts[index].CreatedAt = source.CreatedAt
		source.Artifacts[index].CreatedBy = user.ID
		source.Artifacts[index].SourceUpdatedAt = source.CreatedAt
		source.Artifacts[index].SourceUpdatedBy = user.ID
	}
	for index := range source.Images {
		source.Images[index].ID = newID("image")
		source.Images[index].ReleaseID = newReleaseID
		source.Images[index].CreatedAt = source.CreatedAt
		source.Images[index].CreatedBy = user.ID
		source.Images[index].SourceUpdatedAt = source.CreatedAt
		source.Images[index].SourceUpdatedBy = user.ID
	}
	for index := range source.Actions {
		switch source.Actions[index].Kind {
		case domain.ActionUpgrade:
			source.Actions[index].FromReleaseID, source.Actions[index].ToReleaseID = sourceID, newReleaseID
		case domain.ActionRollback:
			if source.Actions[index].FromReleaseID != "" || source.Actions[index].ToReleaseID != "" {
				source.Actions[index].FromReleaseID, source.Actions[index].ToReleaseID = newReleaseID, sourceID
			}
		}
	}
	manifest, err := p.copyManagedPlaybooksForClone(component, sourceID, &source)
	if err != nil {
		return source, err
	}
	cleanup := func(deleteFinal bool) { _ = p.cleanupComponentImportManifest(manifest, deleteFinal) }
	rewriteReleaseChildren(&source)
	if err := p.validateReleaseContract(ctx, source, false); err != nil {
		cleanup(true)
		return source, err
	}
	audit := newAuditEvent(user, "component_release.cloned", "component_release", source.ID, map[string]any{"sourceReleaseId": sourceID, "version": input.Version, "planDigest": plan.PlanDigest})
	if err := p.store.CreateClonedComponentRelease(ctx, source, audit); err != nil {
		cleanup(true)
		return source, err
	}
	cleanup(false)
	return source, nil
}

func (p *Platform) SetReleaseCandidate(ctx context.Context, user domain.User, id string, candidate bool) (domain.ComponentRelease, error) {
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
	if candidate {
		if err := p.validateReleaseForCandidate(ctx, release); err != nil {
			return release, err
		}
	}
	if err := p.store.SetReleaseCandidate(ctx, id, candidate); err != nil {
		return release, err
	}
	p.audit(ctx, user, "component_release.candidate_updated", "component_release", id, map[string]any{"candidate": candidate})
	return p.store.GetComponentRelease(ctx, id)
}

func (p *Platform) validateReleaseForCandidate(ctx context.Context, release domain.ComponentRelease) error {
	readiness, err := p.releaseReadiness(ctx, release)
	if err != nil {
		return err
	}
	return readinessError(readiness)
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
	targetIncluded := false
	for _, item := range components {
		for _, candidate := range item.Releases {
			if candidate.Status == domain.ReleaseReleased {
				releases = append(releases, candidate)
				targetIncluded = targetIncluded || candidate.ID == release.ID
			}
		}
	}
	// A shared Draft may already be locked by mutable scenario revisions. Keep
	// it in the lookup used by impact analysis so deprecation previews include
	// those exact references even though the normal dependency graph is based on
	// immutable releases.
	if !targetIncluded {
		releases = append(releases, release)
	}
	scenarios, err := p.store.ListScenariosForImpact(ctx)
	if err != nil {
		return domain.ImpactReport{}, err
	}
	report := BuildImpactReport(release.ComponentID, components, releases, scenarios)
	report.ScenarioRunCount, err = p.store.CountScenarioRunsForComponentRelease(ctx, release.ID)
	if err != nil {
		return domain.ImpactReport{}, err
	}
	return report, nil
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
	if release.Status != domain.ReleaseDraft && release.Status != domain.ReleaseReleased {
		return release, fmt.Errorf("%w: only draft or released versions can be deprecated", domain.ErrConflict)
	}
	scenarioRunCount, err := p.store.CountScenarioRunsForComponentRelease(ctx, release.ID)
	if err != nil {
		return release, err
	}
	if scenarioRunCount > 0 {
		base := fmt.Errorf("%w: component release is retained by %d scenario run(s)", domain.ErrConflict, scenarioRunCount)
		return release, actionableExistingError(base, "release.scenario_run_history", "该组件版本已被场景引用并运行，不能废弃", "查看运行记录", "/runs")
	}
	if release.Status == domain.ReleaseDraft {
		if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
			return release, activeErr
		} else if active {
			return release, fmt.Errorf("%w: wait for the active component test before deprecating this draft", domain.ErrConflict)
		}
	}
	previousStatus := release.Status
	now := time.Now().UTC()
	if err := p.store.DeprecateComponentRelease(ctx, id, now); err != nil {
		return release, err
	}
	release.Status, release.Candidate, release.DeprecatedAt = domain.ReleaseDeprecated, false, &now
	p.audit(ctx, user, "component_release.deprecated", "component_release", id, map[string]any{"componentId": component.ID, "version": release.Version, "previousStatus": previousStatus})
	return release, nil
}

func (p *Platform) validateReleaseContract(ctx context.Context, release domain.ComponentRelease, publishing bool) error {
	if err := validateRelease(release); err != nil {
		return err
	}
	if err := p.validateReleaseMappings(ctx, release); err != nil {
		return err
	}
	if publishing {
		return p.validateUpgradeRollbackMappingContracts(ctx, release)
	}
	return nil
}

func (p *Platform) validateReleaseMappings(ctx context.Context, release domain.ComponentRelease) error {
	var downstreamOwnerID string
	for _, dependency := range release.Dependencies {
		upstream, err := p.store.GetComponentRelease(ctx, dependency.UpstreamReleaseID)
		if err != nil {
			return fmt.Errorf("%w: locked upstream release %s does not exist", domain.ErrInvalid, dependency.UpstreamReleaseID)
		}
		if upstream.ComponentID != dependency.UpstreamComponentID {
			return fmt.Errorf("%w: upstream dependency release does not belong to component %s", domain.ErrInvalid, dependency.UpstreamComponentID)
		}
		if upstream.Status != domain.ReleaseReleased {
			sharedCandidate := upstream.Status == domain.ReleaseDraft && upstream.Candidate
			sameOwnerDraft := false
			if upstream.Status == domain.ReleaseDraft {
				if downstreamOwnerID == "" {
					downstreamComponent, componentErr := p.store.GetComponent(ctx, release.ComponentID, false)
					if componentErr != nil {
						return componentErr
					}
					downstreamOwnerID = downstreamComponent.OwnerID
				}
				upstreamComponent, componentErr := p.store.GetComponent(ctx, upstream.ComponentID, false)
				if componentErr != nil {
					return componentErr
				}
				sameOwnerDraft = upstreamComponent.OwnerID == downstreamOwnerID
			}
			if !sharedCandidate && !sameOwnerDraft {
				return fmt.Errorf("%w: upstream dependency must lock a released version, a shared candidate, or a private Draft owned by the same component owner", domain.ErrInvalid)
			}
		}
		for _, mapping := range dependency.ParameterMappings {
			upstreamParameter, ok := domain.ParameterByName(upstream.Parameters, mapping.UpstreamParameter)
			if !ok || upstreamParameter.Visibility != domain.ParameterPublic {
				return fmt.Errorf("%w: mapping source %q must be a public parameter of %s", domain.ErrInvalid, mapping.UpstreamParameter, dependency.UpstreamReleaseID)
			}
			target, _ := domain.ParameterByName(release.Parameters, mapping.TargetParameter)
			if upstreamParameter.Type != target.Type {
				return fmt.Errorf("%w: mapped parameter %q type %s does not match upstream %s", domain.ErrInvalid, mapping.TargetParameter, target.Type, upstreamParameter.Type)
			}
		}
	}
	return nil
}

func (p *Platform) validateUpgradeRollbackMappingContracts(ctx context.Context, release domain.ComponentRelease) error {
	for _, action := range release.Actions {
		var peer domain.ComponentRelease
		var err error
		switch action.Kind {
		case domain.ActionUpgrade:
			peer, err = p.store.GetComponentRelease(ctx, action.FromReleaseID)
		case domain.ActionRollback:
			if action.FromReleaseID == "" && action.ToReleaseID == "" {
				continue
			}
			peer, err = p.store.GetComponentRelease(ctx, action.ToReleaseID)
		default:
			continue
		}
		if err != nil {
			return err
		}
		if !mappingContractsEqual(release.Dependencies, peer.Dependencies) {
			return fmt.Errorf("%w: %s action requires an identical parameter mapping contract", domain.ErrInvalid, action.Kind)
		}
	}
	return nil
}

func mappingContractsEqual(left, right []domain.ComponentDependency) bool {
	return strings.Join(domain.MappingContract(left), "\n") == strings.Join(domain.MappingContract(right), "\n")
}

func (p *Platform) validateReleaseForPublish(ctx context.Context, release domain.ComponentRelease) error {
	if err := p.validateReleaseContract(ctx, release, true); err != nil {
		return err
	}
	if err := p.validateReleaseTransitionContracts(ctx, release); err != nil {
		return err
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
	return nil
}

func (p *Platform) validateReleaseEvidence(ctx context.Context, release domain.ComponentRelease) error {
	readiness, err := p.releaseReadiness(ctx, release)
	if err != nil {
		return err
	}
	return readinessError(readiness)
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
