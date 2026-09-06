package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

func acceptanceManifestDigest(files []Playbook, prefix string) string {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	hash := sha256.New()
	for _, file := range files {
		hash.Write([]byte(strings.TrimPrefix(file.Path, prefix)))
		hash.Write([]byte{0})
		hash.Write([]byte(file.SHA256))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func exportScenarioAcceptancePlaybooks(ctx context.Context, db *sql.DB, root, destination string) ([]Playbook, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,scenario_id,lifecycle_json FROM scenario_revisions WHERE status IN ('released','deprecated') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var revisions []domain.ScenarioRevision
	for rows.Next() {
		var id, scenarioID, raw string
		if err = rows.Scan(&id, &scenarioID, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var revision domain.ScenarioRevision
		if err = json.Unmarshal([]byte(raw), &revision); err != nil {
			rows.Close()
			return nil, err
		}
		revision.ID, revision.ScenarioID = id, scenarioID
		revisions = append(revisions, revision)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return nil, err
	}
	var exported []Playbook
	for _, revision := range revisions {
		prefix := revision.AcceptanceWorkspaceRoot
		if prefix == "" && len(revision.AcceptanceJobs) == 0 {
			continue
		}
		expected := "managed-scenarios/" + revision.ScenarioID + "/" + revision.ID + "/"
		if prefix != expected || revision.AcceptanceTreeSHA256 == "" {
			return nil, fmt.Errorf("scenario %s acceptance workspace identity is invalid", revision.ID)
		}
		current := root
		for _, segment := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
			if segment == "" || segment == "." || segment == ".." {
				return nil, fmt.Errorf("invalid scenario workspace path")
			}
			current = filepath.Join(current, segment)
			info, e := os.Lstat(current)
			if e != nil {
				return nil, e
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil, fmt.Errorf("scenario workspace contains unsafe directory")
			}
		}
		_, directory, e := resolveRelative(root, strings.TrimSuffix(prefix, "/"))
		if e != nil {
			return nil, e
		}
		files := []Playbook{}
		err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("scenario workspace symlinks are not allowed")
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("scenario workspace contains non-regular files")
			}
			relative, e := filepath.Rel(directory, path)
			if e != nil {
				return e
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(data)
			file := Playbook{Path: prefix + filepath.ToSlash(relative), SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))}
			files = append(files, file)
			target := filepath.Join(destination, "playbooks", filepath.FromSlash(file.Path))
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return e
			}
			return os.WriteFile(target, data, 0600)
		})
		if err != nil {
			return nil, err
		}
		if acceptanceManifestDigest(files, prefix) != revision.AcceptanceTreeSHA256 {
			return nil, fmt.Errorf("scenario %s acceptance workspace changed", revision.ID)
		}
		if err = validateAcceptanceJobsAgainstFiles(revision, files); err != nil {
			return nil, err
		}
		exported = append(exported, files...)
	}
	return exported, nil
}

func validateAcceptanceJobsAgainstFiles(revision domain.ScenarioRevision, files []Playbook) error {
	byPath := map[string]string{}
	for _, file := range files {
		byPath[file.Path] = file.SHA256
	}
	for _, job := range revision.AcceptanceJobs {
		if job.Playbook != revision.AcceptanceWorkspaceRoot+"tasks/acceptance/"+job.ID+".yml" || job.PlaybookSHA256 == "" || byPath[job.Playbook] != job.PlaybookSHA256 {
			return fmt.Errorf("scenario %s acceptance job %s is missing its locked entrypoint", revision.ID, job.ID)
		}
	}
	return nil
}

func validateScenarioCatalogAcceptance(catalog Catalog) error {
	tables := map[string]TableDump{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	revisions := tables["scenario_revisions"]
	if len(revisions.Rows) == 0 {
		return nil
	}
	byID := map[string]domain.ScenarioRevision{}
	for _, row := range revisions.Rows {
		var revision domain.ScenarioRevision
		raw, _ := row[columnIndex(revisions.Columns, "lifecycle_json")].Value().(string)
		if err := json.Unmarshal([]byte(raw), &revision); err != nil {
			return err
		}
		revision.ID, _ = row[columnIndex(revisions.Columns, "id")].Value().(string)
		revision.Revision = int(row[columnIndex(revisions.Columns, "revision")].Int)
		revision.ScenarioID, _ = row[columnIndex(revisions.Columns, "scenario_id")].Value().(string)
		raw, _ = row[columnIndex(revisions.Columns, "graph_json")].Value().(string)
		if err := json.Unmarshal([]byte(raw), &revision.Graph); err != nil {
			return err
		}
		raw, _ = row[columnIndex(revisions.Columns, "environment_constraints_json")].Value().(string)
		if err := json.Unmarshal([]byte(raw), &revision.EnvironmentConstraints); err != nil {
			return err
		}
		byID[revision.ID] = revision
		if revision.AcceptanceWorkspaceRoot == "" && len(revision.AcceptanceJobs) == 0 {
			continue
		}
		prefix := "managed-scenarios/" + revision.ScenarioID + "/" + revision.ID + "/"
		if revision.AcceptanceWorkspaceRoot != prefix || revision.AcceptanceTreeSHA256 == "" {
			return fmt.Errorf("Catalog scenario acceptance workspace identity is invalid")
		}
		files := []Playbook{}
		for _, file := range catalog.Playbooks {
			if strings.HasPrefix(file.Path, prefix) {
				files = append(files, file)
			}
		}
		if acceptanceManifestDigest(files, prefix) != revision.AcceptanceTreeSHA256 {
			return fmt.Errorf("Catalog scenario %s acceptance tree digest mismatch", revision.ID)
		}
		if err := validateAcceptanceJobsAgainstFiles(revision, files); err != nil {
			return err
		}
	}
	for _, revision := range byID {
		if (revision.SourceRevisionID == "") != (revision.SourceRunID == "") {
			return fmt.Errorf("Catalog scenario upgrade source requires both revision and formal Run references")
		}
		if revision.SourceRevisionID == "" {
			if len(revision.UpgradeConstraints) > 0 {
				return fmt.Errorf("Catalog scenario without upgrade source has upgrade constraints")
			}
			continue
		}
		source, found := byID[revision.SourceRevisionID]
		if !found || source.ScenarioID != revision.ScenarioID || source.ID == revision.ID || source.Revision >= revision.Revision {
			return fmt.Errorf("Catalog scenario upgrade source must be an older revision of the same scenario")
		}
	}
	scenarios := tables["scenarios"]
	for _, row := range scenarios.Rows {
		sourceID, _ := row[columnIndex(scenarios.Columns, "forked_from_revision_id")].Value().(string)
		if sourceID == "" {
			continue
		}
		sourceScenarioID, _ := row[columnIndex(scenarios.Columns, "forked_from_scenario_id")].Value().(string)
		digest, _ := row[columnIndex(scenarios.Columns, "forked_from_digest")].Value().(string)
		source, found := byID[sourceID]
		if !found || source.ScenarioID != sourceScenarioID || domain.ScenarioRevisionSpecDigest(source) != digest {
			return fmt.Errorf("Catalog scenario branch source is missing or has a different digest")
		}
	}
	return nil
}
