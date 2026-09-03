package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"codex/platform-demo/internal/domain"
)

// beginCatalogWrite takes SQLite's writer slot before any validation reads.
// The no-op fence does not advance the publication epoch or require a migration.
func (s *Store) beginCatalogWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, publicationWriteError(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE publication_state SET generation=generation WHERE id=1"); err != nil {
		_ = tx.Rollback()
		return nil, publicationWriteError(err)
	}
	return tx, nil
}

type CatalogValidationSnapshot struct {
	Options    domain.CatalogOptions
	Parameters []domain.EnvironmentParameterDefinition
	Variables  []domain.EnvironmentVariableDefinition
	Releases   []domain.ComponentRelease
}

func loadCatalogDefinitions(ctx context.Context, q queryer) (CatalogValidationSnapshot, error) {
	var snapshot CatalogValidationSnapshot
	categories, err := listPlatformOptionCategories(ctx, q, newPlatformReferenceIndex())
	if err != nil {
		return snapshot, err
	}
	snapshot.Options, err = domain.NewCatalogOptions(categories)
	if err != nil {
		return snapshot, err
	}
	snapshot.Parameters, err = listEnvironmentParameterDefinitions(ctx, q)
	if err != nil {
		return snapshot, err
	}
	snapshot.Variables, err = listEnvironmentVariableDefinitions(ctx, q)
	return snapshot, err
}

func loadCatalogValidation(ctx context.Context, q queryer) (CatalogValidationSnapshot, error) {
	snapshot, err := loadCatalogDefinitions(ctx, q)
	if err != nil {
		return snapshot, err
	}
	rows, err := q.QueryContext(ctx, "SELECT id,status,released_at,parameters_json FROM component_releases")
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var release domain.ComponentRelease
		var released sql.NullString
		var raw string
		if err := rows.Scan(&release.ID, &release.Status, &released, &raw); err != nil {
			return snapshot, err
		}
		release.ReleasedAt = parseNullTime(released)
		if err := json.Unmarshal([]byte(raw), &release.Parameters); err != nil {
			return snapshot, err
		}
		snapshot.Releases = append(snapshot.Releases, release)
	}
	return snapshot, rows.Err()
}

func (s *Store) ReadCatalogValidation(ctx context.Context) (CatalogValidationSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CatalogValidationSnapshot{}, err
	}
	defer tx.Rollback()
	return loadCatalogValidation(ctx, tx)
}

func (c CatalogValidationSnapshot) ValidateValues(values map[string]any, variables map[string]string, refs []domain.CredentialRef) error {
	return domain.ValidateEnvironmentValues(values, variables, refs, c.Releases, c.Parameters, c.Variables)
}

func catalogWriteValidationError(err error) error {
	if errors.Is(err, domain.ErrInvalid) {
		return fmt.Errorf("%w: catalog reference validation failed; refresh and retry: %v", domain.ErrConflict, err)
	}
	return err
}

func validateReleaseCatalogTx(ctx context.Context, tx *sql.Tx, release domain.ComponentRelease, previous *domain.ComponentRelease) error {
	c, err := loadCatalogDefinitions(ctx, tx)
	if err != nil {
		return err
	}
	if err := c.Options.ValidateConstraints(release.EnvironmentConstraints); err != nil {
		return catalogWriteValidationError(err)
	}
	var old map[string]any
	oldGroups := map[string]string{}
	if previous != nil {
		old = previous.EnvironmentConstraints
		for _, a := range previous.Actions {
			oldGroups[a.ID] = a.HostGroup
		}
	}
	if err := c.Options.ValidateConstraintChanges(release.EnvironmentConstraints, old); err != nil {
		return err
	}
	for _, a := range release.Actions {
		if a.HostGroup == "" {
			continue
		} // incomplete Drafts have no reference yet
		if err := c.Options.ValidateHostGroupChange(a.HostGroup, oldGroups[a.ID]); err != nil {
			return catalogWriteValidationError(err)
		}
	}
	return catalogWriteValidationError(domain.ValidateGlobalParameterBindings(release.Parameters, c.Parameters))
}

