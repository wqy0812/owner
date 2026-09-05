package ansible

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const JobContract = "clusterforge-role-job-v1"

//go:embed job_plugins
var jobPlugins embed.FS

type JobRuntime struct {
	AnsibleCore string `json:"ansibleCore"`
	Python      string `json:"python"`
}

type JobStep struct {
	Stage              string         `json:"stage,omitempty"`
	SourceType         string         `json:"sourceType,omitempty"`
	ScenarioRevisionID string         `json:"scenarioRevisionId,omitempty"`
	RecoveryOfStepID   string         `json:"recoveryOfStepId,omitempty"`
	ID                 string         `json:"id"`
	NodeID             string         `json:"nodeId"`
	Name               string         `json:"name"`
	ReleaseID          string         `json:"releaseId"`
	ComponentID        string         `json:"componentId"`
	ParentActionID     string         `json:"parentActionId"`
	ActionID           string         `json:"actionId"`
	Action             string         `json:"action"`
	Phase              string         `json:"phase"`
	Playbook           string         `json:"source"`
	PlaybookDigest     string         `json:"sourceDigest"`
	WorkspaceDigest    string         `json:"workspaceDigest"`
	Role               string         `json:"role"`
	TasksFrom          string         `json:"tasksFrom"`
	Limit              string         `json:"limit"`
	Variables          map[string]any `json:"variables"`
	TimeoutSeconds     int            `json:"timeoutSeconds"`
	Become             bool           `json:"become"`
	GatherFacts        bool           `json:"gatherFacts"`
	RetrySafe          bool           `json:"retrySafe"`
}

