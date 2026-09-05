package ansible

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoleTaskControlPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		check, bad   bool
	}{
		{"ordinary module data", "- ansible.builtin.debug:\n    msg:\n      hosts: [a]\n      action: install\n", false, false},
		{"old play", "- hosts: all\n  tasks: []", false, true},
		{"ignore", "- command: false\n  ignore_errors: true", false, true},
		{"swallow", "- block:\n  - command: false\n  rescue:\n  - debug: msg=hidden", false, true},
		{"reserved variable", "- set_fact:\n    cf: {}", false, true},
		{"cross role", "- include_role:\n    name: upstream", false, true},
		{"check mutates", "- copy:\n    content: x\n    dest: /tmp/x", true, true},
		{"check probe", "- command: test -f /tmp/x\n  changed_when: false", true, false},
		{"check probe missing declaration", "- command: test -f /tmp/x", true, true},
		{"read check", "- assert:\n    that: cf.inputs.version is defined", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRoleTasks([]byte(tc.source), tc.check); (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
func compileUnitJob(t *testing.T) *JobBundle {
	t.Helper()
	root := t.TempDir()
	role := filepath.Join(root, "managed/c/line/r")
	for path, content := range map[string]string{"tasks/install.yml": "- debug: msg=install\n  register: cf_local_install\n", "tasks/rollback.yml": "- debug: msg=rollback\n", "tasks/checks/ready.yml": "- assert:\n    that: true\n"} {
		path = filepath.Join(role, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(root, "version-only")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" != '--version' ]; then exit 99; fi\nprintf 'ansible-playbook [core 2.19.12]\\n  python version = 3.12.9\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tree, err := TreeDigest(role)
	if err != nil {
		t.Fatal(err)
	}
	stage := func(id, action, path string) JobStep {
		digest, err := FileDigest(filepath.Join(role, "tasks", path))
		if err != nil {
			t.Fatal(err)
		}
		return JobStep{ID: id, NodeID: id, ReleaseID: "release", ComponentID: "component", Name: id, Action: action, Phase: "execute", Playbook: "managed/c/line/r/tasks/" + path, PlaybookDigest: digest, WorkspaceDigest: tree, Limit: "nodes", TimeoutSeconds: 10, Variables: map[string]any{"version": id}}
	}
	runner := &Runner{AllowedRoot: root, Binary: binary}
	plan := JobPlan{Inventory: "[nodes]\nnode ansible_connection=local", Steps: []JobStep{stage("a", "install", "install.yml"), stage("b", "install", "install.yml")}, Recovery: []JobStep{stage("recover", "rollback", "rollback.yml")}}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	return bundle
}
func TestRoleJobCompileAndSeal(t *testing.T) {
	bundle := compileUnitJob(t)
	roles, err := os.ReadDir(filepath.Join(bundle.Path, "roles"))
	if err != nil || len(roles) != 1 {
		t.Fatalf("roles=%v err=%v", roles, err)
	}
	data, err := os.ReadFile(filepath.Join(bundle.Path, "site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []map[string]any
	if err := json.Unmarshal(data, &plays); err != nil {
		t.Fatal(err)
	}
	if len(plays) != 6 {
		t.Fatalf("plays=%d", len(plays))
	}
	for _, i := range []int{1, 4} {
		if plays[i]["strategy"] != "linear" || plays[i]["any_errors_fatal"] != true {
			t.Fatal("failure policy missing")
		}
		cf := plays[i]["vars"].(map[string]any)["cf"].(map[string]any)
		if cf["inputs"].(map[string]any)["version"] != []string{"a", "b"}[(i-1)/3] {
			t.Fatal("parameters crossed nodes")
		}
	}
	if len(bundle.Manifest.Plan.Recovery) != 1 || bundle.Manifest.Plan.Recovery[0].TasksFrom != "rollback.yml" {
		t.Fatal("recovery source missing")
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle.Path, "site.yml"), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Validate(); err == nil {
		t.Fatal("accepted tampered job")
	}
}
func TestControllerPersistsBeforeAdvance(t *testing.T) {
	for _, kind := range []string{"begin", "end", "event-loss", "failure", "no-observation"} {
		t.Run(kind, func(t *testing.T) {
			bundle := compileUnitJob(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := jobController{ctx: ctx, cancel: cancel, bundle: bundle, token: "token", active: -1}
			c.request.OnBoundary = func(_ context.Context, boundary string, _ JobStep, _ JobStepResult) error {
				if boundary == kind {
					return os.ErrPermission
				}
				return nil
			}
			emit := func(e JobEvent) error { e.Token = "token"; e.StepID = "a"; return c.handle(e) }
			err := emit(JobEvent{Kind: "begin"})
			if kind == "begin" {
				if err == nil || ctx.Err() == nil {
					t.Fatal("persistence error did not stop")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind != "no-observation" {
				if err := emit(JobEvent{Kind: "play_start", Sequence: 1}); err != nil {
					t.Fatal(err)
				}
			}
			sequence := 2
			if kind == "event-loss" {
				sequence = 3
			}
			status := "ok"
			if kind == "failure" {
				status = "failed"
			}
			if kind != "no-observation" {
				err = emit(JobEvent{Kind: "result", Sequence: sequence, Host: "node", Status: status, OwnerTask: true})
			}
			if err == nil {
				err = emit(JobEvent{Kind: "end"})
			}
			if err == nil || c.next != 0 || ctx.Err() == nil {
				t.Fatalf("advanced after %s: %v", kind, err)
			}
		})
	}
}
func TestRetryStagesKeepBaselineAndPostOnly(t *testing.T) {
	plan := JobPlan{Steps: []JobStep{{ID: "a-pre", Phase: "pre"}, {ID: "a-main", Phase: "execute", RetrySafe: true}, {ID: "a-post", Phase: "post"}, {ID: "b-pre", Phase: "pre"}, {ID: "b-main", Phase: "execute", RetrySafe: true, Variables: map[string]any{"backup": "original"}}, {ID: "b-post", Phase: "post"}}}
	for i := range plan.Steps {
		if i < 3 {
			plan.Steps[i].NodeID = "a"
		} else {
			plan.Steps[i].NodeID = "b"
		}
	}
	results := []JobStepResult{}
	for _, step := range plan.Steps[:5] {
		results = append(results, JobStepResult{StepID: step.ID, Status: "succeeded", StartedAt: time.Now()})
	}
	stages, err := RetryStages(plan, results)
	if err != nil || len(stages) != 2 || !strings.HasPrefix(stages[0].ID, "refresh-") || stages[1].ID != "b-post" {
		t.Fatalf("post retry=%+v %v", stages, err)
	}
	stages, err = RetryStages(plan, results[:4])
	if err != nil || len(stages) != 4 || stages[1].ID != "b-pre" || stages[2].Variables["backup"] != "original" {
		t.Fatalf("body retry=%+v %v", stages, err)
	}
	plan.Steps[4].RetrySafe = false
	if _, err := RetryStages(plan, results[:4]); err == nil {
		t.Fatal("unsafe retry accepted")
	}
}
