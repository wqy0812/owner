package deploydb

import (
	"codex/platform-demo/internal/domain"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex/platform-demo/internal/store"
)

const userExperienceSourceContract = "clusterforge-v1-20260905-scenario-lifecycle"

const scenarioLifecycleSourceContract = "clusterforge-v1-20260905-role-jobs"

type ScenarioLifecycleConversion struct {
	SourceContract   string `json:"sourceContract"`
	TargetContract   string `json:"targetContract"`
	PreservedTables  int    `json:"preservedTables"`
	PreservedRows    int64  `json:"preservedRows"`
	WorkspaceFiles   int    `json:"workspaceFiles"`
	HistoryPreserved bool   `json:"historyPreserved"`
	SourceUnchanged  bool   `json:"sourceUnchanged"`
}

type conversionTable struct {
	name    string
	columns []string
	digest  string
	count   int64
}

// ConvertScenarioLifecycle operates on a new full snapshot, never a business or
// foundation reset. Existing execution snapshots, timestamps and evidence bytes
// are retained; zero lifecycle versions continue using the historical digest.
func ConvertScenarioLifecycle(ctx context.Context, source, target, sourceRoot, targetRoot string) (report ScenarioLifecycleConversion, err error) {
	if sourceRoot == "" || targetRoot == "" {
		return report, fmt.Errorf("source and target Playbook roots are required")
	}
	report.SourceContract, err = Contract(ctx, source)
	if err != nil {
		return report, err
	}
	if report.SourceContract != scenarioLifecycleSourceContract && report.SourceContract != userExperienceSourceContract && report.SourceContract != store.CurrentSchemaContract {
		return report, fmt.Errorf("unsupported lifecycle conversion source contract %q", report.SourceContract)
	}
	if err = Verify(ctx, source, report.SourceContract); err != nil {
		return report, err
	}
	active, err := ActiveWork(ctx, source)
	if err != nil {
		return report, err
	}
	if len(active) > 0 {
		return report, fmt.Errorf("active work blocks lifecycle conversion: %s", strings.Join(active, ", "))
	}
	for _, path := range []string{target, targetRoot} {
		if _, e := os.Lstat(path); !os.IsNotExist(e) {
			if e != nil {
				return report, e
			}
			return report, fmt.Errorf("target already exists: %s", path)
		}
	}
	sourceAbs, err := filepath.Abs(sourceRoot)
	if err == nil {
		sourceAbs, err = filepath.EvalSymlinks(sourceAbs)
	}
	if err != nil {
		return report, err
	}
	targetParent, err := filepath.EvalSymlinks(filepath.Dir(targetRoot))
	if err != nil {
		return report, err
	}
	targetAbs, err := filepath.Abs(filepath.Join(targetParent, filepath.Base(targetRoot)))
	if err != nil {
		return report, err
	}
	databaseParent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return report, err
	}
	targetDatabaseAbs, err := filepath.Abs(filepath.Join(databaseParent, filepath.Base(target)))
	if err != nil {
		return report, err
	}
	if rel, e := filepath.Rel(sourceAbs, targetDatabaseAbs); e == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
		return report, fmt.Errorf("target database must be outside source workspace")
	}

	if rel, e := filepath.Rel(sourceAbs, targetAbs); e == nil && (rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")) {
		return report, fmt.Errorf("target workspace must be outside source workspace")
	}
	original, err := openReadOnly(source)
	if err != nil {
		return report, err
	}
	defer original.Close()
	// This converter has no authority to assign branch labels or rewrite
	// historical versions. A missing or conflicting scope needs a separate plan.
	if err = verifyExistingBranchScopes(ctx, original); err != nil {
		return report, err
	}
	before, err := conversionTables(ctx, original)
	if err != nil {
		return report, err
	}
	filesBefore, err := conversionWorkspaceManifest(sourceAbs)
	if err != nil {
		return report, err
	}
	if err = validateConversionWorkspaceContracts(ctx, original, filesBefore, report.SourceContract != scenarioLifecycleSourceContract); err != nil {
		return report, err
	}
	if err = Snapshot(ctx, source, target); err != nil {
		return report, err
	}
	createdRoot := false
	defer func() {
		if err != nil {
			os.Remove(target)
			os.Remove(target + "-wal")
			os.Remove(target + "-shm")
			os.Remove(target + "-journal")
			if createdRoot {
				os.RemoveAll(targetRoot)
			}
		}
	}()
	targetDB, err := sql.Open("sqlite", target)
	if err != nil {
		return report, err
	}
	defer targetDB.Close()
	targetDB.SetMaxOpenConns(1)
	if report.SourceContract == scenarioLifecycleSourceContract {
		tx, e := targetDB.BeginTx(ctx, nil)
		if e != nil {
			return report, e
		}
		for _, statement := range []string{
			`ALTER TABLE scenarios ADD COLUMN forked_from_scenario_id TEXT REFERENCES scenarios(id)`,
			`ALTER TABLE scenarios ADD COLUMN forked_from_revision_id TEXT REFERENCES scenario_revisions(id)`,
			`ALTER TABLE scenarios ADD COLUMN forked_from_digest TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE scenario_revisions ADD COLUMN lifecycle_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(lifecycle_json))`,
		} {
			if _, e = tx.ExecContext(ctx, statement); e != nil {
				tx.Rollback()
				return report, e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE schema_contract SET version=? WHERE id=1`, store.CurrentSchemaContract); e != nil {
			tx.Rollback()
			return report, e
		}
		if e = tx.Commit(); e != nil {
			return report, e
		}
	}
	if report.SourceContract != store.CurrentSchemaContract {
		var present int
		if err = targetDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('action_definitions') WHERE name='resource_contract_json'`).Scan(&present); err != nil {
			return report, err
		}
		tx, e := targetDB.BeginTx(ctx, nil)
		if e != nil {
			return report, e
		}
		if present == 0 {
			if _, e = tx.ExecContext(ctx, `ALTER TABLE action_definitions ADD COLUMN resource_contract_json TEXT CHECK(resource_contract_json IS NULL OR json_valid(resource_contract_json))`); e != nil {
				tx.Rollback()
				return report, e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE schema_contract SET version=? WHERE id=1`, store.CurrentSchemaContract); e != nil {
			tx.Rollback()
			return report, e
		}
		if e = tx.Commit(); e != nil {
			return report, e
		}
	}
	if err = targetDB.Close(); err != nil {
		return report, err
	}
	initialized, err := store.Open(ctx, target)
	if err != nil {
		return report, err
	}
	// Legacy history identifies candidates to recheck, never a proven baseline.
	if report.SourceContract == scenarioLifecycleSourceContract {
		_, err = initialized.DB().ExecContext(ctx, `INSERT INTO scenario_installations(environment_id,scenario_id,state,test_only,installation_digest,generation,updated_at)
SELECT r.environment_id,sr.scenario_id,'unverified',1,'',1,MAX(r.created_at)
FROM retained_run_history r JOIN scenario_revisions sr ON sr.id=r.scenario_revision_id
GROUP BY r.environment_id,sr.scenario_id`)
	}
	if closeErr := initialized.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return report, err
	}
	if err = os.Mkdir(targetRoot, 0700); err != nil {
		return report, err
	}
	createdRoot = true
	for relative, expected := range filesBefore {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		data, e := os.ReadFile(filepath.Join(sourceAbs, filepath.FromSlash(relative)))
		if e != nil {
			return report, e
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != expected {
			return report, fmt.Errorf("source workspace changed: %s", relative)
		}
		out := filepath.Join(targetRoot, filepath.FromSlash(relative))
		if err = os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			return report, err
		}
		if err = os.WriteFile(out, data, 0600); err != nil {
			return report, err
		}
	}
	if err = Verify(ctx, target, store.CurrentSchemaContract); err != nil {
		return report, err
	}
	converted, err := openReadOnly(target)
	if err != nil {
		return report, err
	}
	defer converted.Close()
	for _, table := range before {
		if table.name == "schema_contract" {
			continue
		}
		digest, count, e := conversionTableDigest(ctx, converted, table.name, table.columns)
		if e != nil {
			return report, e
		}
		if digest != table.digest || count != table.count {
			return report, fmt.Errorf("conversion changed preserved table %s", table.name)
		}
		report.PreservedTables++
		report.PreservedRows += count
	}
	after, err := conversionTables(ctx, original)
	if err != nil {
		return report, err
	}
	if !sameConversionTables(before, after) {
		return report, fmt.Errorf("source database changed during conversion; stop writers and retry")
	}
	filesAfter, err := conversionWorkspaceManifest(sourceAbs)
	if err != nil {
		return report, err
	}
	targetFiles, err := conversionWorkspaceManifest(targetRoot)
	if err != nil {
		return report, err
	}
	if !sameStringMap(filesBefore, filesAfter) || !sameStringMap(filesBefore, targetFiles) {
		return report, fmt.Errorf("source or target workspace changed during conversion")
	}
	report.TargetContract = store.CurrentSchemaContract
	report.WorkspaceFiles = len(filesBefore)
	report.HistoryPreserved = true
	report.SourceUnchanged = true
	return report, nil
}

func conversionTables(ctx context.Context, db *sql.DB) ([]conversionTable, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []conversionTable
	for rows.Next() {
		var table conversionTable
		if err = rows.Scan(&table.name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range tables {
		rows, err = db.QueryContext(ctx, "SELECT * FROM "+quoteIdentifier(tables[i].name)+" LIMIT 0")
		if err != nil {
			return nil, err
		}
		tables[i].columns, err = rows.Columns()
		rows.Close()
		if err != nil {
			return nil, err
		}
		tables[i].digest, tables[i].count, err = conversionTableDigest(ctx, db, tables[i].name, tables[i].columns)
		if err != nil {
			return nil, err
		}
	}
	return tables, nil
}
func conversionTableDigest(ctx context.Context, db *sql.DB, name string, columns []string) (string, int64, error) {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = quoteIdentifier(column)
	}
	list := strings.Join(quoted, ",")
	rows, err := db.QueryContext(ctx, "SELECT "+list+" FROM "+quoteIdentifier(name)+" ORDER BY "+list)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	hash := sha256.New()
	var count int64
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return "", 0, err
		}
		raw, e := json.Marshal(values)
		if e != nil {
			return "", 0, e
		}
		hash.Write(raw)
		hash.Write([]byte{'\n'})
		count++
	}
	return hex.EncodeToString(hash.Sum(nil)), count, rows.Err()
}
func sameConversionTables(a, b []conversionTable) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].count != b[i].count || a[i].digest != b[i].digest {
			return false
		}
	}
	return true
}
func conversionWorkspaceManifest(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace symlinks are not allowed: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("workspace entry is not a regular file: %s", path)
		}
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, file)
		file.Close()
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(relative)] = hex.EncodeToString(hash.Sum(nil))
		return nil
	})
	return files, err
}
func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Copying the same bytes is insufficient when those bytes already disagree with
// a stored immutable manifest. Validate stored content identities before output.
func validateConversionWorkspaceContracts(ctx context.Context, db *sql.DB, files map[string]string, lifecycle bool) error {
	rows, err := db.QueryContext(ctx, `SELECT id,playbook_workspace_root,playbook_tree_sha256 FROM component_releases ORDER BY id`)
	if err != nil {
		return err
	}
	type workspace struct{ id, root, tree string }
	var workspaces []workspace
	for rows.Next() {
		var w workspace
		if err = rows.Scan(&w.id, &w.root, &w.tree); err != nil {
			rows.Close()
			return err
		}
		workspaces = append(workspaces, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		rows, err = db.QueryContext(ctx, `SELECT relative_path,sha256 FROM component_playbook_files WHERE release_id=?`, w.id)
		if err != nil {
			return err
		}
		expected := map[string]string{}
		for rows.Next() {
			var path, sha string
			if err = rows.Scan(&path, &sha); err != nil {
				rows.Close()
				return err
			}
			expected[path] = sha
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if w.root == "" && w.tree == "" && len(expected) == 0 {
			continue
		}
		if !conversionSafePrefix(w.root) || w.tree == "" {
			return fmt.Errorf("component %s has invalid workspace identity", w.id)
		}
		actual := conversionSubtree(files, w.root)
		if len(actual) != len(expected) {
			return fmt.Errorf("component %s workspace manifest file set changed", w.id)
		}
		for path, sha := range expected {
			if sha == "" || actual[path] != sha {
				return fmt.Errorf("component %s workspace file %s differs from manifest", w.id, path)
			}
		}
		if conversionTreeDigest(actual) != w.tree {
			return fmt.Errorf("component %s workspace tree differs from manifest", w.id)
		}
	}
	if !lifecycle {
		return nil
	}
	rows, err = db.QueryContext(ctx, `SELECT id,scenario_id,lifecycle_json FROM scenario_revisions ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, scenarioID, raw string
		if err = rows.Scan(&id, &scenarioID, &raw); err != nil {
			return err
		}
		var revision domain.ScenarioRevision
		if err = json.Unmarshal([]byte(raw), &revision); err != nil {
			return err
		}
		if revision.AcceptanceWorkspaceRoot == "" && len(revision.AcceptanceJobs) == 0 {
			continue
		}
		prefix := "managed-scenarios/" + scenarioID + "/" + id + "/"
		if !conversionSafePrefix(prefix) || revision.AcceptanceWorkspaceRoot != prefix || revision.AcceptanceTreeSHA256 == "" {
			return fmt.Errorf("scenario %s has invalid acceptance workspace identity", id)
		}
		actual := conversionSubtree(files, prefix)
		if conversionTreeDigest(actual) != revision.AcceptanceTreeSHA256 {
			return fmt.Errorf("scenario %s acceptance workspace differs from manifest", id)
		}
		for _, job := range revision.AcceptanceJobs {
			path := "tasks/acceptance/" + job.ID + ".yml"
			if job.Playbook != prefix+path {
				return fmt.Errorf("scenario %s acceptance entrypoint is invalid", id)
			}
			if job.PlaybookSHA256 != "" && actual[path] != job.PlaybookSHA256 {
				return fmt.Errorf("scenario %s acceptance entrypoint differs from manifest", id)
			}
		}
	}
	return rows.Err()
}
func conversionSafePrefix(prefix string) bool {
	return prefix != "" && !filepath.IsAbs(prefix) && strings.HasSuffix(prefix, "/") && filepath.ToSlash(filepath.Clean(prefix))+"/" == prefix && !strings.HasPrefix(prefix, "../")
}
func conversionSubtree(files map[string]string, prefix string) map[string]string {
	result := map[string]string{}
	for path, sha := range files {
		if strings.HasPrefix(path, prefix) {
			result[strings.TrimPrefix(path, prefix)] = sha
		}
	}
	return result
}
func conversionTreeDigest(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write([]byte(files[path]))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
