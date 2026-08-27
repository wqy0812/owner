package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

type ComponentImportDependency struct {
	ComponentSlug     string                    `json:"componentSlug"`
	Purpose           string                    `json:"purpose"`
	ParameterMappings []domain.ParameterMapping `json:"parameterMappings"`
}

type ComponentImportAction struct {
	Name                string            `json:"name"`
	Type                domain.ActionKind `json:"type"`
	Playbook            string            `json:"playbook"`
	Tags                []string          `json:"tags"`
	Limit               string            `json:"limit"`
	HostGroup           string            `json:"hostGroup"`
	TimeoutSeconds      int               `json:"timeoutSeconds"`
	AllowedParameters   []string          `json:"allowedParameters"`
	RequiredCredentials []string          `json:"requiredCredentials"`
	RiskLevel           domain.RiskLevel  `json:"riskLevel"`
	Destructive         bool              `json:"destructive"`
	Idempotent          bool              `json:"idempotent"`
}

type ComponentImportEntry struct {
	Component struct {
		Name, Slug, Description string
		Layer                   domain.ComponentLayer
		Category                domain.ComponentCategory
		Kind                    domain.ComponentKind
		Requiredness            domain.ComponentRequiredness
	} `json:"component"`
	Release struct {
		Version                string                       `json:"version"`
		Type                   domain.ReleaseType           `json:"type"`
		ReleaseNotes           string                       `json:"releaseNotes"`
		Breaking               bool                         `json:"breaking"`
		RiskLevel              domain.RiskLevel             `json:"riskLevel"`
		EnvironmentConstraints map[string]any               `json:"environmentConstraints"`
		Parameters             []domain.ParameterDefinition `json:"parameters"`
		Dependencies           []ComponentImportDependency  `json:"dependencies"`
		Actions                []ComponentImportAction      `json:"actions"`
	} `json:"release"`
	Playbooks []struct{ Filename, Content string } `json:"playbooks"`
}

type ComponentImportRequest struct {
	Entries            []ComponentImportEntry `json:"entries"`
	ExpectedPlanDigest string                 `json:"expectedPlanDigest,omitempty"`
}

type ComponentImportPlanItem struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	DependencyCount int    `json:"dependencyCount"`
	ActionCount     int    `json:"actionCount"`
	PlaybookCount   int    `json:"playbookCount"`
}
type ComponentImportPlan struct {
	PlanDigest string                    `json:"planDigest"`
	Items      []ComponentImportPlanItem `json:"items"`
	Order      []string                  `json:"order"`
}
type ComponentImportResult struct {
	CompletedComponents []string          `json:"completedComponents"`
	CompletedReleases   []string          `json:"completedReleases"`
	SavedPlaybooks      []string          `json:"savedPlaybooks"`
	CreatedDrafts       map[string]string `json:"createdDrafts"`
}

func normalizeStrings(values []string) []string {
	normalized := make([]string, len(values))
	for index, value := range values {
		normalized[index] = strings.TrimSpace(value)
	}
	return normalized
}

