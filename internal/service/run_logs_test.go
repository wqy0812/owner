package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func runLogFixture(t *testing.T, status domain.RunStatus) (*Platform, *store.Store, domain.User, domain.Run) {
	t.Helper()
	p, db := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.User{ID: "logs-owner", Name: "Log owner", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if e := db.UpsertUser(ctx, owner); e != nil {
		t.Fatal(e)
	}
	env, e := p.environments.Create(ctx, owner, domain.Environment{Name: "Log fixture"}, completeServiceTestFacts())
	if e != nil {
		t.Fatal(e)
	}
	run := domain.Run{ID: "diagnostic-run", Kind: domain.RunScenario, Status: status, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, CreatedAt: now, InputSnapshot: map[string]any{"password": "NEVER-EXPORT", "steps": []any{map[string]any{"nodeId": "locked-check", "componentName": "CoreDNS", "phase": "post"}}}}
	if status != domain.RunRunning {
		run.FinishedAt = &now
	}
	storedRun := run
	if status == domain.RunRunning {
		storedRun.Status = domain.RunFailed
	}
	if e = db.CreateRun(ctx, storedRun, nil); e != nil {
		t.Fatal(e)
	}
	if status == domain.RunRunning {
		if _, e = db.DB().ExecContext(ctx, `UPDATE runs SET status='running' WHERE id=?`, run.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.CreateRunStep(ctx, domain.RunStep{ID: "step-failed", RunID: run.ID, NodeID: "locked-check", Name: "CoreDNS check", Status: status, StartedAt: &now}); e != nil {
		t.Fatal(e)
	}
	return p, db, owner, run
}
func appendDiagnosticLog(t *testing.T, db *store.Store, run domain.Run, stream, message string) {
	t.Helper()
	if _, err := db.AppendRunLog(context.Background(), domain.RunLog{RunID: run.ID, StepID: "step-failed", Stream: stream, Message: message, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}
func diagnosticEvent(host string) string {
	data, _ := json.Marshal(map[string]any{"kind": "result", "host": host, "task": "rollback-post check", "status": "failed", "result": map[string]any{"msg": "non-zero return code", "rc": 1, "stderr": "Shared connection to 192.0.2.59 closed.\r\n", "stdout": "Traceback (most recent call last):\n  File runtime.py, line 118\nRuntimeError: The connection to the server 192.0.2.59:6443 was refused\n", "password": "NEVER-EXPORT"}})
	return string(data)
}
func TestRunDiagnosticsFindsDurableFailuresBeforeTail(t *testing.T) {
	p, db, user, run := runLogFixture(t, domain.RunFailed)
	appendDiagnosticLog(t, db, run, "event", diagnosticEvent("master-4"))
	appendDiagnosticLog(t, db, run, "event", diagnosticEvent("master-5"))
	for i := 0; i < 250; i++ {
		appendDiagnosticLog(t, db, run, "stdout", fmt.Sprintf("master-%d : ok=1 changed=0 unreachable=0 failed=1", i))
	}
	got, e := p.Execution().RunDiagnostics(context.Background(), user, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.LogCount != 252 || len(got.Items) != 2 {
		t.Fatalf("unexpected counts: %#v", got)
	}
	first := got.Items[0]
	if first.Host != "master-4" || first.Component != "CoreDNS" || first.StepID != "step-failed" || first.Phase != "post" || !strings.Contains(first.Message, "6443 was refused") || strings.Contains(first.Raw, "NEVER-EXPORT") {
		t.Fatalf("wrong primary error: %#v", first)
	}
	if _, e = p.Execution().RunDiagnostics(context.Background(), domain.User{ID: "outsider", Role: domain.RoleComponentOwner}, run.ID); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
}
func TestRunDiagnosticTextFallbackAndProtectedOutput(t *testing.T) {
	tests := []struct{ name, line, stream, want string }{
		{"legacy fatal", `fatal: [host]: FAILED! => {"msg":"DNS resolution failed"}`, "stdout", "DNS resolution failed"},
		{"unreachable", `fatal: [host]: UNREACHABLE! => {"msg":"SSH connection timed out"}`, "stdout", "timed out"},
		{"startup", "ERROR! syntax error at role/tasks/main.yml", "stderr", "syntax error"},
		{"timeout", "task timed out after 300s", "system", "timed out"},
		{"protected", `{"kind":"result","status":"failed","task":"SECRET-TASK","result":{"_ansible_no_log":true,"stdout":"SECRET-VALUE"}}`, "event", "no_log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c diagnosticCollector
			c.add(cleanRunLog(domain.RunLog{ID: 1, Stream: tt.stream, Message: tt.line}))
			c.add(domain.RunLog{ID: 2, Stream: "stdout", Message: "host : ok=0 unreachable=0 failed=1"})
			items := c.finish(domain.Run{Status: domain.RunFailed})
			data, _ := json.Marshal(items)
			if len(items) != 1 || !strings.Contains(items[0].Message, tt.want) || strings.Contains(string(data), "SECRET-") {
				t.Fatal(string(data))
			}
		})
	}
	var c diagnosticCollector
	c.add(domain.RunLog{ID: 1, Stream: "stderr", Message: "ERROR! " + strings.Repeat("long ", 20000) + "final cause"})
	items := c.finish(domain.Run{Status: domain.RunFailed})
	if !items[0].Truncated || !strings.Contains(items[0].Message, "final cause") {
		t.Fatal("long error lost its cause")
	}
	for _, status := range []domain.RunStatus{domain.RunInterrupted, domain.RunFailed} {
		var empty diagnosticCollector
		items := empty.finish(domain.Run{Status: status, Error: "executor stopped unexpectedly"})
		if len(items) != 1 || items[0].Message != "executor stopped unexpectedly" {
			t.Fatal(items)
		}
	}
}

func TestRunDiagnosticMultilineLegacyAndLaterTasks(t *testing.T) {
	var c diagnosticCollector
	lines := []string{
		"TASK [Check Kubernetes API] ****************",
		"fatal: [master-4]: FAILED! => {",
		`  "msg": "non-zero return code",`,
		`  "stdout": "Traceback\nRuntimeError: The connection to server 192.0.2.59:6443 was refused",`,
		`  "rc": 1`,
		"}",
		"PLAY RECAP ****************",
		"master-4 : ok=0 unreachable=0 failed=1",
	}
	for i, line := range lines {
		c.add(domain.RunLog{ID: int64(i + 1), StepID: "step", Stream: "stdout", Message: line})
	}
	items := c.finish(domain.Run{Status: domain.RunFailed})
	if len(items) != 1 || items[0].LogID != 2 || items[0].Task != "Check Kubernetes API" || !strings.Contains(items[0].Message, "6443 was refused") || items[0].ExitCode == nil || *items[0].ExitCode != 1 {
		t.Fatalf("multiline legacy diagnostic: %+v", items)
	}
	var events diagnosticCollector
	for i, task := range []string{"Check API", "Check DNS"} {
		data, _ := json.Marshal(map[string]any{"kind": "result", "status": "failed", "host": "master-4", "task": task, "result": map[string]any{"msg": "connection refused"}})
		events.add(domain.RunLog{ID: int64(i + 1), Stream: "event", Message: string(data)})
	}
	events.add(domain.RunLog{ID: 3, Stream: "stdout", Message: `fatal: [master-4]: FAILED! => {"msg":"connection refused"}`})
	if items := events.finish(domain.Run{Status: domain.RunFailed}); len(items) != 2 {
		t.Fatalf("later task lost or duplicate text retained: %+v", items)
	}
}
func readLogBundle(t *testing.T, bundle *RunLogBundle) map[string]string {
	t.Helper()
	gz, e := gzip.NewReader(bundle.File)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		out[h.Name] = string(data)
	}
	return out
}
func TestRunLogBundleCompleteSnapshotAndCleanup(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunRunning, domain.RunFailed, domain.RunInterrupted, domain.RunSucceeded} {
		t.Run(string(status), func(t *testing.T) {
			p, db, user, run := runLogFixture(t, status)
			appendDiagnosticLog(t, db, run, "event", diagnosticEvent("host"))
			for i := 0; i < 250; i++ {
				appendDiagnosticLog(t, db, run, "stdout", fmt.Sprintf("line-%d", i))
			}
			bundle, e := p.Execution().RunLogBundle(context.Background(), user, run.ID)
			if e != nil {
				t.Fatal(e)
			}
			dir := bundle.directory
			files := readLogBundle(t, bundle)
			bundle.Close()
			if _, e = os.Stat(dir); !os.IsNotExist(e) {
				t.Fatal("temporary bundle remains", e)
			}
			if len(files) != 6 || strings.Count(files["logs.ndjson"], "\n") != 251 || !strings.Contains(files["logs.txt"], "line-0\n") || !strings.Contains(files["logs.txt"], "line-249\n") {
				t.Fatal("incomplete bundle")
			}
			for name, content := range files {
				if strings.Contains(content, "NEVER-EXPORT") {
					t.Fatal("secret in", name)
				}
			}
			var manifest struct {
				LogCount, LastLogID int64
				Snapshot            bool
			}
			if e = json.Unmarshal([]byte(files["manifest.json"]), &manifest); e != nil {
				t.Fatal(e)
			}
			if manifest.LogCount != 251 || manifest.LastLogID == 0 || manifest.Snapshot != (status == domain.RunRunning) {
				t.Fatal(manifest)
			}
			if rows, e := db.ListRunLogs(context.Background(), run.ID, 0, 500); e != nil || len(rows) != 251 {
				t.Fatal("export changed source logs", e)
			}
			if _, e = p.Execution().RunLogBundle(context.Background(), domain.User{ID: "outsider"}, run.ID); !errors.Is(e, domain.ErrForbidden) {
				t.Fatal(e)
			}
		})
	}
}
func TestRunLogBundleArchiveSourceAndCorruption(t *testing.T) {
	p, db, user, run := runLogFixture(t, domain.RunSucceeded)
	ctx := context.Background()
	appendDiagnosticLog(t, db, run, "stdout", "before archive")
	root := t.TempDir()
	if e := p.ConfigureRunArchives(root); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Execution().ArchiveRuns(ctx, domain.User{ID: "admin", Role: domain.RolePlatformAdmin}, []string{run.ID}); e != nil {
		t.Fatal(e)
	}
	task, e := db.ClaimRunArchive(ctx, "logs-test", time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.archives.archiveOne(ctx, task); e != nil {
		t.Fatal(e)
	}
	bundle, e := p.Execution().RunLogBundle(ctx, user, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	files := readLogBundle(t, bundle)
	bundle.Close()
	if !strings.Contains(files["logs.txt"], "before archive") {
		t.Fatal("archived logs missing")
	}
	archive, e := db.GetRunArchive(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, archive.RelativePath)
	if e = os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execution().RunLogBundle(ctx, user, run.ID); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("corrupt archive accepted", e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execution().RunDiagnostics(ctx, user, run.ID); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("missing archive accepted", e)
	}
}

func TestRunLogSnapshotKeepsConcurrentWritesOutsideBoundary(t *testing.T) {
	_, source, _, run := runLogFixture(t, domain.RunFailed)
	appendDiagnosticLog(t, source, run, "stdout", "before snapshot")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := source.DB().ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	visited := 0
	snapshot, err := db.StreamRunLogSnapshot(ctx, run.ID, func(l domain.RunLog) error {
		visited++
		if _, err := db.AppendRunLog(ctx, domain.RunLog{RunID: run.ID, Stream: "stdout", Message: "after snapshot", CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		_, err := db.DB().ExecContext(ctx, `UPDATE runs SET error_text='changed after snapshot' WHERE id=?`, run.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited != 1 || snapshot.LogCount != 1 || snapshot.Run.Error != "" {
		t.Fatalf("mixed snapshot: %+v, visited %d", snapshot, visited)
	}
	current, err := db.GetRun(ctx, run.ID)
	if err != nil || current.Error != "changed after snapshot" {
		t.Fatal(current.Error, err)
	}
	logs, err := db.ListRunLogs(ctx, run.ID, 0, 100)
	if err != nil || len(logs) != 2 {
		t.Fatal(logs, err)
	}
}
