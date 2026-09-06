// Package jobcli is the standalone frontend for the same role-job executor
// used by the platform. It never connects to the platform API.
package jobcli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/service"
)

type Receipt struct {
	Checksum     string          `json:"checksum"`
	Operation    string          `json:"operation"`
	OriginalPlan ansible.JobPlan `json:"originalPlan"`
	History      []Attempt       `json:"history"`
	receiptPayload
}
type Attempt struct {
	Plan   ansible.JobPlan   `json:"plan"`
	Result ansible.JobResult `json:"result"`
}
type receiptPayload struct {
	BundleDigest string            `json:"bundleDigest"`
	Plan         ansible.JobPlan   `json:"plan"`
	Result       ansible.JobResult `json:"result"`
	Actions      map[string]string `json:"actions"`
	Attempts     int               `json:"attempts"`
}

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: clusterforge-job run|resume|rollback-preview|rollback BUNDLE_DIR --credentials FILE --results DIRECTORY")
	}
	operation, directory := args[0], args[1]
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	credentialsPath := flags.String("credentials", "", "private JSON credentials file")
	resultDir := flags.String("results", "", "private result directory")
	binary := flags.String("ansible", "ansible-playbook", "Ansible binary matching manifest")
	nodes := flags.String("nodes", "", "comma-separated componentId/nodeId scope in reverse dependency order")
	expected := flags.String("expected-plan-digest", "", "rollback digest returned by rollback-preview")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *resultDir == "" {
		return fmt.Errorf("--results is required")
	}
	bundle, err := ansible.OpenJob(directory)
	if err != nil {
		return err
	}
	if operation != "rollback-preview" {
		if err := os.MkdirAll(*resultDir, 0700); err != nil {
			return err
		}
		lock, err := lockResults(*resultDir)
		if err != nil {
			return fmt.Errorf("result directory is in use: %w", err)
		}
		defer lock.Close()
	}
	receiptPath := filepath.Join(*resultDir, "receipt.json")
	receipt := Receipt{receiptPayload: receiptPayload{BundleDigest: bundle.Manifest.Digest, Plan: ansible.CloneJobPlan(bundle.Manifest.Plan), Actions: map[string]string{}}}
	if operation != "run" {
		data, err := os.ReadFile(receiptPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &receipt); err != nil {
			return err
		}
		if err := validateReceipt(receipt); err != nil {
			return err
		}
		if receipt.BundleDigest != bundle.Manifest.Digest {
			return fmt.Errorf("receipt belongs to a different job bundle")
		}
	} else if _, err := os.Stat(receiptPath); err == nil {
		return fmt.Errorf("results already exist; use resume or a fresh job bundle")
	}
	plan := ansible.CloneJobPlan(receipt.Plan)
	switch operation {
	case "run":
		assignBackupIdentity(&plan, fmt.Sprintf("standalone-%d", time.Now().UTC().UnixNano()))
		receipt.OriginalPlan = ansible.CloneJobPlan(plan)
		receipt.Operation = "run"
	case "resume":
		stages, err := ansible.RetryStages(plan, receipt.Result.Steps)
		if err != nil {
			return err
		}
		plan.Steps = stages
	case "rollback-preview", "rollback":
		if receipt.Operation == "rollback" && !allStagesSucceeded(receipt.Plan, receipt.Result.Steps) {
			return fmt.Errorf("rollback already started; use resume with its saved results")
		}
		plan = ansible.CloneJobPlan(receipt.OriginalPlan)
		stages := []ansible.JobStep{}
		for _, step := range plan.Recovery {
			if status := receipt.Actions[step.RecoveryOfStepID]; status != "" && status != "rolled_back" {
				stages = append(stages, step)
			}
		}
		if len(stages) == 0 {
			return fmt.Errorf("no touched component remains to roll back")
		}
		selectedNodes := []string{}
		if *nodes != "" {
			selectedNodes = strings.Split(*nodes, ",")
		}
		stages, err = ansible.SelectRollbackStages(stages, selectedNodes)
		if err != nil {
			return err
		}
		plan.Steps = stages
		digest := service.DigestStandalonePlan(plan)
		if operation == "rollback-preview" {
			return json.NewEncoder(stdout).Encode(map[string]any{"planDigest": digest, "steps": stages})
		}
		if *expected == "" || *expected != digest {
			return fmt.Errorf("rollback requires --expected-plan-digest from rollback-preview")
		}
		receipt.Operation = "rollback"
	default:
		return fmt.Errorf("unknown operation %s", operation)
	}
	credentials := map[string]any{}
	if *credentialsPath != "" {
		data, err := os.ReadFile(*credentialsPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &credentials); err != nil {
			return err
		}
	}
	for _, name := range plan.RequiredCredentials {
		if value, ok := credentials[name]; !ok || value == nil || value == "" {
			return fmt.Errorf("missing credential %s", name)
		}
	}
	connections := map[string]any{}
	secrets := []string{}
	for k, v := range credentials {
		if strings.HasPrefix(k, "ansible_") {
			connections[k] = v
		}
		if text, ok := v.(string); ok {
			secrets = append(secrets, text)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	runner := &ansible.Runner{Binary: *binary}
	compiled, err := runner.RebuildJob(ctx, bundle, plan)
	if err != nil {
		return err
	}
	defer compiled.Close()
	deferMedia := false
	for _, step := range plan.Steps {
		deferMedia = deferMedia || step.Stage == "source_verify"
	}
	mediaReady := false
	prepareMedia := func() error {
		if mediaReady {
			return nil
		}
		if err := service.PrepareStandaloneJobMedia(ctx, plan.Metadata); err != nil {
			return err
		}
		mediaReady = true
		return nil
	}
	if !deferMedia {
		if err := prepareMedia(); err != nil {
			return err
		}
	}
	if receipt.Attempts > 0 {
		receipt.History = append(receipt.History, Attempt{Plan: receipt.Plan, Result: receipt.Result})
	}
	receipt.Plan = plan
	receipt.Attempts++
	receipt.Result = ansible.JobResult{}
	persist := func() error { return writeReceipt(receiptPath, receipt) }
	if err := persist(); err != nil {
		return err
	}
	logs, err := os.OpenFile(filepath.Join(*resultDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logs.Close()
	var logMu sync.Mutex
	var logErr error
	appendLog := func(value any) error {
		logMu.Lock()
		defer logMu.Unlock()
		if logErr != nil {
			return logErr
		}
		logErr = json.NewEncoder(logs).Encode(value)
		if logErr == nil {
			logErr = logs.Sync()
		}
		if logErr != nil {
			cancel()
		}
		return logErr
	}
	request := ansible.JobRequest{Credentials: credentials, ConnectionVariables: connections, SecretValues: secrets}
	if deferMedia {
		request.StagePreparationTimeout = 30 * time.Minute
	}
	request.LogSink = func(event ansible.LogEvent) { _ = appendLog(event); fmt.Fprintln(stdout, event.Line) }
	request.OnEvent = func(event ansible.JobEvent) error { return appendLog(event) }
	request.OnBoundary = func(_ context.Context, kind string, step ansible.JobStep, result ansible.JobStepResult) error {
		if kind == "begin" {
			if deferMedia && step.Stage != "source_verify" {
				if err := prepareMedia(); err != nil {
					return err
				}
			}
			receipt.Result.Steps = append(receipt.Result.Steps, result)
		} else {
			for i := range receipt.Result.Steps {
				if receipt.Result.Steps[i].StepID == step.ID {
					receipt.Result.Steps[i] = result
				}
			}
		}
		updateActionReceipt(&receipt, kind, step)

		return persist()
	}
	result, runErr := runner.RunBundle(ctx, compiled, request)
	logMu.Lock()
	if logErr != nil {
		runErr = fmt.Errorf("persist execution log: %w", logErr)
	}
	logMu.Unlock()
	receipt.Result = result
	if err := persist(); err != nil {
		return err
	}
	return runErr
}

// Keep the inode in place: removing a flock file lets concurrent processes lock
// different inodes. The kernel releases this lock even when the CLI is killed.
func lockResults(directory string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func allStagesSucceeded(plan ansible.JobPlan, results []ansible.JobStepResult) bool {
	completed := map[string]bool{}
	for _, result := range results {
		completed[result.StepID] = result.Status == "succeeded"
	}
	for _, step := range plan.Steps {
		if !completed[step.ID] {
			return false
		}
	}
	return len(plan.Steps) > 0
}
func writeReceipt(path string, value Receipt) error {
	value.Checksum = ""
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	value.Checksum = fmt.Sprintf("%x", sha256.Sum256(raw))
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".receipt-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func assignBackupIdentity(plan *ansible.JobPlan, id string) {
	capturedAt := time.Now().UTC().Format(time.RFC3339Nano)
	replacements := map[string]string{}
	for _, step := range plan.Steps {
		if step.Phase == "execute" && step.Variables["clusterforge_backup_operation"] == "capture" {
			if old, ok := step.Variables["clusterforge_backup_ref"].(string); ok && old != "" {
				replacements[old] = filepath.ToSlash(filepath.Join(filepath.Dir(filepath.Dir(old)), id, filepath.Base(old)))
			}
		}
	}
	for _, steps := range [][]ansible.JobStep{plan.Steps, plan.Recovery} {
		for i := range steps {
			data, _ := json.Marshal(steps[i].Variables)
			text := string(data)
			for old, newValue := range replacements {
				text = strings.ReplaceAll(text, old, newValue)
			}
			_ = json.Unmarshal([]byte(text), &steps[i].Variables)
			if metadata, ok := steps[i].Variables["clusterforge_backup_metadata"].(map[string]any); ok {
				for _, replacement := range replacements {
					if steps[i].Variables["clusterforge_backup_ref"] == replacement {
						metadata["install_run_id"] = id
						metadata["captured_at"] = capturedAt
					}
				}
			}
		}
	}
	data, _ := json.Marshal(plan.Metadata)
	text := string(data)
	for old, newValue := range replacements {
		text = strings.ReplaceAll(text, old, newValue)
	}
	_ = json.Unmarshal([]byte(text), &plan.Metadata)
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for _, replacement := range replacements {
				if node["backupRef"] == replacement {
					if _, ok := node["installRunId"]; ok {
						node["installRunId"] = id
					}
					if backup, ok := node["backup"].(map[string]any); ok {
						backup["installRunId"] = id
						backup["capturedAt"] = capturedAt
					}
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(plan.Metadata)

}

func validateReceipt(receipt Receipt) error {
	expected := receipt.Checksum
	receipt.Checksum = ""
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if expected == "" || expected != fmt.Sprintf("%x", sha256.Sum256(data)) {
		return fmt.Errorf("execution receipt checksum mismatch")
	}
	return nil
}

func updateActionReceipt(receipt *Receipt, boundary string, step ansible.JobStep) {
	if step.Phase == "execute" {
		status := "started"
		if boundary == "end" {
			status = "main_succeeded"
		}
		receipt.Actions[step.ID] = status
	}
	if step.Phase != "post" || boundary != "end" {
		return
	}
	var parent *ansible.JobStep
	for _, list := range [][]ansible.JobStep{receipt.Plan.Steps, receipt.OriginalPlan.Steps, receipt.OriginalPlan.Recovery} {
		for i := range list {
			candidate := &list[i]
			if candidate.Phase == "execute" && candidate.NodeID == step.NodeID && candidate.ActionID == step.ParentActionID && candidate.RecoveryOfStepID == step.RecoveryOfStepID {
				parent = candidate
				break
			}
		}
		if parent != nil {
			break
		}
	}
	if parent == nil {
		return
	}
	receipt.Actions[parent.ID] = "verified"
	if parent.Action == "rollback" || parent.Action == "uninstall" {
		if step.RecoveryOfStepID != "" {
			receipt.Actions[step.RecoveryOfStepID] = "rolled_back"
		}
		for _, original := range receipt.OriginalPlan.Steps {
			if original.Phase == "execute" && original.Variables["clusterforge_backup_operation"] == "capture" && original.Variables["clusterforge_backup_ref"] == parent.Variables["clusterforge_backup_ref"] {
				receipt.Actions[original.ID] = "rolled_back"
			}
		}
	}
}
