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
)

type targetCatalogState struct {
	ComponentCount        int    `json:"componentCount"`
	ScenarioCount         int    `json:"scenarioCount"`
	PublicationGeneration int64  `json:"publicationGeneration"`
	SchemaContract        string `json:"schemaContract"`
}

func (m *Manager) CurrentDatabaseRestorePlan(ctx context.Context, ref string, database *sql.DB) (RestorePlan, error) {
	plan, err := m.CatalogRestorePlan(ctx, ref)
	if err != nil {
		return RestorePlan{}, err
	}
	state, err := inspectTargetCatalog(ctx, database)
	if err != nil {
		return RestorePlan{}, err
	}
	plan.TargetComponentCount, plan.TargetScenarioCount = state.ComponentCount, state.ScenarioCount
	if state.ComponentCount != 0 || state.ScenarioCount != 0 {
		return RestorePlan{}, targetNotEmptyError(state)
	}
	if state.SchemaContract != plan.SchemaContract {
		return RestorePlan{}, &domain.CodedError{Code: "catalog_schema_mismatch", Message: "Git Catalog schema contract does not match the target database", Cause: domain.ErrConflict, Details: map[string]any{"catalogSchemaContract": plan.SchemaContract, "targetSchemaContract": state.SchemaContract}}
	}
	plan.PlanDigest, err = restorePlanDigest(plan, state)
	return plan, err
}

// RestoreCurrentDatabase restores definitions into an empty live Catalog. The
// empty-target gate and plan digest are rechecked inside the transaction.
func (m *Manager) RestoreCurrentDatabase(ctx context.Context, ref, expectedPlanDigest string, database *sql.DB, playbookRoot string) (RestorePlan, error) {
	plan, err := m.CurrentDatabaseRestorePlan(ctx, ref, database)
	if err != nil {
		return RestorePlan{}, err
	}
	if expectedPlanDigest == "" || expectedPlanDigest != plan.PlanDigest {
		return RestorePlan{}, planChangedError()
	}
	catalog, _, err := m.catalogAt(ctx, plan.GitCommit)
	if err != nil {
		return RestorePlan{}, err
	}
	if err := validateCatalogReferences(catalog); err != nil {
		return RestorePlan{}, err
	}

	if err := os.MkdirAll(m.config.BackupDir, 0o700); err != nil {
		return RestorePlan{}, err
	}
	stage, err := os.MkdirTemp(m.config.BackupDir, ".catalog-restore-")
	if err != nil {
		return RestorePlan{}, err
	}
	defer os.RemoveAll(stage)
	if err := m.restorePlaybooks(ctx, plan.GitCommit, catalog, stage); err != nil {
		return RestorePlan{}, err
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return RestorePlan{}, err
	}
	defer tx.Rollback()
	state, err := inspectTargetCatalog(ctx, tx)
	if err != nil {
		return RestorePlan{}, err
	}
	if state.ComponentCount != 0 || state.ScenarioCount != 0 {
		return RestorePlan{}, targetNotEmptyError(state)
	}
	if state.SchemaContract != plan.SchemaContract {
		return RestorePlan{}, &domain.CodedError{Code: "catalog_schema_mismatch", Message: "Git Catalog schema contract does not match the target database", Cause: domain.ErrConflict, Details: map[string]any{"catalogSchemaContract": plan.SchemaContract, "targetSchemaContract": state.SchemaContract}}
	}
	currentDigest, err := restorePlanDigest(plan, state)
	if err != nil {
		return RestorePlan{}, err
	}
	if currentDigest != expectedPlanDigest {
		return RestorePlan{}, planChangedError()
	}
	if err := checkCatalogObjectConflicts(ctx, tx, catalog); err != nil {
		return RestorePlan{}, err
	}
	if err := restoreUsers(ctx, tx, catalog); err != nil {
		return RestorePlan{}, err
	}
	if err := restorePlatformOptionCatalog(ctx, tx, catalog); err != nil {
		return RestorePlan{}, err
	}
	if err := restoreDefinitionTables(ctx, tx, catalog, state.PublicationGeneration); err != nil {
		return RestorePlan{}, err
	}
	if err := verifyForeignKeysTx(ctx, tx); err != nil {
		return RestorePlan{}, err
	}

	createdFiles, createdDirectories, err := promotePlaybooks(stage, playbookRoot, catalog.Playbooks)
	if err != nil {
		return RestorePlan{}, err
	}
	committed := false
	defer func() {
		if !committed {
			cleanupPromotedPlaybooks(createdFiles, createdDirectories)
		}
	}()
	if err := tx.Commit(); err != nil {
		return RestorePlan{}, err
	}
	committed = true
	return plan, nil
}

type catalogStateQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func inspectTargetCatalog(ctx context.Context, database catalogStateQueryer) (targetCatalogState, error) {
	var state targetCatalogState
	err := database.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM components),(SELECT COUNT(*) FROM scenarios),(SELECT generation FROM publication_state WHERE id=1),(SELECT version FROM schema_contract WHERE id=1)`).Scan(&state.ComponentCount, &state.ScenarioCount, &state.PublicationGeneration, &state.SchemaContract)
	return state, err
}

func targetNotEmptyError(state targetCatalogState) error {
	return &domain.CodedError{Code: "target_catalog_not_empty", Message: "target database already contains components or scenarios", Cause: domain.ErrConflict, Details: map[string]any{"componentCount": state.ComponentCount, "scenarioCount": state.ScenarioCount}}
}

func planChangedError() error {
	return &domain.CodedError{Code: "restore_plan_changed", Message: "restore plan has changed; preview it again", Cause: domain.ErrConflict}
}

func restorePlanDigest(plan RestorePlan, state targetCatalogState) (string, error) {
	payload := struct {
		GitCommit             string `json:"gitCommit"`
		CatalogSHA256         string `json:"catalogSha256"`
		SchemaContract        string `json:"schemaContract"`
		ComponentCount        int    `json:"componentCount"`
		ScenarioCount         int    `json:"scenarioCount"`
		PublicationGeneration int64  `json:"publicationGeneration"`
		TargetSchemaContract  string `json:"targetSchemaContract"`
	}{plan.GitCommit, plan.CatalogSHA256, plan.SchemaContract, state.ComponentCount, state.ScenarioCount, state.PublicationGeneration, state.SchemaContract}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func checkCatalogObjectConflicts(ctx context.Context, tx *sql.Tx, catalog Catalog) error {
	tables := catalogTableMap(catalog)
	conflicts := map[string][]map[string]string{"components": {}, "scenarios": {}}
	for _, spec := range []struct{ catalogTable, databaseTable string }{{"components", "components"}, {"scenarios", "scenarios"}} {
		table := tables[spec.catalogTable]
		idIndex, slugIndex := columnIndex(table.Columns, "id"), columnIndex(table.Columns, "slug")
		for _, row := range table.Rows {
			id, _ := row[idIndex].Value().(string)
			slug, _ := row[slugIndex].Value().(string)
			var existingID, existingSlug string
			err := tx.QueryRowContext(ctx, "SELECT id,slug FROM "+spec.databaseTable+" WHERE id=? OR slug=? LIMIT 1", id, slug).Scan(&existingID, &existingSlug)
			if err == nil {
				conflicts[spec.catalogTable] = append(conflicts[spec.catalogTable], map[string]string{"id": id, "slug": slug, "existingId": existingID, "existingSlug": existingSlug})
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
	}
	if len(conflicts["components"]) > 0 || len(conflicts["scenarios"]) > 0 {
		return &domain.CodedError{Code: "catalog_conflict", Message: "Git Catalog conflicts with existing components or scenarios", Details: conflicts, Cause: domain.ErrConflict}
	}
	return nil
}

func restoreUsers(ctx context.Context, tx *sql.Tx, catalog Catalog) error {
	table := catalogTableMap(catalog)["users"]
	idIndex, nameIndex, roleIndex := columnIndex(table.Columns, "id"), columnIndex(table.Columns, "name"), columnIndex(table.Columns, "role")
	for _, row := range table.Rows {
		id, _ := row[idIndex].Value().(string)
		name, _ := row[nameIndex].Value().(string)
		role, _ := row[roleIndex].Value().(string)
		var existingName, existingRole string
		err := tx.QueryRowContext(ctx, `SELECT name,role FROM users WHERE id=?`, id).Scan(&existingName, &existingRole)
		if err == nil {
			if existingName != name || existingRole != role {
				return &domain.CodedError{Code: "catalog_user_conflict", Message: "Git Catalog user conflicts with an existing identity", Details: map[string]any{"id": id, "catalogName": name, "catalogRole": role, "existingName": existingName, "existingRole": existingRole}, Cause: domain.ErrConflict}
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := insertTableRow(ctx, tx, table, row); err != nil {
			return fmt.Errorf("restore users: %w", err)
		}
	}
	return nil
}

func restoreDefinitionTables(ctx context.Context, tx *sql.Tx, catalog Catalog, currentGeneration int64) error {
	tables := catalogTableMap(catalog)
	for _, name := range []string{"environment_parameter_definitions", "environment_parameter_defaults", "components", "component_release_lines", "component_releases", "component_dependencies", "action_definitions", "scenarios", "scenario_revisions", "component_release_artifacts", "component_release_images"} {
		for _, row := range tables[name].Rows {
			if err := insertTableRow(ctx, tx, tables[name], row); err != nil {
				if (name == "components" || name == "component_release_lines" || name == "scenarios") && strings.Contains(err.Error(), "UNIQUE constraint") {
					return &domain.CodedError{Code: "catalog_conflict", Message: "Git Catalog conflicts with an existing component or scenario", Details: map[string]any{"table": name}, Cause: domain.ErrConflict}
				}
				return fmt.Errorf("restore %s: %w", name, err)
			}
		}
	}
	releases := tables["component_releases"]
	idIndex, generationIndex := columnIndex(releases.Columns, "id"), columnIndex(releases.Columns, "publication_generation")
	for _, row := range releases.Rows {
		if _, err := tx.ExecContext(ctx, `UPDATE component_releases SET publication_generation=? WHERE id=?`, row[generationIndex].Value(), row[idIndex].Value()); err != nil {
			return err
		}
	}
	nextGeneration := catalog.PublicationGeneration
	if currentGeneration > nextGeneration {
		nextGeneration = currentGeneration
	}
	nextGeneration++
	_, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=? WHERE id=1`, nextGeneration)
	return err
}

