package service

import (
	"context"
	"errors"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (p *CatalogService) CanDeleteComponent(ctx context.Context, user domain.User, component domain.Component) (bool, error) {
	if requireOwner(user, domain.RoleComponentOwner, component.OwnerID) != nil || len(component.Releases) > 0 {
		return false, nil
	}
	return p.store.CanDeleteComponent(ctx, component.ID, user.ID)
}

func (p *CatalogService) DeleteComponent(ctx context.Context, user domain.User, id string) error {
	component, err := p.store.GetComponent(ctx, id, false)
	if err != nil {
		return err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return err
	}
	audit := newAuditEvent(user, "component.deleted", "component", id, nil)
	err = p.store.DeleteEmptyComponent(ctx, id, user.ID, audit)
	var blocked *store.ComponentDeletionConflict
	if errors.As(err, &blocked) {
		return actionableExistingError(err, blocked.Code, blocked.Message, "查看组件", "/components?selected="+id)
	}
	if err != nil {
		return err
	}
	p.hub.Publish("component.deleted", map[string]any{"componentId": id})
	return nil
}
