// Package runmigration implements the explicit, offline conversion between two
// exact database contracts. No server code imports the source-contract decoder.
package runmigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/deploydb"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/runarchive"
	"codex/platform-demo/internal/runmigration/legacy"
	"codex/platform-demo/internal/store"
)

const ReportVersion = "clusterforge-run-migration-v1"

type Options struct {
	Source, Target, ArchiveRoot, ReportPath, ExpectedSourceDigest string
	DryRun                                                        bool
}
type TableReport struct {
	Name         string `json:"name"`
	Count        int64  `json:"count"`
	SourceDigest string `json:"sourceDigest"`
	TargetDigest string `json:"targetDigest"`
}
type RunReport struct {
	ID                   string   `json:"id"`
	SourceSnapshotSHA256 string   `json:"sourceSnapshotSha256"`
	TargetSnapshotSHA256 string   `json:"targetSnapshotSha256"`
	References           []string `json:"references"`
}
type ArchiveReport struct {
	RunID  string `json:"runId"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Report struct {
	FormatVersion        string          `json:"formatVersion"`
	Status               string          `json:"status"`
	SourceContract       string          `json:"sourceContract"`
	TargetContract       string          `json:"targetContract"`
	SourceDigest         string          `json:"sourceDigest"`
	SourceBackupSHA256   string          `json:"sourceBackupSha256"`
	SourceBackupPath     string          `json:"sourceBackupPath,omitempty"`
	TargetDatabaseSHA256 string          `json:"targetDatabaseSha256,omitempty"`
	ToolVersion          string          `json:"toolVersion"`
	CreatedAt            time.Time       `json:"createdAt"`
	Tables               []TableReport   `json:"tables"`
	Runs                 []RunReport     `json:"runs"`
	Archives             []ArchiveReport `json:"archives"`
	ArchiveRoot          string          `json:"archiveRoot,omitempty"`
}

func hashBytes(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func fileHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SourceDigest binds the database plus WAL. A main-file checksum alone cannot
// detect writes still held in WAL. Shared-memory lock bytes are not data.
func SourceDigest(path string) (string, error) {
	main, e := fileHash(path)
	if e != nil {
		return "", e
	}
	wal, e := fileHash(path + "-wal")
	if os.IsNotExist(e) {
		wal = ""
	} else if e != nil {
		return "", e
	}
	return hashBytes([]byte(main + "\n" + wal)), nil
}
func openDB(path string, readonly bool) (*sql.DB, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	u := url.URL{Scheme: "file", Path: abs}
	if readonly {
		u.RawQuery = "mode=ro&_pragma=busy_timeout(5000)"
	}
	db, e := sql.Open("sqlite", u.String())
	if e == nil {
		db.SetMaxOpenConns(1)
	}
	return db, e
}
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func toolVersion() string {
	if b, ok := debug.ReadBuildInfo(); ok {
		for _, s := range b.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
		return b.Main.Path + "@" + b.GoVersion
	}
	return "unknown"
}
func requireAbsent(path string) error {
	if path == "" {
		return fmt.Errorf("output path is required")
	}
	if _, e := os.Lstat(path); e == nil {
		return fmt.Errorf("output already exists: %s", path)
	} else if !os.IsNotExist(e) {
		return e
	}
	return nil
}

func preflight(ctx context.Context, path string) error {
	if e := deploydb.Verify(ctx, path, legacy.SchemaContract); e != nil {
		return e
	}
	active, e := deploydb.ActiveWork(ctx, path)
	if e != nil {
		return e
	}
	if len(active) > 0 {
		return fmt.Errorf("active work blocks offline migration: %s", strings.Join(active, ", "))
	}
	db, e := openDB(path, true)
	if e != nil {
		return e
	}
	defer db.Close()
	var pending int
	if e = db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM run_archive_tasks WHERE status IN ('queued','running'))+(SELECT COUNT(*) FROM playbook_action_mutations)`).Scan(&pending); e != nil {
		return e
	}
	if pending != 0 {
		return fmt.Errorf("pending archive or workspace work blocks offline migration")
	}
	return nil
}

