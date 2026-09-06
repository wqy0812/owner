package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type tableSpec struct {
	name    string
	columns []string
	query   string
}

var catalogTables = []tableSpec{
	{name: "users", columns: []string{"id", "name", "role", "created_at"}, query: `
SELECT id,name,role,created_at FROM users WHERE id IN (
  SELECT c.owner_id FROM components c JOIN component_releases r ON r.component_id=c.id WHERE r.status IN ('released','deprecated')
  UNION SELECT s.owner_id FROM scenarios s JOIN scenario_revisions sr ON sr.scenario_id=s.id WHERE sr.status IN ('released','deprecated')
  UNION SELECT a.source_updated_by FROM component_release_artifacts a JOIN component_releases r ON r.id=a.release_id WHERE r.status IN ('released','deprecated')
  UNION SELECT a.created_by FROM component_release_artifacts a JOIN component_releases r ON r.id=a.release_id WHERE r.status IN ('released','deprecated')
  UNION SELECT i.source_updated_by FROM component_release_images i JOIN component_releases r ON r.id=i.release_id WHERE r.status IN ('released','deprecated')
  UNION SELECT i.created_by FROM component_release_images i JOIN component_releases r ON r.id=i.release_id WHERE r.status IN ('released','deprecated')
  UNION SELECT id FROM users WHERE role='platform_admin'
) ORDER BY id`},
	{name: "platform_option_categories", columns: []string{"id", "technical_key", "label", "category_type", "parent_category_id", "environment_required", "retired_at", "sort_order", "created_by", "created_at"}, query: `
SELECT c.id,c.technical_key,c.label,c.category_type,c.parent_category_id,c.environment_required,c.retired_at,c.sort_order,c.created_by,c.created_at
FROM platform_option_categories c
WHERE EXISTS (
  SELECT 1 FROM component_releases r,json_each(r.environment_constraints_json) dimension
  WHERE r.status IN ('released','deprecated') AND dimension.key=c.technical_key
)
OR EXISTS (
  SELECT 1 FROM scenario_revisions sr,json_each(sr.environment_constraints_json) dimension
  WHERE sr.status IN ('released','deprecated') AND dimension.key=c.technical_key
)
OR (c.category_type='host_group' AND (
  EXISTS (SELECT 1 FROM action_definitions a JOIN component_releases r ON r.id=a.release_id WHERE r.status IN ('released','deprecated') AND a.host_group<>'')
  OR EXISTS (
    SELECT 1 FROM scenario_revisions sr,json_each(sr.graph_json,'$.nodes') node
    WHERE sr.status IN ('released','deprecated') AND COALESCE(json_extract(node.value,'$.hostGroup'),'')<>''
  )
  OR EXISTS (SELECT 1 FROM scenario_revisions sr,json_each(sr.lifecycle_json,'$.acceptanceJobs') job WHERE sr.status IN ('released','deprecated') AND COALESCE(json_extract(job.value,'$.hostGroup'),'')<>'')
))
ORDER BY c.sort_order,c.id`},
	{name: "platform_options", columns: []string{"id", "category_id", "parent_option_id", "technical_value", "label", "retired_at", "sort_order", "created_by", "created_at"}, query: `
SELECT o.id,o.category_id,o.parent_option_id,o.technical_value,o.label,o.retired_at,o.sort_order,o.created_by,o.created_at
FROM platform_options o JOIN platform_option_categories c ON c.id=o.category_id
WHERE EXISTS (
  SELECT 1 FROM component_releases r,json_each(r.environment_constraints_json) dimension,json_each(dimension.value) selected
  WHERE r.status IN ('released','deprecated') AND dimension.key=c.technical_key AND selected.value=o.technical_value
)
OR EXISTS (
  SELECT 1 FROM scenario_revisions sr,json_each(sr.environment_constraints_json) dimension,json_each(dimension.value) selected
  WHERE sr.status IN ('released','deprecated') AND dimension.key=c.technical_key AND selected.value=o.technical_value
)
OR (c.category_type='host_group' AND (
  EXISTS (
    SELECT 1 FROM action_definitions a JOIN component_releases r ON r.id=a.release_id
    WHERE r.status IN ('released','deprecated') AND a.host_group=o.technical_value
  )
  OR EXISTS (
    SELECT 1 FROM scenario_revisions sr,json_each(sr.graph_json,'$.nodes') node
    WHERE sr.status IN ('released','deprecated') AND json_extract(node.value,'$.hostGroup')=o.technical_value
  )
  OR EXISTS (SELECT 1 FROM scenario_revisions sr,json_each(sr.lifecycle_json,'$.acceptanceJobs') job WHERE sr.status IN ('released','deprecated') AND json_extract(job.value,'$.hostGroup')=o.technical_value)
))
ORDER BY c.sort_order,o.sort_order,o.id`},
	{name: "environment_parameter_definitions", columns: []string{"id", "technical_key", "label", "description", "parameter_type", "enum_json", "min_length", "created_by", "created_at"}, query: `
SELECT d.id,d.technical_key,d.label,d.description,d.parameter_type,d.enum_json,d.min_length,d.created_by,d.created_at
FROM environment_parameter_definitions d
WHERE EXISTS (
  SELECT 1 FROM component_releases r,json_each(r.parameters_json) p
  WHERE r.status IN ('released','deprecated') AND json_extract(p.value,'$.environmentBinding.definitionId')=d.id
 ) OR EXISTS (SELECT 1 FROM scenario_revisions sr,json_each(sr.lifecycle_json,'$.acceptanceParameters') p WHERE sr.status IN ('released','deprecated') AND json_extract(p.value,'$.environmentBinding.definitionId')=d.id)
 OR EXISTS (SELECT 1 FROM scenario_revisions sr,json_each(sr.lifecycle_json,'$.acceptanceBindings') b WHERE sr.status IN ('released','deprecated') AND json_extract(b.value,'$.source')='environment' AND json_extract(b.value,'$.sourceParameter')=d.technical_key)
 ORDER BY d.id`},
	{name: "environment_parameter_defaults", columns: []string{"definition_id", "value_json"}, query: `
SELECT d.definition_id,d.value_json FROM environment_parameter_defaults d
WHERE EXISTS (
  SELECT 1 FROM component_releases r,json_each(r.parameters_json) p
  WHERE r.status IN ('released','deprecated') AND json_extract(p.value,'$.environmentBinding.definitionId')=d.definition_id
 ) OR EXISTS (SELECT 1 FROM scenario_revisions sr,json_each(sr.lifecycle_json,'$.acceptanceParameters') p WHERE sr.status IN ('released','deprecated') AND json_extract(p.value,'$.environmentBinding.definitionId')=d.definition_id)
 OR EXISTS (SELECT 1 FROM scenario_revisions sr,json_each(sr.lifecycle_json,'$.acceptanceBindings') b JOIN environment_parameter_definitions def ON def.id=d.definition_id WHERE sr.status IN ('released','deprecated') AND json_extract(b.value,'$.source')='environment' AND json_extract(b.value,'$.sourceParameter')=def.technical_key)
 ORDER BY d.definition_id`},
	{name: "components", columns: []string{"id", "slug", "name", "description", "owner_id", "created_at", "updated_at", "layer", "tags_json"}, query: `
SELECT id,slug,name,description,owner_id,created_at,updated_at,layer,tags_json FROM components c
WHERE EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=c.id AND r.status IN ('released','deprecated')) ORDER BY slug,id`},
	{name: "component_release_lines", columns: []string{"id", "component_id", "name", "created_at", "environment_constraints_json"}, query: `
SELECT l.id,l.component_id,l.name,l.created_at,l.environment_constraints_json FROM component_release_lines l
WHERE EXISTS (SELECT 1 FROM component_releases r WHERE r.line_id=l.id AND r.status IN ('released','deprecated')) ORDER BY l.component_id,l.created_at,l.id`},
	{name: "component_releases", columns: []string{"id", "component_id", "line_id", "parent_release_id", "template_source_release_id", "version", "status", "release_notes", "compatibility", "candidate", "review_status", "review_contract_digest", "review_submitted_at", "reviewed_by", "reviewed_at", "review_comment", "publication_generation", "risk_level", "environment_constraints_json", "parameters_json", "playbook_tree_sha256", "playbook_workspace_root", "created_at", "released_at", "deprecated_at"}, query: `
SELECT id,component_id,line_id,parent_release_id,template_source_release_id,version,status,release_notes,compatibility,0,review_status,review_contract_digest,review_submitted_at,reviewed_by,reviewed_at,review_comment,publication_generation,risk_level,environment_constraints_json,parameters_json,playbook_tree_sha256,playbook_workspace_root,created_at,released_at,deprecated_at
FROM component_releases WHERE status IN ('released','deprecated') ORDER BY component_id,created_at,id`},
	{name: "component_dependencies", columns: []string{"id", "release_id", "upstream_component_id", "upstream_release_id", "purpose", "parameter_mappings_json", "kind"}, query: `
SELECT d.id,d.release_id,d.upstream_component_id,d.upstream_release_id,d.purpose,d.parameter_mappings_json,d.kind FROM component_dependencies d
JOIN component_releases r ON r.id=d.release_id JOIN component_releases u ON u.id=d.upstream_release_id
WHERE r.status IN ('released','deprecated') AND u.status IN ('released','deprecated') ORDER BY d.release_id,d.upstream_component_id,d.id`},
	{name: "action_definitions", columns: []string{"id", "release_id", "name", "kind", "playbook", "playbook_sha256", "tags_json", "host_group", "required_credentials_json", "timeout_seconds", "risk_level", "destructive", "idempotent", "from_release_id", "to_release_id", "pre_check_action_id", "post_check_action_id", "become", "gather_facts", "resource_contract_json"}, query: `
SELECT a.id,a.release_id,a.name,a.kind,a.playbook,a.playbook_sha256,a.tags_json,a.host_group,a.required_credentials_json,a.timeout_seconds,a.risk_level,a.destructive,a.idempotent,a.from_release_id,a.to_release_id,a.pre_check_action_id,a.post_check_action_id,a.become,a.gather_facts,a.resource_contract_json
FROM action_definitions a JOIN component_releases r ON r.id=a.release_id WHERE r.status IN ('released','deprecated') ORDER BY a.release_id,a.kind,a.id`},
	{name: "component_playbook_files", columns: []string{"release_id", "relative_path", "sha256", "size_bytes", "media_type", "updated_at"}, query: `
SELECT f.release_id,f.relative_path,f.sha256,f.size_bytes,f.media_type,f.updated_at
FROM component_playbook_files f JOIN component_releases r ON r.id=f.release_id
WHERE r.status IN ('released','deprecated') ORDER BY f.release_id,f.relative_path`},
	{name: "scenarios", columns: []string{"id", "slug", "name", "description", "owner_id", "current_revision_id", "created_at", "updated_at", "forked_from_scenario_id", "forked_from_revision_id", "forked_from_digest", "environment_constraints_json"}, query: `
SELECT s.id,s.slug,s.name,s.description,s.owner_id,
COALESCE((SELECT sr.id FROM scenario_revisions sr WHERE sr.scenario_id=s.id AND sr.status IN ('released','deprecated') ORDER BY CASE sr.status WHEN 'released' THEN 0 ELSE 1 END,sr.revision DESC LIMIT 1),''),
s.created_at,s.updated_at,s.forked_from_scenario_id,s.forked_from_revision_id,s.forked_from_digest,s.environment_constraints_json FROM scenarios s WHERE EXISTS (SELECT 1 FROM scenario_revisions sr WHERE sr.scenario_id=s.id AND sr.status IN ('released','deprecated')) ORDER BY s.slug,s.id`},
	{name: "scenario_revisions", columns: []string{"id", "scenario_id", "revision", "status", "publication_generation", "graph_json", "environment_constraints_json", "created_at", "test_passed_at", "released_at", "deprecated_at", "abandoned_at", "lifecycle_json"}, query: `
SELECT id,scenario_id,revision,status,publication_generation,graph_json,environment_constraints_json,created_at,test_passed_at,released_at,deprecated_at,abandoned_at,lifecycle_json
FROM scenario_revisions WHERE status IN ('released','deprecated') ORDER BY scenario_id,revision,id`},
	{name: "component_release_artifacts", columns: []string{"id", "release_id", "alias", "filename", "sha256", "size_bytes", "source_url", "source_updated_by", "source_updated_at", "created_by", "created_at"}, query: `
SELECT a.id,a.release_id,a.alias,a.filename,a.sha256,a.size_bytes,a.source_url,a.source_updated_by,a.source_updated_at,a.created_by,a.created_at
FROM component_release_artifacts a JOIN component_releases r ON r.id=a.release_id WHERE r.status IN ('released','deprecated') ORDER BY a.release_id,a.alias,a.id`},
	{name: "component_release_images", columns: []string{"id", "release_id", "logical_name", "digest", "source_ref", "source_updated_by", "source_updated_at", "created_by", "created_at"}, query: `
SELECT i.id,i.release_id,i.logical_name,i.digest,i.source_ref,i.source_updated_by,i.source_updated_at,i.created_by,i.created_at
FROM component_release_images i JOIN component_releases r ON r.id=i.release_id WHERE r.status IN ('released','deprecated') ORDER BY i.release_id,i.logical_name,i.id`},
}

