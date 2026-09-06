package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *PlatformOptionService) List(ctx context.Context) ([]domain.PlatformOptionCategory, error) {
	return s.store.ListPlatformOptionCategories(ctx)
}

func (s *PlatformOptionService) CreateCategory(ctx context.Context, actor domain.User, label, parentCategoryID string, required bool) (domain.PlatformOptionCategory, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	label, err := validatePlatformOptionLabel(label)
	if err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	categories, err := s.store.ListPlatformOptionCategories(ctx)
	if err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	sortOrder := 0
	var parent *domain.PlatformOptionCategory
	for _, category := range categories {
		if category.SortOrder >= sortOrder {
			sortOrder = category.SortOrder + 1
		}
		if category.ID == parentCategoryID {
			candidate := category
			parent = &candidate
		}
	}
	if parentCategoryID != "" && (parent == nil || parent.Kind != domain.PlatformOptionEnvironmentDimension || parent.ParentCategoryID != "" || parent.RetiredAt != nil) {
		return domain.PlatformOptionCategory{}, fmt.Errorf("%w: parent category must be an active root environment dimension", domain.ErrInvalid)
	}
	now := time.Now().UTC()
	category := domain.PlatformOptionCategory{
		ID:                  newID("platform-option-category"),
		ParentCategoryID:    parentCategoryID,
		Key:                 generatedPlatformTechnicalValue("dimension"),
		Label:               label,
		Kind:                domain.PlatformOptionEnvironmentDimension,
		EnvironmentRequired: required,
		SortOrder:           sortOrder,
		CreatedBy:           actor.ID,
		CreatedAt:           now,
		Options:             []domain.PlatformOption{},
	}
	audit := newAuditEvent(actor, "platform_option_category.created", "platform_option_category", category.ID, map[string]any{
		"key": category.Key, "label": category.Label, "parentCategoryId": parentCategoryID, "environmentRequired": required,
	})
	if err := s.store.CreatePlatformOptionCategory(ctx, category, audit); err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	s.hub.Publish("platform_options.updated", map[string]any{"categoryId": category.ID, "action": "created"})
	return category, nil
}

func (s *PlatformOptionService) DeleteCategory(ctx context.Context, actor domain.User, id string) error {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return err
	}
	audit := newAuditEvent(actor, "platform_option_category.deleted", "platform_option_category", id, nil)
	if err := s.store.DeletePlatformOptionCategory(ctx, id, audit); err != nil {
		return err
	}
	s.hub.Publish("platform_options.updated", map[string]any{"categoryId": id, "action": "deleted"})
	return nil
}

func (s *PlatformOptionService) RenameCategory(ctx context.Context, actor domain.User, id, label string) (domain.PlatformOptionCategory, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	label, err := validatePlatformOptionLabel(label)
	if err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	categories, err := s.store.ListPlatformOptionCategories(ctx)
	if err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	for _, category := range categories {
		if category.ID != id {
			continue
		}
		if category.Label == label {
			return category, nil
		}
		audit := newAuditEvent(actor, "platform_option_category.renamed", "platform_option_category", id, map[string]any{
			"key": category.Key, "oldLabel": category.Label, "newLabel": label,
		})
		if err := s.store.RenamePlatformOptionCategory(ctx, id, label, audit); err != nil {
			return domain.PlatformOptionCategory{}, err
		}
		category.Label = label
		s.hub.Publish("platform_options.updated", map[string]any{"categoryId": id, "action": "renamed"})
		return category, nil
	}
	return domain.PlatformOptionCategory{}, domain.ErrNotFound
}