// Migrate creates only new files. The caller must stop all writers before taking
// the source fingerprint. Neither dry-run nor apply changes the source database.
func Migrate(ctx context.Context, o Options) (report Report, err error) {
	if o.Source == "" || o.Target == "" || o.ReportPath == "" {
		return report, fmt.Errorf("source, target and report paths are required")
	}
	for _, path := range []*string{&o.Source, &o.Target, &o.ReportPath, &o.ArchiveRoot} {
		if *path != "" {
			abs, e := filepath.Abs(*path)
			if e != nil {
				return report, e
			}
			*path = abs
		}
	}
	for _, p := range []string{o.Target, o.Target + ".source.db", o.ReportPath} {
		if err = requireAbsent(p); err != nil {
			return report, err
		}
	}
	if err = preflight(ctx, o.Source); err != nil {
		return report, err
	}
	sourceDigest, err := SourceDigest(o.Source)
	if err != nil {
		return report, err
	}
	if !o.DryRun && o.ExpectedSourceDigest == "" {
		return report, fmt.Errorf("expected source digest from dry-run is required")
	}
	if o.ExpectedSourceDigest != "" && o.ExpectedSourceDigest != sourceDigest {
		return report, fmt.Errorf("source changed since preflight")
	}
	stage, err := os.MkdirTemp(filepath.Dir(o.Target), ".run-migration-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stage)
	backupPath := filepath.Join(stage, "source.db")
	if err = deploydb.Snapshot(ctx, o.Source, backupPath); err != nil {
		return report, err
	}
	backupHash, err := fileHash(backupPath)
	if err != nil {
		return report, err
	}
	report = Report{FormatVersion: ReportVersion, Status: "checked", SourceContract: legacy.SchemaContract, TargetContract: store.CurrentSchemaContract, SourceDigest: sourceDigest, SourceBackupSHA256: backupHash, ToolVersion: toolVersion(), CreatedAt: time.Now().UTC(), Tables: []TableReport{}, Runs: []RunReport{}, Archives: []ArchiveReport{}, ArchiveRoot: o.ArchiveRoot}
	source, err := openDB(backupPath, true)
	if err != nil {
		return report, err
	}
	defer source.Close()
	if report.Archives, err = verifyArchives(ctx, source, o.ArchiveRoot); err != nil {
		return report, err
	}
	targetPath := filepath.Join(stage, "target.db")
	// SQLite otherwise creates this file with 0666 subject to the caller's umask.
	// Reserve a private file before writing any copied business data.
	privateTarget, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return report, err
	}
	if err = privateTarget.Close(); err != nil {
		return report, err
	}
	// Always build and verify a complete target, even in dry-run. It is never
	// published in dry-run, allowing the preflight to catch import/constraint errors.
	initialized, err := store.Open(ctx, targetPath)
	if err != nil {
		return report, err
	}
	if err = initialized.Close(); err != nil {
		return report, err
	}
	target, err := openDB(targetPath, false)
	if err != nil {
		return report, err
	}
	if _, err = target.ExecContext(ctx, "PRAGMA journal_mode=DELETE; PRAGMA foreign_keys=OFF"); err != nil {
		target.Close()
		return report, err
	}
	err = copyDatabase(ctx, source, target, &report)
	closeErr := target.Close()
	if err != nil {
		return report, err
	}
	if closeErr != nil {
		return report, closeErr
	}
	if err = deploydb.Verify(ctx, targetPath, store.CurrentSchemaContract); err != nil {
		return report, err
	}
	if current, e := SourceDigest(o.Source); e != nil {
		return report, e
	} else if current != sourceDigest {
		return report, fmt.Errorf("source changed during offline migration")
	}
	report.TargetDatabaseSHA256, err = fileHash(targetPath)
	if err != nil {
		return report, err
	}
	if o.DryRun {
		report.TargetDatabaseSHA256 = ""
		return report, writeReport(o.ReportPath, report)
	}
	if err = verifyPrivateTarget(targetPath); err != nil {
		return report, err
	}
	report.Status = "ready"
	report.SourceBackupPath = o.Target + ".source.db"
	// Hard-link publication is atomic and refuses existing destinations. The
	// source, target and report can never be overwritten by a repeated invocation.
	if err = os.Link(backupPath, report.SourceBackupPath); err != nil {
		return report, err
	}
	ownBackup := true
	ownTarget := false
	defer func() {
		if err != nil {
			if ownTarget {
				_ = os.Remove(o.Target)
			}
			if ownBackup {
				_ = os.Remove(report.SourceBackupPath)
			}
		}
	}()
	if err = os.Link(targetPath, o.Target); err != nil {
		return report, err
	}
	ownTarget = true
	if err = runarchive.SyncDir(filepath.Dir(o.Target)); err != nil {
		return report, err
	}
	if err = writeReport(o.ReportPath, report); err != nil {
		return report, err
	}
	return report, nil
}

func verifyPrivateTarget(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("migration target must be a private regular file (0600)")
	}
	return nil
}

