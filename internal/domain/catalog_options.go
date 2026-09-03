package domain

import (
	"encoding/json"
	"fmt"
)

type CatalogOptions struct {
	Dimensions     map[string]PlatformOptionCategory
	CategoriesByID map[string]PlatformOptionCategory
	OptionsByID    map[string]PlatformOption
	HostGroup      PlatformOptionCategory
}

func NewCatalogOptions(categories []PlatformOptionCategory) (CatalogOptions, error) {
	lookup := CatalogOptions{Dimensions: map[string]PlatformOptionCategory{}, CategoriesByID: map[string]PlatformOptionCategory{}, OptionsByID: map[string]PlatformOption{}}
	for _, category := range categories {
		lookup.CategoriesByID[category.ID] = category
		for _, option := range category.Options {
			lookup.OptionsByID[option.ID] = option
		}
		switch category.Kind {
		case PlatformOptionEnvironmentDimension:
			lookup.Dimensions[category.Key] = category
		case PlatformOptionHostGroup:
			lookup.HostGroup = category
		}
	}
	return lookup, nil
}

func PlatformCategoryHasValue(category PlatformOptionCategory, value string) bool {
	for _, option := range category.Options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func PlatformCategoryOption(category PlatformOptionCategory, value string) (PlatformOption, bool) {
	for _, option := range category.Options {
		if option.Value == value {
			return option, true
		}
	}
	return PlatformOption{}, false
}

func ConstraintValues(raw any) []string {
	values := []string{}
	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			if value, ok := item.(string); ok && value != "" {
				values = append(values, value)
			}
		}
	case []string:
		for _, value := range typed {
			if value != "" {
				values = append(values, value)
			}
		}
	case string:
		if typed != "" {
			values = append(values, typed)
		}
	}
	return values
}

func (lookup CatalogOptions) ValidateConstraints(constraints map[string]any) error {
	for key, raw := range constraints {
		category, ok := lookup.Dimensions[key]
		if !ok {
			return fmt.Errorf("%w: unknown environment dimension %q", ErrInvalid, key)
		}
		values, ok := raw.([]any)
		if !ok {
			if strings, stringsOK := raw.([]string); stringsOK {
				values = make([]any, len(strings))
				for i := range strings {
					values[i] = strings[i]
				}
			} else {
				return fmt.Errorf("%w: environment constraint %q must be an array", ErrInvalid, key)
			}
		}
		if len(values) == 0 {
			return fmt.Errorf("%w: empty environment constraint %q must be omitted", ErrInvalid, key)
		}
		seen := map[string]bool{}
		for _, rawValue := range values {
			value, ok := rawValue.(string)
			if !ok || !PlatformCategoryHasValue(category, value) || seen[value] {
				return fmt.Errorf("%w: invalid option for environment dimension %q", ErrInvalid, key)
			}
			seen[value] = true
		}
	}
	for key, category := range lookup.Dimensions {
		if category.ParentCategoryID == "" {
			continue
		}
		childValues := ConstraintValues(constraints[key])
		parent := lookup.CategoriesByID[category.ParentCategoryID]
		parentValues := ConstraintValues(constraints[parent.Key])
		if len(parentValues) > 0 && len(childValues) == 0 {
			return fmt.Errorf("%w: environment dimension %q requires at least one version from %q", ErrInvalid, parent.Key, key)
		}
		selectedParents := map[string]bool{}
		for _, value := range parentValues {
			if option, ok := PlatformCategoryOption(parent, value); ok {
				selectedParents[option.ID] = true
			}
		}
		coveredParents := map[string]bool{}
		for _, value := range childValues {
			option, _ := PlatformCategoryOption(category, value)
			if option.ParentOptionID == "" || !selectedParents[option.ParentOptionID] {
				return fmt.Errorf("%w: option %q does not belong to a selected parent in %q", ErrInvalid, value, parent.Key)
			}
			coveredParents[option.ParentOptionID] = true
		}
		for id := range selectedParents {
			if !coveredParents[id] {
				return fmt.Errorf("%w: each selected option in %q requires at least one version", ErrInvalid, parent.Key)
			}
		}
	}
	return nil
}

func (lookup CatalogOptions) ValidateConstraintChanges(constraints, previous map[string]any) error {
	for key, raw := range constraints {
		category, ok := lookup.Dimensions[key]
		if !ok {
			continue
		}
		values := ConstraintValues(raw)
		previousValues := map[string]bool{}
		for _, value := range ConstraintValues(previous[key]) {
			previousValues[value] = true
		}
		if category.RetiredAt != nil {
			for _, value := range values {
				if !previousValues[value] {
					return fmt.Errorf("%w: retired environment dimension %q can only be preserved unchanged", ErrConflict, key)
				}
			}
			continue
		}
		for _, value := range values {
			option, _ := PlatformCategoryOption(category, value)
			if option.RetiredAt != nil && !previousValues[value] {
				return fmt.Errorf("%w: retired option %q cannot be newly referenced", ErrConflict, value)
			}
		}
	}
	return nil
}