func (s *PlatformOptionService) CreateOption(ctx context.Context, actor domain.User, categoryID, label, parentOptionID string) (domain.PlatformOption, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.PlatformOption{}, err
	}
	label, err := validatePlatformOptionLabel(label)
	if err != nil {
		return domain.PlatformOption{}, err
	}
	categories, err := s.store.ListPlatformOptionCategories(ctx)
	if err != nil {
		return domain.PlatformOption{}, err
	}
	var target *domain.PlatformOptionCategory
	for i := range categories {
		if categories[i].ID == categoryID {
			target = &categories[i]
			break
		}
	}
	if target == nil {
		return domain.PlatformOption{}, domain.ErrNotFound
	}
	if target.RetiredAt != nil {
		return domain.PlatformOption{}, fmt.Errorf("%w: cannot add an option to a retired category", domain.ErrConflict)
	}
	if target.ParentCategoryID == "" && parentOptionID != "" {
		return domain.PlatformOption{}, fmt.Errorf("%w: root category option cannot have a parent", domain.ErrInvalid)
	}
	if target.ParentCategoryID != "" {
		validParent := false
		for _, category := range categories {
			if category.ID == target.ParentCategoryID {
				for _, option := range category.Options {
					if option.ID == parentOptionID && option.RetiredAt == nil {
						validParent = true
					}
				}
			}
		}
		if !validParent {
			return domain.PlatformOption{}, fmt.Errorf("%w: child option must select an active option from the parent category", domain.ErrInvalid)
		}
	}
	sortOrder := 0
	for _, option := range target.Options {
		if option.SortOrder >= sortOrder {
			sortOrder = option.SortOrder + 1
		}
	}
	prefix := "option"
	if target.Kind == domain.PlatformOptionHostGroup {
		prefix = "group"
	}
	now := time.Now().UTC()
	technicalValue := generatedPlatformTechnicalValue(prefix)
	option := domain.PlatformOption{
		ID:             newID("platform-option"),
		CategoryID:     target.ID,
		ParentOptionID: parentOptionID,
		Value:          technicalValue,
		Label:          label,
		SortOrder:      sortOrder,
		CreatedBy:      actor.ID,
		CreatedAt:      now,
	}
	audit := newAuditEvent(actor, "platform_option.created", "platform_option", option.ID, map[string]any{
		"categoryId": target.ID, "categoryKey": target.Key, "parentOptionId": parentOptionID, "value": option.Value, "label": option.Label,
	})
	if err := s.store.CreatePlatformOption(ctx, option, audit); err != nil {
		return domain.PlatformOption{}, err
	}
	s.hub.Publish("platform_options.updated", map[string]any{"categoryId": target.ID, "optionId": option.ID, "action": "created"})
	return option, nil
}

func (s *PlatformOptionService) SetCategoryRetired(ctx context.Context, actor domain.User, id string, retired bool) (domain.PlatformOptionCategory, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	categories, err := s.store.ListPlatformOptionCategories(ctx)
	if err != nil {
		return domain.PlatformOptionCategory{}, err
	}
	for _, category := range categories {
		if category.ID != id {
			continue
		}
		if category.Kind == domain.PlatformOptionHostGroup {
			return domain.PlatformOptionCategory{}, fmt.Errorf("%w: host group category cannot be retired", domain.ErrConflict)
		}
		if retired {
			for _, child := range categories {
				if child.ParentCategoryID == id && child.RetiredAt == nil {
					return domain.PlatformOptionCategory{}, fmt.Errorf("%w: retire child categories first", domain.ErrConflict)
				}
			}
		}
		if !retired && category.ParentCategoryID != "" {
			for _, parent := range categories {
				if parent.ID == category.ParentCategoryID && parent.RetiredAt != nil {
					return domain.PlatformOptionCategory{}, fmt.Errorf("%w: restore the parent category first", domain.ErrConflict)
				}
			}
		}
		var at *time.Time
		action := "restored"
		if retired {
			value := time.Now().UTC()
			at = &value
			action = "retired"
		}
		audit := newAuditEvent(actor, "platform_option_category."+action, "platform_option_category", id, map[string]any{"key": category.Key})
		if err := s.store.SetPlatformOptionCategoryRetired(ctx, id, at, audit); err != nil {
			return domain.PlatformOptionCategory{}, err
		}
		category.RetiredAt = at
		s.hub.Publish("platform_options.updated", map[string]any{"categoryId": id, "action": action})
		return category, nil
	}
	return domain.PlatformOptionCategory{}, domain.ErrNotFound
}

func (s *PlatformOptionService) SetOptionRetired(ctx context.Context, actor domain.User, id string, retired bool) (domain.PlatformOption, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.PlatformOption{}, err
	}
	categories, err := s.store.ListPlatformOptionCategories(ctx)
	if err != nil {
		return domain.PlatformOption{}, err
	}
	for _, category := range categories {
		for _, option := range category.Options {
			if option.ID != id {
				continue
			}
			if !retired && category.RetiredAt != nil {
				return domain.PlatformOption{}, fmt.Errorf("%w: restore the category first", domain.ErrConflict)
			}
			if retired {
				for _, childCategory := range categories {
					for _, child := range childCategory.Options {
						if child.ParentOptionID == id && child.RetiredAt == nil {
							return domain.PlatformOption{}, fmt.Errorf("%w: retire child versions first", domain.ErrConflict)
						}
					}
				}
			}
			var at *time.Time
			action := "restored"
			if retired {
				value := time.Now().UTC()
				at = &value
				action = "retired"
			}
			audit := newAuditEvent(actor, "platform_option."+action, "platform_option", id, map[string]any{"categoryId": category.ID, "value": option.Value})
			if err := s.store.SetPlatformOptionRetired(ctx, id, at, audit); err != nil {
				return domain.PlatformOption{}, err
			}
			option.RetiredAt = at
			s.hub.Publish("platform_options.updated", map[string]any{"categoryId": category.ID, "optionId": id, "action": action})
			return option, nil
		}
	}
	return domain.PlatformOption{}, domain.ErrNotFound
}

