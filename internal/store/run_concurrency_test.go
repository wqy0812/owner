package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func queuedRunFixture(t *testing.T, s *Store, id, environmentID string, offset time.Duration) domain.Run {
	t.Helper()
	ctx := context.Background()
	if _, err := s.GetComponent(ctx, "queue-component", false); errors.Is(err, domain.ErrNotFound) {
		if err := s.CreateComponent(ctx, componentFixture("queue-component", "component-owner-a")); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateComponentRelease(ctx, releaseFixture("queue-release", "queue-component", "1.0.0", domain.ReleaseReleased)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GetEnvironment(ctx, environmentID, false); errors.Is(err, domain.ErrNotFound) {
		env := domain.Environment{ID: environmentID, Name: environmentID, OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
		rev := domain.EnvironmentRevision{ID: environmentID + "-r1", EnvironmentID: environmentID, Revision: 1, Facts: map[string]any{"architecture": "amd64"}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
		if err := s.CreateEnvironment(ctx, env, rev); err != nil {
			t.Fatal(err)
		}
	}
	run := domain.Run{ID: id, Kind: domain.RunComponentTest, Status: domain.RunQueued, RequestedBy: "component-owner-a", EnvironmentID: environmentID, EnvironmentRevisionID: environmentID + "-r1", ComponentReleaseID: "queue-release", Snapshot: domain.SnapshotFromExecutionPlan(domain.RunExecutionPlan{Steps: []domain.RunPlanStep{{ID: "install", NodeID: "node", ComponentID: "queue-component", ReleaseID: "queue-release"}}}), CreatedAt: testNow.Add(offset)}
	if err := s.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestConcurrentClaimsSerializeOneEnvironmentAndAllowAnother(t *testing.T) {
	s := newTestStore(t)
	queuedRunFixture(t, s, "first", "env-a", 0)
	queuedRunFixture(t, s, "second", "env-a", time.Second)
	queuedRunFixture(t, s, "independent", "env-b", 0)
	start := make(chan struct{})
	type result struct {
		run domain.Run
		err error
	}
	results := make(chan result, 3)
	for _, env := range []string{"env-a", "env-a", "env-b"} {
		go func(id string) {
			<-start
			r, err := s.ClaimNextRun(context.Background(), id, testNow.Add(time.Minute))
			results <- result{r, err}
		}(env)
	}
	close(start)
	claimed, conflicts := map[string]int{}, 0
	for range 3 {
		r := <-results
		if errors.Is(r.err, domain.ErrConflict) {
			conflicts++
		} else if r.err != nil {
			t.Fatal(r.err)
		} else {
			claimed[r.run.ID]++
		}
	}
	if !reflect.DeepEqual(claimed, map[string]int{"first": 1, "independent": 1}) || conflicts != 1 {
		t.Fatalf("claimed=%v conflicts=%d", claimed, conflicts)
	}
	if err := s.UpdateRunStatus(context.Background(), "first", []domain.RunStatus{domain.RunRunning}, domain.RunSucceeded, "", testNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimNextRun(context.Background(), "env-a", testNow.Add(3*time.Minute))
	if err != nil || next.ID != "second" {
		t.Fatalf("FIFO next=%+v err=%v", next, err)
	}
}

func receiptFixture(t *testing.T, s *Store) domain.ActionExecutionReceipt {
	run := queuedRunFixture(t, s, "receipt-run", "receipt-env", 0)
	return domain.ActionExecutionReceipt{RunID: run.ID, StepID: "install", EnvironmentID: run.EnvironmentID, ComponentID: "queue-component", ReleaseID: "queue-release", ActionID: "install-action", SourceNodeID: "node", Status: "started", BackupRef: "backup:original", Backup: domain.BackupMetadata{NodeID: "node", ComponentID: "queue-component", CapturedAt: testNow}, StartedAt: testNow, UpdatedAt: testNow}
}

func TestExecutionReceiptStateAndBackupIdentityAreImmutable(t *testing.T) {
	s := newTestStore(t)
	r := receiptFixture(t, s)
	ctx := context.Background()
	for _, state := range []string{"started", "started", "main_succeeded", "main_succeeded", "verified", "verified"} {
		r.Status = state
		r.UpdatedAt = r.UpdatedAt.Add(time.Second)
		if err := s.RecordActionExecution(ctx, r); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
		got, err := s.LatestActionReceipt(ctx, r.EnvironmentID, r.ComponentID, r.ReleaseID)
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Fatalf("persisted receipt=%+v err=%v", got, err)
		}
	}
	for _, mutation := range []struct {
		name   string
		change func(*domain.ActionExecutionReceipt)
	}{
		{"started", func(v *domain.ActionExecutionReceipt) { v.Status = "started" }},
		{"main_succeeded", func(v *domain.ActionExecutionReceipt) { v.Status = "main_succeeded" }},
		{"backup reference", func(v *domain.ActionExecutionReceipt) { v.BackupRef = "replaced" }},
		{"backup bytes", func(v *domain.ActionExecutionReceipt) { v.Backup.NodeID = "other-node" }},
		{"action identity", func(v *domain.ActionExecutionReceipt) { v.ActionID = "other-action" }},
		{"source node", func(v *domain.ActionExecutionReceipt) { v.SourceNodeID = "other-node" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := r
			mutation.change(&candidate)
			candidate.UpdatedAt = candidate.UpdatedAt.Add(time.Hour)
			if err := s.RecordActionExecution(ctx, candidate); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("identity/state mutation accepted: %v", err)
			}
			got, err := s.LatestActionReceipt(ctx, r.EnvironmentID, r.ComponentID, r.ReleaseID)
			if err != nil || !reflect.DeepEqual(got, r) {
				t.Fatalf("rejected receipt changed persisted data: %+v %v", got, err)
			}
		})
	}
}

func TestConcurrentReceiptCallbacksCannotDowngradeVerification(t *testing.T) {
	s := newTestStore(t)
	r := receiptFixture(t, s)
	ctx := context.Background()
	if err := s.RecordActionExecution(ctx, r); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, state := range []string{"started", "main_succeeded", "verified", "main_succeeded", "verified"} {
		workers.Add(1)
		go func(status string) {
			defer workers.Done()
			<-start
			next := r
			next.Status = status
			if err := s.RecordActionExecution(ctx, next); err != nil && !errors.Is(err, domain.ErrConflict) {
				t.Errorf("record %s: %v", status, err)
			}
		}(state)
	}
	close(start)
	workers.Wait()
	got, err := s.LatestActionReceipt(ctx, r.EnvironmentID, r.ComponentID, r.ReleaseID)
	if err != nil || got.Status != "verified" || got.BackupRef != r.BackupRef || !reflect.DeepEqual(got.Backup, r.Backup) {
		t.Fatalf("concurrent receipt=%+v err=%v", got, err)
	}
}
