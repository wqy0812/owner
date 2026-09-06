package ansible

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestNativeJobRealInterruptedRecovery(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(signal.String(), func(t *testing.T) {
			runner, plan, target := roleJobFixture(t, binary, "")
			path := filepath.Join(runner.AllowedRoot, "managed/alpha/line/release/tasks/install.yml")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			pause := "- shell: |\n    touch '{{ cf.inputs.target }}/entered'\n    if [ ! -f '{{ cf.inputs.target }}/continue' ]; then sleep 5; fi\n"
			if err := os.WriteFile(path, append([]byte(pause), original...), 0600); err != nil {
				t.Fatal(err)
			}
			for i := range plan.Steps {
				s := &plan.Steps[i]
				s.ParentActionID = "install.yml"
				s.PlaybookDigest, _ = FileDigest(filepath.Join(runner.AllowedRoot, s.Playbook))
				s.WorkspaceDigest, _ = TreeDigest(filepath.Join(runner.AllowedRoot, "managed", s.ComponentID, "line/release"))
				s.Variables["clusterforge_backup_ref"] = filepath.Join(target, "backups/original", s.NodeID)
				s.Variables["clusterforge_backup_operation"] = "capture"
			}
			bundle, err := runner.BuildJob(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			results := filepath.Join(t.TempDir(), "results")
			command := nativeProcess(t, binary, bundle, "install", results, nil)
			log, err := os.CreateTemp(t.TempDir(), "output-")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			deadline := time.Now().Add(25 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(target, "entered")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					data, _ := os.ReadFile(log.Name())
					t.Fatalf("main did not start: %s", data)
				}
				time.Sleep(25 * time.Millisecond)
			}
			read := func() map[string]any {
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
			baseline := read()["originalPlan"]
			if output, err := nativeCommand(t, binary, bundle, "resume", results, nil); err == nil {
				t.Fatalf("concurrent resume accepted: %s", output)
			}
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err == nil {
				t.Fatal("terminated process reported success")
			}
			// Dispatched shell commands can finish, but cannot release a later phase.
			time.Sleep(6 * time.Second)
			if _, err := os.Stat(filepath.Join(target, "beta-one-c-body")); !os.IsNotExist(err) {
				t.Fatal("later component started after controller termination")
			}
			if err := os.WriteFile(filepath.Join(target, "continue"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := nativeCommand(t, binary, bundle, "resume", results, nil); err != nil {
				t.Fatalf("resume: %v\n%s", err, output)
			}
			receipt := read()
			if !reflect.DeepEqual(baseline, receipt["originalPlan"]) {
				t.Fatal("resume changed original recovery baseline")
			}
			attempts := receipt["attempts"].([]any)
			if len(attempts) != 2 || attempts[1].(map[string]any)["status"] != "succeeded" {
				t.Fatalf("lost attempt chain: %#v", attempts)
			}
			if err := bundle.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
