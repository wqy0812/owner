package ansible

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestYAMLJobHasNoImplicitFactsOrProbeTasks(t *testing.T) {
	bundle := compileUnitJob(t)
	data, err := os.ReadFile(filepath.Join(bundle.Path, "site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Gather phase facts") || strings.Contains(string(data), "Read-only prerequisite") {
		t.Fatal("implicit component task remains")
	}
	var plays []map[string]any
	if err := json.Unmarshal(data, &plays); err != nil {
		t.Fatal(err)
	}
	for _, play := range plays {
		if play["gather_facts"] != false {
			t.Fatal("automatic facts enabled")
		}
	}
	// Old sealed native bundles retain their original contract and checksum.
	bundle.Manifest.Contract = PreviousNativeJobContract
	bundle.Manifest.Plan.Contract = PreviousNativeJobContract
	bundle.Manifest.FullPlanDigest = jsonDigest(bundle.Manifest.Plan)
	bundle.Manifest.Digest = ""
	bundle.Manifest.Digest = jsonDigest(bundle.Manifest)
	if err := bundle.Validate(); err != nil {
		t.Fatalf("historical native bundle rejected: %v", err)
	}
}

func TestYAMLTwoStepRollbackRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires isolated real Ansible")
	}
	runner, plan, target := roleJobFixture(t, binary, "")
	plan.Steps = plan.Steps[:3]
	role := filepath.Join(runner.AllowedRoot, "managed/alpha/line/release")
	prePath := filepath.Join(role, "tasks/checks/pre.yml")
	data, err := os.ReadFile(prePath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("- stat:\n    path: '{{ cf.inputs.target }}/check-ready'\n  register: cf_local_permission\n- assert:\n    that: cf_local_permission.stat.exists\n")...)
	if err := os.WriteFile(prePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	rollbackPath := filepath.Join(role, "tasks/rollback.yml")
	if err := os.WriteFile(rollbackPath, []byte("- shell: 'echo called >> {{ cf.inputs.target }}/rollback-{{ inventory_hostname }}-calls'\n- file:\n    path: '{{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-{{ item }}'\n    state: absent\n  loop: [body, handler]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(target, "check-ready")
	if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	tree, err := TreeDigest(role)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		step.ParentActionID = "install.yml"
		step.WorkspaceDigest = tree
		step.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, step.Playbook))
	}
	for _, phase := range []string{"execute", "post"} {
		step := plan.Steps[1]
		step.ID, step.Phase = "rollback-"+phase, phase
		step.ParentActionID, step.RecoveryOfStepID = "rollback.yml", plan.Steps[1].ID
		step.ActionID, step.Action = "rollback.yml", "rollback"
		entry := "rollback.yml"
		if phase == "post" {
			step.ActionID, step.Action, entry = "checks/pre.yml", "check", "checks/pre.yml"
		}
		step.Playbook = "managed/alpha/line/release/tasks/" + entry
		step.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, step.Playbook))
		plan.Recovery = append(plan.Recovery, step)
	}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	results := filepath.Join(t.TempDir(), "receipts")
	if output, err := nativeCommand(t, binary, bundle, "install", results, nil); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	if err := os.Remove(ready); err != nil {
		t.Fatal(err)
	}
	output, err := nativeCommand(t, binary, bundle, "rollback-preview", results, nil)
	if err != nil {
		t.Fatalf("preview: %v\n%s", err, output)
	}
	match := regexp.MustCompile(`"planDigest":\s*"([a-f0-9]{64})"`).FindSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("no locked preview: %s", output)
	}
	if output, err = nativeCommand(t, binary, bundle, "rollback", results, map[string]any{"expectedPlanDigest": string(match[1])}); err == nil {
		t.Fatalf("failed postcheck accepted: %s", output)
	}
	readReceipt := func() map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(results, "receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		var receipt map[string]any
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	receipt := readReceipt()
	if receipt["actions"].(map[string]any)[plan.Steps[1].ID] == "rolled_back" {
		t.Fatal("failed postcheck confirmed recovery")
	}
	if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := nativeCommand(t, binary, bundle, "resume", results, nil); err != nil {
		t.Fatalf("resume: %v\n%s", err, output)
	}
	receipt = readReceipt()
	if receipt["actions"].(map[string]any)[plan.Steps[1].ID] != "rolled_back" {
		t.Fatal("successful postcheck did not confirm recovery")
	}
	for _, host := range []string{"a", "b"} {
		calls, err := os.ReadFile(filepath.Join(target, "rollback-"+host+"-calls"))
		if err != nil || string(calls) != "called\n" {
			t.Fatalf("rollback body repeated: %q %v", calls, err)
		}
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestJobRejectsWorkspaceAliasBetweenComponents(t *testing.T) {
	bundle := compileUnitJob(t)
	plan := bundle.Manifest.Plan
	for i := range plan.Steps {
		plan.Steps[i].Playbook = "roles/" + plan.Steps[i].Role + "/tasks/" + plan.Steps[i].TasksFrom
		plan.Steps[i].WorkspaceDigest, _ = TreeDigest(filepath.Join(bundle.Path, "roles", plan.Steps[i].Role))
	}
	for i := range plan.Recovery {
		plan.Recovery[i].Playbook = "roles/" + plan.Recovery[i].Role + "/tasks/" + plan.Recovery[i].TasksFrom
		plan.Recovery[i].WorkspaceDigest, _ = TreeDigest(filepath.Join(bundle.Path, "roles", plan.Recovery[i].Role))
	}
	plan.Steps[1].ComponentID = "another-component"
	root := t.TempDir()
	binary := filepath.Join(root, "version-only")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'ansible-playbook [core 2.19.12]\\n  python version = 3.12.9\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{AllowedRoot: bundle.Path, Binary: binary}
	if _, err := runner.BuildJob(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "workspaces") {
		t.Fatalf("aliased component source accepted: %v", err)
	}
}

func TestTwoStepRollbackRetryRespectsOptionalPrecheck(t *testing.T) {
	plan := JobPlan{Steps: []JobStep{{ID: "body", ActionID: "rollback", Action: "rollback", Phase: "execute", RetrySafe: true}, {ID: "post", ParentActionID: "rollback", Action: "check", Phase: "post", RetrySafe: true}}}
	steps, err := RetryStages(plan, nil)
	if err != nil || len(steps) != 2 {
		t.Fatalf("two-step rollback cannot retry: %v %+v", err, steps)
	}
	plan.Steps[0].PreCheckRequired = true
	if _, err := RetryStages(plan, nil); err == nil {
		t.Fatal("required precheck bypassed")
	}
}

func TestRoleLocalSourceStaysInsideItsComponent(t *testing.T) {
	for _, tc := range []struct {
		source string
		bad    bool
	}{
		{"- template:\n    src: config.j2\n    dest: /etc/config\n", false},
		{"- copy:\n    src: '{{ role_path }}/files/config'\n    dest: /tmp/config\n", false},
		{"- copy:\n    src: ../../other/files/config\n    dest: /tmp/config\n", true},
		{"- template:\n    src: /other/component/config.j2\n    dest: /etc/config\n", true},
		{"- copy:\n    src: /target/backup\n    remote_src: true\n    dest: /target/data\n", false},
		{"- copy: src=../../other/file dest=/target/data\n", true},
		{"- copy: {}\n  args:\n    src: ../../other/file\n    dest: /target/data\n", true},
		{"- debug:\n    msg: \"{{ lookup('file', '../../other/file') }}\"\n", true},
		{"- debug:\n    msg: \"{{ lookup('file', 'roles/another/files/file') }}\"\n", true},
		{"- debug:\n    msg: \"{{ lookup('file', 'config.json') }}\"\n", false},
	} {
		if err := ValidateRoleTasks([]byte(tc.source), false); (err != nil) != tc.bad {
			t.Fatalf("%s: %v", tc.source, err)
		}
	}
}