func writeReport(path string, r Report) error {
	raw, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(append(raw, '\n')); e == nil {
		e = f.Sync()
	}
	if c := f.Close(); e == nil {
		e = c
	}
	if e != nil {
		_ = os.Remove(path)
	}
	if e == nil {
		e = runarchive.SyncDir(filepath.Dir(path))
	}
	return e
}
func tableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, e := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func writableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, e := db.QueryContext(ctx, "PRAGMA table_xinfo("+quote(table)+")")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	cols := []string{"rowid"}
	for rows.Next() {
		var cid, notnull, pk, hidden int
		var name, typ string
		var def sql.NullString
		if e = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk, &hidden); e != nil {
			return nil, e
		}
		if hidden == 0 {
			cols = append(cols, name)
		}
	}
	return cols, rows.Err()
}
func scanMap(rows *sql.Rows, cols []string) (map[string]any, error) {
	v := make([]any, len(cols))
	p := make([]any, len(v))
	for i := range v {
		p[i] = &v[i]
	}
	if e := rows.Scan(p...); e != nil {
		return nil, e
	}
	out := map[string]any{}
	for i, c := range cols {
		out[c] = v[i]
	}
	return out, nil
}
func textValue(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	s, _ := v.(string)
	return s
}
func intValue(v any) int64 { n, _ := v.(int64); return n }
func rawRun(row map[string]any) domain.Run {
	return domain.Run{ID: textValue(row["id"]), Kind: domain.RunKind(textValue(row["kind"])), Status: domain.RunStatus(textValue(row["status"])), EnvironmentID: textValue(row["environment_id"]), EnvironmentRevisionID: textValue(row["environment_revision_id"]), ComponentReleaseID: textValue(row["component_release_id"]), ScenarioRevisionID: textValue(row["scenario_revision_id"]), ArtifactDigest: textValue(row["artifact_digest"]), RetryOfRunID: textValue(row["retry_of_run_id"]), RetryRootRunID: textValue(row["retry_root_run_id"]), RetryAttempt: int(intValue(row["retry_attempt"]))}
}