type JobPlan struct {
	Recovery            []JobStep      `json:"recovery"`
	Contract            string         `json:"contract"`
	EnvironmentID       string         `json:"environmentId"`
	Runtime             JobRuntime     `json:"runtime"`
	Steps               []JobStep      `json:"steps"`
	Inventory           string         `json:"inventory"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	RequiredCredentials []string       `json:"requiredCredentials"`
}

type JobManifest struct {
	Contract string            `json:"contract"`
	Plan     JobPlan           `json:"plan"`
	Files    map[string]string `json:"files"`
	Digest   string            `json:"digest"`
}

type JobBundle struct {
	Path     string
	Manifest JobManifest
}

func (b *JobBundle) Close() error {
	if b == nil || b.Path == "" {
		return nil
	}
	err := os.RemoveAll(b.Path)
	b.Path = ""
	return err
}

func jsonDigest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (r *Runner) RuntimeIdentity(ctx context.Context) (JobRuntime, error) {
	binary := r.Binary
	if binary == "" {
		binary = "ansible-playbook"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return JobRuntime{}, fmt.Errorf("Ansible runtime unavailable: %w", err)
	}
	core := regexp.MustCompile(`\[core ([0-9.]+)\]`).FindStringSubmatch(string(out))
	python := regexp.MustCompile(`python version = ([0-9.]+)`).FindStringSubmatch(string(out))
	if len(core) != 2 || len(python) != 2 {
		return JobRuntime{}, fmt.Errorf("Ansible runtime must report ansible-core and Python versions")
	}
	return JobRuntime{AnsibleCore: core[1], Python: python[1]}, nil
}

// BuildJob copies exact release trees and compiles one deterministic playbook.
func (r *Runner) BuildJob(ctx context.Context, plan JobPlan) (*JobBundle, error) {
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("job requires at least one step")
	}
	runtime, err := r.RuntimeIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if plan.Runtime.AnsibleCore != "" && plan.Runtime != runtime {
		return nil, fmt.Errorf("runtime changed after plan was locked")
	}
	plan.Contract, plan.Runtime = JobContract, runtime
	directory, err := r.createWorkspace()
	if err != nil {
		return nil, err
	}
	bundle := &JobBundle{Path: directory}
	fail := func(err error) (*JobBundle, error) { _ = bundle.Close(); return nil, err }
	seen := map[string]string{}
	ids := map[string]bool{}
	allSteps := make([]*JobStep, 0, len(plan.Steps)+len(plan.Recovery))
	for i := range plan.Steps {
		allSteps = append(allSteps, &plan.Steps[i])
	}
	for i := range plan.Recovery {
		allSteps = append(allSteps, &plan.Recovery[i])
	}
	for index, step := range allSteps {
		if index == len(plan.Steps) {
			ids = map[string]bool{}
		}
		if step.ID == "" || ids[step.ID] {
			return fail(fmt.Errorf("job step IDs must be nonempty and unique"))
		}
		ids[step.ID] = true
		root, source, clean, err := r.resolvePlaybook(step.Playbook)
		if err != nil {
			return fail(err)
		}
		roleRoot := workspaceRoot(root, source, clean)
		if step.Role != "" {
			roleRoot = filepath.Join(root, "roles", step.Role)
		}
		rel, err := filepath.Rel(roleRoot, source)
		if err != nil || !strings.HasPrefix(filepath.ToSlash(rel), "tasks/") {
			return fail(fmt.Errorf("action source must be a managed role task entry: %s", step.Playbook))
		}
		digest, err := FileDigest(source)
		if err != nil {
			return fail(err)
		}
		if step.PlaybookDigest == "" || digest != step.PlaybookDigest {
			return fail(fmt.Errorf("action source digest mismatch: %s", step.ID))
		}
		identity := step.ReleaseID
		if step.SourceType == "scenario_acceptance" {
			if step.ScenarioRevisionID == "" || step.ReleaseID != "" || step.ComponentID != "" {
				return fail(fmt.Errorf("invalid scenario acceptance identity"))
			}
			identity = "scenario:" + step.ScenarioRevisionID
		}
		step.Role = RoleName(identity)
		step.TasksFrom = strings.TrimPrefix(filepath.ToSlash(rel), "tasks/")
		target := filepath.Join(directory, "roles", step.Role)
		if old, ok := seen[step.Role]; ok {
			if old != step.WorkspaceDigest {
				return fail(fmt.Errorf("conflicting release tree digests"))
			}
		} else {
			if err := copyRegularTree(roleRoot, target); err != nil {
				return fail(err)
			}
			actual, err := TreeDigest(target)
			if err != nil {
				return fail(err)
			}
			if step.WorkspaceDigest == "" || actual != step.WorkspaceDigest {
				return fail(fmt.Errorf("release tree digest mismatch: %s", step.ReleaseID))
			}
			if err := validateRoleTree(target); err != nil {
				return fail(err)
			}
			seen[step.Role] = actual
		}
		if err := validateRoleEntry(target, step.TasksFrom, step.Action == "check", map[string]bool{}); err != nil {
			return fail(fmt.Errorf("%s: %w", step.Name, err))
		}

		if step.TimeoutSeconds <= 0 {
			return fail(fmt.Errorf("step timeout must be positive"))
		}
		if step.Limit == "" || strings.Contains(step.Limit, "{{") || strings.Contains(step.Limit, "__clusterforge_controller") {
			return fail(fmt.Errorf("invalid target scope for %s", step.ID))
		}
	}
	if strings.Contains(plan.Inventory, "__clusterforge_controller") {
		return fail(fmt.Errorf("inventory uses a reserved controller name"))
	}
	if err := writeJobFiles(directory, plan); err != nil {
		return fail(err)
	}
	manifest := JobManifest{Contract: JobContract, Plan: plan, Files: map[string]string{}}
	if err := filepath.WalkDir(directory, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(directory, path)
		digest, e := FileDigest(path)
		manifest.Files[filepath.ToSlash(rel)] = digest
		return e
	}); err != nil {
		return fail(err)
	}
	manifest.Digest = jsonDigest(manifest)
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writePrivateFile(filepath.Join(directory, "manifest.json"), data); err != nil {
		return fail(err)
	}
	bundle.Manifest = manifest
	return bundle, nil
}

func validateRoleTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "meta/") || strings.Contains(rel, "_plugins/") || strings.HasPrefix(rel, "roles/") || strings.HasPrefix(rel, "library/") {
			return fmt.Errorf("role cannot carry implicit dependencies or execution plugins: %s", rel)
		}
		if strings.HasPrefix(rel, "vars/") || strings.HasPrefix(rel, "defaults/") {
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			return validateRoleVariables(data)
		}
		if (strings.HasPrefix(rel, "tasks/") || strings.HasPrefix(rel, "handlers/")) && (strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")) {
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			return ValidateRoleTasks(data, strings.HasPrefix(rel, "tasks/checks/"))
		}
		return nil
	})
}

func writeJobFiles(directory string, plan JobPlan) error {
	plays := []map[string]any{}
	localVars, err := roleLocalVariables(directory)
	if err != nil {
		return err
	}
	gate := func(step JobStep, kind string) map[string]any {
		return map[string]any{"name": "Controller " + kind + " " + step.ID, "hosts": "__clusterforge_controller", "gather_facts": false, "any_errors_fatal": true, "strategy": "linear", "tasks": []any{map[string]any{"name": "Commit stage boundary", "cf_gate": map[string]any{"kind": kind, "step_id": step.ID, "watchdog": kind == "begin" && step.ID == plan.Steps[0].ID}}}}
	}
	for _, step := range plan.Steps {
		plays = append(plays, gate(step, "begin"))
		contextVars := map[string]any{"environmentId": plan.EnvironmentID, "releaseId": step.ReleaseID, "componentId": step.ComponentID, "backupRef": step.Variables["clusterforge_backup_ref"], "stepId": step.ID, "nodeId": step.NodeID, "actionId": step.ActionID, "parentActionId": step.ParentActionID, "phase": step.Phase}
		vars := map[string]any{"cf_step_id": step.ID, "cf": map[string]any{"inputs": step.Variables, "context": contextVars, "credentials": "{{ cf_credentials }}"}}
		scopeTasks := []any{map[string]any{"name": "Clear phase facts", "ansible.builtin.meta": "clear_facts"}}
		if len(localVars) > 0 {
			scopeTasks = append(scopeTasks, map[string]any{"name": "Reset phase local variables", "ansible.builtin.set_fact": localVars})
		}
		// Automatic gathering runs before pre_tasks and would be erased by
		// clear_facts. Gather explicitly after removing the prior phase's state.
		if step.GatherFacts {
			scopeTasks = append(scopeTasks, map[string]any{"name": "Gather phase facts", "ansible.builtin.setup": map[string]any{}})
		}
		plays = append(plays, map[string]any{"name": step.Name, "hosts": step.Limit + ":!__clusterforge_controller", "gather_facts": false, "become": step.Become, "any_errors_fatal": true, "strategy": "linear", "vars": vars, "pre_tasks": scopeTasks, "tasks": []any{map[string]any{"name": "Execute " + step.Name, "ansible.builtin.import_role": map[string]any{"name": step.Role, "tasks_from": step.TasksFrom, "public": false, "allow_duplicates": true}}}})
		plays = append(plays, gate(step, "end"))
	}
	playbook, _ := json.MarshalIndent(plays, "", "  ")
	files := map[string][]byte{"site.yml": playbook, "inventory.ini": []byte(plan.Inventory + "\n[clusterforge_controller]\n__clusterforge_controller ansible_connection=local\n"), "ansible.cfg": []byte("[defaults]\nretry_files_enabled = False\nstdout_callback = default\ncallbacks_enabled = cf_events\ncallback_plugins = ./callback_plugins\naction_plugins = ./action_plugins\nroles_path = ./roles\nhost_key_checking = True\n[ssh_connection]\npipelining = True\n")}
	for path, data := range files {
		if err := writePrivateFile(filepath.Join(directory, path), data); err != nil {
			return err
		}
	}
	return fs.WalkDir(jobPlugins, "job_plugins", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := jobPlugins.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(directory, strings.TrimPrefix(path, "job_plugins/"))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return writePrivateFile(target, data)
	})
}

// OpenJob validates every source file, including generated plugins and config.
func OpenJob(directory string) (*JobBundle, error) {
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest JobManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	b := &JobBundle{Path: directory, Manifest: manifest}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}
func (b *JobBundle) Validate() error {
	m := b.Manifest
	expected := m.Digest
	m.Digest = ""
	if m.Contract != JobContract || expected == "" || jsonDigest(m) != expected {
		return fmt.Errorf("job manifest digest mismatch")
	}
	for relative, expected := range m.Files {
		clean := filepath.Clean(relative)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe job member")
		}
		path := filepath.Join(b.Path, clean)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("job member must be a regular file")
		}
		actual, err := FileDigest(path)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("job file changed: %s", relative)
		}
	}
	return filepath.WalkDir(b.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("job contains a symlink")
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(b.Path, path)
		if rel == "manifest.json" {
			return nil
		}
		if _, ok := m.Files[filepath.ToSlash(rel)]; !ok {
			return fmt.Errorf("unexpected job file: %s", rel)
		}
		return nil
	})
}
func newJobToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func RoleName(releaseID string) string {
	readable := regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(releaseID, "_")
	if len(readable) > 120 {
		readable = readable[:120]
	}
	return "cf_" + readable + "_" + jsonDigest(releaseID)[:16]
}
