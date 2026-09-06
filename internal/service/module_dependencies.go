package service

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type ActionPlanner struct {
	store actionsStore
}

type actionsStore interface {
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
}

type ApprovalService struct {
	audit     auditPort
	control   *executionControl
	delivery  deliveryPort
	hub       *EventHub
	scheduler schedulerPort
	store     approvalsStore
}

type approvalsStore interface {
	BatchDecideApprovals(ctx context.Context, ids []string, ownerID, decision, reason string, at time.Time) ([]domain.Run, error)
	DecideApprovalWithSnapshot(ctx context.Context, id, userID, decision, reason string, snapshot map[string]any, at time.Time) error
	GetApproval(ctx context.Context, id string) (domain.Approval, error)
	GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	SetScenarioRevisionStatus(ctx context.Context, id string, from []domain.RevisionStatus, to domain.RevisionStatus, at time.Time) error
	UpdateRunStatus(ctx context.Context, id string, from []domain.RunStatus, to domain.RunStatus, errText string, at time.Time) error
}

type AuditRecorder struct {
	store auditStore
}

type auditStore interface {
	AppendAudit(ctx context.Context, e domain.AuditEvent) error
}

type CatalogRules struct {
	store catalogRulesStore
}

type catalogRulesStore interface {
	ReadCatalogOptions(ctx context.Context) (domain.CatalogOptions, error)
}

type CatalogService struct {
	artifactDelivery ArtifactDelivery
	audit            auditPort
	catalogRules     catalogRulesPort
	dockerBinary     string
	hub              *EventHub
	imageBuildRoot   string
	imageDelivery    ImageDelivery
	publication      publicationPort
	releaseRules     *ReleaseRules
	rootCtx          context.Context
	store            catalogStore
	workspace        *WorkspaceFiles
	inspector        WorkspaceInspector
}

