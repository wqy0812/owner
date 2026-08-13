package ansible

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerWithTemporaryLocalPlaybook(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Ansible integration test in short mode")
	}
	if os.Getenv("NEWPLATFORM_ANSIBLE_INTEGRATION") != "1" {
		t.Skip("set NEWPLATFORM_ANSIBLE_INTEGRATION=1 to run the real localhost runner test")
	}
	binary, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed")
	}
	root := t.TempDir()
	inventory := []byte("[runner_test]\nlocalhost ansible_connection=local\n")
	playbook := []byte(`---
- name: Exercise the local Ansible runner
  hosts: runner_test
  gather_facts: false
  tasks:
    - name: Create isolated target directory
      ansible.builtin.file:
        path: "{{ target_root }}"
        state: directory
        mode: "0700"
    - name: Write test marker
      ansible.builtin.copy:
        dest: "{{ target_root }}/VERSION"
        content: "{{ expected_version }}\n"
        mode: "0600"
    - name: Read test marker
      ansible.builtin.slurp:
        src: "{{ target_root }}/VERSION"
      register: marker
    - name: Verify test marker
      ansible.builtin.assert:
        that:
          - (marker.content | b64decode | trim) == expected_version
`)
	if err := os.WriteFile(filepath.Join(root, "runner-test.yml"), playbook, 0o600); err != nil {
		t.Fatal(err)
	}
	targetRoot := filepath.Join(t.TempDir(), "runner-target")
	remoteTemp, err := os.MkdirTemp(os.TempDir(), "newplatform-ansible-remote-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(remoteTemp)

	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary, KillGrace: time.Second}
	result, err := runner.Run(context.Background(), Request{
		Playbook: "runner-test.yml", Inventory: inventory, Limit: "runner_test", Timeout: 45 * time.Second,
		Variables: map[string]any{"target_root": targetRoot, "expected_version": "1.0.0", "ansible_remote_tmp": remoteTemp},
	})
	if err != nil {
		var phaseErr *PhaseError
		if errors.As(err, &phaseErr) {
			t.Fatalf("runner test failed in %s: %v\n%s", phaseErr.Phase, err, joinedLogs(result.Logs))
		}
		t.Fatalf("runner test failed: %v", err)
	}
	if !result.Successful {
		t.Fatal("runner test returned unsuccessful result")
	}
	if recap := result.Recap["localhost"]; recap.Failed != 0 || recap.Unreachable != 0 {
		t.Fatalf("runner recap contains failures: %+v", recap)
	}
	assertVersionFile(t, targetRoot, "1.0.0")
}

func assertVersionFile(t *testing.T, root, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("VERSION = %q, want %q", got, want)
	}
}

func joinedLogs(events []LogEvent) string {
	var output strings.Builder
	for _, event := range events {
		output.WriteString(string(event.Phase))
		output.WriteString("/")
		output.WriteString(string(event.Stream))
		output.WriteString(": ")
		output.WriteString(event.Line)
		output.WriteByte('\n')
	}
	return output.String()
}