func ExportCatalog(ctx context.Context, databasePath, playbookRoot, destination string) (Catalog, []byte, error) {
	database, err := openReadOnly(databasePath)
	if err != nil {
		return Catalog{}, nil, err
	}
	defer database.Close()
	catalog := Catalog{FormatVersion: CatalogFormatVersion}
	if err := database.QueryRowContext(ctx, `SELECT version FROM schema_contract WHERE id=1`).Scan(&catalog.SchemaContract); err != nil {
		return Catalog{}, nil, fmt.Errorf("read schema contract: %w", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT generation FROM publication_state WHERE id=1`).Scan(&catalog.PublicationGeneration); err != nil {
		return Catalog{}, nil, fmt.Errorf("read publication generation: %w", err)
	}
	for _, spec := range catalogTables {
		table, err := dumpTable(ctx, database, spec)
		if err != nil {
			return Catalog{}, nil, err
		}
		catalog.Tables = append(catalog.Tables, table)
	}
	playbooks, err := exportPlaybooks(ctx, database, playbookRoot, destination)
	if err != nil {
		return Catalog{}, nil, err
	}
	acceptance, err := exportScenarioAcceptancePlaybooks(ctx, database, playbookRoot, destination)
	if err != nil {
		return Catalog{}, nil, err
	}
	catalog.Playbooks = append(playbooks, acceptance...)
	sortCatalog(&catalog)
	encoded, err := canonicalJSON(catalog)
	if err != nil {
		return Catalog{}, nil, err
	}
	if err := os.WriteFile(filepath.Join(destination, "catalog.json"), encoded, 0o600); err != nil {
		return Catalog{}, nil, err
	}
	return catalog, encoded, nil
}

func openReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return sql.Open("sqlite", "file:"+abs+"?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
}

func dumpTable(ctx context.Context, database *sql.DB, spec tableSpec) (TableDump, error) {
	rows, err := database.QueryContext(ctx, spec.query)
	if err != nil {
		return TableDump{}, fmt.Errorf("export %s: %w", spec.name, err)
	}
	defer rows.Close()
	table := TableDump{Name: spec.name, Columns: append([]string(nil), spec.columns...)}
	for rows.Next() {
		values := make([]any, len(spec.columns))
		pointers := make([]any, len(values))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return TableDump{}, fmt.Errorf("scan %s: %w", spec.name, err)
		}
		converted := make([]DBCell, len(values))
		for index, value := range values {
			cell, err := databaseCell(value)
			if err != nil {
				return TableDump{}, fmt.Errorf("export %s column %s: %w", spec.name, spec.columns[index], err)
			}
			converted[index] = cell
		}
		table.Rows = append(table.Rows, converted)
	}
	return table, rows.Err()
}

func databaseCell(value any) (DBCell, error) {
	switch typed := value.(type) {
	case nil:
		return DBCell{Kind: "null"}, nil
	case int64:
		return DBCell{Kind: "integer", Int: typed}, nil
	case string:
		return DBCell{Kind: "text", Text: typed}, nil
	case []byte:
		return DBCell{Kind: "text", Text: string(typed)}, nil
	default:
		return DBCell{}, fmt.Errorf("unsupported SQLite value %T", value)
	}
}

func exportPlaybooks(ctx context.Context, database *sql.DB, root, destination string) ([]Playbook, error) {
	type fileSpec struct{ relative, expected string }
	type manifestFile struct{ path, sha string }
	specs := []fileSpec{}
	manifestFiles := map[string][]manifestFile{}
	manifestTrees := map[string]string{}
	rows, err := database.QueryContext(ctx, `
SELECT f.release_id,f.relative_path,f.sha256,r.playbook_tree_sha256,r.playbook_workspace_root
FROM component_playbook_files f JOIN component_releases r ON r.id=f.release_id
WHERE r.status IN ('released','deprecated') ORDER BY f.release_id,f.relative_path`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var releaseID, workspaceRelative, expected, treeSHA, storedRoot string
		if err := rows.Scan(&releaseID, &workspaceRelative, &expected, &treeSHA, &storedRoot); err != nil {
			rows.Close()
			return nil, err
		}
		prefix := strings.TrimSpace(storedRoot)
		if prefix == "" {
			rows.Close()
			return nil, fmt.Errorf("release workspace has files but no stored root")
		}
		manifestFiles[releaseID] = append(manifestFiles[releaseID], manifestFile{path: workspaceRelative, sha: expected})
		manifestTrees[releaseID] = treeSHA
		specs = append(specs, fileSpec{relative: filepath.ToSlash(filepath.Join(filepath.FromSlash(prefix), filepath.FromSlash(workspaceRelative))), expected: expected})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for releaseID, files := range manifestFiles {
		sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
		hash := sha256.New()
		for _, file := range files {
			_, _ = hash.Write([]byte(file.path))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(file.sha))
			_, _ = hash.Write([]byte{0})
		}
		actual := hex.EncodeToString(hash.Sum(nil))
		if manifestTrees[releaseID] == "" || manifestTrees[releaseID] != actual {
			return nil, fmt.Errorf("release %s workspace manifest digest is %s, expected %s", releaseID, actual, manifestTrees[releaseID])
		}
	}
	var incomplete int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_releases r
 WHERE r.status IN ('released','deprecated')
 AND EXISTS (SELECT 1 FROM action_definitions a WHERE a.release_id=r.id)
 AND (r.playbook_workspace_root='' OR r.playbook_tree_sha256='' OR NOT EXISTS (SELECT 1 FROM component_playbook_files f WHERE f.release_id=r.id))`).Scan(&incomplete); err != nil {
		return nil, err
	}
	if incomplete > 0 {
		return nil, fmt.Errorf("released Playbooks require a complete workspace manifest")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("resolve Playbook root: %w", err)
	}
	seen := map[string]string{}
	var playbooks []Playbook
	for _, spec := range specs {
		relative, expected := spec.relative, spec.expected
		clean, source, err := resolveRelative(rootAbs, relative)
		if err != nil {
			return nil, fmt.Errorf("export Playbook %q: %w", relative, err)
		}
		contents, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("read Playbook %q: %w", relative, err)
		}
		digest := sha256.Sum256(contents)
		actual := hex.EncodeToString(digest[:])
		if expected == "" || actual != expected {
			return nil, fmt.Errorf("Playbook %q SHA-256 is %s, expected %s", relative, actual, expected)
		}
		if prior, duplicate := seen[clean]; duplicate {
			if prior != actual {
				return nil, fmt.Errorf("Playbook %q has conflicting content identities", relative)
			}
			continue
		}
		seen[clean] = actual
		target := filepath.Join(destination, "playbooks", filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			return nil, err
		}
		playbooks = append(playbooks, Playbook{Path: filepath.ToSlash(clean), SHA256: actual, SizeBytes: int64(len(contents))})
	}
	sort.Slice(playbooks, func(i, j int) bool { return playbooks[i].Path < playbooks[j].Path })
	return playbooks, nil
}

