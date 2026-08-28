package service

import (
	"context"
	"io"

	"codex/platform-demo/internal/domain"
)

// Catalog application boundary.
func (s *CatalogService) ListComponents(ctx context.Context, user domain.User) ([]domain.Component, error) {
	return s.platform.ListComponents(ctx, user)
}
func (s *CatalogService) Get(ctx context.Context, user domain.User, id string) (domain.Component, error) {
	return s.platform.GetComponent(ctx, user, id)
}
func (s *CatalogService) Create(ctx context.Context, user domain.User, input domain.Component) (domain.Component, error) {
	return s.platform.CreateComponent(ctx, user, input)
}
func (s *CatalogService) Update(ctx context.Context, user domain.User, id string, input domain.Component) (domain.Component, error) {
	return s.platform.UpdateComponent(ctx, user, id, input)
}
func (s *CatalogService) CreateRelease(ctx context.Context, user domain.User, componentID string, input domain.ComponentRelease) (domain.ComponentRelease, error) {
	release, err := s.platform.CreateRelease(ctx, user, componentID, input)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) UpdateRelease(ctx context.Context, user domain.User, id string, input domain.ComponentRelease) (domain.ComponentRelease, error) {
	release, err := s.platform.UpdateRelease(ctx, user, id, input)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) UpdateReleaseContract(ctx context.Context, user domain.User, id string, parameters []domain.ParameterDefinition, dependencies []domain.ComponentDependency) (domain.ComponentRelease, error) {
	release, err := s.platform.UpdateReleaseContract(ctx, user, id, parameters, dependencies)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) CloneRelease(ctx context.Context, user domain.User, id string, input ReleaseCloneRequest) (domain.ComponentRelease, error) {
	release, err := s.platform.CloneRelease(ctx, user, id, input)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) PreviewReleaseClone(ctx context.Context, user domain.User, id string, input ReleaseCloneRequest) (ReleaseClonePlan, error) {
	return s.platform.PreviewReleaseClone(ctx, user, id, input)
}
func (s *CatalogService) Impact(ctx context.Context, user domain.User, id string) (domain.ImpactReport, error) {
	return s.platform.Impact(ctx, user, id)
}
func (s *CatalogService) DeprecateRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
	release, err := s.platform.DeprecateRelease(ctx, user, id)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) SetReleaseCandidate(ctx context.Context, user domain.User, id string, candidate bool) (domain.ComponentRelease, error) {
	release, err := s.platform.SetReleaseCandidate(ctx, user, id, candidate)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) PreviewImport(ctx context.Context, user domain.User, input ComponentImportRequest) (ComponentImportPlan, error) {
	return s.platform.PreviewComponentImport(ctx, user, input)
}
func (s *CatalogService) Import(ctx context.Context, user domain.User, input ComponentImportRequest) (ComponentImportResult, error) {
	return s.platform.ImportComponents(ctx, user, input)
}
func (s *CatalogService) UploadArtifact(ctx context.Context, user domain.User, releaseID, environmentID, alias, filename, checksum string, input io.Reader) (domain.ComponentArtifact, error) {
	return s.platform.UploadComponentArtifact(ctx, user, releaseID, environmentID, alias, filename, checksum, input)
}
func (s *CatalogService) RegisterArtifact(ctx context.Context, user domain.User, releaseID, alias, filename, sourceURL, checksum string) (domain.ComponentArtifact, error) {
	return s.platform.RegisterComponentArtifact(ctx, user, releaseID, alias, filename, sourceURL, checksum)
}
func (s *CatalogService) UpdateArtifactSource(ctx context.Context, user domain.User, releaseID, alias, sourceURL string) (domain.ComponentArtifact, error) {
	return s.platform.UpdateComponentArtifactSource(ctx, user, releaseID, alias, sourceURL)
}
func (s *CatalogService) DeleteArtifact(ctx context.Context, user domain.User, releaseID, alias string) error {
	return s.platform.DeleteComponentArtifact(ctx, user, releaseID, alias)
}
func (s *CatalogService) RegisterImage(ctx context.Context, user domain.User, releaseID, logicalName, sourceRef, digest string) (domain.ComponentImage, error) {
	return s.platform.RegisterComponentImage(ctx, user, releaseID, logicalName, sourceRef, digest)
}
func (s *CatalogService) UpdateImageSource(ctx context.Context, user domain.User, releaseID, logicalName, sourceRef string) (domain.ComponentImage, error) {
	return s.platform.UpdateComponentImageSource(ctx, user, releaseID, logicalName, sourceRef)
}
func (s *CatalogService) DeleteImage(ctx context.Context, user domain.User, releaseID, logicalName string) error {
	return s.platform.DeleteComponentImage(ctx, user, releaseID, logicalName)
}
func (s *CatalogService) StartImageBuild(ctx context.Context, user domain.User, releaseID, environmentID, tag string, dockerfile []byte) (domain.ComponentImageBuild, error) {
	return s.platform.StartComponentImageBuild(ctx, user, releaseID, environmentID, tag, dockerfile)
}
func (s *CatalogService) ListImageBuilds(ctx context.Context, user domain.User, releaseID string) ([]domain.ComponentImageBuild, error) {
	return s.platform.ListComponentImageBuilds(ctx, user, releaseID)
}
func (s *CatalogService) GetImageBuild(ctx context.Context, user domain.User, id string) (domain.ComponentImageBuild, error) {
	return s.platform.GetComponentImageBuild(ctx, user, id)
}
func (s *CatalogService) ReadPlaybook(ctx context.Context, user domain.User, releaseID, path string) (PlaybookFile, error) {
	return s.platform.ReadReleasePlaybook(ctx, user, releaseID, path)
}
func (s *CatalogService) SavePlaybook(ctx context.Context, user domain.User, releaseID, filename string, content []byte) (PlaybookFile, error) {
	return s.platform.SaveReleasePlaybook(ctx, user, releaseID, filename, content)
}

