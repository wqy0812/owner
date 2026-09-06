package service

import (
	"context"
	"net"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/sshcheck"
	"codex/platform-demo/internal/store"
)

// Platform is the composition root. Business objects never retain it.
type Platform struct {
	preparations      *PreparationService
	hub               *EventHub
	cancel            context.CancelFunc
	actions           *ActionPlanner
	approvals         *ApprovalService
	audit             *AuditRecorder
	catalogRules      *CatalogRules
	catalog           *CatalogService
	delivery          *DeliveryService
	environments      *EnvironmentService
	execution         *ExecutionService
	identity          *IdentityService
	recorder          *LifecycleRecorder
	planner           *PlanBuilder
	platformOptions   *PlatformOptionService
	publication       *PublicationBackup
	readModel         *ReadModelService
	releases          *ReleaseCoordinator
	releaseRules      *ReleaseRules
	rollback          *RollbackPlanner
	archives          *RunArchives
	creator           *RunCreator
	executor          *RunExecutor
	scheduler         *RunScheduler
	scenarios         *ScenarioService
	workspace         *WorkspaceFiles
	workspaceVerifier *WorkspaceVerifier
}
type EnvironmentSSHChecker interface {
	Check(context.Context, sshcheck.Request) error
	Probe(context.Context, sshcheck.Request, sshcheck.Probe) (sshcheck.ProbeResult, error)
}
type PublicationBackupRequester interface{ Request(string) }
type PublicationBackupHealthProvider interface {
	CatalogBackupHealth(context.Context) (domain.CatalogBackupHealth, error)
}

func NewPlatform(database *store.Store, runners RunnerDependencies, hub *EventHub) (*Platform, error) {
	if err := runners.validate(); err != nil {
		return nil, err
	}
	if hub == nil {
		hub = NewEventHub()
	}
	ctx, cancel := context.WithCancel(context.Background())
	control := &executionControl{active: make(map[string]context.CancelFunc)}
	artifact := NewHTTPArtifactDelivery(nil)
	image := NewDockerImageDelivery("docker")
	actions := &ActionPlanner{}
	approvals := &ApprovalService{}
	audit := &AuditRecorder{}
	catalogRules := &CatalogRules{}
	catalog := &CatalogService{}
	delivery := &DeliveryService{}
	environments := &EnvironmentService{}
	execution := &ExecutionService{}
	identity := &IdentityService{}
	recorder := &LifecycleRecorder{}
	planner := &PlanBuilder{}
	platformOptions := &PlatformOptionService{}
	publication := &PublicationBackup{}
	readModel := &ReadModelService{}
	releases := &ReleaseCoordinator{}
	releaseRules := &ReleaseRules{}
	rollback := &RollbackPlanner{}
	archives := &RunArchives{}
	creator := &RunCreator{}
	executor := &RunExecutor{}
	scheduler := &RunScheduler{}
	scenarios := &ScenarioService{}
	workspace := &WorkspaceFiles{}
	workspaceVerifier := &WorkspaceVerifier{}
	actions.store = database
	approvals.audit = audit
	approvals.control = control
	approvals.delivery = delivery
	approvals.hub = hub
	approvals.scheduler = scheduler
	approvals.store = database
	audit.store = database
	catalogRules.store = database
	catalog.artifactDelivery = artifact
	catalog.audit = audit
	catalog.catalogRules = catalogRules
	catalog.hub = hub
	catalog.imageDelivery = image
	catalog.publication = publication
	catalog.releaseRules = releaseRules
	catalog.rootCtx = ctx
	catalog.inspector = runners.Workspaces
	catalog.store = database
	catalog.workspace = workspace
	delivery.artifactDelivery = artifact
	delivery.imageDelivery = image
	delivery.store = database
	environments.audit = audit
	environments.catalogRules = catalogRules
	environments.dialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	environments.hub = hub
	environments.store = database
	execution.approvals = approvals
	execution.archives = archives
	execution.audit = audit
	execution.creator = creator
	execution.delivery = delivery
	execution.hub = hub
	execution.planner = planner
	execution.rollback = rollback
	execution.inspector = runners.Workspaces
	execution.jobs = runners.Jobs
	execution.scheduler = scheduler
	execution.store = database
	execution.workspace = workspace
	execution.workspaceVerifier = workspaceVerifier
	identity.store = database
	recorder.audit = audit
	recorder.hub = hub
	recorder.store = database
	planner.actions = actions
	planner.catalogRules = catalogRules
	planner.delivery = delivery
	planner.rollback = rollback
	planner.runtime = runners.Runtime
	planner.scenarios = scenarios
	planner.store = database
	planner.workspaceVerifier = workspaceVerifier
	platformOptions.hub = hub
	platformOptions.store = database
	readModel.catalog = catalog
	readModel.environments = environments
	readModel.releases = releases
	readModel.scenarios = scenarios
	readModel.store = database
	releases.audit = audit
	releases.catalog = catalog
	releases.hub = hub
	releases.publication = publication
	releases.releaseRules = releaseRules
	releases.scenarios = scenarios
	releases.store = database
	releases.workspace = workspace
	releases.workspaceVerifier = workspaceVerifier
	releaseRules.inspector = runners.Workspaces
	releaseRules.store = database
	releaseRules.workspace = workspace
	rollback.actions = actions
	rollback.inspector = runners.Workspaces
	rollback.store = database
	rollback.workspaceVerifier = workspaceVerifier
	archives.hub = hub
	archives.rootCtx = ctx
	archives.store = database
	creator.audit = audit
	creator.hub = hub
	creator.scheduler = scheduler
	creator.store = database
	executor.control = control
	executor.delivery = delivery
	executor.hub = hub
	executor.recorder = recorder
	executor.rollback = rollback
	executor.rootCtx = ctx
	executor.jobs = runners.Jobs
	executor.store = database
	executor.resourceVerifier = planner
	executor.workspaceVerifier = workspaceVerifier
	scheduler.executor = executor
	scheduler.hub = hub
	scheduler.rootCtx = ctx
	scheduler.store = database
	scheduler.workers = make(map[string]environmentWorkerState)
	scenarios.audit = audit
	scenarios.catalogRules = catalogRules
	scenarios.hub = hub
	scenarios.publication = publication
	scenarios.releaseRules = releaseRules
	scenarios.store = database
	scenarios.workspace = workspace
	workspaceVerifier.releaseRules = releaseRules
	workspaceVerifier.inspector = runners.Workspaces
	workspaceVerifier.scenarios = scenarios
	workspaceVerifier.store = database
	preparations := &PreparationService{store: database, execution: execution, environments: environments, runtime: runners.Runtime, hub: hub, root: ctx, active: map[string]context.CancelFunc{}, workers: make(chan struct{}, 2)}
	return &Platform{preparations: preparations,
		hub:               hub,
		cancel:            cancel,
		actions:           actions,
		approvals:         approvals,
		audit:             audit,
		catalogRules:      catalogRules,
		catalog:           catalog,
		delivery:          delivery,
		environments:      environments,
		execution:         execution,
		identity:          identity,
		recorder:          recorder,
		planner:           planner,
		platformOptions:   platformOptions,
		publication:       publication,
		readModel:         readModel,
		releases:          releases,
		releaseRules:      releaseRules,
		rollback:          rollback,
		archives:          archives,
		creator:           creator,
		executor:          executor,
		scheduler:         scheduler,
		scenarios:         scenarios,
		workspace:         workspace,
		workspaceVerifier: workspaceVerifier,
	}, nil
}

