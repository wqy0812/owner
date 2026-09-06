package ansible

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackContinuationPreservesCompletedTeardown(t *testing.T) {
	for _, action := range []string{"rollback", "uninstall", "install"} {
		t.Run(action, func(t *testing.T) {
			_, plan, _ := roleJobFixture(t, "", "")
			for i := range plan.Steps {
				plan.Steps[i].ParentActionID = "install.yml"
				if plan.Steps[i].Phase == "execute" {
					plan.Steps[i].Action = action
				}
			}
			results := []JobStepResult{}
			for _, step := range plan.Steps[:6] {
				results = append(results, JobStepResult{StepID: step.ID, Status: "succeeded"})
			}
			got, err := RetryStages(plan, results)
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			if action == "install" {
				want = 5 // Installation still rechecks both completed providers.
			}
			if len(got) != want || got[len(got)-3].ID != plan.Steps[6].ID {
				t.Fatalf("unexpected continuation: %+v", got)
			}
			// An incomplete postcheck must still run, without repeating its body.
			results = append(results, JobStepResult{StepID: plan.Steps[6].ID, Status: "succeeded"}, JobStepResult{StepID: plan.Steps[7].ID, Status: "succeeded"})
			got, err = RetryStages(plan, results)
			if err != nil || len(got) != want-2 || got[len(got)-1].ID != plan.Steps[8].ID {
				t.Fatalf("incomplete postcheck lost: %+v %v", got, err)
			}
		})
	}
}

func TestNativeRollbackResumeAfterProviderRemoval(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	runner, plan, target := roleJobFixture(t, binary, "")
	appendRole := func(component, path, extra string) {
		t.Helper()
		path = filepath.Join(runner.AllowedRoot, "managed", component, "line/release", path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, []byte(extra)...), 0600); err != nil {
			t.Fatal(err)
		}
	}
	appendRole("alpha", "tasks/checks/pre.yml", "- stat:\n    path: '{{ cf.inputs.target }}/allow'\n  register: cf_local_allow\n- assert:\n    that: cf.context.nodeId != 'alpha-two' or cf_local_allow.stat.exists\n")
	appendRole("alpha", "tasks/checks/post.yml", "- stat:\n    path: '{{ cf.inputs.target }}/api'\n  register: cf_local_api\n- assert:\n    that: cf.context.nodeId != 'alpha-one' or cf_local_api.stat.exists\n")
	appendRole("beta", "tasks/install.yml", "- file:\n    path: '{{ cf.inputs.target }}/api'\n    state: absent\n")
	if err := os.WriteFile(filepath.Join(target, "api"), []byte("available"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		step.ParentActionID = "install.yml"
		if step.Phase == "execute" {
			step.Action = "rollback"
		}
		step.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, step.Playbook))
		step.WorkspaceDigest, _ = TreeDigest(filepath.Join(runner.AllowedRoot, "managed", step.ComponentID, "line/release"))
	}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	results := filepath.Join(t.TempDir(), "results")
	if output, err := nativeCommand(t, binary, bundle, "install", results, nil); err == nil {
		t.Fatalf("expected final component precheck to stop: %s", output)
	}
	if _, err := os.Stat(filepath.Join(target, "api")); !os.IsNotExist(err) {
		t.Fatal("provider was not removed before interruption")
	}
	if err := os.WriteFile(filepath.Join(target, "allow"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := nativeCommand(t, binary, bundle, "resume", results, nil); err != nil {
		t.Fatalf("teardown resume: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(results, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Attempts []struct {
			Status string    `json:"status"`
			Steps  []JobStep `json:"steps"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Attempts) != 2 || receipt.Attempts[1].Status != "succeeded" || len(receipt.Attempts[1].Steps) != 3 || receipt.Attempts[1].Steps[0].NodeID != "alpha-two" {
		t.Fatalf("completed teardown replayed or history lost: %+v", receipt)
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
}
