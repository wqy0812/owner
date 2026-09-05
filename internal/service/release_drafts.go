package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

type ReleaseDraftMode string

const (
	ReleaseDraftNewLine   ReleaseDraftMode = "new_line"
	ReleaseDraftEvolution ReleaseDraftMode = "evolution"
)

type ReleaseDraftRequest struct {
	Mode                    ReleaseDraftMode            `json:"mode"`
	LineName                string                      `json:"lineName,omitempty"`
	ParentReleaseID         string                      `json:"parentReleaseId,omitempty"`
	TemplateSourceReleaseID string                      `json:"templateSourceReleaseId,omitempty"`
	Version                 string                      `json:"version"`
	ReleaseNotes            string                      `json:"releaseNotes"`
	Compatibility           domain.ReleaseCompatibility `json:"compatibility,omitempty"`
	RiskLevel               domain.RiskLevel            `json:"riskLevel,omitempty"`
	EnvironmentConstraints  map[string]any              `json:"environmentConstraints"`
	ExpectedPlanDigest      string                      `json:"expectedPlanDigest,omitempty"`
}

type ReleaseDraftPlan struct {
	ComponentID             string                      `json:"componentId"`
	Mode                    ReleaseDraftMode            `json:"mode"`
	LineID                  string                      `json:"lineId,omitempty"`
	LineName                string                      `json:"lineName"`
	ParentReleaseID         string                      `json:"parentReleaseId,omitempty"`
	ParentVersion           string                      `json:"parentVersion,omitempty"`
	TemplateSourceReleaseID string                      `json:"templateSourceReleaseId,omitempty"`
	TemplateSourceVersion   string                      `json:"templateSourceVersion,omitempty"`
	TargetVersion           string                      `json:"targetVersion"`
	Compatibility           domain.ReleaseCompatibility `json:"compatibility"`
	Actions                 []domain.ActionKind         `json:"actions"`
	RemovedActions          []domain.ActionKind         `json:"removedActions"`
	Playbooks               []string                    `json:"playbooks"`
	ArtifactCount           int                         `json:"artifactCount"`
	ImageCount              int                         `json:"imageCount"`
	PlanDigest              string                      `json:"planDigest"`
}

func normalizeReleaseDraftRequest(input ReleaseDraftRequest) ReleaseDraftRequest {
	input.LineName = strings.TrimSpace(input.LineName)
	input.ParentReleaseID = strings.TrimSpace(input.ParentReleaseID)
	input.TemplateSourceReleaseID = strings.TrimSpace(input.TemplateSourceReleaseID)
	input.Version = strings.TrimSpace(input.Version)
	input.ReleaseNotes = strings.TrimSpace(input.ReleaseNotes)
	input.ExpectedPlanDigest = ""
	return input
}

