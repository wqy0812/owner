package service

import (
	"context"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

// The application services deliberately depend on narrow Store ports. The
// concrete SQLite Store implements all ports at assembly time, while unit tests
// can provide module-sized substitutes.
type identityStore interface {
	ListUsers(context.Context) ([]domain.User, error)
	UserBySession(context.Context, string) (domain.User, error)
	GetUser(context.Context, string) (domain.User, error)
	CreateSession(context.Context, string, string, time.Time) error
}

type catalogStore interface {
	GetComponentRelease(context.Context, string) (domain.ComponentRelease, error)
	GetComponent(context.Context, string, bool) (domain.Component, error)
	GetUser(context.Context, string) (domain.User, error)
	ListReleaseDisplayMetadata(context.Context) (map[string]store.ReleaseDisplayMetadata, error)
	SubmitComponentReleaseReview(context.Context, string, string, int64, time.Time) error
	DecideComponentReleaseReview(context.Context, string, domain.ReleaseReviewStatus, string, string, string, time.Time) error
}

type scenarioStore interface {
	GetScenarioRevision(context.Context, string) (domain.ScenarioRevision, error)
	GetScenario(context.Context, string, bool) (domain.Scenario, error)
	GetUser(context.Context, string) (domain.User, error)
	ListReleaseDisplayMetadata(context.Context) (map[string]store.ReleaseDisplayMetadata, error)
}

type environmentStore interface {
	GetEnvironment(context.Context, string, bool) (domain.Environment, error)
	GetUser(context.Context, string) (domain.User, error)
	GetActiveEnvironmentRun(context.Context, string) (store.ActiveEnvironmentRun, error)
	ListAudit(context.Context, int) ([]domain.AuditEvent, error)
	LatestEnvironmentHealthCheck(context.Context, string) (domain.EnvironmentHealthCheck, error)
	LatestEnvironmentSSHCheck(context.Context, string) (domain.EnvironmentSSHCheck, error)
}

type executionStore interface {
	ListRuns(context.Context, domain.User) ([]domain.Run, error)
	ListRunsForComponentRelease(context.Context, string) ([]domain.Run, error)
	ListRunSteps(context.Context, string) ([]domain.RunStep, error)
	GetApprovalByRun(context.Context, string) (domain.Approval, error)
	GetRun(context.Context, string) (domain.Run, error)
	CanViewRun(context.Context, domain.User, string) (bool, error)
	GetEnvironment(context.Context, string, bool) (domain.Environment, error)
	GetUser(context.Context, string) (domain.User, error)
	GetComponentRelease(context.Context, string) (domain.ComponentRelease, error)
	GetComponent(context.Context, string, bool) (domain.Component, error)
	GetScenarioRevision(context.Context, string) (domain.ScenarioRevision, error)
	GetScenario(context.Context, string, bool) (domain.Scenario, error)
	ListRunLogTail(context.Context, string, int) ([]domain.RunLog, error)
	QueuedRunPosition(context.Context, string, time.Time) (int, error)
}

type readModelStore interface {
	ListNotifications(context.Context, string, bool) ([]domain.Notification, error)
	MarkNotificationRead(context.Context, string, string) error
	GetNotification(context.Context, string) (domain.Notification, error)
}

type platformOptionStore interface {
	ListPlatformOptionCategories(context.Context) ([]domain.PlatformOptionCategory, error)
	CreatePlatformOptionCategory(context.Context, domain.PlatformOptionCategory, domain.AuditEvent) error
	RenamePlatformOptionCategory(context.Context, string, string, domain.AuditEvent) error
	CreatePlatformOption(context.Context, domain.PlatformOption, domain.AuditEvent) error
	RenamePlatformOption(context.Context, string, string, domain.AuditEvent) error
	SetPlatformOptionCategoryRetired(context.Context, string, *time.Time, domain.AuditEvent) error
	SetPlatformOptionRetired(context.Context, string, *time.Time, domain.AuditEvent) error
	DeletePlatformOptionCategory(context.Context, string, domain.AuditEvent) error
	DeletePlatformOption(context.Context, string, domain.AuditEvent) error
	ListEnvironmentVariableDefinitions(context.Context) ([]domain.EnvironmentVariableDefinition, error)
	CreateEnvironmentVariableDefinition(context.Context, domain.EnvironmentVariableDefinition, domain.AuditEvent) error
	DeleteEnvironmentVariableDefinition(context.Context, string, domain.AuditEvent) error
}

type IdentityService struct{ store identityStore }
type CatalogService struct {
	platform *Platform
	store    catalogStore
}
type ScenarioService struct {
	platform *Platform
	store    scenarioStore
}
type EnvironmentService struct {
	platform *Platform
	store    environmentStore
}
type ExecutionService struct {
	platform *Platform
	store    executionStore
}
type ReadModelService struct {
	platform *Platform
	store    readModelStore
}
type PlatformOptionService struct {
	platform *Platform
	store    platformOptionStore
}

func (p *Platform) Identity() *IdentityService              { return p.identity }
func (p *Platform) Catalog() *CatalogService                { return p.catalog }
func (p *Platform) Scenarios() *ScenarioService             { return p.scenarios }
func (p *Platform) Environments() *EnvironmentService       { return p.environments }
func (p *Platform) Execution() *ExecutionService            { return p.execution }
func (p *Platform) ReadModel() *ReadModelService            { return p.readModel }
func (p *Platform) Releases() *ReleaseCoordinator           { return p.releaseCoordinator }
func (p *Platform) PlatformOptions() *PlatformOptionService { return p.platformOptions }

func (s *IdentityService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.store.ListUsers(ctx)
}
func (s *IdentityService) UserBySession(ctx context.Context, token string) (domain.User, error) {
	return s.store.UserBySession(ctx, token)
}
func (s *IdentityService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}
func (s *IdentityService) CreateSession(ctx context.Context, token, userID string, expires time.Time) error {
	return s.store.CreateSession(ctx, token, userID, expires)
}

func (s *CatalogService) GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error) {
	release, err := s.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, err
	}
	return s.platform.decorateReleaseReadiness(ctx, release)
}
func (s *CatalogService) GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error) {
	component, err := s.store.GetComponent(ctx, id, includeReleases)
	if err != nil || !includeReleases {
		return component, err
	}
	return s.platform.decorateComponentReadiness(ctx, component)
}
func (s *CatalogService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}
func (s *CatalogService) ListReleaseDisplayMetadata(ctx context.Context) (map[string]store.ReleaseDisplayMetadata, error) {
	return s.store.ListReleaseDisplayMetadata(ctx)
}

