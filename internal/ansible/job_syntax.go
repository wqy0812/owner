package ansible

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func roleEntrypoint(entry string) string {
	if !strings.Contains(entry, "/") {
		return entry
	}
	return fmt.Sprintf("cf_generated_%x.yml", sha256.Sum256([]byte(entry)))
}

// Ansible 2.8 strips directories from tasks_from. A sealed, platform-owned
// import entry preserves the original source bytes and relative include paths.
func writeRoleEntrypoints(directory string, plan JobPlan) (map[string]string, error) {
	entries := map[string]string{}
	err := filepath.WalkDir(filepath.Join(directory, "roles"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(directory, "roles"), path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.IsDir() || len(parts) < 4 || parts[1] != "tasks" || (!strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml")) {
			return nil
		}
		entry := strings.Join(parts[2:], "/")
		generated := filepath.ToSlash(filepath.Join("roles", parts[0], "tasks", roleEntrypoint(entry)))
		entries[generated] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	for generated, source := range entries {
		file, err := os.OpenFile(filepath.Join(directory, generated), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("generated role entry collides with source: %w", err)
		}
		data, _ := json.Marshal([]any{map[string]any{"import_tasks": source}})
		_, err = file.Write(data)
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return entries, nil
}

// Dynamic include_role keeps defaults private in Ansible 2.8, but its contents
// are not parsed by --syntax-check. Statically load every managed task entry in
// this separate, sealed preflight. It must never be used for execution.
func jobSyntaxPlaybook(directory string, plan JobPlan) ([]byte, error) {
	plays := []map[string]any{}
	seen := map[string]bool{}
	for _, step := range append(append([]JobStep(nil), plan.Steps...), plan.Recovery...) {
		if seen[step.Role] {
			continue
		}
		seen[step.Role] = true
		err := filepath.WalkDir(filepath.Join(directory, "roles", step.Role, "tasks"), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || (!strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml")) {
				return nil
			}
			entry, err := filepath.Rel(filepath.Join(directory, "roles", step.Role, "tasks"), path)
			if err != nil {
				return err
			}
			plays = append(plays, map[string]any{"name": "Syntax only: " + step.Role + "/" + filepath.ToSlash(entry), "hosts": "__clusterforge_controller", "gather_facts": false, "pre_tasks": []any{map[string]any{"fail": map[string]any{"msg": "syntax.yml is only for --syntax-check; execute site.yml"}}}, "vars": map[string]any{"cf": map[string]any{"inputs": step.Variables, "context": map[string]any{"nodeId": step.NodeID}, "credentials": "{{ cf_credentials }}"}}, "tasks": []any{map[string]any{"import_role": map[string]any{"name": step.Role, "tasks_from": roleEntrypoint(filepath.ToSlash(entry))}}}})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return json.MarshalIndent(plays, "", "  ")
}