func (p *Platform) ConfigureEnvironmentSSHChecker(checker EnvironmentSSHChecker, knownHostsPath string) {
	p.environments.sshChecker = checker
	p.environments.sshKnownHostsPath = strings.TrimSpace(knownHostsPath)
}
func (p *Platform) ConfigureEnvironmentHealthDialer(dial func(context.Context, string, string) (net.Conn, error)) {
	if dial != nil {
		p.environments.dialContext = dial
	}
}
func (p *Platform) Hub() *EventHub                    { return p.hub }
func (p *Platform) Audit() *AuditRecorder             { return p.audit }
func (p *Platform) ConfigurePlaybookRoot(root string) { p.workspace.root = strings.TrimSpace(root) }
func (p *Platform) ConfigureImageBuilder(root, binary string) {
	p.catalog.imageBuildRoot = strings.TrimSpace(root)
	p.catalog.dockerBinary = strings.TrimSpace(binary)
	image := NewDockerImageDelivery(p.catalog.dockerBinary)
	p.catalog.imageDelivery = image
	p.delivery.imageDelivery = image
}
func (p *Platform) ConfigureDeliveryAdapters(artifact ArtifactDelivery, image ImageDelivery) {
	if artifact != nil {
		p.catalog.artifactDelivery = artifact
		p.delivery.artifactDelivery = artifact
	}
	if image != nil {
		p.catalog.imageDelivery = image
		p.delivery.imageDelivery = image
	}
}
func (p *Platform) ConfigurePublicationBackup(requester PublicationBackupRequester) {
	p.publication.publicationBackup = requester
}
func (p *Platform) ConfigurePublicationBackupHealth(provider PublicationBackupHealthProvider) {
	p.readModel.publicationBackupHealth = provider
}
func (p *Platform) NotifyPublicationBackupStatus() {
	p.hub.Publish("catalog_backup.updated", map[string]any{"status": "changed"})
}
func (p *Platform) ConfigureRunArchives(root string) error { return p.archives.Configure(root) }
func (p *Platform) StartRunArchiveWorker()                 { p.archives.Start() }
func (p *Platform) Start(ctx context.Context) error {
	if err := p.catalog.recover(ctx); err != nil {
		return err
	}
	return p.scheduler.Start(ctx)
}
func (p *Platform) Close() { p.cancel(); p.executor.control.cancelAll() }

func (p *Platform) Identity() *IdentityService { return p.identity }

func (p *Platform) Catalog() *CatalogService { return p.catalog }

func (p *Platform) Scenarios() *ScenarioService { return p.scenarios }

func (p *Platform) Environments() *EnvironmentService { return p.environments }

func (p *Platform) Execution() *ExecutionService { return p.execution }

func (p *Platform) ReadModel() *ReadModelService { return p.readModel }

func (p *Platform) Releases() *ReleaseCoordinator { return p.releases }

func (p *Platform) PlatformOptions() *PlatformOptionService { return p.platformOptions }

func (p *Platform) Preparations() *PreparationService { return p.preparations }
