package jobcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
)

func TestResultLockSurvivesControllerInterruption(t *testing.T) {
	if directory := os.Getenv("CLUSTERFORGE_LOCK_TEST_CHILD"); directory != "" {
		lock, err := lockResults(directory)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		fmt.Println("locked")
		for {
			time.Sleep(time.Second)
		}
	}
	directory := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestResultLockSurvivesControllerInterruption$")
	child.Env = append(os.Environ(), "CLUSTERFORGE_LOCK_TEST_CHILD="+directory)
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child lock=%q %v", line, err)
	}
	if lock, err := lockResults(directory); err == nil {
		lock.Close()
		t.Fatal("concurrent controller acquired lock")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	lock, err := lockResults(directory)
	if err != nil {
		t.Fatalf("killed controller prevented recovery: %v", err)
	}
	defer lock.Close()
}

func TestReceiptRejectsEditsAndPreservesSourceBaseline(t *testing.T) {
	receipt := Receipt{receiptPayload: receiptPayload{BundleDigest: "locked", Plan: ansible.JobPlan{}, Actions: map[string]string{"install": "main_succeeded"}}}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := writeReceipt(path, receipt); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if err := validateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Actions["install"] = "verified"
	if err := validateReceipt(receipt); err == nil {
		t.Fatal("edited result accepted")
	}
}
func TestStandaloneBackupIdentityDoesNotRelabelExternalBaseline(t *testing.T) {
	vars := func(ref, op string) map[string]any {
		return map[string]any{"clusterforge_backup_ref": ref, "clusterforge_backup_marker": ref + "/.captured", "clusterforge_backup_operation": op, "clusterforge_backup_metadata": map[string]any{"install_run_id": "external"}}
	}
	plan := ansible.JobPlan{Steps: []ansible.JobStep{{Phase: "execute", Variables: vars("/base/preview/node-a", "capture")}, {Phase: "execute", Variables: vars("/base/prior/node-b", "restore")}}, Recovery: []ansible.JobStep{{Variables: vars("/base/preview/node-a", "restore")}}}
	assignBackupIdentity(&plan, "local-run")
	if plan.Steps[0].Variables["clusterforge_backup_ref"] != "/base/local-run/node-a" || plan.Recovery[0].Variables["clusterforge_backup_ref"] != "/base/local-run/node-a" {
		t.Fatal("capture and recovery diverged")
	}
	if plan.Steps[1].Variables["clusterforge_backup_ref"] != "/base/prior/node-b" || plan.Steps[1].Variables["clusterforge_backup_metadata"].(map[string]any)["install_run_id"] != "external" {
		t.Fatal("external baseline was relabeled")
	}
}
func TestPostOnlyRecoveryMarksExactSourceAction(t *testing.T) {
	main := ansible.JobStep{ID: "rollback-main", NodeID: "node", ActionID: "rollback", Action: "rollback", Phase: "execute", RecoveryOfStepID: "upgrade-main", Variables: map[string]any{"clusterforge_backup_ref": "upgrade-backup"}}
	post := ansible.JobStep{ID: "rollback-post", NodeID: "node", ParentActionID: "rollback", Phase: "post", RecoveryOfStepID: "upgrade-main"}
	receipt := Receipt{OriginalPlan: ansible.JobPlan{Recovery: []ansible.JobStep{main, post}}, receiptPayload: receiptPayload{Plan: ansible.JobPlan{Steps: []ansible.JobStep{post}}, Actions: map[string]string{"install-main": "verified", "upgrade-main": "verified"}}}
	updateActionReceipt(&receipt, "end", post)
	if receipt.Actions["upgrade-main"] != "rolled_back" || receipt.Actions["install-main"] != "verified" {
		t.Fatalf("recovery scope=%v", receipt.Actions)
	}
}
