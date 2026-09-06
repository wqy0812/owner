package ansible

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeJobRealLegacySourcePreflight(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	for _, source := range []string{"- name: incompatible dispatch\n  ansible.builtin.assert:\n    that: true\n", "- name: incompatible script arguments\n  script:\n    cmd: files/example.py\n    executable: /usr/bin/python3\n"} {
		runner, plan, target := roleJobFixture(t, binary, "")
		runtime, err := runner.RuntimeIdentity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if runtime.AnsibleCore != "2.8.8" {
			t.Skip("specifically exercises Ansible 2.8.8 action dispatch")
		}
		path := filepath.Join(runner.AllowedRoot, "managed/beta/line/release/tasks/install.yml")
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
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
		output, err := nativeCommand(t, binary, bundle, "install", filepath.Join(t.TempDir(), "results"), nil)
		if err == nil || !strings.Contains(string(output), "incompatible") || !strings.Contains(string(output), "tasks/install.yml") {
			t.Fatalf("no actionable source diagnostic: %v\n%s", err, output)
		}
		if _, err := os.Stat(filepath.Join(target, "alpha-one-a-body")); !os.IsNotExist(err) {
			t.Fatal("earlier component mutated before whole-job compatibility check")
		}
	}
}
