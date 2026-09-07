package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex/platform-demo/internal/store"
)

func TestCatalogExportAndRestoreRejectHistoricalSchemaContracts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "source.db")
	db, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog, raw, err := ExportCatalog(ctx, databasePath, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(raw); err != nil {
		t.Fatalf("current Catalog rejected: %v", err)
	}
	for _, contract := range []string{"", "clusterforge-v1-previous-contract"} {
		catalog.SchemaContract = contract
		raw, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseCatalog(raw); err == nil || !strings.Contains(err.Error(), "unsupported Catalog schema contract") {
			t.Fatalf("historical Catalog accepted: %v", err)
		}
	}
	if _, err := db.DB().ExecContext(ctx, "UPDATE schema_contract SET version='clusterforge-v1-previous-contract'"); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := ExportCatalog(ctx, databasePath, root, destination); err == nil || !strings.Contains(err.Error(), "unsupported Catalog schema contract") {
		t.Fatalf("export accepted a historical schema: %v", err)
	}
	if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
		t.Fatalf("rejected export wrote files: entries=%v err=%v", entries, err)
	}
}