// Scenario application boundary. Publication is intentionally absent and is
// exposed only by ReleaseCoordinator.
func (s *ScenarioService) List(ctx context.Context, user domain.User) ([]domain.Scenario, error) {
	return s.platform.ListScenarios(ctx, user)
}
func (s *ScenarioService) Get(ctx context.Context, user domain.User, id string) (domain.Scenario, error) {
	return s.platform.GetScenario(ctx, user, id)
}
func (s *ScenarioService) Create(ctx context.Context, user domain.User, input domain.Scenario) (domain.Scenario, error) {
	return s.platform.CreateScenario(ctx, user, input)
}
func (s *ScenarioService) Delete(ctx context.Context, user domain.User, id string) error {
	return s.platform.DeleteScenario(ctx, user, id)
}
func (s *ScenarioService) CloneRevision(ctx context.Context, user domain.User, id string, input ScenarioCloneRequest) (domain.ScenarioRevision, error) {
	return s.platform.CloneScenarioRevision(ctx, user, id, input)
}
func (s *ScenarioService) PreviewClone(ctx context.Context, user domain.User, id string, input ScenarioCloneRequest) (ScenarioClonePlan, error) {
	return s.platform.PreviewScenarioClone(ctx, user, id, input)
}
func (s *ScenarioService) AbandonRevision(ctx context.Context, user domain.User, id string) (domain.Scenario, error) {
	return s.platform.AbandonScenarioRevision(ctx, user, id)
}
func (s *ScenarioService) SaveGraph(ctx context.Context, user domain.User, id string, graph domain.ScenarioGraph) (domain.ScenarioRevision, error) {
	return s.platform.SaveScenarioGraph(ctx, user, id, graph)
}
func (s *ScenarioService) Validate(ctx context.Context, user domain.User, id string) ([]domain.ValidationIssue, error) {
	return s.platform.ValidateScenario(ctx, user, id)
}
func (s *ScenarioService) Deprecate(ctx context.Context, user domain.User, id string) (domain.ScenarioRevision, error) {
	return s.platform.DeprecateScenario(ctx, user, id)
}

// Environment application boundary.
func (s *EnvironmentService) List(ctx context.Context, user domain.User, includeArchived ...bool) ([]domain.Environment, error) {
	return s.platform.ListEnvironments(ctx, user, includeArchived...)
}
func (s *EnvironmentService) Create(ctx context.Context, user domain.User, input domain.Environment, facts map[string]any) (domain.Environment, error) {
	return s.platform.CreateEnvironment(ctx, user, input, facts)
}
func (s *EnvironmentService) Lifecycle(ctx context.Context, user domain.User, id string) (EnvironmentLifecycle, error) {
	return s.platform.GetEnvironmentLifecycle(ctx, user, id)
}
func (s *EnvironmentService) Delete(ctx context.Context, user domain.User, id string) error {
	return s.platform.DeleteEnvironment(ctx, user, id)
}
func (s *EnvironmentService) Archive(ctx context.Context, user domain.User, id string) (domain.Environment, error) {
	return s.platform.ArchiveEnvironment(ctx, user, id)
}
func (s *EnvironmentService) Unarchive(ctx context.Context, user domain.User, id string) (domain.Environment, error) {
	return s.platform.UnarchiveEnvironment(ctx, user, id)
}
func (s *EnvironmentService) UpdateInventory(ctx context.Context, user domain.User, id string, hosts []InventoryHost, reason ...string) (domain.Environment, error) {
	return s.platform.UpdateInventory(ctx, user, id, hosts, reason...)
}
func (s *EnvironmentService) UpdateFacts(ctx context.Context, user domain.User, id string, facts map[string]any, reason ...string) (domain.Environment, error) {
	return s.platform.UpdateEnvironmentFacts(ctx, user, id, facts, reason...)
}
func (s *EnvironmentService) UpdateVariables(ctx context.Context, user domain.User, id string, values map[string]string, reason ...string) (domain.Environment, error) {
	return s.platform.UpdateEnvironmentVariables(ctx, user, id, values, reason...)
}
func (s *EnvironmentService) UpdateCredentialRefs(ctx context.Context, user domain.User, id string, refs []domain.CredentialRef, reason ...string) (domain.Environment, error) {
	return s.platform.UpdateCredentialRefs(ctx, user, id, refs, reason...)
}
func (s *EnvironmentService) CheckHealth(ctx context.Context, user domain.User, id string) (domain.EnvironmentHealthCheck, error) {
	return s.platform.CheckEnvironmentHealth(ctx, user, id)
}
func (s *EnvironmentService) RestoreRevision(ctx context.Context, user domain.User, id, revisionID, reason string) (domain.Environment, error) {
	return s.platform.RestoreEnvironmentRevision(ctx, user, id, revisionID, reason)
}
func (s *EnvironmentService) ExportRevision(ctx context.Context, user domain.User, id, revisionID string, refs bool) (EnvironmentExportDocument, error) {
	return s.platform.ExportEnvironmentRevision(ctx, user, id, revisionID, refs)
}
func (s *EnvironmentService) PreviewImport(ctx context.Context, user domain.User, input EnvironmentImportRequest) (EnvironmentImportPlan, error) {
	return s.platform.PreviewEnvironmentImport(ctx, user, input)
}
func (s *EnvironmentService) Import(ctx context.Context, user domain.User, input EnvironmentImportRequest) (domain.Environment, error) {
	return s.platform.ImportEnvironment(ctx, user, input)
}

