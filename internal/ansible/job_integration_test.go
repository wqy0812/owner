package ansible

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoleJobRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("set CLUSTERFORGE_JOB_TEST_ANSIBLE to run the mandatory role-job integration gate")
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, body string
		fail       bool
	}{{"success", `- name: Install marker
  copy:
    dest: "{{ cf.inputs.target }}/installed"
    content: installed
  notify: finish installation
`, false}, {"failure", `- name: Fail install
  fail:
    msg: deliberate install failure
`, true}} {
		t.Run(test.name, func(t *testing.T) {
			root, target := t.TempDir(), t.TempDir()
			role := filepath.Join(root, "managed", "component", "line", "release")
			for path, content := range map[string]string{
				"tasks/checks/before.yml": "- assert:\n    that: cf.inputs.target | length > 0\n",
				"tasks/install.yml":       test.body,
				"tasks/checks/after.yml":  "- stat:\n    path: '{{ cf.inputs.target }}/handler'\n  register: cf_local\n- assert:\n    that: cf_local.stat.exists\n",
				"handlers/main.yml":       "- name: finish installation\n  copy:\n    dest: '{{ cf.inputs.target }}/handler'\n    content: done\n",
			} {
				path = filepath.Join(role, path)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &Runner{AllowedRoot: root, Binary: binary}
			plan := JobPlan{Inventory: "[targets]\ntarget_a ansible_connection=local\ntarget_b ansible_connection=local\n"}
			tree, err := TreeDigest(role)
			if err != nil {
				t.Fatal(err)
			}
			for i, entry := range []string{"checks/before.yml", "install.yml", "checks/after.yml"} {
				path := filepath.Join(role, "tasks", entry)
				digest, _ := FileDigest(path)
				relative, _ := filepath.Rel(root, path)
				action := "check"
				phase := []string{"pre", "execute", "post"}[i]
				if i == 1 {
					action = "install"
				}
				plan.Steps = append(plan.Steps, JobStep{ID: fmt.Sprint(i), ReleaseID: "release", NodeID: "component", Name: phase, Action: action, Phase: phase, Playbook: relative, PlaybookDigest: digest, WorkspaceDigest: tree, Limit: "targets", TimeoutSeconds: 20, Variables: map[string]any{"target": target}})
			}
			boundaries := []string{}
			result, err := runner.RunJob(context.Background(), JobRequest{Plan: plan, OnBoundary: func(_ context.Context, kind string, step JobStep, _ JobStepResult) error {
				boundaries = append(boundaries, kind+":"+step.Phase)
				return nil
			}})
			if test.fail {
				if err == nil {
					t.Fatal("failure was swallowed")
				}
				for _, b := range boundaries {
					if strings.Contains(b, ":post") {
						t.Fatalf("post-check ran after failure: %v", boundaries)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("%v\n%s", err, joinedLogs(result.Logs))
				}
				if len(boundaries) != 6 {
					t.Fatalf("boundaries=%v", boundaries)
				}
				if result.ExitCode == nil || *result.ExitCode != 0 {
					t.Fatalf("exit=%v", result.ExitCode)
				}
			}
			executions := 0
			for _, p := range result.Phases {
				if p.Phase == PhaseExecute {
					executions++
				}
			}
			if executions != 1 {
				t.Fatalf("formal executions=%d\n%s", executions, joinedLogs(result.Logs))
			}
		})
	}
}

func TestRoleJobRealAnsibleFactsAreGatheredAfterPhaseReset(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("set CLUSTERFORGE_JOB_TEST_ANSIBLE for real isolated Ansible acceptance")
	}
	root := t.TempDir()
	role := filepath.Join(root, "managed", "component", "line", "release")
	entries := []string{"with-facts.yml", "without-facts.yml"}
	for i, condition := range []string{"ansible_facts['system'] is defined", "ansible_facts | length == 0"} {
		path := filepath.Join(role, "tasks", entries[i])
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("- assert:\n    that: \""+condition+"\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := TreeDigest(role)
	if err != nil {
		t.Fatal(err)
	}
	plan := JobPlan{Inventory: "[targets]\ntarget ansible_connection=local\n"}
	for i, entry := range entries {
		digest, err := FileDigest(filepath.Join(role, "tasks", entry))
		if err != nil {
			t.Fatal(err)
		}
		plan.Steps = append(plan.Steps, JobStep{ID: entry, ReleaseID: "release", Name: entry, Action: "check", Phase: "check", Playbook: "managed/component/line/release/tasks/" + entry, PlaybookDigest: digest, WorkspaceDigest: tree, Limit: "targets", TimeoutSeconds: 20, GatherFacts: i == 0})
	}
	runner := &Runner{AllowedRoot: root, Binary: binary}
	result, err := runner.RunJob(context.Background(), JobRequest{Plan: plan})
	if err != nil {
		t.Fatalf("%v\n%s", err, joinedLogs(result.Logs))
	}
	if len(result.Steps) != 2 || !result.Successful {
		t.Fatalf("incomplete facts verification: %+v", result.Steps)
	}
}
