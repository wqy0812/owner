package ansible

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two independent component workspaces intentionally operate on the same target
// file. Their YAML pre/post checks prove that the requested order is respected.
func TestRoleJobRealAnsibleSequentialSharedFile(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real isolated Ansible")
	}
	for _, mode := range []string{"platform", "standalone"} {
		t.Run(mode, func(t *testing.T) {
			root, target := t.TempDir(), filepath.Join(t.TempDir(), "shared.conf")
			plan := JobPlan{Inventory: "[targets]\nlocal ansible_connection=local\n"}
			for _, node := range []string{"first", "second"} {
				role := filepath.Join(root, "managed", node, "line", "release")
				pre := "- stat:\n    path: '{{ cf.inputs.path }}'\n  register: cf_local_file\n- assert:\n    that: not cf_local_file.stat.exists\n"
				if node == "second" {
					pre = "- slurp:\n    src: '{{ cf.inputs.path }}'\n  register: cf_local_file\n- assert:\n    that: (cf_local_file.content | b64decode) == 'first'\n"
				}
				for name, content := range map[string]string{
					"checks/pre.yml":  pre,
					"install.yml":     "- copy:\n    dest: '{{ cf.inputs.path }}'\n    content: '{{ cf.context.nodeId }}'\n    mode: '0600'\n",
					"checks/post.yml": "- slurp:\n    src: '{{ cf.inputs.path }}'\n  register: cf_local_file\n- assert:\n    that: (cf_local_file.content | b64decode) == cf.context.nodeId\n",
				} {
					path := filepath.Join(role, "tasks", name)
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				tree, err := TreeDigest(role)
				if err != nil {
					t.Fatal(err)
				}
				for i, entry := range []string{"checks/pre.yml", "install.yml", "checks/post.yml"} {
					digest, err := FileDigest(filepath.Join(role, "tasks", entry))
					if err != nil {
						t.Fatal(err)
					}
					phase, action := []string{"pre", "execute", "post"}[i], "check"
					if i == 1 {
						action = "install"
					}
					plan.Steps = append(plan.Steps, JobStep{ID: node + "-" + phase, NodeID: node, ComponentID: node, ReleaseID: node + "-release", ActionID: entry, ParentActionID: "install.yml", Name: node + "/" + phase, Action: action, Phase: phase, Playbook: "managed/" + node + "/line/release/tasks/" + entry, PlaybookDigest: digest, WorkspaceDigest: tree, Limit: "targets", TimeoutSeconds: 20, Variables: map[string]any{"path": target}})
				}
			}
			runner := &Runner{AllowedRoot: root, Binary: binary}
			if mode == "platform" {
				result, err := runner.RunJob(context.Background(), JobRequest{Plan: plan})
				if err != nil {
					t.Fatalf("%v\n%s", err, joinedLogs(result.Logs))
				}
				if !result.Successful || len(result.Steps) != 6 {
					t.Fatalf("incomplete ordered execution: %+v", result.Steps)
				}
			} else {
				bundle, err := runner.BuildJob(context.Background(), plan)
				if err != nil {
					t.Fatal(err)
				}
				defer bundle.Close()
				encoded, err := json.Marshal(bundle.Manifest)
				if err != nil {
					t.Fatal(err)
				}
				for _, removed := range []string{"resourceContract", "resourcePolicyVersion", "existingResources", "cf_resources.py"} {
					if strings.Contains(string(encoded), removed) {
						t.Fatalf("bundle still contains %s", removed)
					}
				}
				output, err := nativeCommand(t, binary, bundle, "install", filepath.Join(t.TempDir(), "results"), nil)
				if err != nil {
					t.Fatalf("standalone: %v\n%s", err, output)
				}
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "second" {
				t.Fatalf("shared file: %q, %v", data, err)
			}
		})
	}
}