type catalogStore interface {
	AppendComponentImageBuildLog(ctx context.Context, log domain.ImageBuildLog) error
	CompleteComponentImageBuild(ctx context.Context, buildID, pushedDigest string, image domain.ComponentImage, at time.Time) error
	ComponentEvidenceReads(ctx context.Context, user domain.User, componentID string) ([]domain.Run, map[string]string, error)
	ComponentReleaseDeletionImpact(ctx context.Context, id string) (store.ComponentReleaseDeletionImpact, error)
	ComponentSlugExists(ctx context.Context, slug string) (bool, error)
	ComponentUsage(ctx context.Context, viewer domain.User, id, releaseID string, history bool) (store.ComponentUsage, error)
	CountScenarioRunsForComponentRelease(ctx context.Context, releaseID string) (int, error)
	CreateClonedComponentRelease(ctx context.Context, r domain.ComponentRelease, audit domain.AuditEvent) error
	CreateComponent(ctx context.Context, c domain.Component) error
	CreateComponentImageBuild(ctx context.Context, build domain.ComponentImageBuild) error
	CreateComponentImport(ctx context.Context, components []domain.Component, releases []domain.ComponentRelease, audits []domain.AuditEvent) error
	CreatePendingActionFileMutation(ctx context.Context, mutation store.PendingActionFileMutation) error
	DecideComponentReleaseReview(ctx context.Context, id string, status domain.ReleaseReviewStatus, reviewer, comment, digest string, reviewedAt time.Time) error
	DeleteComponentRelease(ctx context.Context, id string, audit domain.AuditEvent) error
	DeleteDraftComponentArtifactAndInvalidate(ctx context.Context, releaseID, alias string) error
	DeleteDraftComponentImage(ctx context.Context, releaseID, logicalName string) error
	DeletePendingActionFileMutation(ctx context.Context, id string) error
	DeprecateComponentRelease(ctx context.Context, id string, at time.Time) error
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentImage(ctx context.Context, releaseID, logicalName string) (domain.ComponentImage, error)
	GetComponentImageBuild(ctx context.Context, id string) (domain.ComponentImageBuild, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error)
	GetReleaseLine(ctx context.Context, lineID string) (domain.ComponentReleaseLine, error)
	GetUser(ctx context.Context, id string) (domain.User, error)
	HasActiveComponentTest(ctx context.Context, releaseID string) (bool, error)
	HasActiveDraftInLine(ctx context.Context, lineID string) (bool, error)
	HasRetainedSuccessor(ctx context.Context, parentReleaseID string) (bool, error)
	LatestPublishedInLine(ctx context.Context, lineID string) (domain.ComponentRelease, error)
	ListComponentArtifacts(ctx context.Context, releaseID string) ([]domain.ComponentArtifact, error)
	ListComponentImageBuilds(ctx context.Context, releaseID string, limit int) ([]domain.ComponentImageBuild, error)
	ListComponentReleases(ctx context.Context, componentID string, releasedOnly bool) ([]domain.ComponentRelease, error)
	ListComponentSummaries(ctx context.Context, viewer domain.User) ([]domain.ComponentSummary, error)
	ListComponents(ctx context.Context, viewer domain.User) ([]domain.Component, error)
	ListPendingActionFileMutations(ctx context.Context) ([]store.PendingActionFileMutation, error)
	ListReleaseDisplayMetadata(ctx context.Context) (map[string]store.ReleaseDisplayMetadata, error)
	ListScenariosForImpact(ctx context.Context) ([]domain.Scenario, error)
	MarkComponentImageBuildsInterrupted(ctx context.Context, at time.Time) error
	RenameReleaseLineAndDraftActions(ctx context.Context, lineID, componentID, name, draftID, workspaceRoot string, actionPaths map[string]string) error
	ReplaceDraftPlaybookFilesAndDeleteAction(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string, actionID string, mutationID string) error
	ReplaceDraftPlaybookFilesAndInvalidate(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string) error
	ReplaceDraftPlaybookFilesAndUpsertAction(ctx context.Context, releaseID, workspaceRoot, treeSHA string, files []domain.ComponentPlaybookFile, actionDigests map[string]string, action domain.ActionDefinition, mutationID string) error
	RestoreDeprecatedComponentRelease(ctx context.Context, id string) error
	SetReleaseCandidate(ctx context.Context, id string, candidate bool, expectedGeneration int64, expectedDigest string) error
	SubmitComponentReleaseReview(ctx context.Context, id, digest string, expectedGeneration int64, submittedAt time.Time) error
	UpdateComponent(ctx context.Context, c domain.Component) error
	UpdateComponentArtifactSource(ctx context.Context, releaseID, alias, sourceURL, actorID string, at time.Time) (domain.ComponentArtifact, error)
	UpdateComponentImageBuildStatus(ctx context.Context, id string, from []domain.ImageBuildStatus, to domain.ImageBuildStatus, digest, errorText string, at time.Time) error
	UpdateComponentImageSource(ctx context.Context, releaseID, logicalName, sourceRef, actorID string, at time.Time) (domain.ComponentImage, error)
	UpdateDraftRelease(ctx context.Context, r domain.ComponentRelease) error
	UpsertDraftComponentArtifactAndInvalidate(ctx context.Context, artifact domain.ComponentArtifact) error
	UpsertDraftComponentImage(ctx context.Context, image domain.ComponentImage) error
}

type DeliveryService struct {
	artifactDelivery ArtifactDelivery
	imageDelivery    ImageDelivery
	store            deliveryStore
}

type deliveryStore interface {
	AppendRunLog(ctx context.Context, l domain.RunLog) (int64, error)
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	RecordComponentArtifactMirror(ctx context.Context, sourceStation, targetStation, relativePath, sha256 string, at time.Time) error
	RecordComponentImageMirror(ctx context.Context, targetRegistry, sourceDigest, targetRef, targetDigest string, at time.Time) error
	UpdateRunDeliveryResults(ctx context.Context, runID string, results any) error
}

type EnvironmentService struct {
	audit             auditPort
	catalogRules      catalogRulesPort
	dialContext       func(context.Context, string, string) (net.Conn, error)
	hub               *EventHub
	sshChecker        EnvironmentSSHChecker
	sshKnownHostsPath string
	store             environmentsStore
}

