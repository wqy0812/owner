package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex/platform-demo/internal/store"
	_ "modernc.org/sqlite"
)

func TestConvertLegacyK8S1175Catalog(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "legacy.db")
	playbookRoot := filepath.Join(root, "jobs")
	createLegacyK8S1175Fixture(t, databasePath, playbookRoot, legacyK8S1175SchemaContract)
	destination := filepath.Join(root, "converted")

	result, err := ConvertLegacyK8S1175Catalog(context.Background(), ConvertLegacyK8S1175Options{
		SourceDatabase: databasePath, SourcePlaybookRoot: playbookRoot,
		ScenarioRevisionID: legacyK8S1175ScenarioRevisionID,
		Destination:        destination,
		TargetSchema:       store.CurrentSchemaContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]int{
		"components": 15, "component_releases": 15, "component_dependencies": 0,
		"action_definitions": 45, "scenarios": 1, "scenario_revisions": 1,
		"playbooks": 45,
	} {
		if result.Counts[name] != expected {
			t.Fatalf("count %s=%d, want %d", name, result.Counts[name], expected)
		}
	}
	contents, err := os.ReadFile(filepath.Join(destination, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseCatalog(contents)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.SchemaContract != store.CurrentSchemaContract || catalog.PublicationGeneration != 1 {
		t.Fatalf("unexpected converted contract: %#v", catalog)
	}
	current, err := store.Open(context.Background(), filepath.Join(root, "current.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	tx, err := current.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreUsers(context.Background(), tx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := restoreDefinitionTables(context.Background(), tx, catalog, 1); err != nil {
		t.Fatal(err)
	}
	if err := verifyForeignKeysTx(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tables := catalogTableMap(catalog)
	tagsIndex := columnIndex(tables["components"].Columns, "tags_json")
	if got, _ := tables["components"].Rows[0][tagsIndex].Value().(string); got != `["legacy-category:preflight","legacy-kind:delivery_stage","legacy-requiredness:core_required"]` {
		t.Fatalf("legacy classification tags = %s", got)
	}
	shaIndex := columnIndex(tables["action_definitions"].Columns, "playbook_sha256")
	for _, row := range tables["action_definitions"].Rows {
		if sha, _ := row[shaIndex].Value().(string); len(sha) != 64 {
			t.Fatalf("invalid converted Playbook digest %q", sha)
		}
	}
	if _, err := ConvertLegacyK8S1175Catalog(context.Background(), ConvertLegacyK8S1175Options{
		SourceDatabase: databasePath, SourcePlaybookRoot: playbookRoot,
		ScenarioRevisionID: legacyK8S1175ScenarioRevisionID,
		Destination:        destination, TargetSchema: store.CurrentSchemaContract,
	}); err == nil || !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("existing destination error = %v", err)
	}
}

func TestConvertLegacyK8S1175CatalogRejectsInvalidGraph(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "legacy.db")
	playbookRoot := filepath.Join(root, "jobs")
	createLegacyK8S1175Fixture(t, databasePath, playbookRoot, legacyK8S1175SchemaContract)
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := database.QueryRow(`SELECT graph_json FROM scenario_revisions WHERE id=?`, legacyK8S1175ScenarioRevisionID).Scan(&raw); err != nil {
		database.Close()
		t.Fatal(err)
	}
	var graph map[string]any
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		database.Close()
		t.Fatal(err)
	}
	edges := graph["edges"].([]any)
	edges[0].(map[string]any)["target"] = "missing-node"
	encoded, err := json.Marshal(graph)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE scenario_revisions SET graph_json=? WHERE id=?`, string(encoded), legacyK8S1175ScenarioRevisionID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = ConvertLegacyK8S1175Catalog(context.Background(), ConvertLegacyK8S1175Options{
		SourceDatabase: databasePath, SourcePlaybookRoot: playbookRoot,
		ScenarioRevisionID: legacyK8S1175ScenarioRevisionID,
		Destination:        filepath.Join(root, "converted"), TargetSchema: store.CurrentSchemaContract,
	})
	if err == nil || !strings.Contains(err.Error(), "legacy scenario graph is invalid") {
		t.Fatalf("invalid graph error = %v", err)
	}
}

func TestConvertLegacyK8S1175CatalogRejectsOtherSourceContract(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "legacy.db")
	playbookRoot := filepath.Join(root, "jobs")
	createLegacyK8S1175Fixture(t, databasePath, playbookRoot, "other-contract")
	_, err := ConvertLegacyK8S1175Catalog(context.Background(), ConvertLegacyK8S1175Options{
		SourceDatabase: databasePath, SourcePlaybookRoot: playbookRoot,
		ScenarioRevisionID: legacyK8S1175ScenarioRevisionID,
		Destination:        filepath.Join(root, "converted"), TargetSchema: "current-test-contract",
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported legacy schema contract "other-contract"`) {
		t.Fatalf("schema rejection error = %v", err)
	}
}