func restorePlatformOptionCatalog(ctx context.Context, tx *sql.Tx, catalog Catalog) error {
	tables := catalogTableMap(catalog)
	categories := tables["platform_option_categories"]
	for _, row := range categories.Rows {
		values := rowValues(row)
		id, _ := values[columnIndex(categories.Columns, "id")].(string)
		key, _ := values[columnIndex(categories.Columns, "technical_key")].(string)
		label, _ := values[columnIndex(categories.Columns, "label")].(string)
		kind, _ := values[columnIndex(categories.Columns, "category_type")].(string)
		required, _ := values[columnIndex(categories.Columns, "environment_required")].(int64)
		var existingID, existingKey, existingLabel, existingKind string
		var existingRequired int64
		err := tx.QueryRowContext(ctx, `
SELECT id,technical_key,label,category_type,environment_required
FROM platform_option_categories WHERE id=? OR technical_key=? OR label=? LIMIT 1`, id, key, label).Scan(
			&existingID, &existingKey, &existingLabel, &existingKind, &existingRequired,
		)
		if err == nil {
			if existingID != id || existingKey != key || existingLabel != label || existingKind != kind || existingRequired != required {
				return &domain.CodedError{Code: "catalog_platform_option_conflict", Message: "Git Catalog option category conflicts with the target directory", Details: map[string]any{"id": id, "key": key}, Cause: domain.ErrConflict}
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := insertTableRow(ctx, tx, categories, row); err != nil {
			return fmt.Errorf("restore platform option categories: %w", err)
		}
	}
	options := tables["platform_options"]
	for _, row := range options.Rows {
		values := rowValues(row)
		id, _ := values[columnIndex(options.Columns, "id")].(string)
		categoryID, _ := values[columnIndex(options.Columns, "category_id")].(string)
		value, _ := values[columnIndex(options.Columns, "technical_value")].(string)
		label, _ := values[columnIndex(options.Columns, "label")].(string)
		var existingID, existingCategoryID, existingValue, existingLabel string
		err := tx.QueryRowContext(ctx, `
SELECT id,category_id,technical_value,label FROM platform_options
WHERE id=? OR (category_id=? AND (technical_value=? OR label=?)) LIMIT 1`, id, categoryID, value, label).Scan(
			&existingID, &existingCategoryID, &existingValue, &existingLabel,
		)
		if err == nil {
			if existingID != id || existingCategoryID != categoryID || existingValue != value || existingLabel != label {
				return &domain.CodedError{Code: "catalog_platform_option_conflict", Message: "Git Catalog option conflicts with the target directory", Details: map[string]any{"id": id, "value": value}, Cause: domain.ErrConflict}
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := insertTableRow(ctx, tx, options, row); err != nil {
			return fmt.Errorf("restore platform options: %w", err)
		}
	}
	return nil
}

func verifyForeignKeysTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("restored Catalog contains a foreign key violation")
	}
	return rows.Err()
}

func insertTableRow(ctx context.Context, tx *sql.Tx, table TableDump, row []DBCell) error {
	values := rowValues(row)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(values)), ",")
	_, err := tx.ExecContext(ctx, "INSERT INTO "+table.Name+"("+strings.Join(table.Columns, ",")+") VALUES("+placeholders+")", values...)
	return err
}

