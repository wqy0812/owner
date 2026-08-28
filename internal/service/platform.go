package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type Platform struct {
	store            *store.Store
	runner           Runner
	artifactDelivery ArtifactDelivery
	imageDelivery    ImageDelivery
	hub              *EventHub
	playbookRoot     string
	imageBuildRoot   string
	dockerBinary     string
	dialContext      func(context.Context, string, string) (net.Conn, error)

	rootCtx         context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	workers         map[string]environmentWorkerState
	nextWorkerToken uint64
	active          map[string]context.CancelFunc

	identity           *IdentityService
	catalog            *CatalogService
	scenarios          *ScenarioService
	environments       *EnvironmentService
	execution          *ExecutionService
	readModel          *ReadModelService
	releaseCoordinator *ReleaseCoordinator
	planBuilder        *PlanBuilder
	runCreator         *RunCreator
	runScheduler       *RunScheduler
	runExecutor        *RunExecutor
	lifecycleRecorder  *LifecycleRecorder
	rollbackPlanner    *RollbackPlanner
	approvals          *ApprovalService
}

type environmentWorkerState struct {
	token     uint64
	heartbeat time.Time
}

func NewPlatform(database *store.Store, runner Runner, hub *EventHub) *Platform {
	if hub == nil {
		hub = NewEventHub()
	}
	ctx, cancel := context.WithCancel(context.Background())
	platform := &Platform{
		store: database, runner: runner, hub: hub,
		artifactDelivery: NewHTTPArtifactDelivery(nil), imageDelivery: NewDockerImageDelivery("docker"),
		dialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		rootCtx:     ctx, cancel: cancel,
		workers: make(map[string]environmentWorkerState), active: make(map[string]context.CancelFunc),
	}
	platform.identity = &IdentityService{store: database}
	platform.catalog = &CatalogService{platform: platform, store: database}
	platform.scenarios = &ScenarioService{platform: platform, store: database}
	platform.environments = &EnvironmentService{platform: platform, store: database}
	platform.execution = &ExecutionService{platform: platform, store: database}
	platform.readModel = &ReadModelService{platform: platform, store: database}
	platform.releaseCoordinator = &ReleaseCoordinator{platform: platform, store: database}
	platform.planBuilder = &PlanBuilder{platform: platform}
	platform.runCreator = &RunCreator{platform: platform}
	platform.runScheduler = &RunScheduler{platform: platform}
	platform.runExecutor = &RunExecutor{platform: platform}
	platform.lifecycleRecorder = &LifecycleRecorder{platform: platform}
	platform.rollbackPlanner = &RollbackPlanner{platform: platform}
	platform.approvals = &ApprovalService{platform: platform}
	return platform
}

func (p *Platform) ConfigureEnvironmentHealthDialer(dial func(context.Context, string, string) (net.Conn, error)) {
	if dial != nil {
		p.dialContext = dial
	}
}

func (p *Platform) Hub() *EventHub { return p.hub }

// ConfigurePlaybookRoot enables owner-managed Playbooks below the same tree
// used by the Ansible runner. Each Draft receives its own directory so online
// edits cannot mutate a Playbook referenced by an immutable release.
func (p *Platform) ConfigurePlaybookRoot(root string) {
	p.playbookRoot = strings.TrimSpace(root)
}

func (p *Platform) ConfigureImageBuilder(buildRoot, dockerBinary string) {
	p.imageBuildRoot = strings.TrimSpace(buildRoot)
	p.dockerBinary = strings.TrimSpace(dockerBinary)
	p.imageDelivery = NewDockerImageDelivery(p.dockerBinary)
}

// ConfigureDeliveryAdapters installs the statically assembled delivery ports.
// Nil values preserve the current adapter, which keeps startup wiring explicit
// while allowing isolated acceptance tests to avoid external registries.
func (p *Platform) ConfigureDeliveryAdapters(artifact ArtifactDelivery, image ImageDelivery) {
	if artifact != nil {
		p.artifactDelivery = artifact
	}
	if image != nil {
		p.imageDelivery = image
	}
}

func (p *Platform) Start(ctx context.Context) error {
	if err := p.RecoverComponentImportFiles(ctx); err != nil {
		return fmt.Errorf("recover component import files: %w", err)
	}
	if err := p.store.MarkComponentImageBuildsInterrupted(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover interrupted image builds: %w", err)
	}
	if _, err := p.store.MarkRunningInterrupted(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	}
	if _, err := p.store.FailInvalidActiveRuns(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("reconcile invalid active runs: %w", err)
	}
	environments, err := p.store.ListQueuedEnvironmentIDs(ctx)
	if err != nil {
		return fmt.Errorf("list queued runs: %w", err)
	}
	for _, environmentID := range environments {
		p.schedule(environmentID)
	}
	go p.queueWatchdog()
	return nil
}

func (p *Platform) Close() {
	p.cancel()
	p.mu.Lock()
	for _, cancel := range p.active {
		cancel()
	}
	p.mu.Unlock()
}

func newID(prefix string) string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		panic(fmt.Sprintf("secure random id: %v", err))
	}
	return prefix + "-" + hex.EncodeToString(random)
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("%w: slug must contain lowercase letters, digits and single hyphens", domain.ErrInvalid)
	}
	return nil
}

func requireOwner(user domain.User, role domain.Role, ownerID string) error {
	if err := domain.ValidateRole(user, role); err != nil {
		return err
	}
	if user.ID != ownerID {
		return fmt.Errorf("%w: resource belongs to another owner", domain.ErrForbidden)
	}
	return nil
}