func convertRow(table string, row map[string]any, report *Report) error {
	switch table {
	case "runs":
		run := rawRun(row)
		original := []byte(textValue(row["input_snapshot_json"]))
		snapshot, results, e := legacy.Decode(original, run)
		if e != nil {
			return e
		}
		raw, e := domain.EncodeRunSnapshot(snapshot)
		if e != nil {
			return e
		}
		if results == nil {
			results = []domain.RunDeliveryResult{}
		}
		observed, e := json.Marshal(results)
		if e != nil {
			return e
		}
		delete(row, "input_snapshot_json")
		row["execution_snapshot_json"] = string(raw)
		row["delivery_results_json"] = string(observed)
		refs := snapshot.ReferencedRunIDs()
		kept := refs[:0]
		for _, id := range refs {
			if id != run.ID {
				kept = append(kept, id)
			}
		}
		report.Runs = append(report.Runs, RunReport{ID: run.ID, SourceSnapshotSHA256: hashBytes(original), TargetSnapshotSHA256: hashBytes(raw), References: kept})
	case "run_cleanup_history":
		var identity struct {
			Cleaned               bool   `json:"cleaned"`
			Component             string `json:"componentReleaseSpecDigest"`
			Scenario              string `json:"scenarioRevisionSpecDigest"`
			EnvironmentRevisionID string `json:"environmentRevisionId"`
			Steps                 []struct {
				ReleaseID         string `json:"releaseId"`
				ReleaseSpecDigest string `json:"releaseSpecDigest"`
			} `json:"steps"`
		}
		d := json.NewDecoder(strings.NewReader(textValue(row["identity_json"])))
		d.DisallowUnknownFields()
		if e := d.Decode(&identity); e != nil {
			return fmt.Errorf("cleaned Run %s: %w", textValue(row["id"]), e)
		}
		if e := d.Decode(new(any)); e != io.EOF {
			return fmt.Errorf("cleaned Run %s: trailing cleanup identity", textValue(row["id"]))
		}
		if !identity.Cleaned {
			return fmt.Errorf("cleaned Run %s: missing cleanup identity", textValue(row["id"]))
		}
		if identity.EnvironmentRevisionID != "" && identity.EnvironmentRevisionID != textValue(row["environment_revision_id"]) {
			return fmt.Errorf("cleaned Run %s: environment revision mismatch", textValue(row["id"]))
		}
		locks := make([]domain.RunReleaseLock, 0, len(identity.Steps))
		for _, s := range identity.Steps {
			if s.ReleaseID == "" {
				return fmt.Errorf("cleaned Run %s: missing release identity", textValue(row["id"]))
			}
			locks = append(locks, domain.RunReleaseLock{ReleaseID: s.ReleaseID, ReleaseSpecDigest: s.ReleaseSpecDigest})
		}
		raw, e := json.Marshal(locks)
		if e != nil {
			return e
		}
		delete(row, "identity_json")
		row["component_spec_digest"] = nullable(identity.Component)
		row["scenario_spec_digest"] = nullable(identity.Scenario)
		row["release_locks_json"] = string(raw)
	}
	return nil
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func copyDatabase(ctx context.Context, source, target *sql.DB, report *Report) error {
	tables, e := tableNames(ctx, source)
	if e != nil {
		return e
	}
	targetTables, e := tableNames(ctx, target)
	if e != nil {
		return e
	}
	allowed := map[string]bool{}
	for _, t := range targetTables {
		allowed[t] = true
	}
	for _, t := range tables {
		if !allowed[t] {
			return fmt.Errorf("unexpected source table %s", t)
		}
	}
	present := map[string]bool{}
	for _, t := range tables {
		present[t] = true
	}
	for _, t := range targetTables {
		if t != "run_snapshot_references" && !present[t] {
			return fmt.Errorf("source table %s is missing", t)
		}
	}
	if present["run_snapshot_references"] {
		return fmt.Errorf("unexpected source table run_snapshot_references")
	}
	triggers, e := target.QueryContext(ctx, "SELECT name,sql FROM sqlite_master WHERE type='trigger' ORDER BY name")
	if e != nil {
		return e
	}
	type trigger struct{ name, sql string }
	restore := []trigger{}
	for triggers.Next() {
		var t trigger
		if e = triggers.Scan(&t.name, &t.sql); e != nil {
			triggers.Close()
			return e
		}
		restore = append(restore, t)
	}
	e = triggers.Err()
	triggers.Close()
	if e != nil {
		return e
	}
	tx, e := target.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, t := range restore {
		if _, e = tx.ExecContext(ctx, "DROP TRIGGER "+quote(t.name)); e != nil {
			return e
		}
	}
	for _, t := range targetTables {
		if t != "schema_contract" {
			if _, e = tx.ExecContext(ctx, "DELETE FROM "+quote(t)); e != nil {
				return e
			}
		}
	}
	for _, table := range tables {
		if table == "schema_contract" {
			continue
		}
		cols, e := writableColumns(ctx, source, table)
		if e != nil {
			return e
		}
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = quote(c)
		}
		rows, e := source.QueryContext(ctx, "SELECT "+strings.Join(quoted, ",")+" FROM "+quote(table)+" ORDER BY rowid")
		if e != nil {
			return e
		}
		entry := TableReport{Name: table}
		before, after := sha256.New(), sha256.New()
		var insertCols []string
		for rows.Next() {
			row, e := scanMap(rows, cols)
			if e != nil {
				rows.Close()
				return e
			}
			raw, e := json.Marshal(row)
			if e != nil {
				rows.Close()
				return e
			}
			before.Write(raw)
			before.Write([]byte{'\n'})
			if e = convertRow(table, row, report); e != nil {
				rows.Close()
				return e
			}
			raw, e = json.Marshal(row)
			if e != nil {
				rows.Close()
				return e
			}
			after.Write(raw)
			after.Write([]byte{'\n'})
			if insertCols == nil {
				for c := range row {
					insertCols = append(insertCols, c)
				}
				sort.Strings(insertCols)
			}
			args := make([]any, len(insertCols))
			names := make([]string, len(insertCols))
			for i, c := range insertCols {
				args[i] = row[c]
				names[i] = quote(c)
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO "+quote(table)+" ("+strings.Join(names, ",")+") VALUES ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+")", args...); e != nil {
				rows.Close()
				return fmt.Errorf("import %s row %v: %w", table, row["rowid"], e)
			}
			entry.Count++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		entry.SourceDigest = hex.EncodeToString(before.Sum(nil))
		entry.TargetDigest = hex.EncodeToString(after.Sum(nil))
		if entry.Count > 0 {
			actual, e := digestRows(ctx, tx, table, insertCols)
			if e != nil {
				return e
			}
			if actual != entry.TargetDigest {
				return fmt.Errorf("target data verification failed for %s", table)
			}
		}
		report.Tables = append(report.Tables, entry)
	}
	// Preserve AUTOINCREMENT high watermarks, including deleted tail records.
	var sequence int
	if e = source.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='sqlite_sequence'").Scan(&sequence); e != nil {
		return e
	}
	if sequence != 0 {
		rows, e := source.QueryContext(ctx, "SELECT name,seq FROM sqlite_sequence")
		if e != nil {
			return e
		}
		for rows.Next() {
			var name string
			var seq int64
			if e = rows.Scan(&name, &seq); e != nil {
				rows.Close()
				return e
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name=?", name); e != nil {
				rows.Close()
				return e
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)", name, seq); e != nil {
				rows.Close()
				return e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	for _, run := range report.Runs {
		for _, id := range run.References {
			if _, e = tx.ExecContext(ctx, "INSERT INTO run_snapshot_references(run_id,referenced_run_id) VALUES(?,?)", run.ID, id); e != nil {
				return e
			}
		}
	}
	for _, t := range restore {
		if _, e = tx.ExecContext(ctx, t.sql); e != nil {
			return e
		}
	}
	invalid, e := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	bad := invalid.Next()
	e = invalid.Err()
	invalid.Close()
	if e != nil {
		return e
	}
	if bad {
		return fmt.Errorf("target foreign key verification failed")
	}
	return tx.Commit()
}
func digestRows(ctx context.Context, tx *sql.Tx, table string, cols []string) (string, error) {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quote(c)
	}
	rows, e := tx.QueryContext(ctx, "SELECT "+strings.Join(quoted, ",")+" FROM "+quote(table)+" ORDER BY rowid")
	if e != nil {
		return "", e
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		row, e := scanMap(rows, cols)
		if e != nil {
			return "", e
		}
		raw, e := json.Marshal(row)
		if e != nil {
			return "", e
		}
		h.Write(raw)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), rows.Err()
}
func verifyArchives(ctx context.Context, db *sql.DB, root string) ([]ArchiveReport, error) {
	rows, e := db.QueryContext(ctx, "SELECT run_id,relative_path,sha256,size_bytes FROM run_archive_files ORDER BY run_id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ArchiveReport{}
	for rows.Next() {
		var a ArchiveReport
		if e = rows.Scan(&a.RunID, &a.Path, &a.SHA256, &a.Size); e != nil {
			return nil, e
		}
		if root == "" {
			return nil, fmt.Errorf("archive directory is required")
		}
		path, e := runarchive.Path(root, a.Path)
		if e != nil {
			return nil, e
		}
		sha, size, e := runarchive.HashFile(path)
		if e != nil {
			return nil, e
		}
		if sha != a.SHA256 || size != a.Size {
			return nil, fmt.Errorf("archive checksum differs for Run %s", a.RunID)
		}
		if e = runarchive.Verify(path, a.RunID); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// VerifyReady is the deployment gate. It verifies the still-stopped source,
// backup and target bytes without opening the source with the new Store.
func VerifyReady(ctx context.Context, source, target, reportPath string) error {
	if err := verifyPrivateTarget(target); err != nil {
		return err
	}
	raw, e := os.ReadFile(reportPath)
	if e != nil {
		return e
	}
	var r Report
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing migration report data")
	}
	if r.FormatVersion != ReportVersion || r.Status != "ready" || r.SourceContract != legacy.SchemaContract || r.TargetContract != store.CurrentSchemaContract {
		return fmt.Errorf("migration report is not ready for the current contract")
	}
	if e = preflight(ctx, source); e != nil {
		return e
	}
	digest, e := SourceDigest(source)
	if e != nil {
		return e
	}
	if digest != r.SourceDigest {
		return fmt.Errorf("source changed after migration")
	}
	if hash, e := fileHash(target); e != nil {
		return e
	} else if hash != r.TargetDatabaseSHA256 {
		return fmt.Errorf("migration target checksum mismatch")
	}
	if hash, e := fileHash(r.SourceBackupPath); e != nil {
		return e
	} else if hash != r.SourceBackupSHA256 {
		return fmt.Errorf("migration source backup checksum mismatch")
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if info, e := os.Stat(target + suffix); e == nil && info.Size() > 0 {
			return fmt.Errorf("migration target has unverified journal data")
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	db, e := openDB(target, true)
	if e != nil {
		return e
	}
	defer db.Close()
	archives, e := verifyArchives(ctx, db, r.ArchiveRoot)
	if e != nil {
		return e
	}
	rawExpected, _ := json.Marshal(r.Archives)
	rawActual, _ := json.Marshal(archives)
	if string(rawExpected) != string(rawActual) {
		return fmt.Errorf("archive manifest mismatch")
	}
	return deploydb.Verify(ctx, target, r.TargetContract)
}