type environmentsStore interface {
	ListScenarios(ctx context.Context, viewer domain.User) ([]domain.Scenario, error)
	ArchiveEnvironment(ctx context.Context, environmentID string, archivedAt time.Time, audit domain.AuditEvent) error
	CreateEnvironment(ctx context.Context, e domain.Environment, r domain.EnvironmentRevision, writes ...store.EnvironmentRevisionWrite) error
	CreateEnvironmentRevision(ctx context.Context, r domain.EnvironmentRevision, writes ...store.EnvironmentRevisionWrite) error
	DeleteEnvironment(ctx context.Context, environmentID string, audit domain.AuditEvent) error
	EnvironmentArchived(ctx context.Context, environmentID string) (bool, error)
	EnvironmentLifecycleImpact(ctx context.Context, environmentID string) (store.EnvironmentLifecycleImpact, error)
	GetActiveEnvironmentRun(ctx context.Context, environmentID string) (store.ActiveEnvironmentRun, error)
	GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error)
	GetEnvironmentRevision(ctx context.Context, id string) (domain.EnvironmentRevision, error)
	GetUser(ctx context.Context, id string) (domain.User, error)
	LatestEnvironmentHealthCheck(ctx context.Context, environmentID string) (domain.EnvironmentHealthCheck, error)
	LatestEnvironmentSSHCheck(ctx context.Context, environmentID string) (domain.EnvironmentSSHCheck, error)
	ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error)
	ListComponents(ctx context.Context, viewer domain.User) ([]domain.Component, error)
	ListEnvironments(ctx context.Context, includeArchived ...bool) ([]domain.Environment, error)
	NextEnvironmentRevision(ctx context.Context, environmentID string) (int, error)
	ReadCatalogValidation(ctx context.Context) (store.CatalogValidationSnapshot, error)
	SaveEnvironmentHealthCheck(ctx context.Context, check domain.EnvironmentHealthCheck) error
	SaveEnvironmentSSHCheck(ctx context.Context, check domain.EnvironmentSSHCheck) error
	UnarchiveEnvironment(ctx context.Context, environmentID string, restoredAt time.Time, audit domain.AuditEvent) error
}

type ExecutionService struct {
	approvals         approvalsPort
	archives          *RunArchives
	audit             auditPort
	creator           creatorPort
	delivery          deliveryPort
	hub               *EventHub
	planner           plannerPort
	rollback          rollbackPort
	scheduler         schedulerPort
	store             executionStore
	workspace         *WorkspaceFiles
	workspaceVerifier workspaceVerifierPort
	inspector         WorkspaceInspector
	jobs              JobBackend
}

type executionStore interface {
	ReadRunActivity(context.Context, string, *int64, int) (store.RunActivity, error)
	StreamRunLogSnapshot(context.Context, string, func(domain.RunLog) error) (store.RunLogSnapshot, error)
	BatchApprovalCandidates(ctx context.Context, viewer domain.User) ([]store.RunSummary, error)
	CanViewRun(ctx context.Context, viewer domain.User, runID string) (bool, error)
	CleanupRuns(ctx context.Context, ids []string, actor, source string, now time.Time) error
	EnqueueRunArchives(ctx context.Context, ids []string, source, actor string, now time.Time) ([]domain.RunArchive, error)
	GetApprovalByRun(ctx context.Context, runID string) (domain.Approval, error)
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	GetRunArchive(ctx context.Context, id string) (domain.RunArchive, error)
	GetRunJob(ctx context.Context, runID string) (string, []byte, *int, error)
	GetScenario(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error)
	GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error)
	GetScenarioSubmission(ctx context.Context, userID, key, digest string) (domain.Run, error)
	GetUser(ctx context.Context, id string) (domain.User, error)
	HasActiveRetry(ctx context.Context, retryRootRunID string) (bool, error)
	LatestEnvironmentActionReceipts(ctx context.Context, environmentID string) ([]store.ActionExecutionReceipt, error)
	ListRunLogTail(ctx context.Context, runID string, limit int) ([]domain.RunLog, error)
	ListRunWaitingObservations(context.Context, string) ([]json.RawMessage, error)
	ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error)
	ListRunSummaries(ctx context.Context, viewer domain.User, options store.RunListOptions) (store.RunPage, error)
	ListRuns(ctx context.Context, viewer domain.User) ([]domain.Run, error)
	ListRunsForComponentRelease(ctx context.Context, releaseID string) ([]domain.Run, error)
	NextRetryAttempt(ctx context.Context, retryRootRunID string) (int, error)
	PreviewRunCleanup(ctx context.Context, ids []string, now time.Time) ([]domain.RunCleanupPreview, error)
	QueuedRunPosition(ctx context.Context, environmentID string, createdAt time.Time) (int, error)
	ReleaseRunSummaries(ctx context.Context, viewer domain.User, releaseID string) ([]store.RunSummary, error)
	RetryRecoveryStateDigest(ctx context.Context, environmentID string) (string, error)
	RunArchiveHealth(ctx context.Context) (domain.RunArchiveHealth, error)
	RunJobSummary(ctx context.Context, id string) (string, *int, error)
	RunWasCleaned(ctx context.Context, id string) (bool, error)
	SaveRunRetentionPolicy(ctx context.Context, p domain.RunRetentionPolicy, actor string, now time.Time) error
}

