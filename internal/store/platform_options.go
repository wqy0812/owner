package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

type platformReferenceSet struct {
	components   map[string]struct{}
	scenarios    map[string]struct{}
	environments map[string]struct{}
}

type platformReferenceIndex struct {
	categories map[string]*platformReferenceSet
	options    map[string]*platformReferenceSet
}

func newPlatformReferenceIndex() platformReferenceIndex {
	return platformReferenceIndex{
		categories: map[string]*platformReferenceSet{},
		options:    map[string]*platformReferenceSet{},
	}
}

func newPlatformReferenceSet() *platformReferenceSet {
	return &platformReferenceSet{
		components:   map[string]struct{}{},
		scenarios:    map[string]struct{}{},
		environments: map[string]struct{}{},
	}
}

func platformOptionReferenceKey(categoryKey, value string) string {
	return categoryKey + "\x00" + value
}

func (index platformReferenceIndex) add(categoryKey, value, resourceType, resourceID string) {
	category := index.categories[categoryKey]
	if category == nil {
		category = newPlatformReferenceSet()
		index.categories[categoryKey] = category
	}
	category.add(resourceType, resourceID)
	if value == "" {
		return
	}
	key := platformOptionReferenceKey(categoryKey, value)
	option := index.options[key]
	if option == nil {
		option = newPlatformReferenceSet()
		index.options[key] = option
	}
	option.add(resourceType, resourceID)
}

func (set *platformReferenceSet) add(resourceType, resourceID string) {
	switch resourceType {
	case "component":
		set.components[resourceID] = struct{}{}
	case "scenario":
		set.scenarios[resourceID] = struct{}{}
	case "environment":
		set.environments[resourceID] = struct{}{}
	}
}

func (set *platformReferenceSet) usage() domain.PlatformOptionUsage {
	if set == nil {
		return domain.PlatformOptionUsage{}
	}
	return domain.PlatformOptionUsage{
		ComponentReleases:    len(set.components),
		ScenarioRevisions:    len(set.scenarios),
		EnvironmentRevisions: len(set.environments),
	}
}

