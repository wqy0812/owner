package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

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
func (s *CatalogService) CreateReleaseDraft(ctx context.Context, user domain.User, componentID string, input ReleaseDraftRequest) (domain.ComponentRelease, error) {
	release, err := s.platform.CreateReleaseDraft(ctx, user, componentID, input)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) PreviewReleaseDraft(ctx context.Context, user domain.User, componentID string, input ReleaseDraftRequest) (ReleaseDraftPlan, error) {
	return s.platform.PreviewReleaseDraft(ctx, user, componentID, input)
}
func (s *CatalogService) RenameReleaseLine(ctx context.Context, user domain.User, lineID, name string) (domain.ComponentReleaseLine, error) {
	return s.platform.RenameReleaseLine(ctx, user, lineID, name)
}
func (s *CatalogService) Impact(ctx context.Context, user domain.User, id string) (domain.ImpactReport, error) {
	return s.platform.Impact(ctx, user, id)
}
func (s *CatalogService) PublicationImpact(ctx context.Context, user domain.User, id string) (domain.ImpactReport, error) {
	return s.platform.PublicationImpact(ctx, user, id)
}
func (s *CatalogService) DeprecateRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
	release, err := s.platform.DeprecateRelease(ctx, user, id)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) RestoreRelease(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
	release, err := s.platform.RestoreRelease(ctx, user, id)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) DeleteRelease(ctx context.Context, user domain.User, id string) error {
	return s.platform.DeleteRelease(ctx, user, id)
}
func (s *CatalogService) SetReleaseCandidate(ctx context.Context, user domain.User, id string, candidate bool) (domain.ComponentRelease, error) {
	release, err := s.platform.SetReleaseCandidate(ctx, user, id, candidate)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}

func (s *CatalogService) SubmitReleaseReview(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
	release, err := s.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, err
	}
	component, err := s.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, err
	}
	if release.Status != domain.ReleaseDraft {
		return release, fmt.Errorf("%w: only a draft release can be submitted for review", domain.ErrConflict)
	}
	if err := s.platform.validateReleaseContract(ctx, release, false); err != nil {
		return release, err
	}
	digest := componentReleaseSpecDigest(release)
	if err := s.store.SubmitComponentReleaseReview(ctx, id, digest, release.PublicationGeneration, time.Now().UTC()); err != nil {
		return release, err
	}
	s.platform.audit(ctx, user, "component_release.review_submitted", "component_release", id, map[string]any{"contractDigest": digest})
	s.platform.hub.Publish("component_release.review_updated", map[string]any{"releaseId": id, "status": domain.ReleaseReviewPending})
	return s.GetComponentRelease(ctx, id)
}

func (s *CatalogService) DecideReleaseReview(ctx context.Context, user domain.User, id string, approve bool, comment, expectedPreviewDigest string) (domain.ComponentRelease, error) {
	if err := domain.ValidateRole(user, domain.RolePlatformAdmin); err != nil {
		return domain.ComponentRelease{}, err
	}
	expectedPreviewDigest = strings.TrimSpace(expectedPreviewDigest)
	if expectedPreviewDigest == "" {
		return domain.ComponentRelease{}, fmt.Errorf("%w: expectedPreviewDigest is required; preview the release before deciding", domain.ErrInvalid)
	}
	preview, err := s.releaseReviewPreview(ctx, id)
	if err != nil {
		return domain.ComponentRelease{}, err
	}
	if preview.PreviewDigest != expectedPreviewDigest {
		return preview.Release, fmt.Errorf("%w: release review preview changed; preview it again", domain.ErrConflict)
	}
	release := preview.Release
	status := domain.ReleaseReviewRejected
	if approve {
		status = domain.ReleaseReviewApproved
	}
	comment = strings.TrimSpace(comment)
	if !approve && comment == "" {
		return release, fmt.Errorf("%w: rejection comment is required", domain.ErrInvalid)
	}
	if err := s.store.DecideComponentReleaseReview(ctx, id, status, user.ID, comment, release.Review.ContractDigest, time.Now().UTC()); err != nil {
		return release, err
	}
	s.platform.audit(ctx, user, "component_release.review_decided", "component_release", id, map[string]any{"status": status, "comment": comment})
	s.platform.hub.Publish("component_release.review_updated", map[string]any{"releaseId": id, "status": status})
	return s.GetComponentRelease(ctx, id)
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
func (s *ScenarioService) ParameterOverview(ctx context.Context, user domain.User, id string) (ScenarioParameterOverview, error) {
	return s.platform.ScenarioParameterOverview(ctx, user, id)
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
func (s *EnvironmentService) ParameterFields(ctx context.Context) ([]EnvironmentParameterField, error) {
	return s.platform.EnvironmentParameterFields(ctx)
}
func (s *EnvironmentService) UpdateParameters(ctx context.Context, user domain.User, id string, values map[string]any, reason ...string) (domain.Environment, error) {
	return s.platform.UpdateEnvironmentParameters(ctx, user, id, values, reason...)
}
func (s *EnvironmentService) UpdateCredentialRefs(ctx context.Context, user domain.User, id string, refs []domain.CredentialRef, reason ...string) (domain.Environment, error) {
	return s.platform.UpdateCredentialRefs(ctx, user, id, refs, reason...)
}
func (s *EnvironmentService) CheckHealth(ctx context.Context, user domain.User, id string) (domain.EnvironmentHealthCheck, error) {
	return s.platform.CheckEnvironmentHealth(ctx, user, id)
}
func (s *EnvironmentService) CheckConnectivity(ctx context.Context, user domain.User, id string) (domain.EnvironmentConnectivityCheck, error) {
	return s.platform.CheckEnvironmentConnectivity(ctx, user, id)
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
func (s *ExecutionService) StartScenarioTest(ctx context.Context, user domain.User, id, environmentID string) (domain.Run, error) {
	return s.platform.StartScenarioTest(ctx, user, id, environmentID)
}
func (s *ExecutionService) StartScenarioRun(ctx context.Context, user domain.User, id, environmentID string) (domain.Run, error) {
	return s.platform.StartScenarioRun(ctx, user, id, environmentID)
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

func (s *ReadModelService) Workbench(ctx context.Context, user domain.User) (domain.Workbench, error) {
	return s.platform.Workbench(ctx, user)
}