type IdentityService struct {
	store identityStore
}

type identityStore interface {
	CreateSession(ctx context.Context, tokenHash, userID string, expires time.Time) error
	GetUser(ctx context.Context, id string) (domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	UserBySession(ctx context.Context, tokenHash string) (domain.User, error)
}

type LifecycleRecorder struct {
	audit auditPort
	hub   *EventHub
	store recorderStore
}

type recorderStore interface {
	AppendRunLog(ctx context.Context, l domain.RunLog) (int64, error)
	CreateRunStep(ctx context.Context, st domain.RunStep) error
	DeleteInstallationBaseline(ctx context.Context, environmentID, componentID, backupRef string) error
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetEnvironmentComponentInstallationForNode(ctx context.Context, environmentID, componentID, nodeID string) (domain.EnvironmentComponentInstallation, error)
	GetUser(ctx context.Context, id string) (domain.User, error)
	MarkScenarioBaselineUnverified(ctx context.Context, run domain.Run) error
	MarkScenarioMutation(ctx context.Context, run domain.Run) error
	RecordActionExecution(ctx context.Context, r store.ActionExecutionReceipt) error
	SetScenarioRevisionStatus(ctx context.Context, id string, from []domain.RevisionStatus, to domain.RevisionStatus, at time.Time) error
	UpdateRunStatus(ctx context.Context, id string, from []domain.RunStatus, to domain.RunStatus, errText string, at time.Time) error
	UpdateRunStep(ctx context.Context, st domain.RunStep) error
	UpsertEnvironmentComponentInstallation(ctx context.Context, installation domain.EnvironmentComponentInstallation) error
}

type PlanBuilder struct {
	actions           actionsPort
	catalogRules      catalogRulesPort
	delivery          deliveryPort
	rollback          rollbackPort
	scenarios         scenariosPort
	store             plannerStore
	workspaceVerifier workspaceVerifierPort
	runtime           RuntimeInspector
}

type plannerStore interface {
	FindScenarioHistoricalBaseline(ctx context.Context, environmentID, scenarioID, revisionID string) (store.ScenarioHistoricalBaseline, error)
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error)
	GetEnvironmentRevision(ctx context.Context, id string) (domain.EnvironmentRevision, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	GetScenario(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error)
	GetScenarioInstallation(ctx context.Context, environmentID, scenarioID string) (domain.ScenarioInstallation, error)
	GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error)
	HasActiveEnvironmentRun(ctx context.Context, environmentID string) (bool, error)
	HasSuccessfulActionTest(ctx context.Context, releaseID, spec, actionID, core, python string) (bool, error)
	LatestEnvironmentActionReceipts(ctx context.Context, environmentID string) ([]store.ActionExecutionReceipt, error)
	ListEnvironmentComponentInstallations(ctx context.Context, environmentID string) ([]domain.EnvironmentComponentInstallation, error)
	ScenarioEnvironmentStateCount(ctx context.Context, id string) (int, error)
	SuccessfulScenarioSourceRun(ctx context.Context, revision domain.ScenarioRevision, runID string) (domain.Run, error)
}

type PlatformOptionService struct {
	hub   *EventHub
	store platformOptionsStore
}

type platformOptionsStore interface {
	CreateEnvironmentVariableDefinition(ctx context.Context, item domain.EnvironmentVariableDefinition, audit domain.AuditEvent) error
	CreatePlatformOption(ctx context.Context, option domain.PlatformOption, audit domain.AuditEvent) error
	CreatePlatformOptionCategory(ctx context.Context, category domain.PlatformOptionCategory, audit domain.AuditEvent) error
	DeleteEnvironmentVariableDefinition(ctx context.Context, id string, audit domain.AuditEvent) error
	DeletePlatformOption(ctx context.Context, id string, audit domain.AuditEvent) error
	DeletePlatformOptionCategory(ctx context.Context, id string, audit domain.AuditEvent) error
	ListEnvironmentVariableDefinitions(ctx context.Context) ([]domain.EnvironmentVariableDefinition, error)
	ListPlatformOptionCategories(ctx context.Context) ([]domain.PlatformOptionCategory, error)
	RenamePlatformOption(ctx context.Context, id, label string, audit domain.AuditEvent) error
	RenamePlatformOptionCategory(ctx context.Context, id, label string, audit domain.AuditEvent) error
	SetPlatformOptionCategoryRetired(ctx context.Context, id string, retiredAt *time.Time, audit domain.AuditEvent) error
	SetPlatformOptionRetired(ctx context.Context, id string, retiredAt *time.Time, audit domain.AuditEvent) error
}