func normalizeComponentImportRequest(input ComponentImportRequest) ComponentImportRequest {
	output := input
	output.Entries = append([]ComponentImportEntry(nil), input.Entries...)
	for entryIndex := range output.Entries {
		entry := &output.Entries[entryIndex]
		entry.Component.Name = strings.TrimSpace(entry.Component.Name)
		entry.Component.Slug = strings.TrimSpace(entry.Component.Slug)
		entry.Component.Description = strings.TrimSpace(entry.Component.Description)
		entry.Release.Version = strings.TrimSpace(entry.Release.Version)
		entry.Release.ReleaseNotes = strings.TrimSpace(entry.Release.ReleaseNotes)
		entry.Release.Parameters = append([]domain.ParameterDefinition(nil), entry.Release.Parameters...)
		for index := range entry.Release.Parameters {
			entry.Release.Parameters[index].Name = strings.TrimSpace(entry.Release.Parameters[index].Name)
			entry.Release.Parameters[index].Description = strings.TrimSpace(entry.Release.Parameters[index].Description)
			entry.Release.Parameters[index].Enum = append([]any(nil), entry.Release.Parameters[index].Enum...)
		}
		entry.Release.Dependencies = append([]ComponentImportDependency(nil), entry.Release.Dependencies...)
		for index := range entry.Release.Dependencies {
			dependency := &entry.Release.Dependencies[index]
			dependency.ComponentSlug = strings.TrimSpace(dependency.ComponentSlug)
			dependency.Purpose = strings.TrimSpace(dependency.Purpose)
			dependency.ParameterMappings = append([]domain.ParameterMapping(nil), dependency.ParameterMappings...)
			for mappingIndex := range dependency.ParameterMappings {
				dependency.ParameterMappings[mappingIndex].UpstreamParameter = strings.TrimSpace(dependency.ParameterMappings[mappingIndex].UpstreamParameter)
				dependency.ParameterMappings[mappingIndex].TargetParameter = strings.TrimSpace(dependency.ParameterMappings[mappingIndex].TargetParameter)
			}
		}
		entry.Release.Actions = append([]ComponentImportAction(nil), entry.Release.Actions...)
		for index := range entry.Release.Actions {
			action := &entry.Release.Actions[index]
			action.Name = strings.TrimSpace(action.Name)
			action.Playbook = strings.TrimSpace(action.Playbook)
			action.Limit = strings.TrimSpace(action.Limit)
			action.HostGroup = strings.TrimSpace(action.HostGroup)
			action.Tags = normalizeStrings(action.Tags)
			action.AllowedParameters = normalizeStrings(action.AllowedParameters)
			action.RequiredCredentials = normalizeStrings(action.RequiredCredentials)
		}
		entry.Playbooks = append([]struct{ Filename, Content string }(nil), entry.Playbooks...)
		for index := range entry.Playbooks {
			entry.Playbooks[index].Filename = strings.TrimSpace(entry.Playbooks[index].Filename)
		}
	}
	return output
}

