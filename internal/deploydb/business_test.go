package deploydb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func businessFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../store/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
 INSERT INTO users VALUES('owner','Owner','component_owner','2026-09-05');
 INSERT INTO components(id,slug,name,owner_id,created_at,updated_at) VALUES('component','component','Component','owner','2026-09-05','2026-09-05');
 INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('line','component','Line','2026-09-05');
 INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,created_at) VALUES('release','component','line','1','draft','not_applicable','2026-09-05');
 INSERT INTO environments(id,name,owner_id,created_at,updated_at) VALUES('env','Env','owner','2026-09-05','2026-09-05');
 INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('env-r1','env',1,'2026-09-05');
 INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,input_snapshot_json,created_at) VALUES('run-history-secret','component_test','succeeded','owner','env','env-r1','release','{"history":"must-not-be-copied"}','2026-09-05');
 INSERT INTO audit_events VALUES('audit-platform-catalog-bootstrapped','system','platform_option_catalog.bootstrapped','platform','platform-option-catalog','{}','2026-09-05');
 INSERT INTO audit_events VALUES('old-audit','owner','run.completed','run','run-history-secret','{}','2026-09-05');
 INSERT INTO notifications(id,user_id,type,title,body,created_at) VALUES('note','owner','run','history','must-not-be-copied','2026-09-05');
 INSERT INTO environment_component_installations VALUES('node','env','component','release','run-history-secret','old-backup','{}',1,'2026-09-05');
 `)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBusinessSnapshotExcludesHistoryAndPreservesFoundation(t *testing.T) {
	ctx := context.Background()
	source := businessFixture(t)
	before, _ := os.ReadFile(source)
	for _, foundation := range []bool{false, true} {
		target := filepath.Join(t.TempDir(), "target.db")
		if err := BusinessSnapshot(ctx, source, target, foundation); err != nil {
			t.Fatal(err)
		}
		if err := Verify(ctx, target, ""); err != nil {
			t.Fatal(err)
		}
		db, err := openReadOnly(target)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range historyTables {
			var count int
			if err := db.QueryRow("SELECT count(*) FROM " + quoteIdentifier(table)).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if table == "audit_events" {
				want = 1
			}
			if count != want {
				t.Fatalf("%s: got %d, want %d", table, count, want)
			}
		}
		var components, users int
		db.QueryRow("SELECT count(*) FROM components").Scan(&components)
		db.QueryRow("SELECT count(*) FROM users").Scan(&users)
		db.Close()
		if users != 1 || (foundation && components != 0) || (!foundation && components != 1) {
			t.Fatalf("foundation=%v components=%d users=%d", foundation, components, users)
		}
		data, _ := os.ReadFile(target)
		if strings.Contains(string(data), "must-not-be-copied") || strings.Contains(string(data), "run-history-secret") {
			t.Fatal("history persisted in destination bytes")
		}
		export := filepath.Join(t.TempDir(), "business.json")
		if err := ExportBusiness(ctx, target, export); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(export)
		if info.Mode().Perm() != 0600 {
			t.Fatal("export permissions")
		}
	}
	after, _ := os.ReadFile(source)
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
}

func TestBusinessSnapshotRejectsActiveUnknownAndExisting(t *testing.T) {
	for _, change := range []string{"UPDATE runs SET status='running'", "CREATE TABLE future_business(id TEXT)"} {
		source := businessFixture(t)
		db, _ := sql.Open("sqlite", source)
		if _, err := db.Exec(change); err != nil {
			t.Fatal(err)
		}
		db.Close()
		target := filepath.Join(t.TempDir(), "out.db")
		if err := BusinessSnapshot(context.Background(), source, target, false); err == nil {
			t.Fatal("unsafe source accepted")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("failed snapshot left behind")
		}
	}
	source := businessFixture(t)
	if err := BusinessSnapshot(context.Background(), source, source, false); err == nil {
		t.Fatal("source overwritten")
	}
}
