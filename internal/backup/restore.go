package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type RestorePlan struct {
	BackupID             string         `json:"backupId,omitempty"`
	GitCommit            string         `json:"gitCommit"`
	SchemaContract       string         `json:"schemaContract"`
	DatabaseSHA256       string         `json:"databaseSha256,omitempty"`
	CatalogSHA256        string         `json:"catalogSha256"`
	Counts               map[string]int `json:"counts"`
	PlaybookCount        int            `json:"playbookCount"`
	TargetComponentCount int            `json:"targetComponentCount,omitempty"`
	TargetScenarioCount  int            `json:"targetScenarioCount,omitempty"`
	PlanDigest           string         `json:"planDigest,omitempty"`
}

// CatalogRestorePlan validates a Git-only recovery point. It is usable when
// the SQLite backup directory was lost but the private Catalog repository
// survived.
func (m *Manager) CatalogRestorePlan(ctx context.Context, ref string) (RestorePlan, error) {
	if strings.TrimSpace(ref) == "" {
		return RestorePlan{}, fmt.Errorf("a Git tag or commit is required")
	}
	if !safeGitName(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return RestorePlan{}, fmt.Errorf("Git tag or commit is not a safe ref")
	}
	catalog, catalogBytes, err := m.catalogAt(ctx, ref)
	if err != nil {
		return RestorePlan{}, err
	}
	if err := validateCatalogReferences(catalog); err != nil {
		return RestorePlan{}, err
	}
	digest, err := m.catalogDigestAt(ctx, ref, catalogBytes, catalog)
	if err != nil {
		return RestorePlan{}, err
	}
	commit, err := m.git(ctx, "rev-parse", ref+"^{commit}")
	if err != nil {
		return RestorePlan{}, err
	}
	return RestorePlan{
		GitCommit: strings.TrimSpace(commit), SchemaContract: catalog.SchemaContract,
		CatalogSHA256: digest, Counts: catalogCounts(catalog), PlaybookCount: len(catalog.Playbooks),
	}, nil
}

func (m *Manager) RestorePlan(ctx context.Context, backupID string) (RestorePlan, error) {
	manifest, err := m.Verify(ctx, backupID, true)
	if err != nil {
		return RestorePlan{}, err
	}
	catalog, catalogBytes, err := m.catalogAt(ctx, manifest.GitCommit)
	if err != nil {
		return RestorePlan{}, err
	}
	if err := validateCatalogReferences(catalog); err != nil {
		return RestorePlan{}, err
	}
	digest, err := m.catalogDigestAt(ctx, manifest.GitCommit, catalogBytes, catalog)
	if err != nil {
		return RestorePlan{}, err
	}
	if digest != manifest.CatalogSHA256 {
		return RestorePlan{}, fmt.Errorf("Catalog SHA-256 mismatch: got %s, expected %s", digest, manifest.CatalogSHA256)
	}
	if catalog.SchemaContract != manifest.SchemaContract {
		return RestorePlan{}, fmt.Errorf("Catalog schema contract %s does not match backup manifest %s", catalog.SchemaContract, manifest.SchemaContract)
	}
	return RestorePlan{
		BackupID: manifest.BackupID, GitCommit: manifest.GitCommit, SchemaContract: catalog.SchemaContract,
		DatabaseSHA256: manifest.DatabaseSHA256, CatalogSHA256: digest,
		Counts: catalogCounts(catalog), PlaybookCount: len(catalog.Playbooks),
	}, nil
}

func (m *Manager) RestoreDatabase(ctx context.Context, backupID, targetDatabase, targetPlaybookRoot string) (plan RestorePlan, err error) {
	plan, err = m.RestorePlan(ctx, backupID)
	if err != nil {
		return RestorePlan{}, err
	}
	if err := requireAbsentPath(targetDatabase); err != nil {
		return RestorePlan{}, err
	}
	if err := requireAbsentPath(targetPlaybookRoot); err != nil {
		return RestorePlan{}, err
	}
	manifest, err := readManifest(filepath.Join(m.config.BackupDir, filepath.Base(backupID), "manifest.json"))
	if err != nil {
		return RestorePlan{}, err
	}
	source := filepath.Join(m.config.BackupDir, filepath.Base(backupID), manifest.DatabaseFile)
	if err := copyNewFile(source, targetDatabase, 0o600); err != nil {
		return RestorePlan{}, err
	}
	createdPlaybooks := false
	defer func() {
		if err != nil {
			_ = os.Remove(targetDatabase)
			if createdPlaybooks {
				_ = safeRemoveTree(targetPlaybookRoot, filepath.Dir(targetPlaybookRoot))
			}
		}
	}()
	catalog, _, err := m.catalogAt(ctx, manifest.GitCommit)
	if err != nil {
		return RestorePlan{}, err
	}
	if err = os.MkdirAll(targetPlaybookRoot, 0o700); err != nil {
		return RestorePlan{}, err
	}
	createdPlaybooks = true
	if err = m.restorePlaybooks(ctx, manifest.GitCommit, catalog, targetPlaybookRoot); err != nil {
		return RestorePlan{}, err
	}
	if err = verifySQLite(ctx, targetDatabase); err != nil {
		return RestorePlan{}, err
	}
	return plan, nil
}