func (s *PlatformOptionService) DeleteOption(ctx context.Context, actor domain.User, id string) error {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return err
	}
	audit := newAuditEvent(actor, "platform_option.deleted", "platform_option", id, nil)
	if err := s.store.DeletePlatformOption(ctx, id, audit); err != nil {
		return err
	}
	s.hub.Publish("platform_options.updated", map[string]any{"optionId": id, "action": "deleted"})
	return nil
}

func (s *PlatformOptionService) RenameOption(ctx context.Context, actor domain.User, id, label string) (domain.PlatformOption, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.PlatformOption{}, err
	}
	label, err := validatePlatformOptionLabel(label)
	if err != nil {
		return domain.PlatformOption{}, err
	}
	categories, err := s.store.ListPlatformOptionCategories(ctx)
	if err != nil {
		return domain.PlatformOption{}, err
	}
	for _, category := range categories {
		for _, option := range category.Options {
			if option.ID != id {
				continue
			}
			if option.Label == label {
				return option, nil
			}
			audit := newAuditEvent(actor, "platform_option.renamed", "platform_option", id, map[string]any{
				"categoryId": category.ID, "categoryKey": category.Key, "value": option.Value, "oldLabel": option.Label, "newLabel": label,
			})
			if err := s.store.RenamePlatformOption(ctx, id, label, audit); err != nil {
				return domain.PlatformOption{}, err
			}
			option.Label = label
			s.hub.Publish("platform_options.updated", map[string]any{"categoryId": category.ID, "optionId": id, "action": "renamed"})
			return option, nil
		}
	}
	return domain.PlatformOption{}, domain.ErrNotFound
}

func generatedPlatformTechnicalValue(prefix string) string {
	return prefix + "_" + strings.TrimPrefix(newID(""), "-")
}

func validatePlatformOptionLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > 64 {
		return "", fmt.Errorf("%w: label must contain 1 to 64 characters", domain.ErrInvalid)
	}
	return label, nil
}

func (p *CatalogRules) platformOptionLookup(ctx context.Context) (domain.CatalogOptions, error) {
	return p.store.ReadCatalogOptions(ctx)
}
func (p *CatalogRules) validateEnvironmentConstraintsCatalog(ctx context.Context, constraints map[string]any) error {
	lookup, err := p.platformOptionLookup(ctx)
	if err != nil {
		return err
	}
	return lookup.ValidateConstraints(constraints)
}
func (p *CatalogRules) validateEnvironmentConstraintRetiredReferences(ctx context.Context, constraints, previous map[string]any) error {
	lookup, err := p.platformOptionLookup(ctx)
	if err != nil {
		return err
	}
	return lookup.ValidateConstraintChanges(constraints, previous)
}
func (p *CatalogRules) validateEnvironmentFactsCatalog(ctx context.Context, facts map[string]any, requireComplete bool) error {
	lookup, err := p.platformOptionLookup(ctx)
	if err != nil {
		return err
	}
	return lookup.ValidateFacts(facts, requireComplete)
}
func (p *CatalogRules) validateEnvironmentFactRetiredReferences(ctx context.Context, facts, previous map[string]any) error {
	lookup, err := p.platformOptionLookup(ctx)
	if err != nil {
		return err
	}
	return lookup.ValidateFactChanges(facts, previous)
}
func (p *CatalogRules) validateHostGroupCatalog(ctx context.Context, value string) error {
	lookup, err := p.platformOptionLookup(ctx)
	if err != nil {
		return err
	}
	return lookup.ValidateHostGroup(value)
}
func (p *CatalogRules) validateEnvironmentInventoryCatalog(ctx context.Context, inventory json.RawMessage) error {
	lookup, err := p.platformOptionLookup(ctx)
	if err != nil {
		return err
	}
	return lookup.ValidateInventory(inventory)
}