func catalogTableMap(catalog Catalog) map[string]TableDump {
	tables := make(map[string]TableDump, len(catalog.Tables))
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	return tables
}
func rowValues(row []DBCell) []any {
	values := make([]any, len(row))
	for i, cell := range row {
		values[i] = cell.Value()
	}
	return values
}

func promotePlaybooks(stage, targetRoot string, playbooks []Playbook) ([]string, []string, error) {
	root, err := filepath.Abs(targetRoot)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	created := []string{}
	createdDirectories := []string{}
	cleanup := func() { cleanupPromotedPlaybooks(created, createdDirectories) }
	for _, playbook := range playbooks {
		directories, err := ensureSafeDirectories(root, filepath.Dir(filepath.FromSlash(playbook.Path)))
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		createdDirectories = append(createdDirectories, directories...)
		target := filepath.Join(root, filepath.FromSlash(playbook.Path))
		if info, statErr := os.Lstat(target); statErr == nil {
			if !info.Mode().IsRegular() {
				cleanup()
				return nil, nil, playbookConflict(playbook.Path, "target is not a regular file")
			}
			digest, digestErr := fileSHA256(target)
			if digestErr != nil {
				cleanup()
				return nil, nil, digestErr
			}
			if digest != playbook.SHA256 {
				cleanup()
				return nil, nil, playbookConflict(playbook.Path, "content SHA-256 differs")
			}
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			cleanup()
			return nil, nil, statErr
		}
		if err := copyExclusive(filepath.Join(stage, filepath.FromSlash(playbook.Path)), target); err != nil {
			cleanup()
			return nil, nil, err
		}
		created = append(created, target)
	}
	return created, createdDirectories, nil
}

func ensureSafeDirectories(root, relative string) ([]string, error) {
	if relative == "." {
		return nil, nil
	}
	current := root
	created := []string{}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			created = append(created, current)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, playbookConflict(filepath.ToSlash(relative), "parent is not a real directory")
		}
	}
	return created, nil
}

func cleanupPromotedPlaybooks(files, directories []string) {
	for _, path := range files {
		_ = os.Remove(path)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		_ = os.Remove(directories[index])
	}
}

func playbookConflict(path, reason string) error {
	return &domain.CodedError{Code: "playbook_conflict", Message: "Playbook path conflicts with existing content", Details: map[string]any{"path": path, "reason": reason}, Cause: domain.ErrConflict}
}

func copyExclusive(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	ok = true
	return output.Close()
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