func (m *Manager) RestoreCatalog(ctx context.Context, ref, targetDatabase, targetPlaybookRoot string) (RestorePlan, error) {
	plan, err := m.CatalogRestorePlan(ctx, ref)
	if err != nil {
		return RestorePlan{}, err
	}
	if err := requireAbsentPath(targetDatabase); err != nil {
		return RestorePlan{}, err
	}
	if err := requireAbsentPath(targetPlaybookRoot); err != nil {
		return RestorePlan{}, err
	}
	catalog, _, err := m.catalogAt(ctx, plan.GitCommit)
	if err != nil {
		return RestorePlan{}, err
	}
	database, err := store.Open(ctx, targetDatabase)
	if err != nil {
		return RestorePlan{}, err
	}
	removeOnFailure := true
	defer func() {
		_ = database.Close()
		if removeOnFailure {
			_ = os.Remove(targetDatabase)
			_ = os.Remove(targetDatabase + "-wal")
			_ = os.Remove(targetDatabase + "-shm")
		}
	}()
	if err := restoreTables(ctx, database.DB(), catalog); err != nil {
		return RestorePlan{}, err
	}
	if err := database.Close(); err != nil {
		return RestorePlan{}, err
	}
	if err := os.MkdirAll(targetPlaybookRoot, 0o700); err != nil {
		return RestorePlan{}, err
	}
	if err := m.restorePlaybooks(ctx, plan.GitCommit, catalog, targetPlaybookRoot); err != nil {
		_ = safeRemoveTree(targetPlaybookRoot, filepath.Dir(targetPlaybookRoot))
		return RestorePlan{}, err
	}
	if err := verifySQLite(ctx, targetDatabase); err != nil {
		_ = safeRemoveTree(targetPlaybookRoot, filepath.Dir(targetPlaybookRoot))
		return RestorePlan{}, err
	}
	removeOnFailure = false
	return plan, nil
}

