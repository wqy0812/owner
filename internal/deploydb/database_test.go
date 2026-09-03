package deploydb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotFailurePreservesExistingFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); INSERT INTO child VALUES(123)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "snapshot.db")
	if err := Snapshot(ctx, source, target); err == nil {
		t.Fatal("snapshot accepted foreign key violations")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid snapshot remains: %v", err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(ctx, source, source); err == nil {
		t.Fatal("snapshot accepted its source as destination")
	}
	after, err := os.ReadFile(source)
	if err != nil || string(before) != string(after) {
		t.Fatalf("source changed: %v", err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(ctx, source, target); err == nil {
		t.Fatal("snapshot accepted an existing target")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "existing" {
		t.Fatalf("existing destination changed: %v", err)
	}
}

func TestInspectionDoesNotCreateOrRepairDatabase(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := Contract(ctx, missing); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("inspection created a database: %v", err)
	}
	if err := os.WriteFile(missing, []byte("invalid database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Contract(ctx, missing); err == nil {
		t.Fatal("corrupt database reported as a missing contract")
	}
	if err := Verify(ctx, missing, "current"); err == nil {
		t.Fatal("corrupt database passed verification")
	}
}