func createLegacyK8S1175Fixture(t *testing.T, databasePath, playbookRoot, contract string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	schema := `
CREATE TABLE schema_contract(id INTEGER PRIMARY KEY,version TEXT NOT NULL);
CREATE TABLE users(id TEXT PRIMARY KEY,name TEXT NOT NULL,role TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE components(id TEXT PRIMARY KEY,slug TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL,owner_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,layer TEXT NOT NULL,category TEXT NOT NULL,component_kind TEXT NOT NULL,requiredness TEXT NOT NULL);
CREATE TABLE component_releases(id TEXT PRIMARY KEY,component_id TEXT NOT NULL,version TEXT NOT NULL,status TEXT NOT NULL,release_notes TEXT NOT NULL,breaking INTEGER NOT NULL,risk_level TEXT NOT NULL,environment_constraints_json TEXT NOT NULL,parameters_json TEXT NOT NULL,created_at TEXT NOT NULL,released_at TEXT,deprecated_at TEXT);
CREATE TABLE component_dependencies(id TEXT PRIMARY KEY,release_id TEXT NOT NULL,upstream_component_id TEXT NOT NULL,upstream_release_id TEXT NOT NULL,purpose TEXT NOT NULL,parameter_mappings_json TEXT NOT NULL);
CREATE TABLE action_definitions(id TEXT PRIMARY KEY,release_id TEXT NOT NULL,name TEXT NOT NULL,kind TEXT NOT NULL,playbook TEXT NOT NULL,tags_json TEXT NOT NULL,limit_pattern TEXT NOT NULL,host_group TEXT NOT NULL,allowed_parameters_json TEXT NOT NULL,required_credentials_json TEXT NOT NULL,timeout_seconds INTEGER NOT NULL,risk_level TEXT NOT NULL,destructive INTEGER NOT NULL,from_release_id TEXT,to_release_id TEXT,idempotent INTEGER NOT NULL);
CREATE TABLE scenarios(id TEXT PRIMARY KEY,slug TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL,owner_id TEXT NOT NULL,current_revision_id TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE scenario_revisions(id TEXT PRIMARY KEY,scenario_id TEXT NOT NULL,revision INTEGER NOT NULL,status TEXT NOT NULL,graph_json TEXT NOT NULL,created_at TEXT NOT NULL,test_passed_at TEXT,released_at TEXT,deprecated_at TEXT,abandoned_at TEXT);
CREATE TABLE component_release_artifacts(id TEXT PRIMARY KEY,release_id TEXT NOT NULL);
`
	if _, err := database.Exec(schema); err != nil {
		t.Fatal(err)
	}
	now := "2026-08-28T00:00:00Z"
	if _, err := database.Exec(`INSERT INTO schema_contract VALUES(1,?); INSERT INTO users VALUES('component-owner','Component Owner','component_owner',?); INSERT INTO users VALUES('scenario-owner','Scenario Owner','scenario_owner',?)`, contract, now, now); err != nil {
		t.Fatal(err)
	}
	releaseIDs := make([]string, legacyK8S1175ReleaseCount)
	for index := 0; index < legacyK8S1175ReleaseCount; index++ {
		componentID := fmt.Sprintf("component-%02d", index)
		releaseID := fmt.Sprintf("release-%02d", index)
		releaseIDs[index] = releaseID
		if _, err := database.Exec(`INSERT INTO components VALUES(?,?,?,?,?,?,?,?,?,?,?)`, componentID, componentID, componentID, "", "component-owner", now, now, "host_foundation", "preflight", "delivery_stage", "core_required"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO component_releases VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, componentID, "v1", "released", "", 0, "low", `{}`, `[]`, now, now, nil); err != nil {
			t.Fatal(err)
		}
		for action := 0; action < 3; action++ {
			path := fmt.Sprintf("component-%02d/action-%d.yml", index, action)
			fullPath := filepath.Join(playbookRoot, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fullPath, []byte(fmt.Sprintf("---\n# %s\n", path)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO action_definitions VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("action-%02d-%d", index, action), releaseID, fmt.Sprintf("action %d", action), []string{"install", "verify", "rollback"}[action], path, `[]`, "", "all", `[]`, `[]`, 1800, "low", 0, nil, nil, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	nodes := make([]map[string]any, legacyK8S1175NodeCount)
	for index := range nodes {
		nodes[index] = map[string]any{"id": fmt.Sprintf("node-%02d", index), "name": fmt.Sprintf("Node %02d", index), "releaseId": releaseIDs[index%len(releaseIDs)], "action": "install", "hostGroup": "all", "values": map[string]any{}, "runInputs": []string{}}
	}
	edges := make([]map[string]any, 0, legacyK8S1175EdgeCount)
	for source := 0; source < len(nodes) && len(edges) < legacyK8S1175EdgeCount; source++ {
		for target := source + 1; target < len(nodes) && len(edges) < legacyK8S1175EdgeCount; target++ {
			edges = append(edges, map[string]any{"id": fmt.Sprintf("edge-%02d", len(edges)), "source": nodes[source]["id"], "target": nodes[target]["id"]})
		}
	}
	graph, err := json.Marshal(map[string]any{"nodes": nodes, "edges": edges})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO scenarios VALUES(?,?,?,?,?,?,?,?)`, "scenario", "k8s1175", "Kubernetes 1.17.5", "", "scenario-owner", legacyK8S1175ScenarioRevisionID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO scenario_revisions VALUES(?,?,?,?,?,?,?,?,?,?)`, legacyK8S1175ScenarioRevisionID, "scenario", 1, "released", string(graph), now, now, now, nil, nil); err != nil {
		t.Fatal(err)
	}
}
