package jobcli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex/platform-demo/internal/ansible"
)

func TestStandaloneRealRunResumeRollback(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	root, target, results := t.TempDir(), t.TempDir(), t.TempDir()
	role := filepath.Join(root, "managed", "offline", "line", "release")
	sources := map[string]string{
		"tasks/checks/pre.yml":           "- assert:\n    that: cf.credentials.test_token | length > 0\n  no_log: true\n",
		"tasks/install.yml":              "- shell: 'echo install >> {{ cf.inputs.target }}/calls'\n- copy:\n    dest: '{{ cf.inputs.target }}/installed'\n    content: '{{ cf.inputs.clusterforge_backup_ref }}'\n",
		"tasks/checks/post.yml":          "- stat:\n    path: '{{ cf.inputs.target }}/allow-install-post'\n  register: cf_local\n- assert:\n    that: cf_local.stat.exists\n",
		"tasks/checks/rollback-pre.yml":  "- stat:\n    path: '{{ cf.inputs.target }}/installed'\n  register: cf_local\n- assert:\n    that: cf_local.stat.exists\n",
		"tasks/rollback.yml":             "- shell: 'echo rollback >> {{ cf.inputs.target }}/calls'\n- file:\n    path: '{{ cf.inputs.target }}/installed'\n    state: absent\n",
		"tasks/checks/rollback-post.yml": "- stat:\n    path: '{{ cf.inputs.target }}/allow-rollback-post'\n  register: cf_local\n- assert:\n    that: cf_local.stat.exists\n",
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
	tree, _ := ansible.TreeDigest(role)
	plan := ansible.JobPlan{Inventory: "[targets]\nlocal ansible_connection=local\n", RequiredCredentials: []string{"test_token"}, Metadata: map[string]any{"steps": []any{map[string]any{"id": "offline"}}}}
	for i, entry := range []string{"checks/pre.yml", "install.yml", "checks/post.yml", "checks/rollback-pre.yml", "rollback.yml", "checks/rollback-post.yml"} {
		phase := []string{"pre", "execute", "post"}[i%3]
		action, parent := "check", "install"
		operation := "capture"
		if i >= 3 {
			parent, operation = "rollback", "restore"
		}
		if phase == "execute" {
			action = parent
		}
		path := filepath.Join(role, "tasks", entry)
		digest, _ := ansible.FileDigest(path)
		relative, _ := filepath.Rel(root, path)
		step := ansible.JobStep{ID: parent + "-" + phase, NodeID: "offline", ComponentID: "offline", ReleaseID: "offline-v1", ActionID: parent + "-" + phase, ParentActionID: parent + "-execute", Action: action, Phase: phase, Playbook: relative, PlaybookDigest: digest, WorkspaceDigest: tree, Limit: "targets", TimeoutSeconds: 15, Variables: map[string]any{"target": target, "clusterforge_backup_ref": filepath.Join(target, "backup", "preview", "offline"), "clusterforge_backup_operation": operation, "clusterforge_backup_metadata": map[string]any{"install_run_id": "preview"}}}
		if i < 3 {
			plan.Steps = append(plan.Steps, step)
		} else {
			step.RecoveryOfStepID = "install-execute"
			plan.Recovery = append(plan.Recovery, step)
		}
	}
	runner := &ansible.Runner{AllowedRoot: root, Binary: binary}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	secret := "offline-never-exported-secret"
	if err := os.WriteFile(credentials, []byte(`{"test_token":"`+secret+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := bundle.WriteArchive(&archive); err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	entries := tar.NewReader(gz)
	for {
		header, err := entries.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(entries)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) || strings.Contains(header.Name, "credentials.json") {
			t.Fatal("credential packaged")
		}
	}
	call := func(op string, extra ...string) (string, error) {
		t.Helper()
		args := []string{op, bundle.Path, "--results", results, "--ansible", binary, "--credentials", credentials}
		args = append(args, extra...)
		var out, stderr bytes.Buffer
		err := Run(args, &out, &stderr)
		if strings.Contains(out.String(), secret) || strings.Contains(stderr.String(), secret) {
			t.Fatal("secret in logs")
		}
		return out.String(), err
	}
	receipt := func() Receipt {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(results, "receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		var r Receipt
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		if err := validateReceipt(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if _, err := call("run"); err == nil {
		t.Fatal("post-check should fail")
	}
	first := receipt()
	if first.Actions["install-execute"] != "main_succeeded" {
		t.Fatalf("partial install receipt=%v", first.Actions)
	}
	baseline := first.Plan.Steps[1].Variables["clusterforge_backup_ref"]
	if err := os.WriteFile(filepath.Join(target, "allow-install-post"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := call("resume"); err != nil {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	resumed := receipt()
	if len(resumed.Plan.Steps) != 1 || resumed.Plan.Steps[0].Phase != "post" || resumed.Plan.Steps[0].Variables["clusterforge_backup_ref"] != baseline {
		t.Fatal("resume reran body or replaced baseline")
	}
	out, err := call("rollback-preview")
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		PlanDigest string `json:"planDigest"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	installed, temporarilyHidden := filepath.Join(target, "installed"), filepath.Join(target, "temporarily-hidden")
	if err := os.Rename(installed, temporarilyHidden); err != nil {
		t.Fatal(err)
	}
	if _, err := call("rollback", "--expected-plan-digest", preview.PlanDigest); err == nil {
		t.Fatal("rollback pre-check should fail")
	}
	beforeBody := receipt()
	if len(beforeBody.Result.Steps) != 1 || beforeBody.Result.Steps[0].StepID != "rollback-pre" || beforeBody.Actions["install-execute"] != "verified" {
		t.Fatalf("pre-check failure overwrote completed installation: %+v", beforeBody)
	}
	if err := os.Rename(temporarilyHidden, installed); err != nil {
		t.Fatal(err)
	}
	if _, err := call("resume"); err == nil {
		t.Fatal("rollback post-check should fail")
	}
	pending := receipt()
	if pending.Actions["install-execute"] == "rolled_back" {
		t.Fatal("unverified rollback reported restored")
	}
	if err := os.WriteFile(filepath.Join(target, "allow-rollback-post"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := call("resume"); err != nil {
		t.Fatalf("rollback resume: %v\n%s", err, out)
	}
	final := receipt()
	if final.Actions["install-execute"] != "rolled_back" {
		t.Fatalf("source not restored: %v", final.Actions)
	}
	calls, err := os.ReadFile(filepath.Join(target, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "install\nrollback\n" {
		t.Fatalf("bodies repeated: %s", calls)
	}
	attempts := append(append([]Attempt{}, final.History...), Attempt{Plan: final.Plan, Result: final.Result})
	if len(attempts) != 5 {
		t.Fatalf("attempts=%d", len(attempts))
	}
	for _, attempt := range attempts {
		formal := 0
		for _, phase := range attempt.Result.Phases {
			if phase.Phase == ansible.PhaseExecute {
				formal++
			}
		}
		if formal != 1 {
			t.Fatalf("formal invocations=%d", formal)
		}
	}
	events, err := os.ReadFile(filepath.Join(results, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(events, []byte(secret)) {
		t.Fatal("credential persisted in results")
	}
	if err := os.WriteFile(filepath.Join(bundle.Path, "roles", ansible.RoleName("offline-v1"), "tasks", "install.yml"), []byte("- debug: msg=changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := call("run"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("tampering accepted: %v", err)
	}
}