func (s *ScenarioService) GetRevision(ctx context.Context, id string) (domain.ScenarioRevision, error) {
	return s.store.GetScenarioRevision(ctx, id)
}
func (s *ScenarioService) GetScenarioRecord(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error) {
	return s.store.GetScenario(ctx, id, includeRevisions)
}
func (s *ScenarioService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}
func (s *ScenarioService) ListReleaseDisplayMetadata(ctx context.Context) (map[string]store.ReleaseDisplayMetadata, error) {
	return s.store.ListReleaseDisplayMetadata(ctx)
}

func (s *EnvironmentService) GetEnvironmentRecord(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error) {
	return s.store.GetEnvironment(ctx, id, includeRevisions)
}
func (s *EnvironmentService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}
func (s *EnvironmentService) ActiveRun(ctx context.Context, environmentID string) (store.ActiveEnvironmentRun, error) {
	return s.store.GetActiveEnvironmentRun(ctx, environmentID)
}
func (s *EnvironmentService) ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	return s.store.ListAudit(ctx, limit)
}
func (s *EnvironmentService) LatestHealthCheck(ctx context.Context, environmentID string) (domain.EnvironmentHealthCheck, error) {
	return s.store.LatestEnvironmentHealthCheck(ctx, environmentID)
}
func (s *EnvironmentService) LatestSSHCheck(ctx context.Context, environmentID string) (domain.EnvironmentSSHCheck, error) {
	return s.store.LatestEnvironmentSSHCheck(ctx, environmentID)
}

func (s *ExecutionService) ListRuns(ctx context.Context, user domain.User) ([]domain.Run, error) {
	return s.store.ListRuns(ctx, user)
}
func (s *ExecutionService) ListRunsForComponentRelease(ctx context.Context, user domain.User, releaseID string) ([]domain.Run, error) {
	release, err := s.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	component, err := s.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return nil, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return nil, err
	}
	return s.store.ListRunsForComponentRelease(ctx, releaseID)
}
func (s *ExecutionService) ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error) {
	return s.store.ListRunSteps(ctx, runID)
}
func (s *ExecutionService) GetApprovalByRun(ctx context.Context, runID string) (domain.Approval, error) {
	return s.store.GetApprovalByRun(ctx, runID)
}
func (s *ExecutionService) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return s.store.GetRun(ctx, id)
}
func (s *ExecutionService) CanViewRun(ctx context.Context, user domain.User, id string) (bool, error) {
	return s.store.CanViewRun(ctx, user, id)
}
func (s *ExecutionService) GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error) {
	return s.store.GetEnvironment(ctx, id, includeRevisions)
}
func (s *ExecutionService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}
func (s *ExecutionService) GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error) {
	return s.store.GetComponentRelease(ctx, id)
}
func (s *ExecutionService) GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error) {
	return s.store.GetComponent(ctx, id, includeReleases)
}
func (s *ExecutionService) GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error) {
	return s.store.GetScenarioRevision(ctx, id)
}
func (s *ExecutionService) GetScenario(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error) {
	return s.store.GetScenario(ctx, id, includeRevisions)
}
func (s *ExecutionService) ListRunLogTail(ctx context.Context, runID string, limit int) ([]domain.RunLog, error) {
	return s.store.ListRunLogTail(ctx, runID, limit)
}
func (s *ExecutionService) QueuedRunPosition(ctx context.Context, environmentID string, createdAt time.Time) (int, error) {
	return s.store.QueuedRunPosition(ctx, environmentID, createdAt)
}

func (s *ReadModelService) ListNotifications(ctx context.Context, userID string, unreadOnly bool) ([]domain.Notification, error) {
	return s.store.ListNotifications(ctx, userID, unreadOnly)
}
func (s *ReadModelService) MarkNotificationRead(ctx context.Context, id, userID string) error {
	return s.store.MarkNotificationRead(ctx, id, userID)
}
func (s *ReadModelService) GetNotification(ctx context.Context, id string) (domain.Notification, error) {
	return s.store.GetNotification(ctx, id)
}