// Execution application boundary.
func (s *ExecutionService) PreviewComponentTest(ctx context.Context, user domain.User, id string, input ComponentTestRequest) (ComponentTestPlan, error) {
	return s.platform.PreviewComponentTest(ctx, user, id, input)
}
func (s *ExecutionService) StartComponentTest(ctx context.Context, user domain.User, id string, input ComponentTestRequest) (domain.Run, error) {
	return s.platform.StartComponentTest(ctx, user, id, input)
}
func (s *ExecutionService) StartScenarioTest(ctx context.Context, user domain.User, id, environmentID string, input map[string]any) (domain.Run, error) {
	return s.platform.StartScenarioTest(ctx, user, id, environmentID, input)
}
func (s *ExecutionService) StartScenarioRun(ctx context.Context, user domain.User, id, environmentID string, input map[string]any) (domain.Run, error) {
	return s.platform.StartScenarioRun(ctx, user, id, environmentID, input)
}
func (s *ExecutionService) PreviewRollback(ctx context.Context, user domain.User, environmentID string) (EnvironmentRollbackPlan, error) {
	return s.platform.PreviewEnvironmentRollback(ctx, user, environmentID)
}
func (s *ExecutionService) StartRollback(ctx context.Context, user domain.User, environmentID string, input EnvironmentRollbackRequest) (domain.Run, error) {
	return s.platform.StartEnvironmentRollback(ctx, user, environmentID, input)
}
func (s *ExecutionService) Cancel(ctx context.Context, user domain.User, id string) (domain.Run, error) {
	return s.platform.CancelRun(ctx, user, id)
}
func (s *ExecutionService) PreviewRetry(ctx context.Context, user domain.User, id string) (RunRetryPlan, error) {
	return s.platform.PreviewRunRetry(ctx, user, id)
}
func (s *ExecutionService) Retry(ctx context.Context, user domain.User, id string, input RunRetryRequest) (domain.Run, error) {
	return s.platform.RetryRun(ctx, user, id, input)
}
func (s *ExecutionService) DecideApproval(ctx context.Context, user domain.User, id, decision, reason string, deliveryDecisions []DeliveryDecisionInput) (domain.Run, error) {
	return s.platform.DecideApproval(ctx, user, id, decision, reason, deliveryDecisions)
}
func (s *ExecutionService) BatchDecideApprovals(ctx context.Context, user domain.User, ids []string, decision, reason string) ([]domain.Run, error) {
	return s.platform.BatchDecideApprovals(ctx, user, ids, decision, reason)
}
func (s *ExecutionService) ListInputPresets(ctx context.Context, user domain.User, resourceType, resourceID, inputContext string) ([]domain.RunInputPreset, error) {
	return s.platform.ListRunInputPresets(ctx, user, resourceType, resourceID, inputContext)
}
func (s *ExecutionService) SaveInputPreset(ctx context.Context, user domain.User, input domain.RunInputPreset) (domain.RunInputPreset, error) {
	return s.platform.SaveRunInputPreset(ctx, user, input)
}
func (s *ExecutionService) DeleteInputPreset(ctx context.Context, user domain.User, id string) error {
	return s.platform.DeleteRunInputPreset(ctx, user, id)
}

func (s *ReadModelService) Workbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	return s.platform.Workbench(ctx, user)
}
