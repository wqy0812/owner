package jobcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
)

func TestStandaloneRealControllerLoss(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	if bundle := os.Getenv("CLUSTERFORGE_CONTROLLER_LOSS_CHILD"); bundle != "" {
		if err := Run([]string{"run", bundle, "--ansible", binary, "--results", os.Getenv("CLUSTERFORGE_CONTROLLER_LOSS_RESULTS")}, os.Stdout, os.Stderr); err != nil {
			t.Fatal(err)
		}
		return
	}
	root, target, results := t.TempDir(), t.TempDir(), t.TempDir()
	pidPath, tailPath := filepath.Join(target, "group"), filepath.Join(target, "must-not-start")
	role := filepath.Join(root, "managed", "component", "line", "release")
	content := map[string]string{
		"checks/pre.yml": "- assert:\n    that: true\n",
		"install.yml":    fmt.Sprintf("- command:\n    argv:\n    - '{{ ansible_playbook_python }}'\n    - '-c'\n    - %q\n", "import os,time; open('"+pidPath+"','w').write(str(os.getpgrp())); time.sleep(60)"),
		"configure.yml":  fmt.Sprintf("- copy:\n    dest: %s\n    content: must-not-start\n", tailPath),
	}
	for name, value := range content {
		path := filepath.Join(role, "tasks", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tree, _ := ansible.TreeDigest(role)
	plan := ansible.JobPlan{Inventory: "[nodes]\nlocal ansible_connection=local\n", Metadata: map[string]any{"steps": []any{map[string]any{"id": "offline"}}}}
	for i, name := range []string{"checks/pre.yml", "install.yml", "configure.yml"} {
		digest, _ := ansible.FileDigest(filepath.Join(role, "tasks", name))
		plan.Steps = append(plan.Steps, ansible.JobStep{ID: name, NodeID: "node", ReleaseID: "release", ActionID: name, Action: []string{"check", "install", "configure"}[i], Phase: []string{"check", "execute", "execute"}[i], Limit: "nodes", Playbook: "managed/component/line/release/tasks/" + name, PlaybookDigest: digest, WorkspaceDigest: tree, TimeoutSeconds: 120})
	}
	runner := ansible.Runner{AllowedRoot: root, Binary: binary}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestStandaloneRealControllerLoss$")
	child.Env = append(os.Environ(), "CLUSTERFORGE_CONTROLLER_LOSS_CHILD="+bundle.Path, "CLUSTERFORGE_CONTROLLER_LOSS_RESULTS="+results)
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	group := 0
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if data, err := os.ReadFile(pidPath); err == nil {
			group, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if group > 1 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if group <= 1 {
		_ = child.Process.Kill()
		_ = child.Wait()
		waited = true
		t.Fatalf("body did not start: %s", output.String())
	}
	remoteGroup := group
	t.Cleanup(func() { _ = syscall.Kill(-remoteGroup, syscall.SIGKILL) })
	// Local Ansible modules may start a separate session, just as dispatched
	// remote commands can finish independently. Inspect the controller's actual
	// ansible-playbook process group, not that remote module session.
	processes, err := exec.Command("ps", "-eo", "pid,ppid,pgid").Output()
	if err != nil {
		t.Fatal(err)
	}
	group = 0
	for _, line := range strings.Split(string(processes), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		if parent == child.Process.Pid {
			group, _ = strconv.Atoi(fields[2])
			break
		}
	}
	if group <= 1 {
		t.Fatal("Ansible controller process not found")
	}
	t.Cleanup(func() { _ = syscall.Kill(-group, syscall.SIGKILL) })
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	waited = true
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		if err := syscall.Kill(-group, 0); err == syscall.ESRCH {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(-group, 0); err != syscall.ESRCH {
		details, _ := exec.Command("ps", "-eo", "pid,ppid,pgid,stat,comm").Output()
		selected := []string{}
		for _, line := range strings.Split(string(details), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 3 && fields[2] == strconv.Itoa(group) {
				selected = append(selected, line)
			}
		}
		t.Fatalf("orphaned Ansible group %d remains after controller death: %v; %v; output=%s", group, err, selected, output.String())
	}
	if _, err := os.Stat(tailPath); !os.IsNotExist(err) {
		t.Fatal("later phase started after controller death")
	}
	data, err := os.ReadFile(filepath.Join(results, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if err := validateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Actions["install.yml"] != "started" {
		t.Fatalf("uncertain body receipt=%v", receipt.Actions)
	}
	if _, err := ansible.RetryStages(receipt.Plan, receipt.Result.Steps); err == nil {
		t.Fatal("unverified body could retry after hard interruption")
	}
	lock, err := lockResults(results)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
}
