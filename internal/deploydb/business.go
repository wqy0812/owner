package deploydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codex/platform-demo/internal/store"
)

// Explicit lists make new schema tables fail closed instead of silently losing
// business data or accidentally retaining execution history.
var foundationTables = []string{"run_retention_policy", "schema_contract", "publication_state", "users", "platform_option_categories", "platform_options", "environment_parameter_definitions", "environment_parameter_defaults", "environment_variable_definitions"}
var businessTables = []string{"components", "component_release_lines", "component_releases", "component_dependencies", "action_definitions", "component_playbook_files", "scenarios", "scenario_revisions", "environments", "environment_revisions", "component_release_artifacts", "component_release_images", "component_artifact_mirrors", "component_image_mirrors"}
var historyTables = []string{"run_archive_tasks", "run_archive_files", "run_cleanup_history", "run_retention_cursors", "sessions", "playbook_action_mutations", "runs", "run_steps", "run_logs", "approvals", "notifications", "audit_events", "component_image_builds", "component_image_build_logs", "environment_component_installations", "environment_health_checks", "environment_ssh_checks"}

func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// BusinessSnapshot never writes Run data, even temporarily. The recovery image
// preserves source schema and triggers; a foundation reset uses the current
// schema. The caller must quiesce writers before producing this pair.
func BusinessSnapshot(ctx context.Context, source, target string, foundationOnly bool) (err error) {
	if foundationOnly {
		return currentFoundationSnapshot(ctx, source, target)
	}
	if err = Verify(ctx, source, ""); err != nil {
		return err
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	f.Close()
	defer func() {
		if err != nil {
			os.Remove(target)
			os.Remove(target + "-journal")
			os.Remove(target + "-wal")
			os.Remove(target + "-shm")
		}
	}()
	db, err := sql.Open("sqlite", target)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	u := url.URL{Scheme: "file", Path: sourceAbs}
	u.RawQuery = "mode=ro"
	if _, err = db.ExecContext(ctx, "ATTACH DATABASE ? AS source", u.String()); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM source.runs WHERE status IN ('queued','running','awaiting_approval')) + (SELECT COUNT(*) FROM source.component_image_builds WHERE status IN ('queued','running'))`).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("active Runs or image builds block business snapshot")
	}
	type object struct{ kind, name, ddl string }
	rows, err := tx.QueryContext(ctx, `SELECT type,name,sql FROM source.sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END,name`)
	if err != nil {
		return err
	}
	var objects []object
	for rows.Next() {
		var o object
		if err = rows.Scan(&o.kind, &o.name, &o.ddl); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	known := map[string]bool{}
	for _, list := range [][]string{foundationTables, businessTables, historyTables} {
		for _, t := range list {
			known[t] = true
		}
	}
	for _, t := range foundationTables {
		allowed[t] = true
	}
	for _, t := range businessTables {
		allowed[t] = true
	}
	for _, o := range objects {
		if o.kind == "table" {
			if !known[o.name] {
				return fmt.Errorf("unclassified table %q; refusing reset", o.name)
			}
			if _, err = tx.ExecContext(ctx, o.ddl); err != nil {
				return err
			}
		}
	}
	for _, o := range objects {
		if o.kind != "table" || !allowed[o.name] {
			continue
		}
		q := quoteIdentifier(o.name)
		if _, err = tx.ExecContext(ctx, "INSERT INTO main."+q+" SELECT * FROM source."+q); err != nil {
			return fmt.Errorf("copy %s: %w", o.name, err)
		}
	}
	// Keep only the fixed bootstrap marker so an intentionally empty directory is
	// not repopulated on restart; no historical audit payload is copied.
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) SELECT id,'system','platform_option_catalog.bootstrapped','platform','platform-option-catalog','{"mode":"business-reset-preserved"}',created_at FROM source.audit_events WHERE id='audit-platform-catalog-bootstrapped'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE scenario_revisions SET status=CASE WHEN status IN ('testing','test_passed') THEN 'draft' ELSE status END,test_passed_at=NULL`); err != nil {
		return err
	}
	for _, o := range objects {
		if o.kind != "table" {
			if _, err = tx.ExecContext(ctx, o.ddl); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	if err = Verify(ctx, target, ""); err != nil {
		return err
	}
	f, err = os.Open(target)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// ExportBusiness emits a readable, private JSON companion from the sanitized DB.
func ExportBusiness(ctx context.Context, source, target string) (err error) {
	if err = VerifyBusiness(ctx, source, false); err != nil {
		return err
	}
	db, err := openReadOnly(source)
	if err != nil {
		return err
	}
	defer db.Close()
	tables := map[string]any{}
	for _, t := range append(append([]string{}, foundationTables...), businessTables...) {
		var exists int
		if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", t).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			tables[t] = []map[string]any{}
			continue
		}
		rows, e := db.QueryContext(ctx, "SELECT * FROM "+quoteIdentifier(t))
		if e != nil {
			return e
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return e
		}
		records := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if e = rows.Scan(ptrs...); e != nil {
				rows.Close()
				return e
			}
			r := map[string]any{}
			for i, k := range cols {
				v := values[i]
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				if s, ok := v.(string); ok && strings.HasSuffix(k, "_json") {
					var decoded any
					if json.Unmarshal([]byte(s), &decoded) == nil {
						v = decoded
					}
				}
				r[k] = v
			}
			records = append(records, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		tables[t] = records
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	defer func() {
		if err != nil {
			os.Remove(target)
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(map[string]any{"format": "clusterforge-business-without-run-history-v1", "tables": tables}); err != nil {
		return err
	}
	return f.Sync()
}

// VerifyBusiness checks that a rollback image cannot reintroduce execution
// history; foundation images additionally cannot contain business objects.
func VerifyBusiness(ctx context.Context, path string, foundationOnly bool) error {
	expected := ""
	if foundationOnly {
		expected = store.CurrentSchemaContract
	}
	if err := Verify(ctx, path, expected); err != nil {
		return err
	}
	db, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	excluded := append([]string{}, historyTables...)
	if foundationOnly {
		excluded = append(excluded, businessTables...)
		excluded = append(excluded, "environment_parameter_definitions", "environment_parameter_defaults")
	}
	known := map[string]bool{}
	for _, list := range [][]string{foundationTables, businessTables, historyTables} {
		for _, table := range list {
			known[table] = true
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		if !known[table] {
			rows.Close()
			return fmt.Errorf("unclassified table %q; refusing business verification", table)
		}
		present[table] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, table := range excluded {
		if !present[table] {
			continue
		}
		query := "SELECT count(*) FROM " + quoteIdentifier(table)
		if table == "audit_events" {
			query += ` WHERE id <> 'audit-platform-catalog-bootstrapped' OR action <> 'platform_option_catalog.bootstrapped' OR metadata_json <> '{"mode":"business-reset-preserved"}'`
		}
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%s contains excluded rows", table)
		}
	}
	return nil
}

func ActiveWork(ctx context.Context, path string) ([]string, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT 'run',id,status FROM runs WHERE status IN ('running','queued','awaiting_approval') UNION ALL SELECT 'image-build',id,status FROM component_image_builds WHERE status IN ('running','queued')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var kind, id, status string
		if err := rows.Scan(&kind, &id, &status); err != nil {
			return nil, err
		}
		result = append(result, kind+"\t"+id+"\t"+status)
	}
	return result, rows.Err()
}