type PublicationBackup struct {
	publicationBackup PublicationBackupRequester
}

type ReadModelService struct {
	catalog                 catalogPort
	environments            environmentsPort
	publicationBackupHealth PublicationBackupHealthProvider
	releases                releasesPort
	scenarios               scenariosPort
	store                   readModelStore
}

type readModelStore interface {
	WorkbenchSubjects(context.Context, domain.User) (store.WorkbenchSubjects, error)
	WorkbenchRuns(context.Context, domain.User) ([]domain.Run, error)
	WorkbenchComponentRun(context.Context, domain.User, string, string) ([]domain.Run, error)
	WorkbenchScenarioRuns(context.Context, domain.User, string, string, bool, time.Time, string) ([]domain.Run, error)
	WorkbenchMetadata(context.Context, domain.User, []domain.Run) (store.WorkbenchMetadata, error)
	WorkbenchContracts(context.Context, map[string]bool, map[string]bool) (store.WorkbenchContracts, error)
	WorkbenchFailedNodes(context.Context, map[string]bool) (map[string]string, error)
	FirstAuditForResourceAfter(ctx context.Context, resourceType, resourceID string, after time.Time, actions []string) (domain.AuditEvent, error)
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetNotification(ctx context.Context, id string) (domain.Notification, error)
	GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error)
	GetUser(ctx context.Context, id string) (domain.User, error)
	ListComponents(ctx context.Context, viewer domain.User) ([]domain.Component, error)
	ListNotifications(ctx context.Context, userID string, unreadOnly bool) ([]domain.Notification, error)
	ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error)
	ListRuns(ctx context.Context, viewer domain.User) ([]domain.Run, error)
	MarkNotificationRead(ctx context.Context, id, userID string) error
}

type ReleaseCoordinator struct {
	audit             auditPort
	catalog           catalogPort
	hub               *EventHub
	publication       publicationPort
	releaseRules      *ReleaseRules
	scenarios         scenariosPort
	store             releasesStore
	workspace         *WorkspaceFiles
	workspaceVerifier workspaceVerifierPort
}

