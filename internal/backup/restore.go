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
	order := []string{"users", "components", "component_releases", "component_dependencies", "action_definitions", "scenarios", "scenario_revisions", "component_release_artifacts", "component_release_images"}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	tables := map[string]TableDump{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	releaseIDs := tableIDs(tables["component_releases"])
	dependencies := tables["component_dependencies"]
	for _, row := range dependencies.Rows {
		for _, column := range []string{"release_id", "upstream_release_id"} {
			value, _ := row[columnIndex(dependencies.Columns, column)].Value().(string)
			if !releaseIDs[value] {
				return fmt.Errorf("Catalog dependency references missing Release %s", value)
			}
		}
	}
	revisions := tables["scenario_revisions"]
	graphIndex := columnIndex(revisions.Columns, "graph_json")
	for _, row := range revisions.Rows {
		raw, _ := row[graphIndex].Value().(string)
		var graph struct {
			Nodes []struct {
				ReleaseID string `json:"releaseId"`
			} `json:"nodes"`
		}
		if err := jsonUnmarshalStrict([]byte(raw), &graph); err != nil {
			return fmt.Errorf("Catalog scenario graph is invalid: %w", err)
		}
		for _, node := range graph.Nodes {
			if !releaseIDs[node.ReleaseID] {
				return fmt.Errorf("Catalog scenario references missing Release %s", node.ReleaseID)
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
