package service

import (
	"context"
	"errors"
	"fmt"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type EnvironmentRevisionDeletionBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type EnvironmentRevisionDeletionImpact struct {
	store.EnvironmentRevisionDeletionImpact
	CanDelete bool                                 `json:"canDelete"`
	Blockers  []EnvironmentRevisionDeletionBlocker `json:"blockers"`
}

func environmentRevisionDeletionFromImpact(impact store.EnvironmentRevisionDeletionImpact) EnvironmentRevisionDeletionImpact {
	result := EnvironmentRevisionDeletionImpact{EnvironmentRevisionDeletionImpact: impact, Blockers: []EnvironmentRevisionDeletionBlocker{}}
	if impact.Current {
		result.Blockers = append(result.Blockers, EnvironmentRevisionDeletionBlocker{"environment_revision.current", "当前环境版本不能删除"})
	}
	if impact.Archived {
		result.Blockers = append(result.Blockers, EnvironmentRevisionDeletionBlocker{"environment.archived", "该环境已归档，请先恢复环境再删除历史版本"})
	}
	if impact.RunCount > 0 {
		result.Blockers = append(result.Blockers, EnvironmentRevisionDeletionBlocker{"environment_revision.run_history", fmt.Sprintf("该版本被 %d 个 Run 引用，必须保留；包含失败、取消及已归档或清理的运行记录", impact.RunCount)})
	}
	if impact.ImageBuildCount > 0 {
		result.Blockers = append(result.Blockers, EnvironmentRevisionDeletionBlocker{"environment_revision.build_history", fmt.Sprintf("该版本被 %d 个镜像构建引用，必须保留", impact.ImageBuildCount)})
	}
	result.CanDelete = len(result.Blockers) == 0
	return result
}

func (p *EnvironmentService) RevisionDeletionImpact(ctx context.Context, user domain.User, environmentID, revisionID string) (EnvironmentRevisionDeletionImpact, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return EnvironmentRevisionDeletionImpact{}, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return EnvironmentRevisionDeletionImpact{}, err
	}
	impact, err := p.store.EnvironmentRevisionDeletionImpact(ctx, environmentID, revisionID)
	if err != nil {
		return EnvironmentRevisionDeletionImpact{}, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, impact.OwnerID); err != nil {
		return EnvironmentRevisionDeletionImpact{}, err
	}
	return environmentRevisionDeletionFromImpact(impact), nil
}

func (p *EnvironmentService) DeleteRevision(ctx context.Context, user domain.User, environmentID, revisionID string) error {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return err
	}
	audit := newAuditEvent(user, "environment.revision_deleted", "environment_revision", revisionID, nil)
	err = p.store.DeleteEnvironmentRevision(ctx, environmentID, revisionID, user.ID, audit)
	var blocked *store.EnvironmentRevisionDeletionConflict
	if errors.As(err, &blocked) {
		impact := environmentRevisionDeletionFromImpact(blocked.Impact)
		blocker := impact.Blockers[0]
		return actionableExistingError(err, blocker.Code, blocker.Message, "查看环境版本", "/environments?selected="+environmentID)
	}
	if err != nil {
		return err
	}
	p.hub.Publish("environment.revision_deleted", map[string]any{"environmentId": environmentID, "revisionId": revisionID})
	return nil
}