func (p *Platform) audit(ctx context.Context, actor domain.User, action, resourceType, resourceID string, metadata map[string]any) {
	event := newAuditEvent(actor, action, resourceType, resourceID, metadata)
	_ = p.store.AppendAudit(ctx, event)
}

func newAuditEvent(actor domain.User, action, resourceType, resourceID string, metadata map[string]any) domain.AuditEvent {
	safeMetadata := map[string]any{}
	if metadata != nil {
		safeMetadata = Redact(metadata).(map[string]any)
	}
	return domain.AuditEvent{
		ID: newID("audit"), ActorID: actor.ID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID,
		Metadata: safeMetadata, CreatedAt: time.Now().UTC(),
	}
}

func actionFor(release domain.ComponentRelease, kind domain.ActionKind) (domain.ActionDefinition, error) {
	for _, action := range release.Actions {
		if action.Kind == kind {
			return action, nil
		}
	}
	if kind == domain.ActionUpgrade {
		for _, action := range release.Actions {
			if action.Kind == domain.ActionInstall && action.Idempotent {
				action.Kind = domain.ActionUpgrade
				return action, nil
			}
		}
	}
	return domain.ActionDefinition{}, fmt.Errorf("%w: release %s has no %s action", domain.ErrInvalid, release.Version, kind)
}

func validateRelease(release domain.ComponentRelease) error {
	if strings.TrimSpace(release.Version) == "" {
		return fmt.Errorf("%w: version is required", domain.ErrInvalid)
	}
	if release.RiskLevel != "" && !validRiskLevel(release.RiskLevel) {
		return fmt.Errorf("%w: invalid release risk level %q", domain.ErrInvalid, release.RiskLevel)
	}
	if err := validateReleaseParameters(release); err != nil {
		return err
	}
	if err := rejectSensitiveMap(release.EnvironmentConstraints, "environment constraint"); err != nil {
		return err
	}
	seenDependencies := make(map[string]struct{}, len(release.Dependencies))
	for _, dependency := range release.Dependencies {
		if dependency.UpstreamComponentID == "" || dependency.UpstreamReleaseID == "" {
			return fmt.Errorf("%w: dependencies must lock a component and release", domain.ErrInvalid)
		}
		if dependency.UpstreamComponentID == release.ComponentID {
			return fmt.Errorf("%w: a release cannot depend on its own component", domain.ErrInvalid)
		}
		if _, exists := seenDependencies[dependency.UpstreamComponentID]; exists {
			return fmt.Errorf("%w: duplicate dependency on component %s", domain.ErrInvalid, dependency.UpstreamComponentID)
		}
		seenDependencies[dependency.UpstreamComponentID] = struct{}{}
	}
	for _, action := range release.Actions {
		if !validActionKind(action.Kind) {
			return fmt.Errorf("%w: invalid action kind %q", domain.ErrInvalid, action.Kind)
		}
		if strings.TrimSpace(action.Playbook) == "" {
			return fmt.Errorf("%w: every action requires a kind and relative playbook", domain.ErrInvalid)
		}
		if action.RiskLevel != "" && !validRiskLevel(action.RiskLevel) {
			return fmt.Errorf("%w: invalid action risk level %q", domain.ErrInvalid, action.RiskLevel)
		}
		if strings.HasPrefix(action.Playbook, "/") || strings.Contains(action.Playbook, "..") {
			return fmt.Errorf("%w: action playbook must be a safe relative path", domain.ErrInvalid)
		}
		if action.TimeoutSeconds <= 0 {
			return fmt.Errorf("%w: action timeout must be positive", domain.ErrInvalid)
		}
		if action.Kind == domain.ActionUpgrade && (action.FromReleaseID == "" || action.ToReleaseID == "") {
			return fmt.Errorf("%w: upgrade action must declare an explicit fromReleaseId and toReleaseId", domain.ErrInvalid)
		}
		if action.Idempotent && action.Kind != domain.ActionInstall {
			return fmt.Errorf("%w: idempotent upgrade reuse is only valid for install actions", domain.ErrInvalid)
		}
		if action.Kind == domain.ActionRollback && (action.FromReleaseID == "") != (action.ToReleaseID == "") {
			return fmt.Errorf("%w: rollback action must declare both fromReleaseId and toReleaseId, or leave both empty for an install rollback", domain.ErrInvalid)
		}
		for _, parameter := range action.AllowedParameters {
			if isSensitiveKey(parameter) {
				return fmt.Errorf("%w: sensitive action parameter %q must use a CredentialRef", domain.ErrInvalid, parameter)
			}
		}
		seenCredentials := map[string]struct{}{}
		for _, credential := range action.RequiredCredentials {
			credential = strings.TrimSpace(credential)
			if credential == "" {
				return fmt.Errorf("%w: required credential names must not be empty", domain.ErrInvalid)
			}
			if _, exists := seenCredentials[credential]; exists {
				return fmt.Errorf("%w: duplicate required credential %q", domain.ErrInvalid, credential)
			}
			seenCredentials[credential] = struct{}{}
		}
	}
	return nil
}

func validActionKind(kind domain.ActionKind) bool {
	switch kind {
	case domain.ActionInspect, domain.ActionPreflight, domain.ActionInstall, domain.ActionConfigure, domain.ActionVerify, domain.ActionUpgrade, domain.ActionRollback, domain.ActionUninstall:
		return true
	default:
		return false
	}
}

func validRiskLevel(level domain.RiskLevel) bool {
	switch level {
	case domain.RiskLow, domain.RiskMedium, domain.RiskHigh, domain.RiskDestructive:
		return true
	default:
		return false
	}
}

func errorIsNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