func restoreTables(ctx context.Context, database *sql.DB, catalog Catalog) error {
	if err := validateCatalogReferences(catalog); err != nil {
		return err
	}
	tables := map[string]TableDump{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	order := []string{"users", "platform_option_categories", "platform_options", "environment_parameter_definitions", "environment_parameter_defaults", "components", "component_release_lines", "component_releases", "component_dependencies", "action_definitions", "component_playbook_files", "scenarios", "scenario_revisions", "component_release_artifacts", "component_release_images"}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	for _, name := range order {
		table := tables[name]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(table.Columns)), ",")
		query := "INSERT INTO " + name + "(" + strings.Join(table.Columns, ",") + ") VALUES(" + placeholders + ")"
		for _, row := range table.Rows {
			values := make([]any, len(row))
			for index, cell := range row {
				values[index] = cell.Value()
			}
			if _, err := tx.ExecContext(ctx, query, values...); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		}
	}
	// Definition insert triggers deliberately advance publication fences. Reset
	// them to the immutable values captured by the Catalog after all rows exist.
	releases := tables["component_releases"]
	idIndex, generationIndex := columnIndex(releases.Columns, "id"), columnIndex(releases.Columns, "publication_generation")
	for _, row := range releases.Rows {
		if _, err := tx.ExecContext(ctx, `UPDATE component_releases SET publication_generation=? WHERE id=?`, row[generationIndex].Value(), row[idIndex].Value()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=? WHERE id=1`, catalog.PublicationGeneration); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return verifyForeignKeys(ctx, database)
}

func validateCatalogReferences(catalog Catalog) error {
	if err := validateCatalogBranchScopes(catalog); err != nil {
		return err
	}
	if err := validateScenarioCatalogAcceptance(catalog); err != nil {
		return err
	}
	if err := validateCatalogParameterDefaults(catalog); err != nil {
		return err
	}
	tables := map[string]TableDump{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	invalid := func(table, objectID, relation, message string) error {
		return &domain.CodedError{
			Code: "catalog_relationship_invalid", Message: message, Cause: domain.ErrInvalid,
			Details: map[string]any{"table": table, "objectId": objectID, "relation": relation},
		}
	}
	requiredText := func(table TableDump, row []DBCell, column string) (string, error) {
		cell := row[columnIndex(table.Columns, column)]
		if cell.Kind != "text" || cell.Text == "" {
			return "", invalid(table.Name, "", column, fmt.Sprintf("Catalog %s contains an invalid %s", table.Name, column))
		}
		return cell.Text, nil
	}
	optionalText := func(table TableDump, row []DBCell, column string) (string, error) {
		cell := row[columnIndex(table.Columns, column)]
		if cell.Kind == "null" {
			return "", nil
		}
		if cell.Kind != "text" {
			return "", invalid(table.Name, "", column, fmt.Sprintf("Catalog %s contains an invalid %s", table.Name, column))
		}
		return cell.Text, nil
	}
	type optionCategoryRecord struct {
		id, key, kind, parentID string
	}
	categoryByID := map[string]optionCategoryRecord{}
	categoryByKey := map[string]optionCategoryRecord{}
	optionCategories := tables["platform_option_categories"]
	for _, row := range optionCategories.Rows {
		id, err := requiredText(optionCategories, row, "id")
		if err != nil {
			return err
		}
		key, err := requiredText(optionCategories, row, "technical_key")
		if err != nil {
			return err
		}
		kind, err := requiredText(optionCategories, row, "category_type")
		if err != nil {
			return err
		}
		if kind != string(domain.PlatformOptionEnvironmentDimension) && kind != string(domain.PlatformOptionHostGroup) {
			return invalid(optionCategories.Name, id, "category_type", fmt.Sprintf("Catalog option category %s has an invalid type", id))
		}
		if _, exists := categoryByID[id]; exists || categoryByKey[key].id != "" {
			return invalid(optionCategories.Name, id, "technical_key", fmt.Sprintf("Catalog contains a duplicate option category %s", key))
		}
		parentID, err := optionalText(optionCategories, row, "parent_category_id")
		if err != nil {
			return err
		}
		record := optionCategoryRecord{id: id, key: key, kind: kind, parentID: parentID}
		categoryByID[id], categoryByKey[key] = record, record
	}
	for _, category := range categoryByID {
		if category.parentID == "" {
			continue
		}
		parent, found := categoryByID[category.parentID]
		if !found || parent.parentID != "" || parent.kind != string(domain.PlatformOptionEnvironmentDimension) || category.kind != parent.kind {
			return invalid(optionCategories.Name, category.id, "parent_category_id", fmt.Sprintf("Catalog option category %s has an invalid parent", category.id))
		}
	}
	optionValues := map[string]bool{}
	type optionRecord struct{ id, categoryID, parentID, value string }
	optionByID := map[string]optionRecord{}
	options := tables["platform_options"]
	for _, row := range options.Rows {
		id, err := requiredText(options, row, "id")
		if err != nil {
			return err
		}
		categoryID, err := requiredText(options, row, "category_id")
		if err != nil {
			return err
		}
		value, err := requiredText(options, row, "technical_value")
		if err != nil {
			return err
		}
		category, found := categoryByID[categoryID]
		if !found {
			return invalid(options.Name, id, "category_id", fmt.Sprintf("Catalog option %s references a missing category", id))
		}
		key := category.key + "\x00" + value
		if optionValues[key] {
			return invalid(options.Name, id, "technical_value", fmt.Sprintf("Catalog contains a duplicate option value %s", value))
		}
		optionValues[key] = true
		parentID, err := optionalText(options, row, "parent_option_id")
		if err != nil {
			return err
		}
		optionByID[id] = optionRecord{id: id, categoryID: categoryID, parentID: parentID, value: value}
	}
	for _, option := range optionByID {
		category := categoryByID[option.categoryID]
		if category.parentID == "" && option.parentID != "" {
			return invalid(options.Name, option.id, "parent_option_id", fmt.Sprintf("Catalog root option %s cannot have a parent", option.id))
		}
		if category.parentID != "" {
			parent, found := optionByID[option.parentID]
			if !found || parent.categoryID != category.parentID {
				return invalid(options.Name, option.id, "parent_option_id", fmt.Sprintf("Catalog child option %s has an invalid parent", option.id))
			}
		}
	}

	categories := []domain.PlatformOptionCategory{}
	for _, c := range categoryByID {
		category := domain.PlatformOptionCategory{ID: c.id, Key: c.key, Kind: domain.PlatformOptionCategoryKind(c.kind), ParentCategoryID: c.parentID}
		for _, o := range optionByID {
			if o.categoryID == c.id {
				category.Options = append(category.Options, domain.PlatformOption{ID: o.id, CategoryID: o.categoryID, ParentOptionID: o.parentID, Value: o.value})
			}
		}
		categories = append(categories, category)
	}
	catalogOptions, err := domain.NewCatalogOptions(categories)
	if err != nil {
		return err
	}

	componentIDs := tableIDs(tables["components"])
	type lineRecord struct{ componentID string }
	lines := map[string]lineRecord{}
	lineTable := tables["component_release_lines"]
	for _, row := range lineTable.Rows {
		id, err := requiredText(lineTable, row, "id")
		if err != nil {
			return err
		}
		componentID, err := requiredText(lineTable, row, "component_id")
		if err != nil {
			return err
		}
		if !componentIDs[componentID] {
			return invalid(lineTable.Name, id, "component_id", fmt.Sprintf("Catalog release line %s references missing Component %s", id, componentID))
		}
		lines[id] = lineRecord{componentID: componentID}
	}

	type releaseRecord struct {
		id, componentID, lineID, parentID, templateID, status, compatibility, releasedAt string
	}
	releaseRecords := map[string]releaseRecord{}
	releases := tables["component_releases"]
	for _, row := range releases.Rows {
		id, err := requiredText(releases, row, "id")
		if err != nil {
			return err
		}
		componentID, err := requiredText(releases, row, "component_id")
		if err != nil {
			return err
		}
		lineID, err := requiredText(releases, row, "line_id")
		if err != nil {
			return err
		}
		parentID, err := optionalText(releases, row, "parent_release_id")
		if err != nil {
			return err
		}
		templateID, err := optionalText(releases, row, "template_source_release_id")
		if err != nil {
			return err
		}
		status, err := requiredText(releases, row, "status")
		if err != nil {
			return err
		}
		compatibility, err := requiredText(releases, row, "compatibility")
		if err != nil {
			return err
		}
		releasedAt, err := optionalText(releases, row, "released_at")
		if err != nil {
			return err
		}
		line, found := lines[lineID]
		if !found {
			return invalid(releases.Name, id, "line_id", fmt.Sprintf("Catalog Release %s references missing release line %s", id, lineID))
		}
		if !componentIDs[componentID] || line.componentID != componentID {
			return invalid(releases.Name, id, "line_component", fmt.Sprintf("Catalog Release %s does not belong to the Component that owns release line %s", id, lineID))
		}
		if status == string(domain.ReleaseDraft) {
			return invalid(releases.Name, id, "status", fmt.Sprintf("Git Catalog must not contain Draft Release %s", id))
		}
		if status != string(domain.ReleaseReleased) && status != string(domain.ReleaseDeprecated) {
			return invalid(releases.Name, id, "status", fmt.Sprintf("Catalog Release %s has unsupported status %s", id, status))
		}
		if status == string(domain.ReleaseReleased) && releasedAt == "" {
			return invalid(releases.Name, id, "released_at", fmt.Sprintf("Released Catalog Release %s has no publication timestamp", id))
		}
		if parentID == "" && compatibility != string(domain.CompatibilityNotApplicable) {
			return invalid(releases.Name, id, "compatibility", fmt.Sprintf("Catalog baseline Release %s must use not_applicable compatibility", id))
		}
		if parentID != "" && compatibility != string(domain.CompatibilityCompatible) && compatibility != string(domain.CompatibilityBreaking) {
			return invalid(releases.Name, id, "compatibility", fmt.Sprintf("Catalog evolution Release %s must use compatible or breaking compatibility", id))
		}
		constraintsRaw, err := requiredText(releases, row, "environment_constraints_json")
		if err != nil {
			return err
		}
		constraints := map[string][]string{}
		if err := jsonUnmarshalStrict([]byte(constraintsRaw), &constraints); err != nil {
			return invalid(releases.Name, id, "environment_constraints_json", fmt.Sprintf("Catalog Release %s has invalid environment constraints", id))
		}
		for categoryKey, values := range constraints {
			category, found := categoryByKey[categoryKey]
			if !found || category.kind != string(domain.PlatformOptionEnvironmentDimension) || len(values) == 0 {
				return invalid(releases.Name, id, "environment_constraints_json", fmt.Sprintf("Catalog Release %s references an unknown environment dimension", id))
			}
			for _, value := range values {
				if !optionValues[categoryKey+"\x00"+value] {
					return invalid(releases.Name, id, "environment_constraints_json", fmt.Sprintf("Catalog Release %s references an unknown environment option", id))
				}
			}
		}
		constraintValues := map[string]any{}
		for key, values := range constraints {
			constraintValues[key] = values
		}
		if err := catalogOptions.ValidateConstraints(constraintValues); err != nil {
			return invalid(releases.Name, id, "environment_constraints_json", err.Error())
		}
		releaseRecords[id] = releaseRecord{id: id, componentID: componentID, lineID: lineID, parentID: parentID, templateID: templateID, status: status, compatibility: compatibility, releasedAt: releasedAt}
	}

	retainedSuccessors := map[string]string{}
	for _, release := range releaseRecords {
		if release.parentID != "" {
			parent, found := releaseRecords[release.parentID]
			if !found {
				return invalid(releases.Name, release.id, "parent_release_id", fmt.Sprintf("Catalog Release %s references missing parent %s", release.id, release.parentID))
			}
			if parent.id == release.id || parent.componentID != release.componentID || parent.lineID != release.lineID {
				return invalid(releases.Name, release.id, "parent_release_id", fmt.Sprintf("Catalog Release %s parent must be a different Release in the same Component and release line", release.id))
			}
			if release.status == string(domain.ReleaseReleased) || release.releasedAt != "" {
				if prior := retainedSuccessors[release.parentID]; prior != "" && prior != release.id {
					return invalid(releases.Name, release.id, "parent_release_id", fmt.Sprintf("Catalog parent Release %s has multiple retained successors", release.parentID))
				}
				retainedSuccessors[release.parentID] = release.id
			}
		}
		if release.templateID != "" {
			template, found := releaseRecords[release.templateID]
			if !found {
				return invalid(releases.Name, release.id, "template_source_release_id", fmt.Sprintf("Catalog Release %s references missing template source %s", release.id, release.templateID))
			}
			if template.id == release.id || template.componentID != release.componentID {
				return invalid(releases.Name, release.id, "template_source_release_id", fmt.Sprintf("Catalog Release %s template source must be a different Release of the same Component", release.id))
			}
		}
	}
	for id := range releaseRecords {
		seen := map[string]bool{}
		for current := id; current != ""; current = releaseRecords[current].parentID {
			if seen[current] {
				return invalid(releases.Name, id, "parent_release_id", fmt.Sprintf("Catalog Release %s belongs to a cyclic parent chain", id))
			}
			seen[current] = true
		}
	}

	dependencies := tables["component_dependencies"]
	for _, row := range dependencies.Rows {
		id, err := requiredText(dependencies, row, "id")
		if err != nil {
			return err
		}
		releaseID, err := requiredText(dependencies, row, "release_id")
		if err != nil {
			return err
		}
		upstreamComponentID, err := requiredText(dependencies, row, "upstream_component_id")
		if err != nil {
			return err
		}
		upstreamReleaseID, err := requiredText(dependencies, row, "upstream_release_id")
		if err != nil {
			return err
		}
		if _, found := releaseRecords[releaseID]; !found {
			return invalid(dependencies.Name, id, "release_id", fmt.Sprintf("Catalog dependency %s references missing Release %s", id, releaseID))
		}
		upstream, found := releaseRecords[upstreamReleaseID]
		if !found || upstream.componentID != upstreamComponentID {
			return invalid(dependencies.Name, id, "upstream_release_id", fmt.Sprintf("Catalog dependency %s upstream Release does not belong to Component %s", id, upstreamComponentID))
		}
	}

	actions := tables["action_definitions"]
	actionReleases := map[string]domain.ComponentRelease{}
	for _, row := range actions.Rows {
		id, err := requiredText(actions, row, "id")
		if err != nil {
			return err
		}
		releaseID, err := requiredText(actions, row, "release_id")
		if err != nil {
			return err
		}
		kind, err := requiredText(actions, row, "kind")
		if err != nil {
			return err
		}
		fromID, err := optionalText(actions, row, "from_release_id")
		if err != nil {
			return err
		}
		toID, err := optionalText(actions, row, "to_release_id")
		if err != nil {
			return err
		}
		pre, err := optionalText(actions, row, "pre_check_action_id")
		if err != nil {
			return err
		}
		post, err := optionalText(actions, row, "post_check_action_id")
		if err != nil {
			return err
		}
		action := domain.ActionDefinition{ID: id, ReleaseID: releaseID, Kind: domain.ActionKind(kind), PreCheckActionID: pre, PostCheckActionID: post}
		entry, err := domain.ActionTaskPath(action)
		if err != nil {
			return err
		}
		source, err := requiredText(actions, row, "playbook")
		if err != nil {
			return err
		}
		if !strings.HasSuffix(source, "/"+entry) {
			return invalid(actions.Name, id, "playbook", "action source must use its managed role entry")
		}
		release := actionReleases[releaseID]
		release.ID = releaseID
		release.Actions = append(release.Actions, action)
		actionReleases[releaseID] = release
		owner, found := releaseRecords[releaseID]
		if !found {
			return invalid(actions.Name, id, "release_id", fmt.Sprintf("Catalog Action %s references missing Release %s", id, releaseID))
		}
		hostGroup, err := optionalText(actions, row, "host_group")
		if err != nil {
			return err
		}
		hostGroupCategory, found := categoryByKey["hostGroup"]
		if !(kind == "check" && hostGroup == "") && (!found || hostGroupCategory.kind != string(domain.PlatformOptionHostGroup) || !optionValues["hostGroup\x00"+hostGroup]) {
			return invalid(actions.Name, id, "host_group", fmt.Sprintf("Catalog Action %s references an unknown host group", id))
		}
		for _, endpoint := range []struct{ name, id string }{{"from_release_id", fromID}, {"to_release_id", toID}} {
			if endpoint.id == "" {
				continue
			}
			target, found := releaseRecords[endpoint.id]
			if !found || target.componentID != owner.componentID || target.lineID != owner.lineID {
				return invalid(actions.Name, id, endpoint.name, fmt.Sprintf("Catalog Action %s endpoint %s must be a Release in the same Component and release line", id, endpoint.id))
			}
		}
		switch domain.ActionKind(kind) {
		case domain.ActionUpgrade:
			if owner.parentID == "" || fromID != owner.parentID || toID != owner.id {
				return invalid(actions.Name, id, "transition", fmt.Sprintf("Catalog Upgrade Action %s must point from its parent to its owning Release", id))
			}
		case domain.ActionRollback:
			if owner.parentID == "" {
				if fromID != "" || toID != "" {
					return invalid(actions.Name, id, "transition", fmt.Sprintf("Catalog baseline Rollback Action %s must not bind cross-Release endpoints", id))
				}
			} else if fromID != owner.id || toID != owner.parentID {
				return invalid(actions.Name, id, "transition", fmt.Sprintf("Catalog Rollback Action %s must point from its owning Release to its parent", id))
			}
		default:
			if fromID != "" || toID != "" {
				return invalid(actions.Name, id, "transition", fmt.Sprintf("Catalog non-transition Action %s must not bind Release endpoints", id))
			}
		}
	}

	for _, row := range releases.Rows {
		id, _ := requiredText(releases, row, "id")
		status, _ := requiredText(releases, row, "status")
		release := actionReleases[id]
		release.ID = id
		if err := domain.ValidateActionBindings(release, status != "draft"); err != nil {
			return invalid(actions.Name, id, "bindings", err.Error())
		}
	}

	releaseConstraints := map[string]map[string]any{}
	for _, row := range releases.Rows {
		id, _ := requiredText(releases, row, "id")
		raw, _ := requiredText(releases, row, "environment_constraints_json")
		var c map[string]any
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
		releaseConstraints[id] = c
	}
	revisions := tables["scenario_revisions"]
	graphIndex := columnIndex(revisions.Columns, "graph_json")
	for _, row := range revisions.Rows {
		labelsRaw, err := requiredText(revisions, row, "environment_constraints_json")
		if err != nil {
			return err
		}
		labels := map[string]any{}
		if err = json.Unmarshal([]byte(labelsRaw), &labels); err != nil {
			return err
		}
		if err := catalogOptions.ValidateConstraints(labels); err != nil {
			return fmt.Errorf("Catalog scenario adaptation is invalid: %w", err)
		}
		raw, _ := row[graphIndex].Value().(string)
		var graph struct {
			Nodes []struct {
				ReleaseID string `json:"releaseId"`
				HostGroup string `json:"hostGroup"`
			} `json:"nodes"`
		}
		if err := jsonUnmarshalStrict([]byte(raw), &graph); err != nil {
			return fmt.Errorf("Catalog scenario graph is invalid: %w", err)
		}
		var lifecycle domain.ScenarioRevision
		lifecycleRaw, _ := row[columnIndex(revisions.Columns, "lifecycle_json")].Value().(string)
		if err := json.Unmarshal([]byte(lifecycleRaw), &lifecycle); err != nil {
			return err
		}
		for _, job := range lifecycle.AcceptanceJobs {
			if !optionValues["hostGroup\x00"+job.HostGroup] {
				return invalid(revisions.Name, "", "lifecycle_json", fmt.Sprintf("Catalog scenario acceptance references unknown host group %s", job.HostGroup))
			}
		}
		for _, node := range graph.Nodes {
			if issues := domain.ScenarioAdaptationIssues(labels, releaseConstraints[node.ReleaseID], "", node.ReleaseID, true); len(issues) > 0 {
				return fmt.Errorf("Catalog scenario adaptation mismatch: %v", issues)
			}
			if _, found := releaseRecords[node.ReleaseID]; !found {
				return invalid(revisions.Name, "", "graph_json", fmt.Sprintf("Catalog scenario references missing Release %s", node.ReleaseID))
			}
			if !optionValues["hostGroup\x00"+node.HostGroup] {
				return invalid(revisions.Name, "", "graph_json", fmt.Sprintf("Catalog scenario references unknown host group %s", node.HostGroup))
			}
		}
	}
	return nil
}

func jsonUnmarshalStrict(contents []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	return decoder.Decode(target)
}

func tableIDs(table TableDump) map[string]bool {
	result := map[string]bool{}
	index := columnIndex(table.Columns, "id")
	for _, row := range table.Rows {
		value, _ := row[index].Value().(string)
		result[value] = true
	}
	return result
}

func columnIndex(columns []string, target string) int {
	for index, column := range columns {
		if column == target {
			return index
		}
	}
	panic("Catalog contract missing column " + target)
}

func verifyForeignKeys(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("restored Catalog violates a foreign key")
	}
	return rows.Err()
}

func (m *Manager) catalogAt(ctx context.Context, ref string) (Catalog, []byte, error) {
	contents, err := m.gitShow(ctx, ref, "catalog.json")
	if err != nil {
		return Catalog{}, nil, err
	}
	catalog, err := ParseCatalog(contents)
	return catalog, contents, err
}

func (m *Manager) catalogDigestAt(ctx context.Context, ref string, catalogBytes []byte, catalog Catalog) (string, error) {
	hash := sha256.New()
	_, _ = hash.Write(catalogBytes)
	for _, item := range catalog.Playbooks {
		_, _ = io.WriteString(hash, item.Path+"\x00"+item.SHA256+"\x00")
		contents, err := m.gitShow(ctx, ref, "playbooks/"+item.Path)
		if err != nil {
			return "", err
		}
		actual := sha256.Sum256(contents)
		if hex.EncodeToString(actual[:]) != item.SHA256 || int64(len(contents)) != item.SizeBytes {
			return "", fmt.Errorf("Git Playbook %s does not match its Catalog identity", item.Path)
		}
		_, _ = hash.Write(contents)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (m *Manager) restorePlaybooks(ctx context.Context, ref string, catalog Catalog, targetRoot string) error {
	rootAbs, err := filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	for _, item := range catalog.Playbooks {
		contents, err := m.gitShow(ctx, ref, "playbooks/"+item.Path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != item.SHA256 || int64(len(contents)) != item.SizeBytes {
			return fmt.Errorf("Playbook %s does not match its Catalog identity", item.Path)
		}
		target := filepath.Join(rootAbs, filepath.FromSlash(item.Path))
		rel, err := filepath.Rel(rootAbs, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Playbook %s escapes the restore root", item.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.Write(contents); err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func requireAbsentPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("target path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("target already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func copyNewFile(source, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		output.Close()
		if remove {
			os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}
