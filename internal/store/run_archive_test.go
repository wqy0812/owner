package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestArchiveBoundaryLeaseAndLogSafety(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	r := evidenceRun("archive-test", "digest", "install_verify", "", "", now.Add(-100*24*time.Hour))
	if err := s.CreateRun(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "stdout", Message: "first", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.EnqueueRunArchives(ctx, []string{r.ID}, "manual", "component-owner-a", now)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task, err := s.ClaimRunArchive(ctx, "token-one", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimRunArchive(ctx, "token-two", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("concurrent lease", err)
	}
	boundary, err := s.ExportRunArchive(ctx, r.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	task.RelativePath = "test.tar.gz"
	task.FormatVersion = "v1"
	task.SHA256 = "checksum"
	task.SizeBytes = 10
	if _, err = s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "stdout", Message: "late", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteRunArchive(ctx, task, boundary, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed logs accepted", err)
	}
	logs, _ := s.ListRunLogs(ctx, r.ID, 0, 100)
	if len(logs) != 2 {
		t.Fatal("logs removed before commit")
	}
	boundary, err = s.ExportRunArchive(ctx, r.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteRunArchive(ctx, task, boundary, now); err != nil {
		t.Fatal(err)
	}
	logs, _ = s.ListRunLogs(ctx, r.ID, 0, 100)
	if len(logs) != 0 {
		t.Fatal(logs)
	}
	if _, err = s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "stdout", Message: "too late", CreatedAt: now}); err == nil {
		t.Fatal("archived log mutation accepted")
	}
	if _, err = s.GetRun(ctx, r.ID); err != nil {
		t.Fatal("core removed", err)
	}
	if id, err := s.lookupComponentEvidence(ctx, r.ComponentReleaseID, "digest", "install_verify"); err != nil || id != r.ID {
		t.Fatal("archive lost publication evidence", id, err)
	}
	tasks, err = s.EnqueueRunArchives(ctx, []string{r.ID}, "manual", "component-owner-a", now)
	if err != nil || tasks[0].Status != "archived" {
		t.Fatal("not idempotent", tasks, err)
	}
}

func TestRetentionStateDeadlineAndBatchGuards(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for _, status := range []domain.RunStatus{domain.RunSucceeded, domain.RunFailed, domain.RunCancelled, domain.RunRejected, domain.RunInterrupted, domain.RunQueued, domain.RunRunning, domain.RunAwaitingApproval} {
		r := evidenceRun(string(status), "digest", "install_verify", "", "", now.Add(-91*24*time.Hour))
		r.Status = status
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
		_, err := s.EnqueueRunArchives(ctx, []string{r.ID}, "manual", "component-owner-a", now)
		if (err == nil) != (status == domain.RunSucceeded) {
			t.Fatalf("archive %s: %v", status, err)
		}
		preview, err := s.PreviewRunCleanup(ctx, []string{r.ID}, now)
		if err != nil || preview[0].Eligible != (status == domain.RunFailed) {
			t.Fatalf("cleanup %s: %v %v", status, preview, err)
		}
	}
	for _, offset := range []time.Duration{-90 * 24 * time.Hour, -90*24*time.Hour - time.Second} {
		r := evidenceRun(fmt.Sprint(offset), "digest", "install_verify", "", "", now.Add(offset))
		r.Status = domain.RunFailed
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
		preview, err := s.PreviewRunCleanup(ctx, []string{r.ID}, now)
		if err != nil || preview[0].Eligible != (offset < -90*24*time.Hour) {
			t.Fatal(preview, err)
		}
	}
	r := evidenceRun("missing-finish", "digest", "install_verify", "", "", now)
	r.FinishedAt = nil
	if err := s.CreateRun(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueRunArchives(ctx, []string{r.ID}, "manual", "component-owner-a", now); err == nil {
		t.Fatal("missing finish accepted")
	}
	for _, ids := range [][]string{nil, {"failed", "failed"}, make([]string, 101)} {
		if _, err := s.EnqueueRunArchives(ctx, ids, "manual", "component-owner-a", now); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
		if err := s.CleanupRuns(ctx, ids, "component-owner-a", "manual", now); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestArchiveExpiredLeaseAndTransactionFailureKeepLogs(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	r := evidenceRun("recover", "digest", "install_verify", "", "", now)
	if err := s.CreateRun(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Message: "retained", Stream: "stdout", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueRunArchives(ctx, []string{r.ID}, "manual", "component-owner-a", now); err != nil {
		t.Fatal(err)
	}
	old, err := s.ClaimRunArchive(ctx, "crashed", now)
	if err != nil {
		t.Fatal(err)
	}
	newTask, err := s.ClaimRunArchive(ctx, "recovered", now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := s.ExportRunArchive(ctx, r.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteRunArchive(ctx, old, boundary, now.Add(6*time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old writer accepted", err)
	}
	newTask.RelativePath = "recover.tar.gz"
	newTask.FormatVersion = "v1"
	newTask.SHA256 = "hash"
	newTask.SizeBytes = 10
	if _, err = s.db.Exec(`CREATE TRIGGER reject_archive_commit BEFORE INSERT ON run_archive_files BEGIN SELECT RAISE(ABORT,'simulated failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteRunArchive(ctx, newTask, boundary, now.Add(6*time.Minute)); err == nil {
		t.Fatal("failure not injected")
	}
	logs, err := s.ListRunLogs(ctx, r.ID, 0, 100)
	if err != nil || len(logs) != 1 {
		t.Fatal("premature log loss", logs, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_archive_commit`); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteRunArchive(ctx, newTask, boundary, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupConcurrentReferenceAndRetainedLifecycle(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := evidenceHistoryStore(t, 0)
		ctx := context.Background()
		now := time.Now().UTC()
		r := evidenceRun("parent", "digest", "install_verify", "", "", now.Add(-91*24*time.Hour))
		r.Status = domain.RunFailed
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
		child := evidenceRun("child", "digest", "install_verify", "", "", now)
		child.RetryOfRunID = r.ID
		start := make(chan struct{})
		result := make(chan error, 2)
		go func() { <-start; result <- s.CleanupRuns(ctx, []string{r.ID}, "component-owner-a", "manual", now) }()
		go func() { <-start; result <- s.CreateRun(ctx, child, nil) }()
		close(start)
		a, b := <-result, <-result
		if (a == nil) == (b == nil) {
			t.Fatalf("expected exactly one competing write: %v / %v", a, b)
		}
		var dangling int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs c LEFT JOIN runs p ON p.id=c.retry_of_run_id WHERE c.retry_of_run_id IS NOT NULL AND p.id IS NULL`).Scan(&dangling); err != nil || dangling != 0 {
			t.Fatal(dangling, err)
		}
		impact, err := s.ComponentReleaseDeletionImpact(ctx, r.ComponentReleaseID)
		if err != nil || impact.RunCount < 1 {
			t.Fatal("lost component history", impact, err)
		}
		env, err := s.EnvironmentLifecycleImpact(ctx, r.EnvironmentID)
		if err != nil || env.RunCount < 1 {
			t.Fatal("lost environment history", env, err)
		}
	}
}
func TestCleanupAtomicHistoryAndReferences(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	create := func(id string, at time.Time) domain.Run {
		r := evidenceRun(id, "digest", "install_verify", "", "", at)
		r.Status = domain.RunFailed
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
		return r
	}
	old := create("old", now.Add(-91*24*time.Hour))
	fresh := create("fresh", now)
	if err := s.CleanupRuns(ctx, []string{old.ID, fresh.ID}, "component-owner-a", "manual", now); err == nil {
		t.Fatal("partial batch cleanup")
	}
	if _, err := s.GetRun(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	child := create("child", now)
	child.RetryOfRunID = old.ID
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.steps',json(?)) WHERE id=?`, `[{"backup":{"installRunId":"old"}}]`, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewRunCleanup(ctx, []string{old.ID}, now)
	if err != nil || preview[0].Eligible {
		t.Fatal(preview, err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE runs SET input_snapshot_json='{}' WHERE id=?`, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupRuns(ctx, []string{old.ID}, "component-owner-a", "manual", now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetRun(ctx, old.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	visible, err := s.CanViewRun(ctx, domain.User{ID: "component-owner-a", Role: domain.RoleComponentOwner}, old.ID)
	if err != nil || !visible {
		t.Fatal("lost history permission", err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM run_cleanup_history WHERE id=?`, old.ID); err == nil {
		t.Fatal("mutable audit history")
	}
	retry := create("retry-prepared", now)
	retry.ID = "retry-new"
	retry.RetryOfRunID = old.ID
	if err = s.CreateRun(ctx, retry, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("dangling retry accepted", err)
	}
}

func TestRunReferencesDoNotInterpretBusinessParameterNames(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	old := evidenceRun("old-business-reference", "digest", "install_verify", "", "", now.Add(-91*24*time.Hour))
	old.Status = domain.RunFailed
	if err := s.CreateRun(ctx, old, nil); err != nil {
		t.Fatal(err)
	}
	r := evidenceRun("business-parameters", "digest", "install_verify", "", "", now)
	r.InputSnapshot["steps"] = []any{map[string]any{"variables": map[string]any{
		"runId": "external-job-42", "config": map[string]any{"installRunId": old.ID},
	}}}
	r.InputSnapshot["resolvedParametersByNode"] = map[string]any{"node": map[string]any{
		"runId":  map[string]any{"value": "external-job-42"},
		"config": map[string]any{"value": map[string]any{"sourceRunId": old.ID}},
	}}
	if err := s.CreateRun(ctx, r, nil); err != nil {
		t.Fatalf("ordinary business parameters treated as platform Run references: %v", err)
	}
	preview, err := s.PreviewRunCleanup(ctx, []string{old.ID}, now)
	if err != nil || !preview[0].Eligible {
		t.Fatalf("ordinary parameter value blocked cleanup: %+v, %v", preview, err)
	}
	if err := s.CleanupRuns(ctx, []string{old.ID}, "component-owner-a", "manual", now); err != nil {
		t.Fatal(err)
	}
}
