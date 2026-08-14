package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestComponentClassificationMigrationRemovesDemoHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/001_init.sql", "migrations/002_audit_append_only.sql"} {
		content, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO users(id,name,role,created_at) VALUES('component-alice','Alice','component_owner','` + now + `'),('component-bob','Bob','component_owner','` + now + `'),('scenario-carol','Carol','scenario_owner','` + now + `'),('environment-dave','Dave','environment_owner','` + now + `')`,
		`INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at) VALUES('component-containerd','containerd','containerd','runtime','component-alice','` + now + `','` + now + `'),('component-demo-agent','demo-node-agent','Demo Node Agent','fixture','component-alice','` + now + `','` + now + `'),('component-demo-policy-bundle','demo-policy-bundle','Demo Policy Bundle','fixture','component-bob','` + now + `','` + now + `')`,
		`INSERT INTO component_releases(id,component_id,version,release_type,status,risk_level,created_at) VALUES('release-containerd-2.1.1','component-containerd','v2.1.1','atomic','released','low','` + now + `'),('release-demo-agent-1.0.0','component-demo-agent','v1.0.0','atomic','released','low','` + now + `'),('release-demo-policy-bundle-1.0','component-demo-policy-bundle','v1.0.0','bundle','released','low','` + now + `')`,
		`INSERT INTO component_dependencies(id,release_id,upstream_component_id,upstream_release_id,purpose) VALUES('dependency-demo','release-demo-policy-bundle-1.0','component-demo-agent','release-demo-agent-1.0.0','fixture')`,
		`INSERT INTO scenarios(id,slug,name,owner_id,current_revision_id,created_at,updated_at) VALUES('scenario-demo-agent','demo-node-agent-upgrade','Demo lifecycle','scenario-carol','scenario-demo-agent-r1','` + now + `','` + now + `')`,
		`INSERT INTO scenario_revisions(id,scenario_id,revision,status,created_at) VALUES('scenario-demo-agent-r1','scenario-demo-agent',1,'released','` + now + `')`,
		`INSERT INTO environments(id,name,owner_id,current_revision_id,created_at,updated_at) VALUES('environment-local','Localhost Safe Lab','environment-dave','environment-local-r1','` + now + `','` + now + `')`,
		`INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('environment-local-r1','environment-local',1,'` + now + `')`,
		`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,created_at) VALUES('run-demo-history','component_test','succeeded','component-alice','environment-local','environment-local-r1','release-demo-agent-1.0.0','scenario-demo-agent-r1','` + now + `')`,
		`INSERT INTO run_steps(id,run_id,name,status) VALUES('step-demo-history','run-demo-history','fixture','succeeded')`,
		`INSERT INTO run_logs(run_id,step_id,message,created_at) VALUES('run-demo-history','step-demo-history','fixture','` + now + `')`,
		`INSERT INTO approvals(id,run_id,status,requested_at) VALUES('approval-demo-history','run-demo-history','approved','` + now + `')`,
		`INSERT INTO notifications(id,user_id,type,title,body,resource_url,payload_json,created_at) VALUES('notification-demo-history','component-bob','component_released','fixture','fixture','/components?selected=component-demo-agent','{"componentId":"component-demo-agent"}','` + now + `')`,
		`INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES('audit-demo-history','component-alice','run.finished','run','run-demo-history','{}','` + now + `'),('audit-keep','system','keep','platform','keep','{}','` + now + `')`,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy database: %v\n%s", err, statement)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer migrated.Close()
	component, err := migrated.GetComponent(ctx, "component-containerd", false)
	if err != nil {
		t.Fatal(err)
	}
	if component.Layer != "runtime_state" || component.Category != "runtime" || component.Kind != "software" || component.Requiredness != "profile_required" {
		t.Fatalf("containerd classification=%+v", component)
	}
	for _, table := range []string{"runs", "run_steps", "run_logs", "approvals", "notifications", "component_dependencies"} {
		assertMigrationTableCount(t, migrated, table, 0)
	}
	for table, id := range map[string]string{
		"components": "component-demo-agent", "component_releases": "release-demo-agent-1.0.0",
		"scenarios": "scenario-demo-agent", "scenario_revisions": "scenario-demo-agent-r1",
		"environments": "environment-local", "environment_revisions": "environment-local-r1",
	} {
		var count int
		if err := migrated.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %s: count=%d err=%v", table, id, count, err)
		}
	}
	var foreignKeyIssue string
	if err := migrated.DB().QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_check LIMIT 1`).Scan(&foreignKeyIssue); err != sql.ErrNoRows {
		t.Fatalf("foreign key check issue=%q err=%v", foreignKeyIssue, err)
	}
	if _, err := migrated.DB().ExecContext(ctx, `DELETE FROM audit_events WHERE id='audit-keep'`); err == nil {
		t.Fatal("append-only audit trigger was not restored")
	}
	var applied int
	if err := migrated.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version='migrations/003_component_classification.sql'`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("migration applied=%d err=%v", applied, err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("repeat startup after migration: %v", err)
	}
	defer reopened.Close()
	if err := reopened.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version='migrations/003_component_classification.sql'`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("migration was not idempotent: applied=%d err=%v", applied, err)
	}
}

