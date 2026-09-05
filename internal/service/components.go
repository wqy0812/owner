package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (p *Platform) ListComponents(ctx context.Context, user domain.User) ([]domain.Component, error) {
	components, err := p.store.ListComponents(ctx, user)
	if err != nil {
		return nil, err
	}
	evaluation := newReadinessEvaluation(p)
	evaluation.preparePlaybooks(components)
	for i := range components {
		components[i], err = evaluation.decorateComponent(ctx, components[i])
		if err != nil {
			return nil, err
		}
		// Catalog lists expose workspace identity and size, not an unbounded file
		// manifest. The dedicated workspace endpoint remains the source for files.
		for releaseIndex := range components[i].Releases {
			release := &components[i].Releases[releaseIndex]
			release.PlaybookFileCount = len(release.PlaybookFiles)
			release.PlaybookFiles = nil
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
	if user.Role == domain.RolePlatformAdmin {
		return p.decorateComponentReadiness(ctx, component)
	}
	filtered := make([]domain.ComponentRelease, 0, len(component.Releases))
	for _, release := range component.Releases {
		if release.Status == domain.ReleaseReleased || release.IsApprovedCandidate() {
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
		if patch.Slug != component.Slug && len(component.Releases) > 0 {
			return component, fmt.Errorf("%w: component slug is frozen after its first Release is created", domain.ErrConflict)
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

func rewriteReleaseChildren(release *domain.ComponentRelease) {
	for i := range release.Dependencies {
		release.Dependencies[i].ID = newID("dependency")
		release.Dependencies[i].ReleaseID = release.ID
	}
	for i := range release.Actions {
		if release.Actions[i].ID == "" {
			release.Actions[i].ID = newID("action")
		}
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
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
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
	patch.LineID, patch.LineName = release.LineID, release.LineName
	patch.ParentReleaseID, patch.TemplateSourceReleaseID = release.ParentReleaseID, release.TemplateSourceReleaseID
	if patch.Compatibility == "" {
		patch.Compatibility = release.Compatibility
	}
	// Evidence is derived from the current spec digest. Definition changes make
	// old runs inapplicable without mutating the owner's candidate intent.
	patch.Candidate = release.Candidate
	if patch.RiskLevel == "" {
		patch.RiskLevel = release.RiskLevel
	}
	patch.PlaybookWorkspaceRoot = release.PlaybookWorkspaceRoot
	if patch.Version != release.Version || patch.LineName != release.LineName {
		patch.PlaybookWorkspaceRoot = generatedManagedReleasePrefix(component, patch)
	}
	existingActions := make(map[string]domain.ActionKind, len(release.Actions))
	for _, action := range release.Actions {
		existingActions[action.ID] = action.Kind
	}
	for index := range patch.Actions {
		if patch.Actions[index].ID == "" {
			return release, fmt.Errorf("%w: save new actions with their Playbook before saving the Draft", domain.ErrConflict)
		}
		kind, exists := existingActions[patch.Actions[index].ID]
		if !exists {
			return release, fmt.Errorf("%w: action %s no longer exists", domain.ErrConflict, patch.Actions[index].ID)
		}
		if kind != patch.Actions[index].Kind {
			return release, fmt.Errorf("%w: saved action kind is immutable; delete it and create a new action", domain.ErrConflict)
		}
	}
	if len(patch.Actions) != len(release.Actions) {
		return release, fmt.Errorf("%w: save or delete actions through the atomic Action operation before saving the Draft", domain.ErrConflict)
	}
	// Action metadata is owned by the atomic Action endpoint. A general Draft
	// save must not replay a stale browser copy over a newer Action mutation.
	patch.Actions = append([]domain.ActionDefinition(nil), release.Actions...)
	for index := range patch.Actions {
		path, err := actionPlaybookPath(component, patch, patch.Actions[index].Kind)
		if err != nil {
			return release, err
		}
		patch.Actions[index].Playbook = path
	}
	patch.PlaybookFiles, patch.PlaybookTreeSHA256 = release.PlaybookFiles, release.PlaybookTreeSHA256
	undoWorkspaceMove, err := p.moveDraftWorkspace(component, release, patch)
	if err != nil {
		return release, err
	}
	committed := false
	defer func() {
		if !committed {
			undoWorkspaceMove()
		}
	}()
	rewriteReleaseChildren(&patch)
	if err := p.validateReleaseContract(ctx, patch, false); err != nil {
		return release, err
	}
	if err := p.validateEnvironmentConstraintRetiredReferences(ctx, patch.EnvironmentConstraints, release.EnvironmentConstraints); err != nil {
		return release, err
	}
	if err := p.store.UpdateDraftRelease(ctx, patch); err != nil {
		return release, err
	}
	committed = true
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
		if release.Review.Status != domain.ReleaseReviewApproved || release.Review.ContractDigest != componentReleaseSpecDigest(release) {
			return release, fmt.Errorf("%w: component release contract must be approved before candidate sharing", domain.ErrConflict)
		}
		if err := p.validateReleaseForCandidate(ctx, release); err != nil {
			return release, err
		}
	}
	if err := p.store.SetReleaseCandidate(ctx, id, candidate, release.PublicationGeneration, componentReleaseSpecDigest(release)); err != nil {
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
	return p.releaseImpact(ctx, user, releaseID, false)
}

func (p *Platform) PublicationImpact(ctx context.Context, user domain.User, releaseID string) (domain.ImpactReport, error) {
	return p.releaseImpact(ctx, user, releaseID, true)
}

func (p *Platform) releaseImpact(ctx context.Context, user domain.User, releaseID string, publication bool) (domain.ImpactReport, error) {
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
	if publication && release.ParentReleaseID == "" {
		return domain.ImpactReport{ComponentID: release.ComponentID, ChangeKind: "new_line", LineID: release.LineID, LineName: release.LineName, ToReleaseID: release.ID, Recipients: []domain.ImpactRecipient{}}, nil
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
	startReleaseID, changeKind := release.ID, "deprecation"
	if publication {
		startReleaseID, changeKind = release.ParentReleaseID, "evolution"
	}
	report := BuildReleaseImpactReport(startReleaseID, components, releases, scenarios)
	report.ComponentID, report.ChangeKind = release.ComponentID, changeKind
	report.LineID, report.LineName = release.LineID, release.LineName
	report.FromReleaseID, report.ToReleaseID = release.ParentReleaseID, release.ID
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
	if release.Status == domain.ReleaseReleased {
		scenarioRunCount, err := p.store.CountScenarioRunsForComponentRelease(ctx, release.ID)
		if err != nil {
			return release, err
		}
		if scenarioRunCount > 0 {
			base := fmt.Errorf("%w: component release is retained by %d scenario run(s)", domain.ErrConflict, scenarioRunCount)
			return release, actionableExistingError(base, "release.scenario_run_history", "该组件版本已被场景引用并运行，不能废弃", "查看运行记录", "/runs")
		}
	}
	previousStatus := release.Status
	now := time.Now().UTC()
	if err := p.store.DeprecateComponentRelease(ctx, id, now); err != nil {
		return release, err
	}
	release.Status, release.Candidate, release.DeprecatedAt = domain.ReleaseDeprecated, false, &now
	if previousStatus == domain.ReleaseReleased {
		p.requestPublicationBackup("component-release-deprecated:" + id)
	}
	p.audit(ctx, user, "component_release.deprecated", "component_release", id, map[string]any{"componentId": component.ID, "version": release.Version, "previousStatus": previousStatus})
	p.hub.Publish("component_release.deprecated", map[string]any{"componentId": component.ID, "releaseId": release.ID})
	return release, nil
}

func (p *Platform) RestoreRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
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
	if release.Status != domain.ReleaseDeprecated || release.ReleasedAt != nil {
		base := fmt.Errorf("%w: only never-published deprecated releases can be restored", domain.ErrConflict)
		return release, actionableExistingError(base, "release.restore_not_allowed", "只有从未发布的已废弃 Release 可以恢复为 Draft", "查看版本历史", "/components?selected="+component.ID+"&release="+release.ID)
	}
	if err := p.store.RestoreDeprecatedComponentRelease(ctx, id); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return release, actionableExistingError(err, "release.restore_conflict", "该发布线已有新的 Draft 或后继版本，不能恢复旧草稿", "查看发布线", "/components?selected="+component.ID+"&release="+release.ID)
		}
		return release, err
	}
	release.Status, release.Candidate, release.DeprecatedAt = domain.ReleaseDraft, false, nil
	p.audit(ctx, user, "component_release.restored", "component_release", id, map[string]any{"componentId": component.ID, "version": release.Version})
	p.hub.Publish("component_release.restored", map[string]any{"componentId": component.ID, "releaseId": release.ID})
	return release, nil
}

func (p *Platform) DeleteRelease(ctx context.Context, user domain.User, id string) error {
	release, err := p.store.GetComponentRelease(ctx, id)
	if err != nil {
		return err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return err
	}
	if release.Status != domain.ReleaseDeprecated || release.ReleasedAt != nil {
		base := fmt.Errorf("%w: only never-published deprecated releases can be permanently deleted", domain.ErrConflict)
		return actionableExistingError(base, "release.delete_not_allowed", "请先废弃未发布 Release；已发布版本必须永久保留", "查看版本历史", "/components?selected="+component.ID+"&release="+release.ID)
	}
	impact, err := p.store.ComponentReleaseDeletionImpact(ctx, id)
	if err != nil {
		return err
	}
	if impact.RunCount > 0 {
		base := fmt.Errorf("%w: component release is retained by %d run(s)", domain.ErrConflict, impact.RunCount)
		return actionableExistingError(base, "release.run_history", "该 Release 已产生 Run，必须保留其合同和执行证据", "查看 Run 证据", "/components?selected="+component.ID+"&release="+release.ID)
	}
	if impact.ImageBuildCount > 0 {
		base := fmt.Errorf("%w: component release is retained by %d image build(s)", domain.ErrConflict, impact.ImageBuildCount)
		return actionableExistingError(base, "release.image_build_history", "该 Release 已产生镜像构建记录，不能永久删除", "查看镜像构建", "/components?selected="+component.ID+"&release="+release.ID)
	}
	if impact.ReferenceCount() > 0 {
		base := fmt.Errorf("%w: component release is retained by %d reference(s)", domain.ErrConflict, impact.ReferenceCount())
		return actionableExistingError(base, "release.still_referenced", "该 Release 仍被组件、场景、版本合同或环境安装记录引用", "查看版本详情", "/components?selected="+component.ID+"&release="+release.ID)
	}
	audit := newAuditEvent(user, "component_release.deleted", "component_release", id, map[string]any{
		"componentId": component.ID, "componentSlug": component.Slug, "version": release.Version, "lineId": release.LineID,
	})
	if err := p.store.DeleteComponentRelease(ctx, id, audit); err != nil {
		return err
	}
	if err := p.removeManagedReleasePlaybooks(component, release); err != nil {
		p.audit(ctx, user, "component_release.playbook_cleanup_failed", "component_release", id, map[string]any{"componentId": component.ID, "version": release.Version, "error": err.Error()})
	}
	p.hub.Publish("component_release.deleted", map[string]any{"componentId": component.ID, "releaseId": release.ID})
	return nil
}

func (p *Platform) validateReleaseContract(ctx context.Context, release domain.ComponentRelease, publishing bool) error {
	return p.validateReleaseContractWithCatalog(ctx, release, publishing, p.store.ReadCatalogDefinitions)
}

func (p *Platform) validateReleaseContractWithCatalog(ctx context.Context, release domain.ComponentRelease, publishing bool, loadCatalog func(context.Context) (store.CatalogValidationSnapshot, error)) error {
	if !publishing || release.Status == domain.ReleaseDraft {
		if err := domain.ValidateComponentParameterAuthoring(release.Parameters); err != nil {
			return err
		}
	}
	if err := validateRelease(release); err != nil {
		return err
	}
	catalog, err := loadCatalog(ctx)
	if err != nil {
		return err
	}
	if err := validateReleaseCatalogValues(release, catalog); err != nil {
		return err
	}
	if err := p.validateReleaseMappings(ctx, release); err != nil {
		return err
	}
	if err := p.validateConfigurationReferenceClosure(ctx, release); err != nil {
		return err
	}
	if publishing {
		return p.validateUpgradeRollbackMappingContracts(ctx, release)
	}
	return nil
}

func (p *Platform) validateReleaseCatalogValues(ctx context.Context, release domain.ComponentRelease) error {
	catalog, err := p.store.ReadCatalogDefinitions(ctx)
	if err != nil {
		return err
	}
	return validateReleaseCatalogValues(release, catalog)
}

func validateReleaseCatalogValues(release domain.ComponentRelease, catalog store.CatalogValidationSnapshot) error {
	if err := catalog.Options.ValidateConstraints(release.EnvironmentConstraints); err != nil {
		return err
	}
	for _, action := range release.Actions {
		if err := catalog.Options.ValidateHostGroup(action.HostGroup); err != nil {
			return err
		}
	}
	return domain.ValidateComponentParameterAuthoring(release.Parameters)
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
		if dependency.Kind != domain.DependencyConfiguration && dependencyPathExists(forward, dependency.UpstreamComponentID, release.ComponentID) {
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
