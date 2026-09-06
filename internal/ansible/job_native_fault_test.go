package ansible

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeJobRealFailureStops(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	for _, failure := range []string{"pre", "execute", "handler", "post", "unreachable", "timeout", "event-loss", "event-write", "missing-callback"} {
		t.Run(failure, func(t *testing.T) {
			runner, plan, target := roleJobFixture(t, binary, failure)
			if failure == "unreachable" {
				plan.Inventory = "[first]\na ansible_host=127.0.0.1 ansible_port=1 ansible_connection=ssh ansible_ssh_timeout=1\nb ansible_connection=local\n[second]\nc ansible_connection=local\n"
			}
			for i := range plan.Steps {
				plan.Steps[i].ParentActionID = "install.yml"
			}
			bundle, err := runner.BuildJob(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			name, before, after := "", "", ""
			switch failure {
			case "event-loss":
				name = "callback_plugins/cf_events.py"
				before = "        self.sequence += 1\n"
				after = before + "        if self.step_id == 'alpha-one-execute' and kind == 'play_start':\n            return\n"
			case "event-write":
				name = "cf_native.py"
				before = "        self.events.write("
				after = "        if event['stepId'] == 'alpha-one-execute':\n            raise OSError('injected event persistence failure')\n" + before
			case "missing-callback":
				name, before, after = "ansible.cfg", "= cf_events", "= disabled_fixture_callback"
			}
			if name != "" {
				path := filepath.Join(bundle.Path, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				changed := strings.ReplaceAll(string(data), before, after)
				if changed == string(data) {
					t.Fatal("fault was not injected")
				}
				if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
					t.Fatal(err)
				}
				bundle.Manifest.Files[name], _ = FileDigest(path)
				bundle.Manifest.Digest = ""
				bundle.Manifest.Digest = jsonDigest(bundle.Manifest)
				manifest, _ := json.Marshal(bundle.Manifest)
				if err := os.WriteFile(filepath.Join(bundle.Path, "manifest.json"), manifest, 0600); err != nil {
					t.Fatal(err)
				}
			}
			results := filepath.Join(t.TempDir(), "results")
			output, err := nativeCommand(t, binary, bundle, "install", results, nil)
			if err == nil {
				t.Fatalf("failure was swallowed: %s", output)
			}
			if _, err := os.Stat(filepath.Join(target, "beta-one-c-body")); !os.IsNotExist(err) {
				t.Fatalf("later component started after %s", failure)
			}
			if failure != "missing-callback" {
				data, err := os.ReadFile(filepath.Join(results, "receipt.json"))
				if err != nil {
					t.Fatalf("missing interrupted receipt: %v\n%s", err, output)
				}
				if strings.Contains(string(data), `"status":"succeeded","steps"`) {
					t.Fatalf("failed attempt marked successful: %s", data)
				}
				// A damaged receipt must stop before any new dispatch.
				if err := os.WriteFile(filepath.Join(results, "receipt.json"), append(data, 'x'), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := nativeCommand(t, binary, bundle, "resume", results, nil); err == nil {
					t.Fatal("corrupt receipt accepted")
				}
			}
		})
	}
}

func TestNativeJobRealCredentialRedaction(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	runner, plan, _ := roleJobFixture(t, binary, "")
	path := filepath.Join(runner.AllowedRoot, "managed/alpha/line/release/tasks/install.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = append([]byte("- debug:\n    msg: '{{ cf.credentials.test_secret }}'\n"), content...)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	plan.RequiredCredentials = []string{"test_secret"}
	for i := range plan.Steps {
		s := &plan.Steps[i]
		s.ParentActionID = "install.yml"
		s.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, s.Playbook))
		s.WorkspaceDigest, _ = TreeDigest(filepath.Join(runner.AllowedRoot, "managed", s.ComponentID, "line/release"))
	}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	results := filepath.Join(t.TempDir(), "results")
	command := nativeProcess(t, binary, bundle, "install", results, nil)
	secret := "test-only-引号\"-反斜线\\-换行\nsecret-value"
	input, _ := json.Marshal(map[string]any{"cf_job": map[string]any{"operation": "install", "results": results}, "cf_credentials": map[string]any{"test_secret": secret}})
	command.Args[len(command.Args)-1] = string(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	for _, path := range []string{filepath.Join(results, "events.jsonl"), filepath.Join(results, "receipt.json"), filepath.Join(bundle.Path, "manifest.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
	}
	if strings.Contains(string(output), "test-only-") || strings.Contains(string(output), "secret-value") {
		t.Fatal("credential leaked in output, receipt, events or bundle")
	}
	if !strings.Contains(string(output), "[REDACTED]") {
		t.Fatal("test did not exercise credential output")
	}
}
