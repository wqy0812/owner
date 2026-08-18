package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type Runner interface {
	Run(context.Context, ansiblerunner.Request) (ansiblerunner.Result, error)
}

type Platform struct {
	store  *store.Store
	runner Runner
	hub    *EventHub

	rootCtx context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	workers map[string]struct{}
	active  map[string]context.CancelFunc
}

func NewPlatform(database *store.Store, runner Runner, hub *EventHub) *Platform {
	if hub == nil {
		hub = NewEventHub()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Platform{
		store: database, runner: runner, hub: hub,
		rootCtx: ctx, cancel: cancel,
		workers: make(map[string]struct{}), active: make(map[string]context.CancelFunc),
	}
}

func (p *Platform) Store() *store.Store { return p.store }
func (p *Platform) Hub() *EventHub      { return p.hub }

func (p *Platform) Start(ctx context.Context) error {
	if _, err := p.store.MarkRunningInterrupted(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	}
	environments, err := p.store.ListQueuedEnvironmentIDs(ctx)
	if err != nil {
		return fmt.Errorf("list queued runs: %w", err)
	}
	for _, environmentID := range environments {
		p.schedule(environmentID)
	}
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
	safeMetadata := map[string]any{}
	if metadata != nil {
		safeMetadata = Redact(metadata).(map[string]any)
	}
	event := domain.AuditEvent{
		ID: newID("audit"), ActorID: actor.ID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID,
		Metadata: safeMetadata, CreatedAt: time.Now().UTC(),
	}
	_ = p.store.AppendAudit(ctx, event)
}

func actionFor(release domain.ComponentRelease, kind domain.ActionKind) (domain.ActionDefinition, error) {
	for _, action := range release.Actions {
		if action.Kind == kind {
			return action, nil
		}
	}
	return domain.ActionDefinition{}, fmt.Errorf("%w: release %s has no %s action", domain.ErrInvalid, release.Version, kind)
}

func validateRelease(release domain.ComponentRelease) error {
	if strings.TrimSpace(release.Version) == "" {
		return fmt.Errorf("%w: version is required", domain.ErrInvalid)
	}
	if release.Type != domain.ReleaseAtomic && release.Type != domain.ReleaseBundle {
		return fmt.Errorf("%w: release type must be atomic or bundle", domain.ErrInvalid)
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
		if action.Kind == "" || strings.TrimSpace(action.Playbook) == "" {
			return fmt.Errorf("%w: every action requires a kind and relative playbook", domain.ErrInvalid)
		}
		if strings.HasPrefix(action.Playbook, "/") || strings.Contains(action.Playbook, "..") {
			return fmt.Errorf("%w: action playbook must be a safe relative path", domain.ErrInvalid)
		}
		if action.TimeoutSeconds <= 0 {
			return fmt.Errorf("%w: action timeout must be positive", domain.ErrInvalid)
		}
		if (action.Kind == domain.ActionUpgrade || action.Kind == domain.ActionRollback) && (action.FromReleaseID == "" || action.ToReleaseID == "") {
			return fmt.Errorf("%w: %s action must declare an explicit fromReleaseId and toReleaseId", domain.ErrInvalid, action.Kind)
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

func errorIsNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