type releasesStore interface {
	CreateNotifications(ctx context.Context, notifications []domain.Notification) error
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetPublicationEpoch(ctx context.Context) (int64, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	LatestSuccessfulScenarioTestRun(ctx context.Context, revisionID string) (domain.Run, error)
	PublishCandidateReleaseSet(ctx context.Context, revisionGuard store.ScenarioPublicationGuard, candidateReleaseIDs []string, releaseGuards []store.ReleasePublicationGuard, evidenceRunID string, expectedEpoch int64, at time.Time) error
	PublishComponentRelease(ctx context.Context, id string, expectedEpoch int64, guards []store.ReleasePublicationGuard, at time.Time) error
	ScenarioTestEvidence(ctx context.Context, id string) (map[string]string, error)
}

type ReleaseRules struct {
	store     releaseRulesStore
	workspace *WorkspaceFiles
	inspector WorkspaceInspector
}

type releaseRulesStore interface {
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	LatestPublishedInLine(ctx context.Context, lineID string) (domain.ComponentRelease, error)
	ListReleasedDependencyLinks(ctx context.Context) ([]store.DependencyLink, error)
	ReadCatalogDefinitions(ctx context.Context) (store.CatalogValidationSnapshot, error)
	SuccessfulComponentEvidenceRunIDs(ctx context.Context, releaseID, digest string) (string, string, error)
	SuccessfulComponentEvolutionEvidenceRunID(ctx context.Context, releaseID, digest string) (string, error)
}

type RollbackPlanner struct {
	actions           actionsPort
	store             rollbackStore
	workspaceVerifier workspaceVerifierPort
	inspector         WorkspaceInspector
}

type rollbackStore interface {
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetEnvironmentComponentInstallationForNode(ctx context.Context, environmentID, componentID, nodeID string) (domain.EnvironmentComponentInstallation, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	HasSuccessfulActionTest(ctx context.Context, releaseID, spec, actionID, core, python string) (bool, error)
	IsActionReceiptRecovered(ctx context.Context, receipt store.ActionExecutionReceipt) (bool, error)
	LatestActionReceiptForNode(ctx context.Context, environmentID, componentID, releaseID, nodeID string) (store.ActionExecutionReceipt, error)
	LatestEnvironmentActionReceipts(ctx context.Context, environmentID string) ([]store.ActionExecutionReceipt, error)
	ListEnvironmentComponentInstallations(ctx context.Context, environmentID string) ([]domain.EnvironmentComponentInstallation, error)
}

type RunArchives struct {
	archiveRoot string
	hub         *EventHub
	rootCtx     context.Context
	store       archivesStore
}

type archivesStore interface {
	AppendAudit(ctx context.Context, e domain.AuditEvent) error
	ClaimRunArchive(ctx context.Context, token string, now time.Time) (domain.RunArchive, error)
	CleanupRuns(ctx context.Context, ids []string, actor, source string, now time.Time) error
	CompleteRunArchive(ctx context.Context, a domain.RunArchive, b store.ArchiveBoundary, now time.Time) error
	EnqueueRunArchives(ctx context.Context, ids []string, source, actor string, now time.Time) ([]domain.RunArchive, error)
	ExportRunArchive(ctx context.Context, id string, write func(string, []byte) error) (store.ArchiveBoundary, error)
	FailRunArchive(ctx context.Context, id, token, message string, now time.Time) error
	PreviewRunCleanup(ctx context.Context, ids []string, now time.Time) ([]domain.RunCleanupPreview, error)
	ProtectedArchivePaths(ctx context.Context) (map[string]bool, error)
	RecordRetentionScan(ctx context.Context, now time.Time, result string) error
	RenewRunArchive(ctx context.Context, id, token string, now time.Time) error
	RetentionCandidates(ctx context.Context, status string, before time.Time, afterTime, afterID string, limit int) ([]string, string, string, error)
	RetentionCursor(ctx context.Context, status string) (string, string, error)
	RunArchiveBytes(ctx context.Context, id string) (int64, error)
	RunRetentionPolicy(ctx context.Context) (domain.RunRetentionPolicy, error)
	SaveRetentionCursor(ctx context.Context, status, at, id string) error
}

type RunCreator struct {
	audit     auditPort
	hub       *EventHub
	scheduler schedulerPort
	store     creatorStore
}

type creatorStore interface {
	CreateRun(ctx context.Context, r domain.Run, approval *domain.Approval) error
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetScenarioInstallation(ctx context.Context, environmentID, scenarioID string) (domain.ScenarioInstallation, error)
	GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error)
	GetScenarioSubmission(ctx context.Context, userID, key, digest string) (domain.Run, error)
	NextRetryAttempt(ctx context.Context, retryRootRunID string) (int, error)
	SetScenarioRevisionStatus(ctx context.Context, id string, from []domain.RevisionStatus, to domain.RevisionStatus, at time.Time) error
}

type resourcePlanVerifierPort interface {
	verifyRunResources(context.Context, domain.Run, lockedPlan) error
}

type RunExecutor struct {
	resourceVerifier  resourcePlanVerifierPort
	control           *executionControl
	delivery          deliveryPort
	hub               *EventHub
	recorder          recorderPort
	rollback          rollbackPort
	rootCtx           context.Context
	store             executorStore
	workspaceVerifier workspaceVerifierPort
	jobs              JobBackend
}

type executorStore interface {
	GetEnvironmentRevision(ctx context.Context, id string) (domain.EnvironmentRevision, error)
	SaveRunJob(ctx context.Context, runID, digest string, bundle []byte) error
	SetRunJobExitCode(ctx context.Context, runID string, code *int) error
	ValidateRetryState(ctx context.Context, run domain.Run) error
	ValidateScenarioExecutionBaseline(ctx context.Context, run domain.Run) error
}

type RunScheduler struct {
	executor        executorPort
	hub             *EventHub
	mu              sync.Mutex
	nextWorkerToken uint64
	rootCtx         context.Context
	store           schedulerStore
	workers         map[string]environmentWorkerState
}

type schedulerStore interface {
	ClaimNextRun(ctx context.Context, environmentID string, at time.Time) (domain.Run, error)
	FailInvalidActiveRuns(ctx context.Context, at time.Time) (int64, error)
	HasRunningRun(ctx context.Context, environmentID string) (bool, error)
	ListQueuedEnvironmentIDs(ctx context.Context) ([]string, error)
	MarkRunningInterrupted(ctx context.Context, at time.Time) (int64, error)
}

type ScenarioService struct {
	audit        auditPort
	catalogRules catalogRulesPort
	hub          *EventHub
	publication  publicationPort
	releaseRules *ReleaseRules
	store        scenariosStore
	workspace    *WorkspaceFiles
}

type scenariosStore interface {
	AbandonScenarioRevision(ctx context.Context, id string, at time.Time) (string, error)
	CreateScenario(ctx context.Context, sc domain.Scenario, rev domain.ScenarioRevision) error
	CreateScenarioRevisionFromSource(ctx context.Context, sourceRevisionID string, r domain.ScenarioRevision) error
	HasActiveScenarioRevisionRun(ctx context.Context, revisionID string) (bool, error)
	DeleteScenario(ctx context.Context, scenarioID string, audit domain.AuditEvent) error
	DeprecateScenarioRevision(ctx context.Context, id string, at time.Time) error
	GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error)
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetScenario(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error)
	GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error)
	GetUser(ctx context.Context, id string) (domain.User, error)
	ListReleaseDisplayMetadata(ctx context.Context) (map[string]store.ReleaseDisplayMetadata, error)
	ListScenarios(ctx context.Context, viewer domain.User) ([]domain.Scenario, error)
	NextScenarioRevision(ctx context.Context, scenarioID string) (int, error)
	SaveScenarioRevisionDefinition(ctx context.Context, revision domain.ScenarioRevision, expectedDigest string) error
	ScenarioDeletionImpact(ctx context.Context, scenarioID string) (store.ScenarioDeletionImpact, error)
	ScenarioTestEvidence(ctx context.Context, id string) (map[string]string, error)
	SuccessfulScenarioSourceRun(ctx context.Context, revision domain.ScenarioRevision, runID string) (domain.Run, error)
}