func resolveRelative(rootAbs, relative string) (string, string, error) {
	if filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes the Playbook root")
	}
	joined := filepath.Join(rootAbs, clean)
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootAbs, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("resolved path escapes the Playbook root")
	}
	return filepath.ToSlash(clean), resolved, nil
}

func CatalogDigest(catalogBytes []byte, root string, playbooks []Playbook) (string, error) {
	hash := sha256.New()
	_, _ = hash.Write(catalogBytes)
	for _, item := range playbooks {
		_, _ = io.WriteString(hash, item.Path+"\x00"+item.SHA256+"\x00")
		contents, err := os.ReadFile(filepath.Join(root, "playbooks", filepath.FromSlash(item.Path)))
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(contents)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ParseCatalog(contents []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode Catalog: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Catalog{}, err
	}
	if catalog.FormatVersion != CatalogFormatVersion {
		return Catalog{}, fmt.Errorf("unsupported Catalog format %q", catalog.FormatVersion)
	}
	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON document contains trailing data")
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}

func validateCatalog(catalog Catalog) error {
	allowed := map[string]tableSpec{}
	for _, spec := range catalogTables {
		allowed[spec.name] = spec
	}
	seen := map[string]bool{}
	for _, table := range catalog.Tables {
		spec, ok := allowed[table.Name]
		if !ok || seen[table.Name] {
			return fmt.Errorf("unsupported or duplicate Catalog table %q", table.Name)
		}
		seen[table.Name] = true
		if strings.Join(table.Columns, "\x00") != strings.Join(spec.columns, "\x00") {
			return fmt.Errorf("Catalog table %s columns do not match the current contract", table.Name)
		}
		for _, row := range table.Rows {
			if len(row) != len(table.Columns) {
				return fmt.Errorf("Catalog table %s contains a malformed row", table.Name)
			}
			for _, cell := range row {
				if cell.Kind != "null" && cell.Kind != "integer" && cell.Kind != "text" {
					return fmt.Errorf("Catalog table %s contains unsupported cell kind %q", table.Name, cell.Kind)
				}
			}
		}
	}
	for name := range allowed {
		if !seen[name] {
			return fmt.Errorf("Catalog is missing table %s", name)
		}
	}
	paths := map[string]bool{}
	for _, playbook := range catalog.Playbooks {
		clean := filepath.ToSlash(filepath.Clean(playbook.Path))
		if clean != playbook.Path || clean == "." || strings.HasPrefix(clean, "../") || playbook.SHA256 == "" || paths[clean] {
			return fmt.Errorf("Catalog contains invalid Playbook entry %q", playbook.Path)
		}
		paths[clean] = true
	}
	return nil
}
