package ansible

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDemoNodeAgentLifecycleWithAnsible(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Ansible integration test in short mode")
	}
	if os.Getenv("NEWPLATFORM_ANSIBLE_INTEGRATION") != "1" {
		t.Skip("set NEWPLATFORM_ANSIBLE_INTEGRATION=1 to run the real localhost lifecycle")
	}
	binary, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "ansible", "demo-node-agent"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := os.ReadFile(filepath.Join(root, "inventory.ini"))
	if err != nil {
		t.Fatal(err)
	}
	agentRoot := filepath.Join(os.TempDir(), fmt.Sprintf("newplatform-demo-agent-test-%d", os.Getpid()))
	remoteTemp, err := os.MkdirTemp(os.TempDir(), "newplatform-ansible-remote-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(remoteTemp)
	if err := os.RemoveAll(agentRoot); err != nil {
		t.Fatal(err)
	}

	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary, KillGrace: time.Second}
	run := func(playbook string, variables map[string]any) Result {
		t.Helper()
		if variables == nil {
			variables = make(map[string]any)
		}
		variables["agent_root"] = agentRoot
		variables["ansible_remote_tmp"] = remoteTemp
		result, err := runner.Run(context.Background(), Request{
			Playbook:  playbook,
			Inventory: inventory,
			Variables: variables,
			Limit:     "demo_nodes",
			Timeout:   45 * time.Second,
		})
		if err != nil {
			var phaseErr *PhaseError
			if errors.As(err, &phaseErr) {
				t.Fatalf("%s failed in %s: %v\n%s", playbook, phaseErr.Phase, err, joinedLogs(result.Logs))
			}
			t.Fatalf("%s failed: %v", playbook, err)
		}
		if !result.Successful {
			t.Fatalf("%s returned unsuccessful result", playbook)
		}
		return result
	}
	defer func() {
		result, cleanupErr := runner.Run(context.Background(), Request{
			Playbook:  "cleanup.yml",
			Inventory: inventory,
			Variables: map[string]any{"agent_root": agentRoot, "ansible_remote_tmp": remoteTemp},
			Limit:     "demo_nodes",
			Timeout:   45 * time.Second,
		})
		if cleanupErr != nil {
			t.Errorf("cleanup failed: %v\n%s", cleanupErr, joinedLogs(result.Logs))
		}
		_ = os.RemoveAll(agentRoot)
	}()

	run("install-v1.0.yml", nil)
	assertVersionFile(t, agentRoot, "1.0.0")
	run("upgrade-v1.1.yml", nil)
	assertVersionFile(t, agentRoot, "1.1.0")
	verified := run("verify.yml", map[string]any{"expected_version": "1.1.0"})
	if recap := verified.Recap["localhost"]; recap.Failed != 0 || recap.Unreachable != 0 {
		t.Fatalf("verify recap contains failures: %+v", recap)
	}
	run("rollback-v1.0.yml", nil)
	assertVersionFile(t, agentRoot, "1.0.0")
	run("verify.yml", map[string]any{"expected_version": "1.0.0"})
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