func (p *Platform) PreviewComponentImport(ctx context.Context, user domain.User, input ComponentImportRequest) (ComponentImportPlan, error) {
	input = normalizeComponentImportRequest(input)
	if err := domain.ValidateRole(user, domain.RoleComponentOwner); err != nil {
		return ComponentImportPlan{}, err
	}
	if len(input.Entries) < 1 || len(input.Entries) > 50 {
		return ComponentImportPlan{}, fmt.Errorf("%w: import requires 1 to 50 components", domain.ErrInvalid)
	}
	bySlug := map[string]ComponentImportEntry{}
	for _, entry := range input.Entries {
		component := domain.Component{Name: entry.Component.Name, Slug: entry.Component.Slug, Layer: entry.Component.Layer, Category: entry.Component.Category, Kind: entry.Component.Kind, Requiredness: entry.Component.Requiredness}
		if component.Name == "" {
			return ComponentImportPlan{}, fmt.Errorf("%w: component name is required", domain.ErrInvalid)
		}
		if err := validateSlug(component.Slug); err != nil {
			return ComponentImportPlan{}, err
		}
		if err := domain.ValidateComponentClassification(component); err != nil {
			return ComponentImportPlan{}, err
		}
		if _, duplicate := bySlug[component.Slug]; duplicate {
			return ComponentImportPlan{}, fmt.Errorf("%w: duplicate component slug %q", domain.ErrInvalid, component.Slug)
		}
		var exists int
		if err := p.store.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM components WHERE slug=?)`, component.Slug).Scan(&exists); err != nil {
			return ComponentImportPlan{}, err
		}
		if exists != 0 {
			return ComponentImportPlan{}, fmt.Errorf("%w: component slug %q already exists", domain.ErrConflict, component.Slug)
		}
		bySlug[component.Slug] = entry
	}
	indegree, adjacency := map[string]int{}, map[string][]string{}
	for slug, entry := range bySlug {
		indegree[slug] = 0
		if entry.Release.Version == "" {
			return ComponentImportPlan{}, fmt.Errorf("%w: %s release version is required", domain.ErrInvalid, slug)
		}
		if entry.Release.Type != domain.ReleaseAtomic && entry.Release.Type != domain.ReleaseBundle {
			return ComponentImportPlan{}, fmt.Errorf("%w: %s release type is invalid", domain.ErrInvalid, slug)
		}
		if err := rejectSensitiveMap(entry.Release.EnvironmentConstraints, "environment constraint"); err != nil {
			return ComponentImportPlan{}, err
		}
		playbooks := map[string]bool{}
		for _, playbook := range entry.Playbooks {
			if !managedPlaybookFilename.MatchString(playbook.Filename) || strings.TrimSpace(playbook.Content) == "" || len([]byte(playbook.Content)) > MaxPlaybookBytes {
				return ComponentImportPlan{}, fmt.Errorf("%w: %s Playbook %q is invalid", domain.ErrInvalid, slug, playbook.Filename)
			}
			if playbooks[playbook.Filename] {
				return ComponentImportPlan{}, fmt.Errorf("%w: duplicate Playbook %q", domain.ErrInvalid, playbook.Filename)
			}
			playbooks[playbook.Filename] = true
		}
		actions := make([]domain.ActionDefinition, 0, len(entry.Release.Actions))
		usedPlaybooks, actionKinds := map[string]bool{}, map[domain.ActionKind]bool{}
		for _, action := range entry.Release.Actions {
			if action.Type == domain.ActionUpgrade {
				return ComponentImportPlan{}, fmt.Errorf("%w: imported upgrade actions require existing release IDs", domain.ErrInvalid)
			}
			if !playbooks[action.Playbook] {
				return ComponentImportPlan{}, fmt.Errorf("%w: action %s references missing Playbook %q", domain.ErrInvalid, action.Type, action.Playbook)
			}
			if actionKinds[action.Type] {
				return ComponentImportPlan{}, fmt.Errorf("%w: %s contains duplicate action %s", domain.ErrInvalid, slug, action.Type)
			}
			actionKinds[action.Type], usedPlaybooks[action.Playbook] = true, true
			if action.Idempotent && action.Type != domain.ActionInstall {
				return ComponentImportPlan{}, fmt.Errorf("%w: only install may be idempotent", domain.ErrInvalid)
			}
			timeout := action.TimeoutSeconds
			if timeout == 0 {
				timeout = 1800
			}
			risk := action.RiskLevel
			if risk == "" {
				risk = domain.RiskLow
			}
			actions = append(actions, domain.ActionDefinition{Name: action.Name, Kind: action.Type, Playbook: action.Playbook, Tags: action.Tags, Limit: action.Limit, HostGroup: action.HostGroup, TimeoutSeconds: timeout, AllowedParameters: action.AllowedParameters, RequiredCredentials: action.RequiredCredentials, RiskLevel: risk, Destructive: action.Destructive, Idempotent: action.Idempotent})
		}
		for filename := range playbooks {
			if !usedPlaybooks[filename] {
				return ComponentImportPlan{}, fmt.Errorf("%w: Playbook %q is not referenced by an action", domain.ErrInvalid, filename)
			}
		}
		validationRelease := domain.ComponentRelease{ID: "import-release-" + slug, ComponentID: "import-component-" + slug, Version: entry.Release.Version, Type: entry.Release.Type, RiskLevel: entry.Release.RiskLevel, EnvironmentConstraints: entry.Release.EnvironmentConstraints, Parameters: entry.Release.Parameters, Actions: actions}
		if validationRelease.RiskLevel == "" {
			validationRelease.RiskLevel = domain.RiskLow
		}
		for _, dependency := range entry.Release.Dependencies {
			validationRelease.Dependencies = append(validationRelease.Dependencies, domain.ComponentDependency{UpstreamComponentID: "import-component-" + dependency.ComponentSlug, UpstreamReleaseID: "import-release-" + dependency.ComponentSlug, Purpose: dependency.Purpose, ParameterMappings: dependency.ParameterMappings})
		}
		if err := validateRelease(validationRelease); err != nil {
			return ComponentImportPlan{}, err
		}
	}
	for slug, entry := range bySlug {
		seen := map[string]bool{}
		for _, dependency := range entry.Release.Dependencies {
			if _, found := bySlug[dependency.ComponentSlug]; !found {
				return ComponentImportPlan{}, fmt.Errorf("%w: %s references component outside this import", domain.ErrInvalid, slug)
			}
			if dependency.ComponentSlug == slug || seen[dependency.ComponentSlug] {
				return ComponentImportPlan{}, fmt.Errorf("%w: invalid dependency for %s", domain.ErrInvalid, slug)
			}
			seen[dependency.ComponentSlug] = true
			upstream := bySlug[dependency.ComponentSlug]
			upstreamParameters := map[string]domain.ParameterDefinition{}
			for _, parameter := range upstream.Release.Parameters {
				upstreamParameters[parameter.Name] = parameter
			}
			targetParameters := map[string]domain.ParameterDefinition{}
			for _, parameter := range entry.Release.Parameters {
				targetParameters[parameter.Name] = parameter
			}
			for _, mapping := range dependency.ParameterMappings {
				source, sourceOK := upstreamParameters[mapping.UpstreamParameter]
				target, targetOK := targetParameters[mapping.TargetParameter]
				if !sourceOK || source.Visibility != domain.ParameterPublic || !targetOK || source.Type != target.Type {
					return ComponentImportPlan{}, fmt.Errorf("%w: invalid parameter mapping from %s to %s", domain.ErrInvalid, dependency.ComponentSlug, slug)
				}
			}
			indegree[slug]++
			adjacency[dependency.ComponentSlug] = append(adjacency[dependency.ComponentSlug], slug)
		}
	}
	queue := make([]string, 0)
	for slug, degree := range indegree {
		if degree == 0 {
			queue = append(queue, slug)
		}
	}
	sort.Strings(queue)
	order := make([]string, 0, len(bySlug))
	for len(queue) > 0 {
		slug := queue[0]
		queue = queue[1:]
		order = append(order, slug)
		for _, next := range adjacency[slug] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
				sort.Strings(queue)
			}
		}
	}
	if len(order) != len(bySlug) {
		return ComponentImportPlan{}, fmt.Errorf("%w: component dependency graph contains a cycle", domain.ErrInvalid)
	}
	plan := ComponentImportPlan{Order: order}
	for _, slug := range order {
		entry := bySlug[slug]
		plan.Items = append(plan.Items, ComponentImportPlanItem{Slug: slug, Name: entry.Component.Name, Version: entry.Release.Version, DependencyCount: len(entry.Release.Dependencies), ActionCount: len(entry.Release.Actions), PlaybookCount: len(entry.Playbooks)})
	}
	canonical := input
	canonical.ExpectedPlanDigest = ""
	plan.PlanDigest = digestValue(struct {
		Request ComponentImportRequest
		Order   []string
	}{canonical, order})
	return plan, nil
}

func (p *Platform) ImportComponents(ctx context.Context, user domain.User, input ComponentImportRequest) (ComponentImportResult, error) {
	input = normalizeComponentImportRequest(input)
	plan, err := p.PreviewComponentImport(ctx, user, input)
	if err != nil {
		return ComponentImportResult{}, err
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != plan.PlanDigest {
		return ComponentImportResult{}, fmt.Errorf("%w: component import plan changed; preview again", domain.ErrConflict)
	}
	prepared, err := p.prepareComponentImport(user, input, plan)
	if err != nil {
		return ComponentImportResult{}, err
	}
	manifest, err := p.stageComponentImportFiles(prepared.Files)
	if err != nil {
		return ComponentImportResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = p.cleanupComponentImportManifest(manifest, true)
		}
	}()
	if err := p.promoteComponentImportFiles(manifest); err != nil {
		return ComponentImportResult{}, err
	}
	if err := p.store.CreateComponentImport(ctx, prepared.Components, prepared.Releases, prepared.Audits); err != nil {
		return ComponentImportResult{}, err
	}
	committed = true
	_ = p.cleanupComponentImportManifest(manifest, false)
	return prepared.Result, nil
}

type preparedComponentImport struct {
	Components []domain.Component
	Releases   []domain.ComponentRelease
	Audits     []domain.AuditEvent
	Files      []componentImportFile
	Result     ComponentImportResult
}

type componentImportFile struct {
	Component    domain.Component
	Release      domain.ComponentRelease
	RelativePath string
	Content      string
}

type componentImportManifestFile struct {
	ComponentSlug string `json:"componentSlug"`
	ReleaseID     string `json:"releaseId"`
	RelativePath  string `json:"relativePath"`
	Promoted      bool   `json:"-"`
}

type componentImportManifest struct {
	BatchID    string                        `json:"batchId"`
	ReleaseIDs []string                      `json:"releaseIds"`
	Files      []componentImportManifestFile `json:"files"`
	BatchDir   string                        `json:"-"`
}

func (p *Platform) prepareComponentImport(user domain.User, input ComponentImportRequest, plan ComponentImportPlan) (preparedComponentImport, error) {
	bySlug := make(map[string]ComponentImportEntry, len(input.Entries))
	for _, entry := range input.Entries {
		bySlug[entry.Component.Slug] = entry
	}
	now := time.Now().UTC()
	prepared := preparedComponentImport{Result: ComponentImportResult{CreatedDrafts: map[string]string{}}}
	components := make(map[string]domain.Component, len(plan.Order))
	releases := make(map[string]domain.ComponentRelease, len(plan.Order))
	for _, slug := range plan.Order {
		entry := bySlug[slug]
		component := domain.Component{
			ID: newID("component"), Name: entry.Component.Name, Slug: slug, Description: entry.Component.Description,
			Layer: entry.Component.Layer, Category: entry.Component.Category, Kind: entry.Component.Kind,
			Requiredness: entry.Component.Requiredness, OwnerID: user.ID, CreatedAt: now, UpdatedAt: now,
		}
		if err := domain.ValidateComponentClassification(component); err != nil {
			return preparedComponentImport{}, err
		}
		release := domain.ComponentRelease{
			ID: newID("release"), ComponentID: component.ID, Version: entry.Release.Version, Type: entry.Release.Type,
			Status: domain.ReleaseDraft, ReleaseNotes: entry.Release.ReleaseNotes, Breaking: entry.Release.Breaking,
			RiskLevel: entry.Release.RiskLevel, EnvironmentConstraints: entry.Release.EnvironmentConstraints,
			Parameters: entry.Release.Parameters, CreatedAt: now,
		}
		if release.RiskLevel == "" {
			release.RiskLevel = domain.RiskLow
		}
		components[slug], releases[slug] = component, release
		prepared.Components = append(prepared.Components, component)
		prepared.Result.CompletedComponents = append(prepared.Result.CompletedComponents, slug)
		prepared.Result.CreatedDrafts[slug] = release.ID
	}
	for _, slug := range plan.Order {
		entry, release := bySlug[slug], releases[slug]
		for _, dependency := range entry.Release.Dependencies {
			upstreamComponent, upstreamRelease := components[dependency.ComponentSlug], releases[dependency.ComponentSlug]
			release.Dependencies = append(release.Dependencies, domain.ComponentDependency{
				UpstreamComponentID: upstreamComponent.ID, UpstreamReleaseID: upstreamRelease.ID,
				Purpose: dependency.Purpose, ParameterMappings: dependency.ParameterMappings,
			})
		}
		managed := make(map[string]string, len(entry.Playbooks))
		for _, playbook := range entry.Playbooks {
			relative := managedReleasePrefix(components[slug], release) + playbook.Filename
			managed[playbook.Filename] = relative
			prepared.Files = append(prepared.Files, componentImportFile{Component: components[slug], Release: release, RelativePath: relative, Content: playbook.Content})
			prepared.Result.SavedPlaybooks = append(prepared.Result.SavedPlaybooks, slug+"/"+playbook.Filename)
		}
		for _, action := range entry.Release.Actions {
			timeout := action.TimeoutSeconds
			if timeout == 0 {
				timeout = 1800
			}
			risk := action.RiskLevel
			if risk == "" {
				risk = domain.RiskLow
			}
			release.Actions = append(release.Actions, domain.ActionDefinition{
				Name: action.Name, Kind: action.Type, Playbook: managed[action.Playbook], Tags: action.Tags,
				Limit: action.Limit, HostGroup: action.HostGroup, TimeoutSeconds: timeout,
				AllowedParameters: action.AllowedParameters, RequiredCredentials: action.RequiredCredentials,
				RiskLevel: risk, Destructive: action.Destructive, Idempotent: action.Idempotent,
			})
		}
		rewriteReleaseChildren(&release)
		if err := validateRelease(release); err != nil {
			return preparedComponentImport{}, err
		}
		releases[slug] = release
		prepared.Releases = append(prepared.Releases, release)
		prepared.Result.CompletedReleases = append(prepared.Result.CompletedReleases, slug)
	}
	for _, component := range prepared.Components {
		prepared.Audits = append(prepared.Audits, newAuditEvent(user, "component.created", "component", component.ID, map[string]any{
			"slug": component.Slug, "layer": component.Layer, "category": component.Category,
			"kind": component.Kind, "requiredness": component.Requiredness, "componentImport": plan.PlanDigest,
		}))
	}
	for _, release := range prepared.Releases {
		prepared.Audits = append(prepared.Audits, newAuditEvent(user, "component_release.created", "component_release", release.ID, map[string]any{"version": release.Version, "componentImport": plan.PlanDigest}))
	}
	for _, file := range prepared.Files {
		digest := sha256.Sum256([]byte(file.Content))
		prepared.Audits = append(prepared.Audits, newAuditEvent(user, "component_playbook.saved", "component_release", file.Release.ID, map[string]any{
			"path": file.RelativePath, "sha256": hex.EncodeToString(digest[:]), "size": len([]byte(file.Content)), "componentImport": plan.PlanDigest,
		}))
	}
	prepared.Audits = append(prepared.Audits, newAuditEvent(user, "component_import.completed", "component_import", plan.PlanDigest, map[string]any{"components": len(prepared.Components), "releases": len(prepared.Releases)}))
	return prepared, nil
}

func (p *Platform) componentImportStagingRoot() (string, error) {
	if strings.TrimSpace(p.playbookRoot) == "" {
		return "", fmt.Errorf("playbook management is not configured")
	}
	root, err := filepath.Abs(p.playbookRoot)
	if err != nil {
		return "", fmt.Errorf("resolve playbook root: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create playbook root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve playbook root: %w", err)
	}
	staging := filepath.Join(root, ".component-import-staging")
	info, err := os.Lstat(staging)
	if os.IsNotExist(err) {
		if err := os.Mkdir(staging, 0o750); err != nil {
			return "", fmt.Errorf("create component import staging root: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect component import staging root: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%w: component import staging root must be a real directory", domain.ErrInvalid)
	}
	return staging, nil
}

func writeSyncedFile(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(contents); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (p *Platform) stageComponentImportFiles(files []componentImportFile) (componentImportManifest, error) {
	if len(files) == 0 {
		return componentImportManifest{}, nil
	}
	stagingRoot, err := p.componentImportStagingRoot()
	if err != nil {
		return componentImportManifest{}, err
	}
	manifest := componentImportManifest{BatchID: newID("component-import")}
	manifest.BatchDir = filepath.Join(stagingRoot, manifest.BatchID)
	if err := os.Mkdir(manifest.BatchDir, 0o750); err != nil {
		return componentImportManifest{}, fmt.Errorf("create component import staging directory: %w", err)
	}
	cleanup := func(cause error) (componentImportManifest, error) {
		_ = os.RemoveAll(manifest.BatchDir)
		return componentImportManifest{}, cause
	}
	releases := map[string]bool{}
	for _, item := range files {
		clean, _, err := p.resolveManagedPlaybookPath(item.Component, item.Release, item.RelativePath, true)
		if err != nil {
			return cleanup(err)
		}
		stagedPath := filepath.Join(manifest.BatchDir, "files", filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(stagedPath), 0o750); err != nil {
			return cleanup(fmt.Errorf("create staged Playbook directory: %w", err))
		}
		if err := writeSyncedFile(stagedPath, []byte(item.Content), 0o640); err != nil {
			return cleanup(fmt.Errorf("stage Playbook %s: %w", clean, err))
		}
		manifest.Files = append(manifest.Files, componentImportManifestFile{ComponentSlug: item.Component.Slug, ReleaseID: item.Release.ID, RelativePath: clean})
		if !releases[item.Release.ID] {
			releases[item.Release.ID] = true
			manifest.ReleaseIDs = append(manifest.ReleaseIDs, item.Release.ID)
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return cleanup(err)
	}
	if err := writeSyncedFile(filepath.Join(manifest.BatchDir, "manifest.json"), encoded, 0o640); err != nil {
		return cleanup(fmt.Errorf("write component import manifest: %w", err))
	}
	return manifest, nil
}

func (p *Platform) promoteComponentImportFiles(manifest componentImportManifest) error {
	if manifest.BatchDir == "" {
		return nil
	}
	for index := range manifest.Files {
		item := &manifest.Files[index]
		component := domain.Component{Slug: item.ComponentSlug}
		release := domain.ComponentRelease{ID: item.ReleaseID}
		clean, target, err := p.resolveManagedPlaybookWriteTarget(component, release, item.RelativePath, true)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("%w: managed Playbook %s already exists", domain.ErrConflict, item.RelativePath)
		} else if !os.IsNotExist(err) {
			return err
		}
		staged := filepath.Join(manifest.BatchDir, "files", filepath.FromSlash(clean))
		if err := os.Rename(staged, target); err != nil {
			return fmt.Errorf("promote Playbook %s: %w", item.RelativePath, err)
		}
		item.Promoted = true
	}
	return nil
}

func (p *Platform) cleanupComponentImportManifest(manifest componentImportManifest, deleteFinal bool) error {
	var firstErr error
	if deleteFinal {
		for _, item := range manifest.Files {
			if manifest.BatchDir != "" && !item.Promoted {
				continue
			}
			component := domain.Component{Slug: item.ComponentSlug}
			release := domain.ComponentRelease{ID: item.ReleaseID}
			_, target, err := p.resolveManagedPlaybookWriteTarget(component, release, item.RelativePath, false)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
			_ = os.Remove(filepath.Dir(target))
			_ = os.Remove(filepath.Dir(filepath.Dir(target)))
		}
	}
	// Keep the manifest when final-file cleanup failed so startup recovery can
	// retry it instead of leaving an untracked orphan.
	if firstErr != nil {
		return firstErr
	}
	if manifest.BatchDir != "" {
		if err := os.RemoveAll(manifest.BatchDir); err != nil {
			return err
		}
	}
	return nil
}

func (p *Platform) RecoverComponentImportFiles(ctx context.Context) error {
	if strings.TrimSpace(p.playbookRoot) == "" {
		return nil
	}
	stagingRoot, err := p.componentImportStagingRoot()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		batchDir := filepath.Join(stagingRoot, entry.Name())
		contents, err := os.ReadFile(filepath.Join(batchDir, "manifest.json"))
		if err != nil {
			return fmt.Errorf("read component import manifest %s: %w", entry.Name(), err)
		}
		var manifest componentImportManifest
		if err := json.Unmarshal(contents, &manifest); err != nil {
			return fmt.Errorf("decode component import manifest %s: %w", entry.Name(), err)
		}
		if manifest.BatchID != entry.Name() || len(manifest.ReleaseIDs) == 0 {
			return fmt.Errorf("invalid component import manifest %s", entry.Name())
		}
		manifest.BatchDir = batchDir
		found := 0
		for _, releaseID := range manifest.ReleaseIDs {
			if _, err := p.store.GetComponentRelease(ctx, releaseID); err == nil {
				found++
			} else if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		if found != 0 && found != len(manifest.ReleaseIDs) {
			return fmt.Errorf("component import %s has a partial database commit", manifest.BatchID)
		}
		if found == 0 {
			for index := range manifest.Files {
				manifest.Files[index].Promoted = true
			}
		}
		if err := p.cleanupComponentImportManifest(manifest, found == 0); err != nil {
			return err
		}
	}
	return nil
}
