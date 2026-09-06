package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

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

func (p *AuditRecorder) Record(ctx context.Context, actor domain.User, action, resourceType, resourceID string, metadata map[string]any) {
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
	// Focused domain tests may validate an unpersisted release definition. All
	// persisted creation paths assign a line before reaching the store.
	if (release.LineID == "") != (strings.TrimSpace(release.LineName) == "") {
		return fmt.Errorf("%w: release line id and name must be provided together", domain.ErrInvalid)
	}
	if release.Compatibility != "" && !release.Compatibility.Valid() {
		return fmt.Errorf("%w: invalid release compatibility %q", domain.ErrInvalid, release.Compatibility)
	}
	if release.ParentReleaseID == "" && release.Compatibility != "" && release.Compatibility != domain.CompatibilityNotApplicable {
		return fmt.Errorf("%w: a release-line baseline must use not_applicable compatibility", domain.ErrInvalid)
	}
	if release.ParentReleaseID != "" && release.Compatibility == domain.CompatibilityNotApplicable {
		return fmt.Errorf("%w: an evolution release must declare compatible or breaking", domain.ErrInvalid)
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
		if dependency.Kind != "" && dependency.Kind != domain.DependencyConfiguration {
			return fmt.Errorf("%w: invalid dependency kind", domain.ErrInvalid)
		}
		if dependency.Kind == domain.DependencyConfiguration && len(dependency.ParameterMappings) == 0 {
			return fmt.Errorf("%w: configuration references require parameter mappings", domain.ErrInvalid)
		}
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
	seenActionKinds := map[domain.ActionKind]bool{}
	for _, action := range release.Actions {
		if !validActionKind(action.Kind) {
			return fmt.Errorf("%w: invalid action kind %q", domain.ErrInvalid, action.Kind)
		}
		if strings.TrimSpace(action.Playbook) == "" {
			return fmt.Errorf("%w: every action requires a kind and relative playbook", domain.ErrInvalid)
		}
		if action.Kind != domain.ActionCheck && seenActionKinds[action.Kind] {
			return fmt.Errorf("%w: every release action kind must be unique", domain.ErrInvalid)
		}
		seenActionKinds[action.Kind] = true
		if action.Kind != domain.ActionCheck && strings.TrimSpace(action.HostGroup) == "" {
			return fmt.Errorf("%w: every action must select a host group", domain.ErrInvalid)
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
		if action.Idempotent && action.Kind == domain.ActionCheck {
			return fmt.Errorf("%w: check actions cannot declare executable retry capability", domain.ErrInvalid)
		}
		if action.Kind == domain.ActionRollback && (action.FromReleaseID == "") != (action.ToReleaseID == "") {
			return fmt.Errorf("%w: rollback action must declare both fromReleaseId and toReleaseId, or leave both empty for an install rollback", domain.ErrInvalid)
		}
		if containsString(action.Tags, rollbackSelfVerifyTag) && (action.Kind != domain.ActionRollback || action.FromReleaseID != "" || action.ToReleaseID != "") {
			return fmt.Errorf("%w: %s is only valid on a clean-state rollback without fromReleaseId/toReleaseId", domain.ErrInvalid, rollbackSelfVerifyTag)
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
	case domain.ActionCheck, domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade, domain.ActionRollback, domain.ActionUninstall:
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
