package service

import (
	"context"
	"errors"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func actionableError(base error, code, message, causeSummary, label, href string) error {
	action := workAction(label, href)
	return &domain.ActionableError{
		Base: base,
		Explanation: domain.WorkExplanation{
			Reasons: []domain.WorkReason{{
				Code: code, Message: message, Cause: ruleCause(causeSummary), NextAction: action,
			}},
			PrimaryAction:    action,
			SecondaryActions: []domain.WorkAction{},
		},
	}
}

func (p *Platform) planRefreshHref(ctx context.Context, kind domain.RunKind, releaseID, revisionID, environmentID string) string {
	if releaseID != "" {
		if release, err := p.store.GetComponentRelease(ctx, releaseID); err == nil {
			return fmt.Sprintf("/components?selected=%s&release=%s&action=validate", release.ComponentID, release.ID)
		}
	}
	if revisionID != "" {
		if revision, err := p.store.GetScenarioRevision(ctx, revisionID); err == nil {
			return fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=test", revision.ScenarioID, revision.ID)
		}
	}
	if kind == domain.RunEnvironmentRollback {
		return "/environments?selected=" + environmentID + "&action=rollback"
	}
	return "/runs"
}

func actionableExistingError(base error, code, causeSummary, label, href string) error {
	if !errors.Is(base, domain.ErrConflict) && !errors.Is(base, domain.ErrInvalid) && !errors.Is(base, domain.ErrForbidden) {
		return base
	}
	message := base.Error()
	for _, sentinel := range []error{domain.ErrConflict, domain.ErrInvalid, domain.ErrForbidden} {
		if errors.Is(base, sentinel) {
			prefix := sentinel.Error() + ": "
			if len(message) >= len(prefix) && message[:len(prefix)] == prefix {
				message = message[len(prefix):]
			}
			break
		}
	}
	return actionableError(base, code, message, causeSummary, label, href)
}