func (lookup CatalogOptions) ValidateFacts(facts map[string]any, requireComplete bool) error {
	for key, raw := range facts {
		category, ok := lookup.Dimensions[key]
		if !ok {
			return fmt.Errorf("%w: unknown environment fact %q", ErrInvalid, key)
		}
		value, ok := raw.(string)
		if !ok || !PlatformCategoryHasValue(category, value) {
			return fmt.Errorf("%w: invalid option for environment fact %q", ErrInvalid, key)
		}
	}
	for key, category := range lookup.Dimensions {
		if category.ParentCategoryID == "" {
			continue
		}
		childValue, childSet := facts[key].(string)
		parent := lookup.CategoriesByID[category.ParentCategoryID]
		parentValue, parentSet := facts[parent.Key].(string)
		if !childSet && !parentSet {
			continue
		}
		if !childSet || !parentSet {
			return fmt.Errorf("%w: environment facts %q and %q must be selected together", ErrInvalid, parent.Key, key)
		}
		child, _ := PlatformCategoryOption(category, childValue)
		selectedParent, _ := PlatformCategoryOption(parent, parentValue)
		if child.ParentOptionID == "" || child.ParentOptionID != selectedParent.ID {
			return fmt.Errorf("%w: environment runtime version does not belong to selected runtime", ErrInvalid)
		}
	}
	if requireComplete {
		for key, category := range lookup.Dimensions {
			if category.EnvironmentRequired && category.RetiredAt == nil {
				if value, ok := facts[key].(string); !ok || value == "" {
					return &CodedError{Code: "environment.facts_incomplete", Message: "环境缺少当前必填事实：" + category.Label, Cause: ErrInvalid}
				}
			}
		}
	}
	return nil
}

func (lookup CatalogOptions) ValidateFactChanges(facts, previous map[string]any) error {
	for key, raw := range facts {
		category, ok := lookup.Dimensions[key]
		if !ok {
			continue
		}
		value, _ := raw.(string)
		previousValue, previousSet := previous[key].(string)
		if category.RetiredAt != nil && (!previousSet || previousValue != value) {
			return fmt.Errorf("%w: retired environment fact %q can only be preserved unchanged", ErrConflict, key)
		}
		option, _ := PlatformCategoryOption(category, value)
		if option.RetiredAt != nil && (!previousSet || previousValue != value) {
			return fmt.Errorf("%w: retired option %q cannot be newly referenced", ErrConflict, value)
		}
	}
	return nil
}

func (lookup CatalogOptions) ValidateHostGroup(value string) error {
	if value == "" || !PlatformCategoryHasValue(lookup.HostGroup, value) {
		return fmt.Errorf("%w: unknown host group %q", ErrInvalid, value)
	}
	return nil
}

func (lookup CatalogOptions) ValidateHostGroupChange(value, previous string) error {
	if err := lookup.ValidateHostGroup(value); err != nil {
		return err
	}
	option, _ := PlatformCategoryOption(lookup.HostGroup, value)
	if (lookup.HostGroup.RetiredAt != nil || option.RetiredAt != nil) && value != previous {
		return fmt.Errorf("%w: retired host group %q cannot be newly referenced", ErrConflict, value)
	}
	return nil
}

func (lookup CatalogOptions) ValidateInventoryChanges(inventory, previous json.RawMessage) error {
	type host struct {
		Name   string
		Groups []string
	}
	var next, old struct{ Hosts []host }
	if err := json.Unmarshal(inventory, &next); err != nil {
		return err
	}
	if len(previous) > 0 {
		if err := json.Unmarshal(previous, &old); err != nil {
			return err
		}
	}
	prior := map[string]map[string]bool{}
	for _, h := range old.Hosts {
		prior[h.Name] = map[string]bool{}
		for _, g := range h.Groups {
			prior[h.Name][g] = true
		}
	}
	for _, h := range next.Hosts {
		for _, g := range h.Groups {
			before := ""
			if prior[h.Name][g] {
				before = g
			}
			if err := lookup.ValidateHostGroupChange(g, before); err != nil {
				return err
			}
		}
	}
	return nil
}

func (lookup CatalogOptions) ValidateInventory(inventory json.RawMessage) error {
	var parsed struct {
		Hosts []struct {
			Name   string   `json:"name"`
			Groups []string `json:"groups"`
		} `json:"hosts"`
	}
	if len(inventory) == 0 || json.Unmarshal(inventory, &parsed) != nil {
		return fmt.Errorf("%w: inventory must be valid JSON", ErrInvalid)
	}
	for _, host := range parsed.Hosts {
		if len(host.Groups) == 0 {
			return fmt.Errorf("%w: inventory host %q must select at least one host group", ErrInvalid, host.Name)
		}
		seen := map[string]bool{}
		for _, group := range host.Groups {
			if !PlatformCategoryHasValue(lookup.HostGroup, group) || seen[group] {
				return fmt.Errorf("%w: inventory host %q has an invalid host group", ErrInvalid, host.Name)
			}
			seen[group] = true
		}
	}
	return nil
}
