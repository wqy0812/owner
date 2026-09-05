package deploydb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex/platform-demo/internal/store"
)

// currentFoundationSnapshot is an explicit reset, not an in-place migration.
// Only stable identity and directory columns cross into a freshly built schema.
// Removed global parameter defaults and automatic retention settings stay empty
// or disabled; no source DDL, business definitions, or Run data are copied.
func currentFoundationSnapshot(ctx context.Context, source, target string) (err error) {
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
	if err = f.Close(); err != nil {
		os.Remove(target)
		return err
	}
	defer func() {
		if err != nil {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				os.Remove(target + suffix)
			}
		}
	}()
	destination, err := store.Open(ctx, target)
	if err != nil {
		return err
	}
	defer destination.Close()
	db := destination.DB()
	db.SetMaxOpenConns(1)
	u := url.URL{Scheme: "file", Path: sourceAbs, RawQuery: "mode=ro"}
	if _, err = db.ExecContext(ctx, "ATTACH DATABASE ? AS source", u.String()); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var contract string
	if err = tx.QueryRowContext(ctx, "SELECT version FROM source.schema_contract WHERE id=1").Scan(&contract); err != nil {
		return err
	}
	if !strings.HasPrefix(contract, "clusterforge-v1-") {
		return fmt.Errorf("unsupported foundation source contract %q", contract)
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM source.runs WHERE status IN ('queued','running','awaiting_approval')) + (SELECT COUNT(*) FROM source.component_image_builds WHERE status IN ('queued','running'))`).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("active Runs or image builds block foundation reset")
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		return err
	}
	for _, table := range []struct{ name, columns string }{
		{"users", "id,name,role,created_at"},
		{"platform_option_categories", "id,parent_category_id,technical_key,label,category_type,environment_required,sort_order,created_by,created_at,retired_at"},
		{"platform_options", "id,category_id,parent_option_id,technical_value,label,sort_order,created_by,created_at,retired_at"},
		{"environment_variable_definitions", "id,variable_name,label,description,created_by,created_at"},
	} {
		if _, err = tx.ExecContext(ctx, "INSERT INTO main."+table.name+"("+table.columns+") SELECT "+table.columns+" FROM source."+table.name); err != nil {
			return fmt.Errorf("convert foundation %s: %w", table.name, err)
		}
	}
	// Suppress bootstrap even when the old directory was intentionally empty.
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES('audit-platform-catalog-bootstrapped','system','platform_option_catalog.bootstrapped','platform','platform-option-catalog','{"mode":"business-reset-preserved"}',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "DETACH DATABASE source"); err != nil {
		return err
	}
	if err = destination.Close(); err != nil {
		return err
	}
	if err = Verify(ctx, target, store.CurrentSchemaContract); err != nil {
		return err
	}
	if err = VerifyBusiness(ctx, target, true); err != nil {
		return err
	}
	f, err = os.Open(target)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