func (p *Platform) PreviewReleaseDraft(ctx context.Context, user domain.User, componentID string, input ReleaseDraftRequest) (ReleaseDraftPlan, error) {
	input = normalizeReleaseDraftRequest(input)
	component, err := p.store.GetComponent(ctx, componentID, false)
	if err != nil {
		return ReleaseDraftPlan{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return ReleaseDraftPlan{}, err
	}
	if input.Version == "" || input.ReleaseNotes == "" {
		return ReleaseDraftPlan{}, fmt.Errorf("%w: version and releaseNotes are required", domain.ErrInvalid)
	}
	if err := rejectSensitiveMap(input.EnvironmentConstraints, "environment constraint"); err != nil {
		return ReleaseDraftPlan{}, err
	}
	plan := ReleaseDraftPlan{
		ComponentID: componentID, Mode: input.Mode, TargetVersion: input.Version,
		Actions: []domain.ActionKind{}, RemovedActions: []domain.ActionKind{}, Playbooks: []string{},
	}
	var source domain.ComponentRelease
	switch input.Mode {
	case ReleaseDraftNewLine:
		if input.LineName == "" {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: lineName is required for a new release line", domain.ErrInvalid)
		}
		if input.ParentReleaseID != "" || (input.Compatibility != "" && input.Compatibility != domain.CompatibilityNotApplicable) {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: a new release line cannot declare a parent or upgrade compatibility", domain.ErrInvalid)
		}
		plan.LineName, plan.Compatibility = input.LineName, domain.CompatibilityNotApplicable
		if input.TemplateSourceReleaseID != "" {
			source, err = p.store.GetComponentRelease(ctx, input.TemplateSourceReleaseID)
			if err != nil {
				return ReleaseDraftPlan{}, err
			}
			if source.ComponentID != componentID || (source.Status != domain.ReleaseReleased && source.Status != domain.ReleaseDeprecated) {
				return ReleaseDraftPlan{}, fmt.Errorf("%w: template source must be a retained release of the same component", domain.ErrInvalid)
			}
			plan.TemplateSourceReleaseID, plan.TemplateSourceVersion = source.ID, source.Version
		}
	case ReleaseDraftEvolution:
		if input.ParentReleaseID == "" || input.LineName != "" || input.TemplateSourceReleaseID != "" {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: evolution requires only parentReleaseId", domain.ErrInvalid)
		}
		if input.Compatibility != domain.CompatibilityCompatible && input.Compatibility != domain.CompatibilityBreaking {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: evolution compatibility must be compatible or breaking", domain.ErrInvalid)
		}
		source, err = p.store.GetComponentRelease(ctx, input.ParentReleaseID)
		if err != nil {
			return ReleaseDraftPlan{}, err
		}
		if source.ComponentID != componentID || source.Status != domain.ReleaseReleased {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: evolution parent must be a released version of the same component", domain.ErrInvalid)
		}
		active, activeErr := p.store.HasActiveDraftInLine(ctx, source.LineID)
		if activeErr != nil {
			return ReleaseDraftPlan{}, activeErr
		}
		if active {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: release line already has an active Draft", domain.ErrConflict)
		}
		latest, latestErr := p.store.LatestPublishedInLine(ctx, source.LineID)
		if latestErr != nil {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: release line has no retained published version", domain.ErrConflict)
		}
		if latest.ID != source.ID {
			if latest.Status == domain.ReleaseDeprecated {
				return ReleaseDraftPlan{}, fmt.Errorf("%w: the latest published version %s is Deprecated; create a new release line", domain.ErrConflict, latest.Version)
			}
			return ReleaseDraftPlan{}, fmt.Errorf("%w: evolution must start from the latest published version in the line", domain.ErrConflict)
		}
		retained, retainedErr := p.store.HasRetainedSuccessor(ctx, source.ID)
		if retainedErr != nil {
			return ReleaseDraftPlan{}, retainedErr
		}
		if retained {
			return ReleaseDraftPlan{}, fmt.Errorf("%w: evolution parent already has a retained successor", domain.ErrConflict)
		}
		plan.LineID, plan.LineName = source.LineID, source.LineName
		plan.ParentReleaseID, plan.ParentVersion = source.ID, source.Version
		plan.TemplateSourceReleaseID, plan.TemplateSourceVersion = source.ID, source.Version
		plan.Compatibility = input.Compatibility
	default:
		return ReleaseDraftPlan{}, fmt.Errorf("%w: unsupported release draft mode %q", domain.ErrInvalid, input.Mode)
	}

	if err := domain.ValidateComponentParameterAuthoring(source.Parameters); err != nil {
		return ReleaseDraftPlan{}, err
	}
	executableDigests := map[string]string{}
	if source.ID != "" {
		plan.ArtifactCount, plan.ImageCount = len(source.Artifacts), len(source.Images)
		for _, action := range source.Actions {
			if input.Mode == ReleaseDraftNewLine && action.Kind == domain.ActionUpgrade {
				plan.RemovedActions = append(plan.RemovedActions, action.Kind)
				continue
			}
			plan.Actions = append(plan.Actions, action.Kind)
			plan.Playbooks = append(plan.Playbooks, action.Playbook)
			if digester, ok := p.runner.(digestRunner); ok {
				playbookDigest, treeDigest, digestErr := digester.Digest(action.Playbook)
				if digestErr != nil {
					return ReleaseDraftPlan{}, digestErr
				}
				executableDigests[action.Playbook] = playbookDigest + ":" + treeDigest
			}
		}
	}
	plan.PlanDigest = digestValue(struct {
		SourceDigest      string
		ExecutableDigests map[string]string
		Input             ReleaseDraftRequest
	}{componentReleaseSpecDigest(source), executableDigests, input})
	return plan, nil
}

func (p *Platform) CreateReleaseDraft(ctx context.Context, user domain.User, componentID string, input ReleaseDraftRequest) (domain.ComponentRelease, error) {
	expected := input.ExpectedPlanDigest
	input = normalizeReleaseDraftRequest(input)
	plan, err := p.PreviewReleaseDraft(ctx, user, componentID, input)
	if err != nil {
		return domain.ComponentRelease{}, err
	}
	if expected == "" || expected != plan.PlanDigest {
		return domain.ComponentRelease{}, fmt.Errorf("%w: release Draft plan changed; preview again", domain.ErrConflict)
	}
	component, err := p.store.GetComponent(ctx, componentID, false)
	if err != nil {
		return domain.ComponentRelease{}, err
	}
	var release domain.ComponentRelease
	if plan.TemplateSourceReleaseID != "" {
		release, err = p.store.GetComponentRelease(ctx, plan.TemplateSourceReleaseID)
		if err != nil {
			return release, err
		}
	}
	newReleaseID := newID("release")
	now := time.Now().UTC()
	release.ID, release.ComponentID = newReleaseID, componentID
	release.Version, release.ReleaseNotes = input.Version, input.ReleaseNotes
	release.Status, release.Candidate = domain.ReleaseDraft, false
	release.RiskLevel = input.RiskLevel
	if release.RiskLevel == "" {
		release.RiskLevel = domain.RiskLow
	}
	if input.EnvironmentConstraints != nil {
		release.EnvironmentConstraints = input.EnvironmentConstraints
	}
	if release.EnvironmentConstraints == nil {
		release.EnvironmentConstraints = map[string]any{}
	}
	release.CreatedAt, release.ReleasedAt, release.DeprecatedAt = now, nil, nil
	if input.Mode == ReleaseDraftNewLine {
		release.LineID, release.LineName = newID("release-line"), input.LineName
		release.ParentReleaseID = ""
		release.TemplateSourceReleaseID = plan.TemplateSourceReleaseID
		release.Compatibility = domain.CompatibilityNotApplicable
		filtered := make([]domain.ActionDefinition, 0, len(release.Actions))
		for _, action := range release.Actions {
			if action.Kind == domain.ActionUpgrade {
				continue
			}
			if action.Kind == domain.ActionRollback {
				action.FromReleaseID, action.ToReleaseID = "", ""
			}
			filtered = append(filtered, action)
		}
		release.Actions = filtered
	} else {
		release.LineID, release.LineName = plan.LineID, plan.LineName
		release.ParentReleaseID, release.TemplateSourceReleaseID = plan.ParentReleaseID, plan.ParentReleaseID
		release.Compatibility = input.Compatibility
		for index := range release.Actions {
			switch release.Actions[index].Kind {
			case domain.ActionUpgrade:
				release.Actions[index].FromReleaseID, release.Actions[index].ToReleaseID = plan.ParentReleaseID, newReleaseID
			case domain.ActionRollback:
				release.Actions[index].FromReleaseID, release.Actions[index].ToReleaseID = newReleaseID, plan.ParentReleaseID
			}
		}
	}
	release.PlaybookWorkspaceRoot = generatedManagedReleasePrefix(component, release)
	for index := range release.Artifacts {
		release.Artifacts[index].ID, release.Artifacts[index].ReleaseID = newID("artifact"), newReleaseID
		release.Artifacts[index].CreatedAt, release.Artifacts[index].CreatedBy = now, user.ID
		release.Artifacts[index].SourceUpdatedAt, release.Artifacts[index].SourceUpdatedBy = now, user.ID
	}
	for index := range release.Images {
		release.Images[index].ID, release.Images[index].ReleaseID = newID("image"), newReleaseID
		release.Images[index].CreatedAt, release.Images[index].CreatedBy = now, user.ID
		release.Images[index].SourceUpdatedAt, release.Images[index].SourceUpdatedBy = now, user.ID
	}
	// The template's child identities belong to the source Release. The clone
	// receives fresh identities while later Draft updates preserve its own IDs.
	for index := range release.Actions {
		release.Actions[index].ID = ""
	}
	manifest := componentImportManifest{}
	if plan.TemplateSourceReleaseID != "" {
		manifest, err = p.copyManagedPlaybooksForClone(component, plan.TemplateSourceReleaseID, &release)
		if err != nil {
			return release, err
		}
	}
	cleanup := func(deleteFinal bool) { _ = p.cleanupComponentImportManifest(manifest, deleteFinal) }
	rewriteReleaseChildren(&release)
	if err := p.validateReleaseContract(ctx, release, false); err != nil {
		cleanup(true)
		return release, err
	}
	if err := p.validateEnvironmentConstraintRetiredReferences(ctx, release.EnvironmentConstraints, nil); err != nil {
		cleanup(true)
		return release, err
	}
	audit := newAuditEvent(user, "component_release.draft_created", "component_release", release.ID, map[string]any{
		"componentId": componentID, "mode": input.Mode, "lineId": release.LineID, "lineName": release.LineName,
		"parentReleaseId": release.ParentReleaseID, "templateSourceReleaseId": release.TemplateSourceReleaseID,
		"compatibility": release.Compatibility, "version": release.Version, "planDigest": plan.PlanDigest,
	})
	if err := p.store.CreateClonedComponentRelease(ctx, release, audit); err != nil {
		cleanup(true)
		return release, err
	}
	cleanup(false)
	return p.store.GetComponentRelease(ctx, release.ID)
}

func (p *Platform) RenameReleaseLine(ctx context.Context, user domain.User, lineID, name string) (domain.ComponentReleaseLine, error) {
	p.workspaceMu.Lock()
	defer p.workspaceMu.Unlock()
	line, err := p.store.GetReleaseLine(ctx, lineID)
	if err != nil {
		return line, err
	}
	component, err := p.store.GetComponent(ctx, line.ComponentID, false)
	if err != nil {
		return line, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return line, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return line, fmt.Errorf("%w: release line name is required", domain.ErrInvalid)
	}
	var draftBefore, draftAfter domain.ComponentRelease
	actionPaths := map[string]string{}
	releases, err := p.store.ListComponentReleases(ctx, component.ID, false)
	if err != nil {
		return line, err
	}
	for _, release := range releases {
		if release.LineID == lineID && release.Status == domain.ReleaseDraft {
			draftBefore, draftAfter = release, release
			draftAfter.LineName = name
			draftAfter.PlaybookWorkspaceRoot = generatedManagedReleasePrefix(component, draftAfter)
			for index := range draftAfter.Actions {
				path, pathErr := actionPlaybookPath(component, draftAfter, draftAfter.Actions[index].Kind)
				if pathErr != nil {
					return line, pathErr
				}
				draftAfter.Actions[index].Playbook = path
				actionPaths[draftAfter.Actions[index].ID] = path
			}
			break
		}
	}
	undo := func() {}
	if draftBefore.ID != "" {
		undo, err = p.moveDraftWorkspace(component, draftBefore, draftAfter)
		if err != nil {
			return line, err
		}
	}
	if err := p.store.RenameReleaseLineAndDraftActions(ctx, lineID, component.ID, name, draftBefore.ID, draftAfter.PlaybookWorkspaceRoot, actionPaths); err != nil {
		undo()
		return line, err
	}
	p.audit(ctx, user, "component_release_line.renamed", "component_release_line", lineID, map[string]any{"componentId": component.ID, "oldName": line.Name, "newName": name})
	return p.store.GetReleaseLine(ctx, lineID)
}
