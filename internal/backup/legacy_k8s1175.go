package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

const (
	legacyK8S1175SchemaContract     = "first-version-20260826-reuse-workflows"
	legacyK8S1175ScenarioRevisionID = "scenario-revision-00ca404dda3a898409437be7"
	legacyK8S1175ReleaseCount       = 15
	legacyK8S1175DependencyCount    = 0
	legacyK8S1175ActionCount        = 45
	legacyK8S1175NodeCount          = 21
	legacyK8S1175EdgeCount          = 50
)

type ConvertLegacyK8S1175Options struct {
	SourceDatabase     string
	SourcePlaybookRoot string
	ScenarioRevisionID string
	Destination        string
	TargetSchema       string
}

type LegacyK8S1175Conversion struct {
	SourceSchemaContract string         `json:"sourceSchemaContract"`
	TargetSchemaContract string         `json:"targetSchemaContract"`
	ScenarioRevisionID   string         `json:"scenarioRevisionId"`
	Destination          string         `json:"destination"`
	CatalogSHA256        string         `json:"catalogSha256"`
	Counts               map[string]int `json:"counts"`
}

// ConvertLegacyK8S1175Catalog converts one audited historical scenario into a
// current-format Catalog. It deliberately accepts only the known source schema,
// scenario revision and object counts; it is not a general schema migration.
func ConvertLegacyK8S1175Catalog(ctx context.Context, options ConvertLegacyK8S1175Options) (LegacyK8S1175Conversion, error) {
	if strings.TrimSpace(options.SourceDatabase) == "" || strings.TrimSpace(options.SourcePlaybookRoot) == "" || strings.TrimSpace(options.Destination) == "" || strings.TrimSpace(options.TargetSchema) == "" {
		return LegacyK8S1175Conversion{}, fmt.Errorf("source database, source Playbook root, destination and target schema are required")
	}
	if options.ScenarioRevisionID != legacyK8S1175ScenarioRevisionID {
		return LegacyK8S1175Conversion{}, fmt.Errorf("unsupported legacy scenario revision %q", options.ScenarioRevisionID)
	}
	if _, err := os.Lstat(options.Destination); err == nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("destination already exists: %s", options.Destination)
	} else if !os.IsNotExist(err) {
		return LegacyK8S1175Conversion{}, err
	}

	database, err := openReadOnly(options.SourceDatabase)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	defer database.Close()
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("begin legacy Catalog snapshot: %w", err)
	}
	defer tx.Rollback()
	var sourceSchema string
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_contract WHERE id=1`).Scan(&sourceSchema); err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("read legacy schema contract: %w", err)
	}
	if sourceSchema != legacyK8S1175SchemaContract {
		return LegacyK8S1175Conversion{}, fmt.Errorf("unsupported legacy schema contract %q", sourceSchema)
	}

	var scenarioID, scenarioStatus, currentRevisionID, graphJSON string
	err = tx.QueryRowContext(ctx, `
SELECT sr.scenario_id,sr.status,s.current_revision_id,sr.graph_json
FROM scenario_revisions sr JOIN scenarios s ON s.id=sr.scenario_id
WHERE sr.id=?`, options.ScenarioRevisionID).Scan(&scenarioID, &scenarioStatus, &currentRevisionID, &graphJSON)
	if err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("read legacy scenario revision: %w", err)
	}
	if scenarioStatus != "released" || currentRevisionID != options.ScenarioRevisionID {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy scenario revision is not the current Released revision")
	}
	var graph domain.ScenarioGraph
	if err := json.Unmarshal([]byte(graphJSON), &graph); err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("decode legacy scenario graph: %w", err)
	}
	if issues := domain.ValidateGraph(graph); len(issues) > 0 {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy scenario graph is invalid: %s", issues[0].Message)
	}
	if len(graph.Nodes) != legacyK8S1175NodeCount || len(graph.Edges) != legacyK8S1175EdgeCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy graph contains %d nodes and %d edges; expected %d and %d", len(graph.Nodes), len(graph.Edges), legacyK8S1175NodeCount, legacyK8S1175EdgeCount)
	}
	releaseSet := map[string]bool{}
	for _, node := range graph.Nodes {
		if node.ReleaseID == "" {
			return LegacyK8S1175Conversion{}, fmt.Errorf("legacy graph contains a node without releaseId")
		}
		releaseSet[node.ReleaseID] = true
	}
	if len(releaseSet) != legacyK8S1175ReleaseCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy graph references %d Releases; expected %d", len(releaseSet), legacyK8S1175ReleaseCount)
	}
	releaseIDs := sortedSetKeys(releaseSet)
	placeholders := sqlPlaceholders(len(releaseIDs))
	args := stringArgs(releaseIDs)

	components, err := dumpQuery(ctx, tx, "components", catalogTableSpec("components").columns, `