type WorkspaceFiles struct {
	mu   sync.Mutex
	root string
}

type WorkspaceVerifier struct {
	releaseRules *ReleaseRules
	scenarios    scenariosPort
	store        workspaceVerifierStore
	inspector    WorkspaceInspector
}

type workspaceVerifierStore interface {
	GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error)
	GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error)
}

type actionsPort interface {
	expandActionSteps(ctx context.Context, steps []lockedStep) ([]lockedStep, error)
	lockAction(component domain.Component, nodeID string, release domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) (lockedStep, error)
}

type approvalsPort interface {
	BatchDecideApprovals(ctx context.Context, user domain.User, approvalIDs []string, decision, reason string) ([]domain.Run, error)
	CancelRun(ctx context.Context, user domain.User, runID string) (domain.Run, error)
	DecideApproval(ctx context.Context, user domain.User, approvalID, decision, reason string, deliveryDecisions []DeliveryDecisionInput) (domain.Run, error)
}

type auditPort interface {
	Record(ctx context.Context, actor domain.User, action, resourceType, resourceID string, metadata map[string]any)
}

type catalogRulesPort interface {
	platformOptionLookup(ctx context.Context) (domain.CatalogOptions, error)
	validateEnvironmentConstraintRetiredReferences(ctx context.Context, constraints, previous map[string]any) error
	validateEnvironmentFactRetiredReferences(ctx context.Context, facts, previous map[string]any) error
	validateEnvironmentFactsCatalog(ctx context.Context, facts map[string]any, requireComplete bool) error
	validateEnvironmentInventoryCatalog(ctx context.Context, inventory json.RawMessage) error
	validateHostGroupCatalog(ctx context.Context, value string) error
}

type catalogPort interface {
	WorkbenchReadiness(context.Context, []domain.Component) ([]domain.Component, error)
	ListComponents(ctx context.Context, user domain.User) ([]domain.Component, error)
	PublicationImpact(ctx context.Context, user domain.User, releaseID string) (domain.ImpactReport, error)
}

type deliveryPort interface {
	verifyLockedMedia(ctx context.Context, plan lockedPlan) error
	bindComponentArtifacts(ctx context.Context, revision domain.EnvironmentRevision, plan *lockedPlan) error
	bindComponentImages(ctx context.Context, revision domain.EnvironmentRevision, plan *lockedPlan) error
	finalizeDeliveryPlan(ctx context.Context, plan lockedPlan, inputs []DeliveryDecisionInput, user domain.User, at time.Time) (lockedPlan, error)
	mirrorRunArtifacts(ctx context.Context, runID string, plan *lockedPlan) error
	mirrorRunImages(ctx context.Context, runID string, plan *lockedPlan) error
}