func TestMinimalKubernetesMigrationRemovesLegacyStagesAndWidensKinds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-k8s.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/001_init.sql", "migrations/002_audit_append_only.sql", "migrations/003_component_classification.sql"} {
		content, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`INSERT INTO users(id,name,role,created_at) VALUES('component-bob','Bob','component_owner','` + now + `'),('scenario-carol','Carol','scenario_owner','` + now + `'),('environment-dave','Dave','environment_owner','` + now + `')`,
		`INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,category,component_kind,requiredness) VALUES('component-k8s-1.17.5-master','k8s-master','Control Plane','legacy','component-bob','` + now + `','` + now + `','orchestration_core','control_plane','delivery_stage','core_required'),('component-keep','keep','Keep','generic','component-bob','` + now + `','` + now + `','runtime_state','runtime','software','profile_required')`,
		`INSERT INTO component_releases(id,component_id,version,release_type,status,risk_level,created_at) VALUES('release-k8s-1.17.5-master','component-k8s-1.17.5-master','v1.17.5','atomic','released','destructive','` + now + `'),('release-keep','component-keep','1.0.0','atomic','released','low','` + now + `')`,
		`INSERT INTO action_definitions(id,release_id,name,kind,playbook,timeout_seconds,risk_level,destructive) VALUES('action-k8s-master','release-k8s-1.17.5-master','install','install','master.yml',60,'destructive',1)`,
		`INSERT INTO scenarios(id,slug,name,owner_id,current_revision_id,created_at,updated_at) VALUES('scenario-k8s-1.17.5','legacy-k8s','Legacy K8s','scenario-carol','scenario-k8s-1.17.5-r1','` + now + `','` + now + `')`,
		`INSERT INTO scenario_revisions(id,scenario_id,revision,status,created_at) VALUES('scenario-k8s-1.17.5-r1','scenario-k8s-1.17.5',1,'draft','` + now + `')`,
		`INSERT INTO environments(id,name,owner_id,current_revision_id,created_at,updated_at) VALUES('environment-k8s-1.17.5-template','Legacy K8s','environment-dave','environment-k8s-1.17.5-template-r1','` + now + `','` + now + `')`,
		`INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('environment-k8s-1.17.5-template-r1','environment-k8s-1.17.5-template',1,'` + now + `')`,
		`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,created_at) VALUES('run-k8s-legacy','scenario_test','succeeded','scenario-carol','environment-k8s-1.17.5-template','environment-k8s-1.17.5-template-r1','release-k8s-1.17.5-master','scenario-k8s-1.17.5-r1','` + now + `')`,
		`INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES('audit-k8s-legacy','system','run.finished','run','run-k8s-legacy','{"release":"release-k8s-1.17.5-master"}','` + now + `'),('audit-keep','system','keep','component','component-keep','{}','` + now + `')`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy Kubernetes database: %v\n%s", err, statement)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migrate legacy Kubernetes database: %v", err)
	}
	defer migrated.Close()
	for table, id := range map[string]string{
		"components": "component-k8s-1.17.5-master", "component_releases": "release-k8s-1.17.5-master",
		"scenarios": "scenario-k8s-1.17.5", "scenario_revisions": "scenario-k8s-1.17.5-r1",
		"environments": "environment-k8s-1.17.5-template", "environment_revisions": "environment-k8s-1.17.5-template-r1",
		"runs": "run-k8s-legacy", "audit_events": "audit-k8s-legacy",
	} {
		var count int
		if err := migrated.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %s: count=%d err=%v", table, id, count, err)
		}
	}
	if _, err := migrated.DB().ExecContext(ctx, `INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,category,component_kind,requiredness) VALUES('component-config','config','Config','','component-bob',?,?, 'host_foundation','security','configuration','core_required'),('component-artifacts','artifacts','Artifacts','','component-bob',?,?, 'host_foundation','security','artifact_set','core_required')`, now, now, now, now); err != nil {
		t.Fatalf("new component kinds rejected: %v", err)
	}
	var foreignKeyIssue string
	if err := migrated.DB().QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_check LIMIT 1`).Scan(&foreignKeyIssue); err != sql.ErrNoRows {
		t.Fatalf("foreign key check issue=%q err=%v", foreignKeyIssue, err)
	}
	if _, err := migrated.DB().ExecContext(ctx, `DELETE FROM audit_events WHERE id='audit-keep'`); err == nil {
		t.Fatal("append-only audit trigger was not restored")
	}
}

func TestOpenFuyaoContractMigrationDirectlyReplacesOnlySeedData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "openfuyao-legacy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"migrations/001_init.sql", "migrations/002_audit_append_only.sql",
		"migrations/003_component_classification.sql", "migrations/004_minimal_kubernetes_components.sql",
	} {
		content, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`INSERT INTO users(id,name,role,created_at) VALUES('component-bob','Bob','component_owner','` + now + `'),('scenario-carol','Carol','scenario_owner','` + now + `'),('environment-dave','Dave','environment_owner','` + now + `')`,
		`INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,category,component_kind,requiredness) VALUES('component-bke-master','bke-master','BKE Master','legacy','component-bob','` + now + `','` + now + `','platform_extension','platform','software','profile_required'),('component-kubelet','kubelet','Kubelet','keep','component-bob','` + now + `','` + now + `','orchestration_core','worker','software','core_required'),('component-user','user-component','User Component','keep','component-bob','` + now + `','` + now + `','runtime_state','runtime','software','optional')`,
		`INSERT INTO component_releases(id,component_id,version,release_type,status,risk_level,created_at) VALUES('release-bke-master-25.12','component-bke-master','v25.12','atomic','released','destructive','` + now + `'),('release-kubelet-1.17.5','component-kubelet','v1.17.5','atomic','released','destructive','` + now + `'),('release-bke-user-25.12','component-user','v1','atomic','released','low','` + now + `')`,
		`INSERT INTO action_definitions(id,release_id,name,kind,playbook,timeout_seconds,risk_level,destructive) VALUES('action-bke-master-install','release-bke-master-25.12','install','install','openfuyao/component-bke-master.platform.yml',60,'destructive',1),('action-user','release-bke-user-25.12','install','install','user.yml',60,'low',0)`,
		`INSERT INTO scenarios(id,slug,name,owner_id,current_revision_id,created_at,updated_at) VALUES('scenario-openfuyao','openfuyao','OpenFuyao','scenario-carol','scenario-openfuyao-r1','` + now + `','` + now + `'),('scenario-user','user','User','scenario-carol','scenario-user-r1','` + now + `','` + now + `')`,
		`INSERT INTO scenario_revisions(id,scenario_id,revision,status,created_at) VALUES('scenario-openfuyao-r1','scenario-openfuyao',1,'draft','` + now + `'),('scenario-user-r1','scenario-user',1,'draft','` + now + `')`,
		`INSERT INTO environments(id,name,owner_id,current_revision_id,created_at,updated_at) VALUES('environment-openfuyao-template','OpenFuyao','environment-dave','environment-openfuyao-template-r1','` + now + `','` + now + `'),('environment-user','User','environment-dave','environment-user-r1','` + now + `','` + now + `')`,
		`INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('environment-openfuyao-template-r1','environment-openfuyao-template',1,'` + now + `'),('environment-user-r1','environment-user',1,'` + now + `')`,
		`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,created_at) VALUES('run-openfuyao','scenario_test','succeeded','scenario-carol','environment-openfuyao-template','environment-openfuyao-template-r1','release-bke-master-25.12','scenario-openfuyao-r1','` + now + `'),('run-user','component_test','succeeded','component-bob','environment-user','environment-user-r1','release-bke-user-25.12',NULL,'` + now + `')`,
		`INSERT INTO approvals(id,run_id,status,requested_at) VALUES('approval-openfuyao','run-openfuyao','approved','` + now + `')`,
		`INSERT INTO notifications(id,user_id,type,title,body,resource_url,payload_json,created_at) VALUES('notification-openfuyao','component-bob','run_finished','legacy','legacy','/scenarios?selected=scenario-openfuyao','{}','` + now + `'),('notification-user','component-bob','run_finished','keep','keep','/user','{}','` + now + `')`,
		`INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES('audit-openfuyao','system','run.finished','run','run-openfuyao','{"scenario":"openfuyao"}','` + now + `'),('audit-user','system','keep','component','component-user','{}','` + now + `')`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed legacy OpenFuyao database: %v\n%s", err, statement)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migrate OpenFuyao database: %v", err)
	}
	defer migrated.Close()
	for table, id := range map[string]string{
		"components": "component-bke-master", "component_releases": "release-bke-master-25.12",
		"scenarios": "scenario-openfuyao", "scenario_revisions": "scenario-openfuyao-r1",
		"environments": "environment-openfuyao-template", "environment_revisions": "environment-openfuyao-template-r1",
		"runs": "run-openfuyao", "approvals": "approval-openfuyao", "notifications": "notification-openfuyao", "audit_events": "audit-openfuyao",
	} {
		var count int
		if err := migrated.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %s: count=%d err=%v", table, id, count, err)
		}
	}
	for table, id := range map[string]string{
		"components": "component-user", "component_releases": "release-bke-user-25.12", "scenarios": "scenario-user",
		"environments": "environment-user", "runs": "run-user", "notifications": "notification-user", "audit_events": "audit-user",
	} {
		var count int
		if err := migrated.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s lost %s: count=%d err=%v", table, id, count, err)
		}
	}
	if _, err := migrated.GetComponentRelease(ctx, "release-kubelet-1.17.5"); err != nil {
		t.Fatalf("Kubernetes release was removed: %v", err)
	}
	var columnCount int
	if err := migrated.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('action_definitions') WHERE name='required_credentials_json'`).Scan(&columnCount); err != nil || columnCount != 1 {
		t.Fatalf("required_credentials_json column count=%d err=%v", columnCount, err)
	}
	if _, err := migrated.DB().ExecContext(ctx, `DELETE FROM audit_events WHERE id='audit-user'`); err == nil {
		t.Fatal("append-only audit trigger was not restored")
	}
}

func assertMigrationTableCount(t *testing.T, database *Store, table string, want int) {
	t.Helper()
	var count int
	if err := database.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s=%d, want %d", table, count, want)
	}
}