SELECT c.id,c.slug,c.name,c.description,c.owner_id,c.created_at,c.updated_at,c.layer,
json_array('legacy-category:'||c.category,'legacy-kind:'||c.component_kind,'legacy-requiredness:'||c.requiredness)
FROM components c JOIN component_releases r ON r.component_id=c.id
WHERE r.id IN (`+placeholders+`) ORDER BY c.slug,c.id`, args...)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(components.Rows) != legacyK8S1175ReleaseCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure contains %d Components; expected %d", len(components.Rows), legacyK8S1175ReleaseCount)
	}
	lines, err := dumpQuery(ctx, tx, "component_release_lines", catalogTableSpec("component_release_lines").columns, `
SELECT 'line-'||r.id,r.component_id,c.name||' '||r.version,r.created_at
FROM component_releases r JOIN components c ON c.id=r.component_id
WHERE r.id IN (`+placeholders+`) AND r.status='released' ORDER BY r.component_id,r.created_at,r.id`, args...)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}

	releases, err := dumpQuery(ctx, tx, "component_releases", catalogTableSpec("component_releases").columns, `
SELECT id,component_id,'line-'||id,NULL,NULL,version,status,release_notes,'not_applicable',0,1,risk_level,environment_constraints_json,parameters_json,created_at,released_at,deprecated_at
FROM component_releases WHERE id IN (`+placeholders+`) AND status='released' ORDER BY component_id,created_at,id`, args...)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(releases.Rows) != legacyK8S1175ReleaseCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure contains %d Released Releases; expected %d", len(releases.Rows), legacyK8S1175ReleaseCount)
	}

	dependencies, err := dumpQuery(ctx, tx, "component_dependencies", catalogTableSpec("component_dependencies").columns, `
SELECT id,release_id,upstream_component_id,upstream_release_id,purpose,parameter_mappings_json
FROM component_dependencies WHERE release_id IN (`+placeholders+`) ORDER BY release_id,upstream_component_id,id`, args...)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(dependencies.Rows) != legacyK8S1175DependencyCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure contains %d Component dependencies; expected %d", len(dependencies.Rows), legacyK8S1175DependencyCount)
	}

	actions, err := dumpQuery(ctx, tx, "action_definitions", catalogTableSpec("action_definitions").columns, `
SELECT id,release_id,name,kind,playbook,'',tags_json,limit_pattern,host_group,allowed_parameters_json,required_credentials_json,timeout_seconds,risk_level,destructive,idempotent,from_release_id,to_release_id
FROM action_definitions WHERE release_id IN (`+placeholders+`) ORDER BY release_id,kind,id`, args...)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(actions.Rows) != legacyK8S1175ActionCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure contains %d Actions; expected %d", len(actions.Rows), legacyK8S1175ActionCount)
	}

	scenarios, err := dumpQuery(ctx, tx, "scenarios", catalogTableSpec("scenarios").columns, `
SELECT id,slug,name,description,owner_id,current_revision_id,created_at,updated_at FROM scenarios WHERE id=?`, scenarioID)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	revisions, err := dumpQuery(ctx, tx, "scenario_revisions", catalogTableSpec("scenario_revisions").columns, `
SELECT id,scenario_id,revision,status,1,graph_json,created_at,test_passed_at,released_at,deprecated_at,abandoned_at FROM scenario_revisions WHERE id=?`, options.ScenarioRevisionID)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(scenarios.Rows) != 1 || len(revisions.Rows) != 1 {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy scenario selection is not singular")
	}

	ownerSet := map[string]bool{}
	ownerIndex := columnIndex(components.Columns, "owner_id")
	for _, row := range components.Rows {
		owner, _ := row[ownerIndex].Value().(string)
		ownerSet[owner] = true
	}
	scenarioOwnerIndex := columnIndex(scenarios.Columns, "owner_id")
	scenarioOwner, _ := scenarios.Rows[0][scenarioOwnerIndex].Value().(string)
	ownerSet[scenarioOwner] = true
	ownerIDs := sortedSetKeys(ownerSet)
	users, err := dumpQuery(ctx, tx, "users", catalogTableSpec("users").columns, `