type environmentsPort interface {
	List(ctx context.Context, user domain.User, includeArchived ...bool) ([]domain.Environment, error)
}

type recorderPort interface {
	beginStep(ctx context.Context, run domain.Run, locked lockedStep, result ansiblerunner.JobStepResult) (domain.RunStep, error)
	completeStep(ctx context.Context, run domain.Run, locked lockedStep, parents []lockedStep, step domain.RunStep, result ansiblerunner.JobStepResult) (domain.RunStep, error)
	finishRun(run domain.Run, status domain.RunStatus, cause error)
	markMutation(ctx context.Context, run domain.Run) error
	recordEvent(ctx context.Context, runID, stepID string, event ansiblerunner.JobEvent) error
	recordFailedSteps(stages []ansiblerunner.JobStepResult, active map[string]domain.RunStep)
	recordOutput(runID, stepID string, event ansiblerunner.LogEvent) error
}

type plannerPort interface {
	componentTestPlanDTO(ctx context.Context, environment domain.Environment, plan lockedPlan, digest string, destructive bool) ComponentTestPlan
	environmentRollbackPlanDTO(ctx context.Context, prepared preparedEnvironmentRollback, plan lockedPlan, digest string, destructive bool) EnvironmentRollbackPlan
	prepareComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (preparedComponentTest, error)
	prepareEnvironmentRollback(ctx context.Context, user domain.User, environmentID string, nodes ...string) (preparedEnvironmentRollback, error)
	prepareLockedPlan(ctx context.Context, environment domain.Environment, kind domain.RunKind, runID string, capturedAt time.Time, steps []lockedStep) (lockedPlan, string, bool, error)
	prepareScenarioLifecycle(ctx context.Context, user domain.User, id string, input ScenarioExecutionRequest, kind domain.RunKind, runID string, at time.Time) (scenarioExecution, error)
}

type publicationPort interface {
	requestPublicationBackup(reason string)
}

type releasesPort interface {
	scenarioRunDefinitionCurrent(ctx context.Context, run domain.Run, revision domain.ScenarioRevision) (bool, error)
}

type rollbackPort interface {
	bindRollbackCheckSources(ctx context.Context, environmentID string, steps []lockedStep) error
	addRecoverySteps(ctx context.Context, job *ansiblerunner.JobPlan, plan lockedPlan) error
	bindBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error
	recoveryBaselines(ctx context.Context, environmentID string) ([]domain.EnvironmentComponentInstallation, error)
	validateCurrentInstallEvidence(ctx context.Context, installation domain.EnvironmentComponentInstallation) error
	validateLockedRollbackPlan(ctx context.Context, run domain.Run, plan lockedPlan) error
}

type creatorPort interface {
	createRetryRun(ctx context.Context, user domain.User, source domain.Run, locked lockedPlan, preview RunRetryPlan, runID string, now time.Time) (domain.Run, error)
	createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, prepared lockedRunPreparation, resolvedParametersByNode map[string]map[string]resolvedParameter, expectedScenarioDigest ...string) (domain.Run, error)
	createScenarioRun(ctx context.Context, user domain.User, id string, input ScenarioExecutionRequest, kind domain.RunKind, runID string, at time.Time, x scenarioExecution, requestDigest string) (domain.Run, error)
}

type executorPort interface {
	executeRun(run domain.Run)
}

type schedulerPort interface {
	schedule(environmentID string)
}

type scenariosPort interface {
	List(ctx context.Context, user domain.User) ([]domain.Scenario, error)
	Validate(ctx context.Context, user domain.User, revisionID string) ([]domain.ValidationIssue, error)
	normalizeScenarioAcceptance(ctx context.Context, revision *domain.ScenarioRevision) error
	ownedScenarioRevision(ctx context.Context, user domain.User, revisionID string) (domain.ScenarioRevision, domain.Scenario, error)
	validateScenarioAcceptanceWorkspace(revision domain.ScenarioRevision) error
}

type workspaceVerifierPort interface {
	bindVerifiedWorkspaceDigests(ctx context.Context, steps []lockedStep) error
	verifyLockedWorkspaceDigests(ctx context.Context, locked []lockedStep) error
	verifyScenarioAcceptanceStep(ctx context.Context, step *lockedStep) error
}

type executionControl struct {
	mu     sync.Mutex
	active map[string]context.CancelFunc
}
