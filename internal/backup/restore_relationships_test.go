package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
)

func relationshipTestCatalog() Catalog {
	catalog := Catalog{FormatVersion: CatalogFormatVersion, SchemaContract: "test", PublicationGeneration: 1}
	for _, spec := range catalogTables {
		catalog.Tables = append(catalog.Tables, TableDump{Name: spec.name, Columns: append([]string(nil), spec.columns...), Rows: [][]DBCell{}})
	}
	appendRow := func(table string, values map[string]DBCell) {
		for index := range catalog.Tables {
			if catalog.Tables[index].Name != table {
				continue
			}
			row := make([]DBCell, len(catalog.Tables[index].Columns))
			for columnIndex, column := range catalog.Tables[index].Columns {
				row[columnIndex] = DBCell{Kind: "null"}
				if value, ok := values[column]; ok {
					row[columnIndex] = value
				}
			}
			catalog.Tables[index].Rows = append(catalog.Tables[index].Rows, row)
			return
		}
	}
	text := func(value string) DBCell { return DBCell{Kind: "text", Text: value} }
	appendRow("components", map[string]DBCell{"id": text("component-a")})
	appendRow("components", map[string]DBCell{"id": text("component-b")})
	appendRow("component_release_lines", map[string]DBCell{"id": text("line-a"), "component_id": text("component-a")})
	appendRow("component_release_lines", map[string]DBCell{"id": text("line-b"), "component_id": text("component-b")})
	appendRow("component_releases", map[string]DBCell{
		"id": text("release-root"), "component_id": text("component-a"), "line_id": text("line-a"),
		"status": text("released"), "compatibility": text("not_applicable"), "released_at": text("2026-09-01T00:00:00Z"),
	})
	appendRow("component_releases", map[string]DBCell{
		"id": text("release-child"), "component_id": text("component-a"), "line_id": text("line-a"),
		"parent_release_id": text("release-root"), "template_source_release_id": text("release-root"),
		"status": text("released"), "compatibility": text("compatible"), "released_at": text("2026-09-01T01:00:00Z"),
	})
	appendRow("component_releases", map[string]DBCell{
		"id": text("release-b"), "component_id": text("component-b"), "line_id": text("line-b"),
		"status": text("released"), "compatibility": text("not_applicable"), "released_at": text("2026-09-01T00:00:00Z"),
	})
	appendRow("action_definitions", map[string]DBCell{
		"id": text("upgrade-child"), "release_id": text("release-child"), "kind": text("upgrade"),
		"from_release_id": text("release-root"), "to_release_id": text("release-child"),
	})
	appendRow("action_definitions", map[string]DBCell{
		"id": text("rollback-child"), "release_id": text("release-child"), "kind": text("rollback"),
		"from_release_id": text("release-child"), "to_release_id": text("release-root"),
	})
	return catalog
}

func TestInvalidCatalogRelationshipFailsBeforeRestoreCreatesFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "catalog")
	if _, err := runCommand(ctx, root, "git", "init", repo); err != nil {
		t.Fatal(err)
	}
	catalog := relationshipTestCatalog()
	mutateCatalogCell(t, &catalog, "component_releases", "release-child", "line_id", DBCell{Kind: "text", Text: "line-b"})
	contents, err := canonicalJSON(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "catalog.json"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, repo, "git", "add", "catalog.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "corrupt catalog"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		DatabasePath: filepath.Join(root, "source.db"), PlaybookRoot: filepath.Join(root, "source-jobs"),
		BackupDir: filepath.Join(root, "backups"), CatalogRepo: repo, CatalogRemote: "origin", CatalogBranch: "master",
	})
	if err != nil {
		t.Fatal(err)
	}
	targetDatabase := filepath.Join(root, "restored.db")
	targetPlaybooks := filepath.Join(root, "restored-jobs")
	_, err = manager.RestoreCatalog(ctx, "HEAD", targetDatabase, targetPlaybooks)
	var coded *domain.CodedError
	if !errors.As(err, &coded) || coded.Code != "catalog_relationship_invalid" {
		t.Fatalf("restore error=%v", err)
	}
	for _, path := range []string{targetDatabase, targetDatabase + "-wal", targetDatabase + "-shm", targetPlaybooks} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid restore left %s: %v", path, statErr)
		}
	}
}