SELECT id,name,role,created_at FROM users WHERE id IN (`+sqlPlaceholders(len(ownerIDs))+`) ORDER BY id`, stringArgs(ownerIDs)...)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(users.Rows) != len(ownerIDs) {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure is missing an owner identity")
	}

	var artifactCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_release_artifacts WHERE release_id IN (`+placeholders+`)`, args...).Scan(&artifactCount); err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("inspect legacy artifacts: %w", err)
	}
	if artifactCount != 0 {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure unexpectedly contains %d artifacts", artifactCount)
	}
	if err := tx.Commit(); err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("finish legacy Catalog snapshot: %w", err)
	}

	parent := filepath.Dir(options.Destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	stage, err := os.MkdirTemp(parent, ".legacy-k8s1175-")
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	defer os.RemoveAll(stage)
	playbooks, err := convertLegacyPlaybooks(actions, options.SourcePlaybookRoot, stage)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if len(playbooks) != legacyK8S1175ActionCount {
		return LegacyK8S1175Conversion{}, fmt.Errorf("legacy closure contains %d unique Playbooks; expected %d", len(playbooks), legacyK8S1175ActionCount)
	}

	emptyTable := func(name string) TableDump {
		return TableDump{Name: name, Columns: append([]string(nil), catalogTableSpec(name).columns...)}
	}
	catalog := Catalog{
		FormatVersion:         CatalogFormatVersion,
		SchemaContract:        options.TargetSchema,
		PublicationGeneration: 1,
		Tables: []TableDump{users, components, lines, releases, dependencies, actions, scenarios, revisions,
			emptyTable("component_release_artifacts"), emptyTable("component_release_images")},
		Playbooks: playbooks,
	}
	sortCatalog(&catalog)
	encoded, err := canonicalJSON(catalog)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	parsed, err := ParseCatalog(encoded)
	if err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("validate converted Catalog: %w", err)
	}
	if err := validateCatalogReferences(parsed); err != nil {
		return LegacyK8S1175Conversion{}, fmt.Errorf("validate converted Catalog references: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "catalog.json"), encoded, 0o600); err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	digest, err := CatalogDigest(encoded, stage, playbooks)
	if err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	if err := os.Rename(stage, options.Destination); err != nil {
		return LegacyK8S1175Conversion{}, err
	}
	return LegacyK8S1175Conversion{
		SourceSchemaContract: sourceSchema,
		TargetSchemaContract: options.TargetSchema,
		ScenarioRevisionID:   options.ScenarioRevisionID,
		Destination:          options.Destination,
		CatalogSHA256:        digest,
		Counts:               catalogCounts(catalog),
	}, nil
}

func catalogTableSpec(name string) tableSpec {
	for _, spec := range catalogTables {
		if spec.name == name {
			return spec
		}
	}
	panic("unknown Catalog table " + name)
}

type legacyQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func dumpQuery(ctx context.Context, database legacyQueryer, name string, columns []string, query string, args ...any) (TableDump, error) {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return TableDump{}, fmt.Errorf("convert legacy %s: %w", name, err)
	}
	defer rows.Close()
	table := TableDump{Name: name, Columns: append([]string(nil), columns...)}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return TableDump{}, fmt.Errorf("scan legacy %s: %w", name, err)
		}
		row := make([]DBCell, len(values))
		for index, value := range values {
			row[index], err = databaseCell(value)
			if err != nil {
				return TableDump{}, fmt.Errorf("convert legacy %s column %s: %w", name, columns[index], err)
			}
		}
		table.Rows = append(table.Rows, row)
	}
	return table, rows.Err()
}

func convertLegacyPlaybooks(actions TableDump, root, destination string) ([]Playbook, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("resolve legacy Playbook root: %w", err)
	}
	playbookIndex := columnIndex(actions.Columns, "playbook")
	shaIndex := columnIndex(actions.Columns, "playbook_sha256")
	seen := map[string]bool{}
	var playbooks []Playbook
	for rowIndex := range actions.Rows {
		relative, _ := actions.Rows[rowIndex][playbookIndex].Value().(string)
		clean, source, err := resolveRelative(rootAbs, relative)
		if err != nil {
			return nil, fmt.Errorf("convert legacy Playbook %q: %w", relative, err)
		}
		if seen[clean] {
			return nil, fmt.Errorf("legacy Actions reuse Playbook %q; expected one immutable Playbook per Action", clean)
		}
		seen[clean] = true
		contents, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("read legacy Playbook %q: %w", relative, err)
		}
		digest := sha256Bytes(contents)
		actions.Rows[rowIndex][shaIndex] = DBCell{Kind: "text", Text: digest}
		target := filepath.Join(destination, "playbooks", filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			return nil, err
		}
		playbooks = append(playbooks, Playbook{Path: clean, SHA256: digest, SizeBytes: int64(len(contents))})
	}
	sort.Slice(playbooks, func(i, j int) bool { return playbooks[i].Path < playbooks[j].Path })
	return playbooks, nil
}

func sortedSetKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func sqlPlaceholders(count int) string {
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for index := range values {
		args[index] = values[index]
	}
	return args
}
