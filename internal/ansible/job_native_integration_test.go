package ansible

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func nativeCommand(t *testing.T, binary string, bundle *JobBundle, operation, results string, options map[string]any) ([]byte, error) {
	return nativeProcess(t, binary, bundle, operation, results, options).CombinedOutput()
}

func nativeProcess(t *testing.T, binary string, bundle *JobBundle, operation, results string, options map[string]any) *exec.Cmd {
	t.Helper()
	input := map[string]any{"operation": operation, "results": results}
	for key, value := range options {
		input[key] = value
	}
	args, _ := json.Marshal(map[string]any{"cf_job": input})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, binary, "-i", "inventory.ini", "site.yml", "-e", string(args))
	command.Dir = bundle.Path
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "CLUSTERFORGE_JOB_") && !strings.HasPrefix(item, "ANSIBLE_CONFIG=") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "PYTHONDONTWRITEBYTECODE=1", "ANSIBLE_CONFIG="+filepath.Join(bundle.Path, "ansible.cfg"), "ANSIBLE_LOCAL_TEMP="+t.TempDir())
	return command
}

func TestNativeJobRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	for _, failure := range []string{"", "post"} {
		t.Run("resume_"+failure, func(t *testing.T) {
			runner, plan, target := roleJobFixture(t, binary, "")
			for _, component := range []string{"alpha", "beta"} {
				role := filepath.Join(runner.AllowedRoot, "managed", component, "line", "release")
				install := filepath.Join(role, "tasks/install.yml")
				content, _ := os.ReadFile(install)
				content = append([]byte("- shell: 'echo called >> {{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-calls'\n"), content...)
				if err := os.WriteFile(install, content, 0600); err != nil {
					t.Fatal(err)
				}
				other := "alpha"
				if component == "alpha" {
					other = "beta"
				}
				for name, source := range map[string]string{
					"defaults/main.yml":                "role_color: " + component + "\n" + component + "_only: private\n",
					"vars/main.yml":                    "role_owner: " + component + "\n",
					"tasks/checks/rollback-before.yml": "- assert:\n    that: true\n",
					"tasks/rollback.yml":               "- file:\n    path: '{{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-{{ item }}'\n    state: absent\n  loop: [body, handler]\n",
					"tasks/checks/rollback-after.yml":  "- stat:\n    path: '{{ cf.inputs.target }}/{{ cf.context.nodeId }}-{{ inventory_hostname }}-body'\n  register: cf_local_absent\n- assert:\n    that: not cf_local_absent.stat.exists\n",
				} {
					path := filepath.Join(role, name)
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(source), 0600); err != nil {
						t.Fatal(err)
					}
				}
				pre := filepath.Join(role, "tasks/checks/pre.yml")
				content, _ = os.ReadFile(pre)
				content = append(content, []byte("- assert:\n    that:\n      - role_color == '"+component+"'\n      - role_owner == '"+component+"'\n      - "+other+"_only is not defined\n")...)
				if err := os.WriteFile(pre, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			allowed := filepath.Join(target, "allow-post")
			// The same immutable source fails until its external condition is fixed.
			if failure != "" {
				path := filepath.Join(runner.AllowedRoot, "managed/alpha/line/release/tasks/checks/post.yml")
				content, _ := os.ReadFile(path)
				content = append(content, []byte("- stat:\n    path: '"+allowed+"'\n  register: cf_local_allowed\n- assert:\n    that: cf_local_allowed.stat.exists\n")...)
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := range plan.Steps {
				step := &plan.Steps[i]
				step.ParentActionID = "install.yml"
				path := filepath.Join(runner.AllowedRoot, step.Playbook)
				step.PlaybookDigest, _ = FileDigest(path)
				role := filepath.Join(runner.AllowedRoot, "managed", step.ComponentID, "line", "release")
				step.WorkspaceDigest, _ = TreeDigest(role)
			}
			for i := len(plan.Steps) - 1; i >= 0; i-- {
				main := plan.Steps[i]
				if main.Phase != "execute" {
					continue
				}
				for j, entry := range []string{"checks/rollback-before.yml", "rollback.yml", "checks/rollback-after.yml"} {
					step := main
					step.Phase = []string{"pre", "execute", "post"}[j]
					step.ID = main.NodeID + "-rollback-" + step.Phase
					step.ParentActionID, step.ActionID = "rollback.yml", entry
					step.RecoveryOfStepID = main.ID
					step.Action = "check"
					if j == 1 {
						step.Action = "rollback"
					}
					step.Playbook = "managed/" + step.ComponentID + "/line/release/tasks/" + entry
					step.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, step.Playbook))
					plan.Recovery = append(plan.Recovery, step)
				}
			}
			bundle, err := runner.BuildJob(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			results := filepath.Join(t.TempDir(), "private-results")
			output, err := nativeCommand(t, binary, bundle, "install", results, nil)
			if failure == "" && err != nil {
				t.Fatalf("native install: %v\n%s", err, output)
			}
			if failure != "" {
				if err == nil {
					t.Fatal("expected native postcheck failure")
				}
				if _, err := os.Stat(filepath.Join(target, "alpha-one-a-handler")); err != nil {
					t.Fatalf("main did not complete: %v\n%s", err, output)
				}
				if _, err := os.Stat(filepath.Join(target, "beta-one-c-handler")); !os.IsNotExist(err) {
					t.Fatal("later component started after failure")
				}
				before, _ := os.Stat(filepath.Join(target, "alpha-one-a-handler"))
				if output, err := nativeCommand(t, binary, bundle, "resume", results, nil); err == nil {
					t.Fatalf("resume ignored unresolved postcondition: %s", output)
				}
				if err := os.WriteFile(allowed, []byte("ready"), 0600); err != nil {
					t.Fatal(err)
				}
				output, err = nativeCommand(t, binary, bundle, "resume", results, nil)
				if err != nil {
					t.Fatalf("native resume: %v\n%s", err, output)
				}
				after, _ := os.Stat(filepath.Join(target, "alpha-one-a-handler"))
				if !before.ModTime().Equal(after.ModTime()) {
					t.Fatal("successful main/handler was repeated")
				}
			}
			for _, name := range []string{"alpha-one-a", "alpha-one-b", "beta-one-c", "alpha-two-a", "alpha-two-b"} {
				if _, err := os.Stat(filepath.Join(target, name+"-handler")); err != nil {
					t.Fatal(err)
				}
				calls, err := os.ReadFile(filepath.Join(target, name+"-calls"))
				if err != nil || string(calls) != "called\n" {
					t.Fatalf("main repeated for %s: %q %v", name, calls, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(results, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Attempts []struct {
					Status string `json:"status"`
				} `json:"attempts"`
			}
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Attempts[len(receipt.Attempts)-1].Status != "succeeded" {
				t.Fatalf("no durable success: %s", data)
			}
			if failure != "" && len(receipt.Attempts) != 3 {
				t.Fatalf("lost repeated resume history: %s", data)
			}
			if err := bundle.Validate(); err != nil {
				t.Fatal(err)
			}
			if _, err := nativeCommand(t, binary, bundle, "install", results, nil); err == nil {
				t.Fatal("allowed duplicate install into existing results")
			}
			output, err = nativeCommand(t, binary, bundle, "rollback-preview", results, nil)
			if err != nil {
				t.Fatalf("rollback preview: %v\n%s", err, output)
			}
			match := regexp.MustCompile(`"planDigest":\s*"([a-f0-9]{64})"`).FindSubmatch(output)
			if len(match) != 2 {
				t.Fatalf("missing rollback digest: %s", output)
			}
			afterPreview, _ := os.ReadFile(filepath.Join(results, "receipt.json"))
			if string(afterPreview) != string(data) {
				t.Fatal("rollback preview changed receipt")
			}
			output, err = nativeCommand(t, binary, bundle, "rollback", results, map[string]any{"expectedPlanDigest": string(match[1])})
			if err != nil {
				t.Fatalf("native rollback: %v\n%s", err, output)
			}
			for _, name := range []string{"alpha-one-a", "alpha-one-b", "beta-one-c", "alpha-two-a", "alpha-two-b"} {
				if _, err := os.Stat(filepath.Join(target, name+"-body")); !os.IsNotExist(err) {
					t.Fatalf("rollback left %s", name)
				}
			}
		})
	}
}