func (s *Store) ListPlatformOptionCategories(ctx context.Context) ([]domain.PlatformOptionCategory, error) {
	index, err := loadPlatformReferenceIndex(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return listPlatformOptionCategories(ctx, s.db, index)
}

// BootstrapPlatformCatalog installs the initial platform-owned directory once.
// The audit marker is persistent so a later administrator deletion is not
// mistaken for an incomplete bootstrap on process restart. A restored current
// Catalog is preserved instead of being overwritten with the initial directory.
// Variable definitions have their own empty-directory check: Catalog restores
// include option categories but deliberately do not include environment variables.
func (s *Store) BootstrapPlatformCatalog(ctx context.Context, categories []domain.PlatformOptionCategory, options []domain.PlatformOption, variableDefinitions []domain.EnvironmentVariableDefinition, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// beginCatalogWrite holds the writer slot before reading the marker.
	var completed int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE id=?)`, audit.ID).Scan(&completed); err != nil {
		return err
	}
	if completed != 0 {
		return nil
	}
	var existingOptions, existingVariables int
	if err := tx.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM platform_option_categories)
     + (SELECT COUNT(*) FROM platform_options),
       (SELECT COUNT(*) FROM environment_variable_definitions)`).Scan(&existingOptions, &existingVariables); err != nil {
		return err
	}
	if existingOptions == 0 && existingVariables == 0 {
		for _, category := range categories {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO platform_option_categories(id,parent_category_id,technical_key,label,category_type,environment_required,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, category.ID, nullString(category.ParentCategoryID), category.Key, category.Label, category.Kind, category.EnvironmentRequired, category.SortOrder, category.CreatedBy, timeText(category.CreatedAt), ptrTimeText(category.RetiredAt)); err != nil {
				return mapSQLError(err)
			}
		}
		for _, option := range options {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO platform_options(id,category_id,parent_option_id,technical_value,label,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?)`, option.ID, option.CategoryID, nullString(option.ParentOptionID), option.Value, option.Label, option.SortOrder, option.CreatedBy, timeText(option.CreatedAt), ptrTimeText(option.RetiredAt)); err != nil {
				return mapSQLError(err)
			}
		}
	}
	if existingVariables == 0 {
		for _, definition := range variableDefinitions {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO environment_variable_definitions(id,variable_name,label,description,created_by,created_at)
VALUES(?,?,?,?,?,?)`, definition.ID, definition.Name, definition.Label, definition.Description, definition.CreatedBy, timeText(definition.CreatedAt)); err != nil {
				return mapSQLError(err)
			}
		}
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func listPlatformOptionCategories(ctx context.Context, q queryer, index platformReferenceIndex) ([]domain.PlatformOptionCategory, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id,parent_category_id,technical_key,label,category_type,environment_required,sort_order,created_by,created_at,retired_at
FROM platform_option_categories
ORDER BY sort_order,label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	categories := make([]domain.PlatformOptionCategory, 0)
	for rows.Next() {
		var category domain.PlatformOptionCategory
		var required bool
		var created string
		var parent, retired sql.NullString
		if err := rows.Scan(&category.ID, &parent, &category.Key, &category.Label, &category.Kind, &required, &category.SortOrder, &category.CreatedBy, &created, &retired); err != nil {
			return nil, err
		}
		category.EnvironmentRequired = required
		category.CreatedAt = parseTime(created)
		category.ParentCategoryID = parent.String
		if retired.Valid {
			value := parseTime(retired.String)
			category.RetiredAt = &value
		}
		category.Usage = index.categories[category.Key].usage()
		category.Options = []domain.PlatformOption{}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range categories {
		optionRows, err := q.QueryContext(ctx, `
SELECT id,category_id,parent_option_id,technical_value,label,sort_order,created_by,created_at,retired_at
FROM platform_options WHERE category_id=? ORDER BY sort_order,label`, categories[i].ID)
		if err != nil {
			return nil, err
		}
		for optionRows.Next() {
			var option domain.PlatformOption
			var created string
			var parent, retired sql.NullString
			if err := optionRows.Scan(&option.ID, &option.CategoryID, &parent, &option.Value, &option.Label, &option.SortOrder, &option.CreatedBy, &created, &retired); err != nil {
				optionRows.Close()
				return nil, err
			}
			option.CreatedAt = parseTime(created)
			option.ParentOptionID = parent.String
			if retired.Valid {
				value := parseTime(retired.String)
				option.RetiredAt = &value
			}
			option.Usage = index.options[platformOptionReferenceKey(categories[i].Key, option.Value)].usage()
			categories[i].Options = append(categories[i].Options, option)
		}
		if err := optionRows.Close(); err != nil {
			return nil, err
		}
	}
	return categories, nil
}

func (s *Store) CreatePlatformOptionCategory(ctx context.Context, category domain.PlatformOptionCategory, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if category.ParentCategoryID != "" {
		if err := lockPlatformOptionCategory(ctx, tx, category.ParentCategoryID); err != nil {
			return err
		}
		var kind, parent string
		var retired sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT category_type,COALESCE(parent_category_id,''),retired_at FROM platform_option_categories WHERE id=?`, category.ParentCategoryID).Scan(&kind, &parent, &retired); err != nil {
			return mapSQLError(err)
		}
		if kind != string(domain.PlatformOptionEnvironmentDimension) || parent != "" || retired.Valid {
			return fmt.Errorf("%w: parent category must still be an active root environment dimension", domain.ErrConflict)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO platform_option_categories(id,parent_category_id,technical_key,label,category_type,environment_required,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, category.ID, nullString(category.ParentCategoryID), category.Key, category.Label, category.Kind, category.EnvironmentRequired, category.SortOrder, category.CreatedBy, timeText(category.CreatedAt), ptrTimeText(category.RetiredAt)); err != nil {
		return mapSQLError(err)
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreatePlatformOption(ctx context.Context, option domain.PlatformOption, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockPlatformOptionCategory(ctx, tx, option.CategoryID); err != nil {
		return err
	}
	var parentCategoryID string
	var categoryRetired sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(parent_category_id,''),retired_at FROM platform_option_categories WHERE id=?`, option.CategoryID).Scan(&parentCategoryID, &categoryRetired); err != nil {
		return mapSQLError(err)
	}
	if categoryRetired.Valid {
		return fmt.Errorf("%w: cannot add an option to a retired category", domain.ErrConflict)
	}
	if parentCategoryID == "" && option.ParentOptionID != "" {
		return fmt.Errorf("%w: root category option cannot have a parent", domain.ErrInvalid)
	}
	if parentCategoryID != "" {
		if option.ParentOptionID == "" {
			return fmt.Errorf("%w: child category option requires an active parent option", domain.ErrConflict)
		}
		var actualParentCategoryID string
		var parentRetired sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT category_id,retired_at FROM platform_options WHERE id=?`, option.ParentOptionID).Scan(&actualParentCategoryID, &parentRetired); err != nil {
			return mapSQLError(err)
		}
		if actualParentCategoryID != parentCategoryID || parentRetired.Valid {
			return fmt.Errorf("%w: child category option requires an active option from its parent category", domain.ErrConflict)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO platform_options(id,category_id,parent_option_id,technical_value,label,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?)`, option.ID, option.CategoryID, nullString(option.ParentOptionID), option.Value, option.Label, option.SortOrder, option.CreatedBy, timeText(option.CreatedAt), ptrTimeText(option.RetiredAt)); err != nil {
		return mapSQLError(err)
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenamePlatformOptionCategory(ctx context.Context, id, label string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE platform_option_categories SET label=? WHERE id=?`, label, id)
	if err != nil {
		return mapSQLError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenamePlatformOption(ctx context.Context, id, label string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE platform_options SET label=? WHERE id=?`, label, id)
	if err != nil {
		return mapSQLError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetPlatformOptionCategoryRetired(ctx context.Context, id string, retiredAt *time.Time, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockPlatformOptionCategory(ctx, tx, id); err != nil {
		return err
	}
	var parentCategoryID, kind string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(parent_category_id,''),category_type FROM platform_option_categories WHERE id=?`, id).Scan(&parentCategoryID, &kind); err != nil {
		return mapSQLError(err)
	}
	if retiredAt != nil {
		if kind == string(domain.PlatformOptionHostGroup) {
			return fmt.Errorf("%w: host group category cannot be retired", domain.ErrConflict)
		}
		var activeChildren int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_option_categories WHERE parent_category_id=? AND retired_at IS NULL`, id).Scan(&activeChildren); err != nil {
			return err
		}
		if activeChildren != 0 {
			return fmt.Errorf("%w: retire child categories first", domain.ErrConflict)
		}
	} else if parentCategoryID != "" {
		var parentRetired sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT retired_at FROM platform_option_categories WHERE id=?`, parentCategoryID).Scan(&parentRetired); err != nil {
			return mapSQLError(err)
		}
		if parentRetired.Valid {
			return fmt.Errorf("%w: restore the parent category first", domain.ErrConflict)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE platform_option_categories SET retired_at=? WHERE id=?`, ptrTimeText(retiredAt), id); err != nil {
		return mapSQLError(err)
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetPlatformOptionRetired(ctx context.Context, id string, retiredAt *time.Time, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE platform_options SET sort_order=sort_order WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	var categoryID string
	if err := tx.QueryRowContext(ctx, `SELECT category_id FROM platform_options WHERE id=?`, id).Scan(&categoryID); err != nil {
		return mapSQLError(err)
	}
	if retiredAt != nil {
		var activeChildren int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_options WHERE parent_option_id=? AND retired_at IS NULL`, id).Scan(&activeChildren); err != nil {
			return err
		}
		if activeChildren != 0 {
			return fmt.Errorf("%w: retire child options first", domain.ErrConflict)
		}
	} else {
		var categoryRetired sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT retired_at FROM platform_option_categories WHERE id=?`, categoryID).Scan(&categoryRetired); err != nil {
			return mapSQLError(err)
		}
		if categoryRetired.Valid {
			return fmt.Errorf("%w: restore the category first", domain.ErrConflict)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE platform_options SET retired_at=? WHERE id=?`, ptrTimeText(retiredAt), id); err != nil {
		return mapSQLError(err)
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

// lockPlatformOptionCategory starts the SQLite write transaction before any
// hierarchy read. Concurrent create/retire requests are therefore serialized
// and every invariant check observes the state committed by the previous one.
func lockPlatformOptionCategory(ctx context.Context, tx *sql.Tx, id string) error {
	result, err := tx.ExecContext(ctx, `UPDATE platform_option_categories SET sort_order=sort_order WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeletePlatformOptionCategory(ctx context.Context, id string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key string
	var kind domain.PlatformOptionCategoryKind
	if err := tx.QueryRowContext(ctx, `SELECT technical_key,category_type FROM platform_option_categories WHERE id=?`, id).Scan(&key, &kind); err != nil {
		return mapSQLError(err)
	}
	if kind == domain.PlatformOptionHostGroup {
		return &domain.CodedError{Code: "platform_option_category.protected", Message: "主机组类别受平台保护，不能删除", Cause: domain.ErrConflict}
	}
	index, err := loadPlatformReferenceIndex(ctx, tx)
	if err != nil {
		return err
	}
	usage := index.categories[key].usage()
	if usage.InUse() {
		return platformCategoryInUseError(usage)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM platform_option_categories WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeletePlatformOption(ctx context.Context, id string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var categoryKey, value string
	if err := tx.QueryRowContext(ctx, `
SELECT c.technical_key,o.technical_value
FROM platform_options o JOIN platform_option_categories c ON c.id=o.category_id
WHERE o.id=?`, id).Scan(&categoryKey, &value); err != nil {
		return mapSQLError(err)
	}
	index, err := loadPlatformReferenceIndex(ctx, tx)
	if err != nil {
		return err
	}
	usage := index.options[platformOptionReferenceKey(categoryKey, value)].usage()
	if usage.InUse() {
		return &domain.CodedError{
			Code:    "platform_option.in_use",
			Message: "选项已被业务历史引用，不能删除",
			Details: usage,
			Cause:   domain.ErrConflict,
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM platform_options WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func platformCategoryInUseError(usage domain.PlatformOptionUsage) error {
	return &domain.CodedError{
		Code:    "platform_option_category.in_use",
		Message: "类别或其选项已被业务历史引用，不能删除",
		Details: usage,
		Cause:   domain.ErrConflict,
	}
}

func appendAuditTx(ctx context.Context, tx *sql.Tx, event domain.AuditEvent) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at)
VALUES(?,?,?,?,?,?,?)`, event.ID, event.ActorID, event.Action, event.ResourceType, event.ResourceID, jsonText(event.Metadata), timeText(event.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) UpsertPlatformOptionCategory(ctx context.Context, category domain.PlatformOptionCategory) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO platform_option_categories(id,parent_category_id,technical_key,label,category_type,environment_required,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET parent_category_id=excluded.parent_category_id,label=excluded.label,environment_required=excluded.environment_required,sort_order=excluded.sort_order,retired_at=excluded.retired_at`,
		category.ID, nullString(category.ParentCategoryID), category.Key, category.Label, category.Kind, category.EnvironmentRequired, category.SortOrder, category.CreatedBy, timeText(category.CreatedAt), ptrTimeText(category.RetiredAt))
	return mapSQLError(err)
}

func (s *Store) UpsertPlatformOption(ctx context.Context, option domain.PlatformOption) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO platform_options(id,category_id,parent_option_id,technical_value,label,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET parent_option_id=excluded.parent_option_id,label=excluded.label,sort_order=excluded.sort_order,retired_at=excluded.retired_at`,
		option.ID, option.CategoryID, nullString(option.ParentOptionID), option.Value, option.Label, option.SortOrder, option.CreatedBy, timeText(option.CreatedAt), ptrTimeText(option.RetiredAt))
	return mapSQLError(err)
}

func (s *Store) EnsurePlatformOption(ctx context.Context, categoryKey string, option domain.PlatformOption) error {
	var categoryID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM platform_option_categories WHERE technical_key=?`, categoryKey).Scan(&categoryID); err != nil {
		return mapSQLError(err)
	}
	option.CategoryID = categoryID
	_, err := s.db.ExecContext(ctx, `
INSERT INTO platform_options(id,category_id,parent_option_id,technical_value,label,sort_order,created_by,created_at,retired_at)
VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(category_id,technical_value) DO NOTHING`,
		option.ID, option.CategoryID, nullString(option.ParentOptionID), option.Value, option.Label, option.SortOrder, option.CreatedBy, timeText(option.CreatedAt), ptrTimeText(option.RetiredAt))
	return mapSQLError(err)
}

func loadPlatformReferenceIndex(ctx context.Context, q queryer) (platformReferenceIndex, error) {
	index := newPlatformReferenceIndex()
	releases, err := q.QueryContext(ctx, `SELECT id,environment_constraints_json FROM component_releases`)
	if err != nil {
		return index, err
	}
	for releases.Next() {
		var id, raw string
		if err := releases.Scan(&id, &raw); err != nil {
			releases.Close()
			return index, err
		}
		constraints := decodeJSON(raw, map[string]any{})
		for categoryKey, rawValues := range constraints {
			values := platformReferenceValues(rawValues)
			if len(values) == 0 {
				index.add(categoryKey, "", "component", id)
			}
			for _, value := range values {
				index.add(categoryKey, value, "component", id)
			}
		}
	}
	if err := releases.Close(); err != nil {
		return index, err
	}
	actions, err := q.QueryContext(ctx, `SELECT release_id,host_group FROM action_definitions WHERE host_group<>''`)
	if err != nil {
		return index, err
	}
	for actions.Next() {
		var releaseID, group string
		if err := actions.Scan(&releaseID, &group); err != nil {
			actions.Close()
			return index, err
		}
		index.add("hostGroup", group, "component", releaseID)
	}
	if err := actions.Close(); err != nil {
		return index, err
	}
	scenarios, err := q.QueryContext(ctx, `SELECT id,graph_json FROM scenario_revisions`)
	if err != nil {
		return index, err
	}
	for scenarios.Next() {
		var id, raw string
		if err := scenarios.Scan(&id, &raw); err != nil {
			scenarios.Close()
			return index, err
		}
		graph := decodeJSON(raw, domain.ScenarioGraph{})
		for _, node := range graph.Nodes {
			if node.HostGroup != "" {
				index.add("hostGroup", node.HostGroup, "scenario", id)
			}
		}
	}
	if err := scenarios.Close(); err != nil {
		return index, err
	}
	environments, err := q.QueryContext(ctx, `SELECT id,facts_json,inventory_json FROM environment_revisions`)
	if err != nil {
		return index, err
	}
	for environments.Next() {
		var id, factsRaw, inventoryRaw string
		if err := environments.Scan(&id, &factsRaw, &inventoryRaw); err != nil {
			environments.Close()
			return index, err
		}
		facts := decodeJSON(factsRaw, map[string]any{})
		for categoryKey, rawValue := range facts {
			values := platformReferenceValues(rawValue)
			if len(values) == 0 {
				index.add(categoryKey, "", "environment", id)
			}
			for _, value := range values {
				index.add(categoryKey, value, "environment", id)
			}
		}
		var inventory struct {
			Hosts []struct {
				Groups []string `json:"groups"`
			} `json:"hosts"`
		}
		if json.Unmarshal([]byte(inventoryRaw), &inventory) == nil {
			for _, host := range inventory.Hosts {
				for _, group := range host.Groups {
					index.add("hostGroup", group, "environment", id)
				}
			}
		}
	}
	if err := environments.Close(); err != nil {
		return index, err
	}
	return index, nil
}

func platformReferenceValues(value any) []string {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return typed
	default:
		return nil
	}
}
