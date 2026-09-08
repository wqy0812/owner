package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil/runfixture"
)

func TestSnapshotFreezeAndDeliveryResultsAreIndependent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	r := queuedRunFixture(t, s, "frozen", "freeze-env", 0)
	var before string
	if e := s.DB().QueryRow(`SELECT execution_snapshot_json FROM runs WHERE id=?`, r.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for _, state := range []domain.RunStatus{domain.RunQueued, domain.RunRunning, domain.RunFailed} {
		if _, e := s.DB().Exec(`UPDATE runs SET status=? WHERE id=?`, state, r.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB().Exec(`UPDATE runs SET execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.treeDigest','forged') WHERE id=?`, r.ID); e == nil {
			t.Fatalf("snapshot mutable in %s", state)
		}
		if state == domain.RunRunning {
			if e := s.UpdateRunDeliveryResults(ctx, r.ID, []domain.RunDeliveryResult{{RequirementID: "media", Mode: "transfer", Status: "failed"}}); e != nil {
				t.Fatal(e)
			}
		}
	}
	var after string
	if e := s.DB().QueryRow(`SELECT execution_snapshot_json FROM runs WHERE id=?`, r.ID).Scan(&after); e != nil || before != after {
		t.Fatal("observations changed snapshot", e)
	}
	got, e := s.GetRun(ctx, r.ID)
	if e != nil || len(got.DeliveryResults) != 1 || got.DeliveryResults[0].Status != "failed" {
		t.Fatal("results lost", e)
	}
}
func TestInvalidClaimFailsAtomicallyWithoutBlockingNextRun(t *testing.T) {
	for _, corruption := range []string{`json_set(execution_snapshot_json,'$.contract','unknown')`, `json_set(execution_snapshot_json,'$.plan.steps',json('[]'))`, `json_set(execution_snapshot_json,'$.plan.steps[0].timeoutSeconds','wrong')`} {
		t.Run(corruption, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			queuedRunFixture(t, s, "corrupt", "env", 0)
			queuedRunFixture(t, s, "next", "env", time.Second)
			if _, e := runfixture.CorruptSnapshot(ctx, s.DB(), `UPDATE runs SET execution_snapshot_json=`+corruption+` WHERE id='corrupt'`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.ClaimNextRun(ctx, "env", testNow); !errors.Is(e, domain.ErrInvalid) {
				t.Fatal("corrupt Run claimed", e)
			}
			var status, message string
			if e := s.DB().QueryRow(`SELECT status,error_text FROM runs WHERE id='corrupt'`).Scan(&status, &message); e != nil || status != "failed" || !strings.Contains(message, "invalid active run") {
				t.Fatalf("bad row stranded: %s %s %v", status, message, e)
			}
			next, e := s.ClaimNextRun(ctx, "env", testNow)
			if e != nil || next.ID != "next" {
				t.Fatal("queue blocked", e)
			}
		})
	}
}
func TestApprovalFinalizesSnapshotAndReferencesInOneTransaction(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	source := evidenceRun("source", "digest", "install_verify", "", "", testNow)
	source.Status = domain.RunFailed
	if e := s.CreateRun(ctx, source, nil); e != nil {
		t.Fatal(e)
	}
	run := evidenceRun("approval-snapshot", "digest", "install_verify", "", "", testNow)
	run.Status = domain.RunAwaitingApproval
	run.FinishedAt = nil
	a := domain.Approval{ID: "snapshot-approval", RunID: run.ID, Status: "pending", RequestedAt: testNow}
	if e := s.CreateRun(ctx, run, &a); e != nil {
		t.Fatal(e)
	}
	snapshot, e := run.Snapshot.Clone()
	if e != nil {
		t.Fatal(e)
	}
	snapshot.Plan.Steps[0].Backup = &domain.BackupMetadata{InstallRunID: "missing"}
	if e = s.DecideApprovalWithSnapshot(ctx, a.ID, "environment-owner-a", "approved", "window", &snapshot, nil, testNow); e == nil {
		t.Fatal("missing reference approved")
	}
	before, e := s.GetApproval(ctx, a.ID)
	if e != nil || before.Status != "pending" {
		t.Fatal("approval partially committed", e)
	}
	snapshot.Plan.Steps[0].Backup.InstallRunID = source.ID
	if e = s.DecideApprovalWithSnapshot(ctx, a.ID, "environment-owner-a", "approved", "window", &snapshot, []domain.RunDeliveryResult{}, testNow); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetRun(ctx, run.ID)
	if e != nil || got.Status != domain.RunQueued || got.Snapshot.Plan.Steps[0].Backup.InstallRunID != source.ID {
		t.Fatal("snapshot lost", e)
	}
	var ref string
	if e = s.DB().QueryRow(`SELECT referenced_run_id FROM run_snapshot_references WHERE run_id=?`, run.ID).Scan(&ref); e != nil || ref != source.ID {
		t.Fatal("reference missing", e)
	}
	if e = s.DecideApprovalWithSnapshot(ctx, a.ID, "environment-owner-a", "approved", "late", &snapshot, nil, testNow); e == nil {
		t.Fatal("approval consumed twice")
	}
}

func TestInvalidDeliveryResultsDoNotStrandClaimedRun(t *testing.T) {
	s := newTestStore(t)
	queuedRunFixture(t, s, "corrupt-result", "env", 0)
	if _, err := s.DB().Exec(`UPDATE runs SET delivery_results_json='[{"requirementId":"image","mode":"direct","status":42}]' WHERE id='corrupt-result'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextRun(context.Background(), "env", testNow); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("corrupt result claimed", err)
	}
	var status string
	if err := s.DB().QueryRow(`SELECT status FROM runs WHERE id='corrupt-result'`).Scan(&status); err != nil || status != "failed" {
		t.Fatal("invalid observation stranded Run", status, err)
	}
}

func TestInvalidRunRemainsDiagnosableAndCleanableWithoutExecutionDecode(t *testing.T) {
	for _, corruption := range []string{
		`execution_snapshot_json=json_set(execution_snapshot_json,'$.contract','unknown')`,
		`execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.steps[0].timeoutSeconds','wrong')`,
		`delivery_results_json='[{"requirementId":"image","mode":"direct","status":42}]'`,
	} {
		t.Run(corruption, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			r := queuedRunFixture(t, s, "invalid-diagnostic", "diagnostic-env", 0)
			if err := s.CreateRunStep(ctx, domain.RunStep{ID: "actual-step", RunID: r.ID, NodeID: "node", Name: "Actual step", Status: domain.RunFailed}); err != nil {
				t.Fatal(err)
			}
			if _, err := runfixture.CorruptSnapshot(ctx, s.DB(), `UPDATE runs SET `+corruption+` WHERE id=?`, r.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimNextRun(ctx, r.EnvironmentID, testNow); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("corrupt execution accepted: %v", err)
			}
			if _, err := s.GetRun(ctx, r.ID); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("strict execution read accepted corruption: %v", err)
			}
			diagnostic, err := s.GetRunDiagnosticRecord(ctx, r.ID)
			if err != nil || diagnostic.Status != domain.RunFailed || diagnostic.FinishedAt == nil || diagnostic.CreatedAt != r.CreatedAt || !strings.Contains(diagnostic.Error, "invalid active run") || len(diagnostic.Steps) != 1 {
				t.Fatalf("diagnostic record: %+v %v", diagnostic, err)
			}
			if err := s.CleanupRuns(ctx, []string{r.ID}, "admin", "manual", testNow.Add(100*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			var locks string
			if err := s.DB().QueryRow(`SELECT release_locks_json FROM run_cleanup_history WHERE id=?`, r.ID).Scan(&locks); err != nil || !strings.Contains(locks, "queue-release") {
				t.Fatalf("cleanup lost release visibility: %s %v", locks, err)
			}
		})
	}
}

func TestInvalidRunCleanupStillRejectsReferencesAndDamagedIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	r := queuedRunFixture(t, s, "invalid-referenced", "env", 0)
	child := queuedRunFixture(t, s, "referencing-run", "env", time.Second)
	if _, err := s.DB().Exec(`INSERT INTO run_snapshot_references(run_id,referenced_run_id) VALUES(?,?)`, child.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runfixture.CorruptSnapshot(ctx, s.DB(), `UPDATE runs SET execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.steps[0].timeoutSeconds','wrong') WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextRun(ctx, "env", testNow); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.CleanupRuns(ctx, []string{r.ID}, "admin", "manual", testNow.Add(100*24*time.Hour)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("referenced invalid Run removed: %v", err)
	}
	if _, err := s.DB().Exec(`DELETE FROM run_snapshot_references WHERE run_id=?`, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runfixture.CorruptSnapshot(ctx, s.DB(), `UPDATE runs SET execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.steps[0].releaseId',42) WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupRuns(ctx, []string{r.ID}, "admin", "manual", testNow.Add(100*24*time.Hour)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("damaged retained identity accepted: %v", err)
	}
	if cleaned, err := s.RunWasCleaned(ctx, r.ID); err != nil || cleaned {
		t.Fatalf("partial cleanup: %v %v", cleaned, err)
	}
}

func TestWorkbenchSelectsIdentityBeforeReadingSnapshotProjection(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	older := evidenceRun("older-unselected", "digest", "install_verify", "", "", testNow)
	current := evidenceRun("latest-selected", "digest", "install_verify", "", "", testNow.Add(time.Hour))
	for _, r := range []domain.Run{older, current} {
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Valid JSON at rest, but reading this step projection would fail. It is
	// outside the selected latest identity and must not poison the bounded read.
	if _, err := runfixture.CorruptSnapshot(ctx, s.DB(), `UPDATE runs SET execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.steps',json('["invalid step object"]')) WHERE id=?`, older.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkbenchComponentRun(ctx, domain.User{ID: "component-owner-a", Role: domain.RoleComponentOwner}, "history-release", "digest")
	if err != nil || len(rows) != 1 || rows[0].ID != current.ID {
		t.Fatalf("unselected history was read: %+v %v", rows, err)
	}
}

func TestRetainedHistoryContainsOnlyIdentityProjection(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.DB().Query(`SELECT * FROM retained_run_history LIMIT 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range columns {
		if strings.Contains(name, "snapshot") || strings.Contains(name, "steps") || strings.Contains(name, "delivery") {
			t.Fatal("history view exposes business inputs", name)
		}
	}
}