func cloneRelationshipCatalog(t *testing.T, catalog Catalog) Catalog {
	t.Helper()
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var result Catalog
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func mutateCatalogCell(t *testing.T, catalog *Catalog, table, objectID, column string, value DBCell) {
	t.Helper()
	for tableIndex := range catalog.Tables {
		current := &catalog.Tables[tableIndex]
		if current.Name != table {
			continue
		}
		idIndex, targetIndex := columnIndex(current.Columns, "id"), columnIndex(current.Columns, column)
		for rowIndex := range current.Rows {
			if current.Rows[rowIndex][idIndex].Text == objectID {
				current.Rows[rowIndex][targetIndex] = value
				return
			}
		}
	}
	t.Fatalf("missing %s %s", table, objectID)
}

func TestValidateCatalogRejectsInvalidReleaseRelationships(t *testing.T) {
	valid := relationshipTestCatalog()
	if err := validateCatalogReferences(valid); err != nil {
		t.Fatalf("valid Catalog failed: %v", err)
	}
	text := func(value string) DBCell { return DBCell{Kind: "text", Text: value} }
	null := DBCell{Kind: "null"}
	tests := []struct {
		name   string
		mutate func(*Catalog)
	}{
		{"cross-component-line", func(c *Catalog) {
			mutateCatalogCell(t, c, "component_releases", "release-child", "line_id", text("line-b"))
		}},
		{"missing-parent", func(c *Catalog) {
			mutateCatalogCell(t, c, "component_releases", "release-child", "parent_release_id", text("missing"))
		}},
		{"cross-component-template", func(c *Catalog) {
			mutateCatalogCell(t, c, "component_releases", "release-child", "template_source_release_id", text("release-b"))
		}},
		{"invalid-baseline-compatibility", func(c *Catalog) {
			mutateCatalogCell(t, c, "component_releases", "release-root", "compatibility", text("compatible"))
		}},
		{"draft-in-git-catalog", func(c *Catalog) {
			mutateCatalogCell(t, c, "component_releases", "release-child", "status", text("draft"))
			mutateCatalogCell(t, c, "component_releases", "release-child", "released_at", null)
		}},
		{"wrong-upgrade-direction", func(c *Catalog) {
			mutateCatalogCell(t, c, "action_definitions", "upgrade-child", "from_release_id", text("release-child"))
		}},
		{"cyclic-parent-chain", func(c *Catalog) {
			mutateCatalogCell(t, c, "component_releases", "release-root", "parent_release_id", text("release-child"))
			mutateCatalogCell(t, c, "component_releases", "release-root", "compatibility", text("compatible"))
		}},
		{"dependency-component-mismatch", func(c *Catalog) {
			for index := range c.Tables {
				if c.Tables[index].Name != "component_dependencies" {
					continue
				}
				row := make([]DBCell, len(c.Tables[index].Columns))
				for columnIndex, column := range c.Tables[index].Columns {
					row[columnIndex] = null
					switch column {
					case "id":
						row[columnIndex] = text("dependency")
					case "release_id":
						row[columnIndex] = text("release-child")
					case "upstream_component_id":
						row[columnIndex] = text("component-b")
					case "upstream_release_id":
						row[columnIndex] = text("release-root")
					}
				}
				c.Tables[index].Rows = append(c.Tables[index].Rows, row)
			}
		}},
		{"multiple-retained-successors", func(c *Catalog) {
			for index := range c.Tables {
				if c.Tables[index].Name != "component_releases" {
					continue
				}
				row := append([]DBCell(nil), c.Tables[index].Rows[1]...)
				row[columnIndex(c.Tables[index].Columns, "id")] = text("release-child-2")
				c.Tables[index].Rows = append(c.Tables[index].Rows, row)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := cloneRelationshipCatalog(t, valid)
			test.mutate(&catalog)
			err := validateCatalogReferences(catalog)
			var coded *domain.CodedError
			if !errors.As(err, &coded) || coded.Code != "catalog_relationship_invalid" || !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("error=%v, want catalog_relationship_invalid", err)
			}
		})
	}
}
