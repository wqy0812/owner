package ansible

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func roleJobFixture(t *testing.T, binary string, mutation string) (*Runner, JobPlan, string) {
	t.Helper()
	root, target := t.TempDir(), t.TempDir()
	plan := JobPlan{Inventory: "[first]\na ansible_connection=local\nb ansible_connection=local\n[second]\nc ansible_connection=local\n"}
	for _, component := range []string{"alpha", "beta"} {
		role := filepath.Join(root, "managed", component, "line", "release")
		sources := map[string]string{
			"tasks/checks/pre.yml":  "- assert:\n    that:\n      - cf.inputs.label == cf.context.nodeId\n      - cf_local_leak is none\n",
			"tasks/install.yml":     "- set_fact:\n    cf_local_leak: '{{ cf.context.nodeId }}'\n- copy:\n    dest: '{{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-body'\n    content: installed\n  notify: finish\n",
			"handlers/main.yml":     "- name: finish\n  copy:\n    dest: '{{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-handler'\n    content: done\n",
			"tasks/checks/post.yml": "- stat:\n    path: '{{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-handler'\n  register: cf_local_stat\n- assert:\n    that: cf_local_stat.stat.exists\n",
		}
		if component == "alpha" {
			switch mutation {
			case "pre":
				sources["tasks/checks/pre.yml"] = "- fail:\n    msg: pre failed\n"
			case "execute":
				sources["tasks/install.yml"] = "- fail:\n    msg: body failed\n"
			case "handler":
				sources["handlers/main.yml"] = "- name: finish\n  fail:\n    msg: handler failed\n"
			case "post":
				sources["tasks/checks/post.yml"] = "- fail:\n    msg: post failed\n"
			case "timeout", "cancel":
				sources["tasks/install.yml"] = "- command: /bin/sleep 10\n"
			}
		}
		for name, content := range sources {
			path := filepath.Join(role, name)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for n, node := range []string{"alpha-one", "beta-one", "alpha-two"} {
		component, group := "alpha", "first"
		if n == 1 {
			component, group = "beta", "second"
		}
		role := filepath.Join(root, "managed", component, "line", "release")
		tree, err := TreeDigest(role)
		if err != nil {
			t.Fatal(err)
		}
		for i, entry := range []string{"checks/pre.yml", "install.yml", "checks/post.yml"} {
			path := filepath.Join(role, "tasks", entry)
			digest, _ := FileDigest(path)
			relative, _ := filepath.Rel(root, path)
			phase := []string{"pre", "execute", "post"}[i]
			action := "check"
			if i == 1 {
				action = "install"
			}
			timeout := 15
			if mutation == "timeout" && n == 0 && i == 1 {
				timeout = 1
			}
			plan.Steps = append(plan.Steps, JobStep{ID: node + "-" + phase, NodeID: node, ComponentID: component, ReleaseID: component + "-immutable-v1", ActionID: entry, ParentActionID: "install", Name: node + "/" + phase, Action: action, Phase: phase, Playbook: relative, PlaybookDigest: digest, WorkspaceDigest: tree, Limit: group, TimeoutSeconds: timeout, RetrySafe: true, Variables: map[string]any{"target": target, "label": node}})
		}
	}
	return &Runner{AllowedRoot: root, Binary: binary}, plan, target
}

func TestRoleJobAcceptance(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("set CLUSTERFORGE_JOB_TEST_ANSIBLE for real isolated Ansible acceptance")
	}
	for _, failure := range []string{"", "pre", "execute", "handler", "post", "unreachable", "timeout", "cancel", "begin persistence", "end persistence", "event persistence", "event loss"} {
		name := failure
		if name == "" {
			name = "serial groups repeated release and variable isolation"
		}
		t.Run(name, func(t *testing.T) {
			runner, plan, target := roleJobFixture(t, binary, failure)
			if failure == "unreachable" {
				plan.Inventory = "[first]\na ansible_host=127.0.0.1 ansible_port=1 ansible_connection=ssh ansible_ssh_timeout=1\n[second]\nc ansible_connection=local\n"
				plan.Steps[0].GatherFacts = true
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			boundaries := []string{}
			request := JobRequest{Plan: plan, OnBoundary: func(_ context.Context, kind string, s JobStep, _ JobStepResult) error {
				boundaries = append(boundaries, kind+":"+s.ID)
				if s.ID == "alpha-one-execute" && ((failure == "begin persistence" && kind == "begin") || (failure == "end persistence" && kind == "end")) {
					return fmt.Errorf("deliberate persistence failure")
				}
				return nil
			}, OnEvent: func(e JobEvent) error {
				if e.StepID == "alpha-one-execute" {
					if failure == "event persistence" {
						return fmt.Errorf("deliberate event persistence failure")
					}
					if failure == "cancel" && e.Kind == "task_start" {
						cancel()
					}
				}
				return nil
			}}
			bundle, err := runner.BuildJob(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if failure == "event loss" {
				// Fault-inject a lost observation in the real callback transport.
				// Seal the fixture before execution so this tests sequence control,
				// independently of the separately tested file-tampering guard.
				name := "callback_plugins/cf_events.py"
				path := filepath.Join(bundle.Path, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				source := strings.Replace(string(data), "        self.sequence += 1\n", "        self.sequence += 1\n        if self.step_id == 'alpha-one-execute' and kind == 'play_start':\n            return\n", 1)
				if source == string(data) {
					t.Fatal("event-loss fixture was not injected")
				}
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				bundle.Manifest.Files[name], err = FileDigest(path)
				if err != nil {
					t.Fatal(err)
				}
				bundle.Manifest.Digest = ""
				bundle.Manifest.Digest = jsonDigest(bundle.Manifest)
				manifest, _ := json.Marshal(bundle.Manifest)
				if err := os.WriteFile(filepath.Join(bundle.Path, "manifest.json"), manifest, 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := runner.RunBundle(ctx, bundle, request)
			if failure == "event loss" && (err == nil || !strings.Contains(err.Error(), "missing or duplicated job event")) {
				t.Fatalf("event loss did not fail closed: %v", err)
			}
			if failure == "" {
				if err != nil {
					t.Fatalf("%v\n%s", err, joinedLogs(result.Logs))
				}
				if len(boundaries) != 18 {
					t.Fatalf("boundaries %v", boundaries)
				}
				for _, suffix := range []string{"alpha-one-a", "alpha-one-b", "beta-one-c", "alpha-two-a", "alpha-two-b"} {
					if _, err := os.Stat(filepath.Join(target, suffix+"-handler")); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				for _, b := range boundaries {
					if strings.Contains(b, "beta-one") || strings.Contains(b, "alpha-two") {
						t.Fatalf("later component started: %v", boundaries)
					}
				}
				if result.Duration > 8*time.Second && (failure == "cancel" || failure == "timeout") {
					t.Fatalf("process group was not terminated promptly: %v", result.Duration)
				}
			}
			formal := 0
			for _, phase := range result.Phases {
				if phase.Phase == PhaseExecute {
					formal++
				}
			}
			if formal != 1 {
				t.Fatalf("formal executions=%d, error=%v\n%s", formal, err, joinedLogs(result.Logs))
			}
			if result.ExitCode == nil {
				t.Fatal("missing real process exit code")
			}
		})
	}
}