func validateScenarioCatalogTx(ctx context.Context, tx *sql.Tx, graph, previous domain.ScenarioGraph) error {
	categories, err := listPlatformOptionCategories(ctx, tx, newPlatformReferenceIndex())
	if err != nil {
		return err
	}
	options, err := domain.NewCatalogOptions(categories)
	if err != nil {
		return err
	}
	oldGroups := map[string]string{}
	for _, n := range previous.Nodes {
		oldGroups[n.ID] = n.HostGroup
	}
	for _, n := range graph.Nodes {
		if n.HostGroup == "" {
			continue
		}
		if err := options.ValidateHostGroupChange(n.HostGroup, oldGroups[n.ID]); err != nil {
			return catalogWriteValidationError(err)
		}
	}
	return nil
}

type EnvironmentRevisionWrite struct {
	ExpectedCurrentRevisionID string
	RestoreSourceRevisionID   string
	ValidateAllValues         bool
	RequireCompleteFacts      bool
	ApplyParameterDefaults    bool
	RequireGlobalParameters   bool
}

func materializeEnvironmentParametersTx(ctx context.Context, tx *sql.Tx, r *domain.EnvironmentRevision, write EnvironmentRevisionWrite) error {
	if !write.ApplyParameterDefaults || write.RestoreSourceRevisionID != "" {
		return nil
	}
	c, err := loadCatalogValidation(ctx, tx)
	if err != nil {
		return err
	}
	r.Parameters, err = domain.MaterializeEnvironmentParameterDefaults(r.Parameters, c.Releases, c.Parameters, write.RequireGlobalParameters)
	return err
}

func sameEnvironmentSnapshot(a, b domain.EnvironmentRevision) bool {
	return (len(a.Facts) == 0 && len(b.Facts) == 0 || jsonText(a.Facts) == jsonText(b.Facts)) &&
		reflect.DeepEqual(a.Inventory, b.Inventory) &&
		(len(a.Parameters) == 0 && len(b.Parameters) == 0 || jsonText(a.Parameters) == jsonText(b.Parameters)) &&
		(len(a.Variables) == 0 && len(b.Variables) == 0 || jsonText(a.Variables) == jsonText(b.Variables)) &&
		(len(a.CredentialRefs) == 0 && len(b.CredentialRefs) == 0 || jsonText(a.CredentialRefs) == jsonText(b.CredentialRefs))
}

func environmentRevisionTx(ctx context.Context, tx *sql.Tx, id string) (domain.EnvironmentRevision, error) {
	r, err := scanEnvironmentRevision(tx.QueryRowContext(ctx, "SELECT id,environment_id,revision,facts_json,inventory_json,variables_json,parameters_json,credential_refs_json,created_by,change_reason,created_at FROM environment_revisions WHERE id=?", id))
	return r, mapSQLError(err)
}

func validateEnvironmentCatalogTx(ctx context.Context, tx *sql.Tx, r, previous domain.EnvironmentRevision, restore bool, allValues bool, requireComplete bool) error {
	c, err := loadCatalogValidation(ctx, tx)
	if err != nil {
		return err
	}
	if err := c.Options.ValidateFacts(r.Facts, requireComplete && !restore); err != nil {
		return catalogWriteValidationError(err)
	}
	if err := c.Options.ValidateInventory(r.Inventory); err != nil {
		return catalogWriteValidationError(err)
	}
	if restore {
		return nil
	} // exact retained snapshot was checked by the caller
	if err := c.Options.ValidateFactChanges(r.Facts, previous.Facts); err != nil {
		return err
	}
	if err := c.Options.ValidateInventoryChanges(r.Inventory, previous.Inventory); err != nil {
		return catalogWriteValidationError(err)
	}
	values := map[string]any{}
	for k, v := range r.Parameters {
		prior, found := previous.Parameters[k]
		if allValues || !found || !reflect.DeepEqual(prior, v) {
			values[k] = v
		}
	}
	variables := map[string]string{}
	for k, v := range r.Variables {
		prior, found := previous.Variables[k]
		if allValues || !found || prior != v {
			variables[k] = v
		}
	}
	if err := c.ValidateValues(values, variables, r.CredentialRefs); err != nil {
		return catalogWriteValidationError(err)
	}
	_, err = domain.NormalizeEnvironmentVariables(r.Variables, r.CredentialRefs)
	return catalogWriteValidationError(err)
}
