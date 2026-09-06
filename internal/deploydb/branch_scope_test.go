package deploydb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyConverterCannotInventBranchScopes(t *testing.T) {
	source, workspace := lifecycleConversionFixture(t)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`DROP TRIGGER release_line_scope_immutable`, `ALTER TABLE component_release_lines DROP COLUMN environment_constraints_json`} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "converted.db")
	_, err = ConvertScenarioLifecycle(context.Background(), source, target, workspace, target+"-files")
	if err == nil || !strings.Contains(err.Error(), "separate reviewed assignment plan") {
		t.Fatalf("legacy conversion accepted missing scope: %v", err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("rejected conversion created target: %v", err)
	}
}
