package ansible

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResetTaskTargetsCoverDelegatesBlocksIncludesAndHandlers(t *testing.T) {
	scope := &TaskTargetScope{Hosts: []string{"control", "worker"}, Groups: map[string][]string{"control_plane": {"control"}, "mixed": {"control", "file_station"}}}
	for _, tc := range []struct {
		name, body, handler, included string
		bad                           bool
	}{
		{"direct allowed", "- debug: {msg: ok}\n  delegate_to: control\n", "", "", false},
		{"group allowed", "- debug: {msg: ok}\n  delegate_to: \"{{ groups['control_plane'][0] }}\"\n", "", "", false},
		{"self", "- debug: {msg: ok}\n  delegate_to: '{{ inventory_hostname }}'\n", "", "", false},
		{"shared host", "- debug: {msg: no}\n  delegate_to: file_station\n", "", "", true},
		{"mixed group", "- debug: {msg: no}\n  delegate_to: \"{{ groups['mixed'][0] }}\"\n", "", "", true},
		{"dynamic", "- debug: {msg: no}\n  delegate_to: '{{ cf.inputs.target }}'\n", "", "", true},
		{"connection", "- debug: {msg: no}\n  connection: local\n", "", "", true},
		{"block", "- block:\n    - debug: {msg: no}\n  delegate_to: file_station\n", "", "", true},
		{"include", "- import_tasks: nested.txt\n", "", "- debug: {msg: no}\n  delegate_to: file_station\n", true},
		{"legacy include", "- ansible.legacy.import_tasks: nested.txt\n", "", "- debug: {msg: no}\n  delegate_to: file_station\n", true},
		{"apply", "- include_tasks:\n    file: nested.txt\n    apply:\n      delegate_to: file_station\n", "", "- debug: {msg: ok}\n", true},
		{"args apply", "- include_tasks: nested.txt\n  args:\n    apply:\n      delegate_to: file_station\n", "", "- debug: {msg: ok}\n", true},
		{"handler", "- debug: {msg: ok}\n", "- name: cleanup\n  debug: {msg: no}\n  delegate_to: file_station\n", "", true},
		{"handler include", "- debug: {msg: ok}\n", "- name: cleanup\n  import_tasks: nested.txt\n", "- debug: {msg: no}\n  delegate_to: file_station\n", true},
		{"module data", "- debug:\n    msg:\n      delegate_to: arbitrary_data\n      connection: example\n", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, data := range map[string]string{"tasks/rollback.yml": tc.body, "handlers/main.yml": tc.handler, "tasks/nested.txt": tc.included} {
				if data == "" {
					continue
				}
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := validateRoleTaskTargets(root, "rollback.yml", scope); (err != nil) != tc.bad {
				t.Fatalf("unexpected target validation: %v", err)
			}
			if err := validateRoleTaskTargets(root, "rollback.yml", nil); err != nil {
				t.Fatalf("ordinary jobs changed: %v", err)
			}
		})
	}
}

func TestResetTaskTargetsRealAnsibleRejectsSharedDelegateBeforeMutation(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires isolated real Ansible")
	}
	for _, delegate := range []string{"file_station", "a", "{{ groups['first'][0] }}"} {
		t.Run(delegate, func(t *testing.T) {
			runner, plan, target := roleJobFixture(t, binary, "")
			plan.Steps = plan.Steps[:3]
			plan.Inventory += "\n[shared]\nfile_station ansible_connection=local\n"
			plan.TaskTargetScope = &TaskTargetScope{Hosts: []string{"a", "b"}, Groups: map[string][]string{"first": {"a", "b"}, "shared": {"file_station"}}}
			source := filepath.Join(runner.AllowedRoot, "managed/alpha/line/release/tasks/install.yml")
			contents, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(target, "delegated-marker")
			task := "- copy:\n    dest: '" + marker + "'\n    content: changed\n  delegate_to: " + strconv.Quote(delegate) + "\n"
			if err := os.WriteFile(source, append([]byte(task), contents...), 0600); err != nil {
				t.Fatal(err)
			}
			paths := []string{}
			for i := range plan.Steps {
				step := &plan.Steps[i]
				step.ParentActionID, step.TimeoutSeconds = "install.yml", 30
				if step.Phase == "execute" {
					step.Action = "rollback"
				}
				step.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, step.Playbook))
				step.WorkspaceDigest, _ = TreeDigest(filepath.Join(runner.AllowedRoot, "managed/alpha/line/release"))
				paths = append(paths, step.Playbook)
			}
			previewErr := runner.ValidateTaskTargets(paths, *plan.TaskTargetScope)
			result, err := runner.RunJob(context.Background(), JobRequest{Plan: plan})
			if delegate == "file_station" {
				if previewErr == nil || err == nil || !strings.Contains(err.Error(), "delegate_to") {
					t.Fatalf("unsafe delegate accepted: preview=%v execution=%v", previewErr, err)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("shared host mutated: %v", err)
				}
				if len(result.Steps) != 0 {
					t.Fatal("job dispatched before target rejection")
				}
			} else {
				if previewErr != nil || err != nil {
					t.Fatalf("allowed delegate failed: preview=%v execution=%v\n%s", previewErr, err, joinedLogs(result.Logs))
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestResetTaskTargetsRealAnsibleRejectsApplyConnectionBeforeMutation(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires isolated real Ansible")
	}
	for _, form := range []string{"include", "args", "handler"} {
		t.Run(form, func(t *testing.T) {
			runner, plan, target := roleJobFixture(t, binary, "")
			plan.Steps = plan.Steps[:3]
			plan.TaskTargetScope = &TaskTargetScope{Hosts: []string{"a", "b"}}
			role := filepath.Join(runner.AllowedRoot, "managed/alpha/line/release")
			marker := filepath.Join(target, "controller-marker")
			nested := "- copy:\n    dest: '" + marker + "'\n    content: changed\n"
			source := "- include_tasks:\n    file: nested.yml\n    apply:\n      vars:\n        ansible_connection: local\n"
			if form == "args" {
				source = "- ansible.builtin.include_tasks: nested.yml\n  args:\n    apply:\n      vars:\n        ansible_connection: local\n"
			}
			entry := "tasks/install.yml"
			if form == "handler" {
				entry = "handlers/main.yml"
				source = "- name: finish\n  include_tasks:\n    file: nested.yml\n    apply:\n      vars:\n        ansible_connection: local\n"
			}
			for name, content := range map[string]string{entry: source, "tasks/nested.yml": nested} {
				if err := os.WriteFile(filepath.Join(role, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			paths := []string{}
			for i := range plan.Steps {
				step := &plan.Steps[i]
				step.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, step.Playbook))
				step.WorkspaceDigest, _ = TreeDigest(role)
				paths = append(paths, step.Playbook)
			}
			if err := runner.ValidateTaskTargets(paths, *plan.TaskTargetScope); err == nil || !strings.Contains(err.Error(), "reserved variable") {
				t.Fatalf("preview accepted apply override: %v", err)
			}
			result, err := runner.RunJob(context.Background(), JobRequest{Plan: plan})
			if err == nil || !strings.Contains(err.Error(), "reserved variable") || len(result.Steps) != 0 {
				t.Fatalf("job dispatched: %v %+v", err, result)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("controller mutated: %v", err)
			}
		})
	}
}
