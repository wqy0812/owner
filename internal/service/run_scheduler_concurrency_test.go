package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

type schedulerProbe struct {
	schedulerStore
	claim   func(string) (domain.Run, error)
	queued  func() ([]string, error)
	running func(string) (bool, error)
}

func (p schedulerProbe) ClaimNextRun(_ context.Context, id string, _ time.Time) (domain.Run, error) {
	return p.claim(id)
}
func (p schedulerProbe) ListQueuedEnvironmentIDs(context.Context) ([]string, error) {
	return p.queued()
}
func (p schedulerProbe) HasRunningRun(_ context.Context, id string) (bool, error) {
	return p.running(id)
}
func (p schedulerProbe) FailInvalidActiveRuns(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type scheduledExecution func(domain.Run)

func (f scheduledExecution) executeRun(run domain.Run) { f(run) }

func awaitWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reach the expected barrier")
	}
}

func TestSchedulerClosesEmptyClaimEnqueueHandoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	empty, release, finished, executed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := &RunScheduler{rootCtx: ctx, hub: NewEventHub(), workers: map[string]environmentWorkerState{"env": {token: 1}}, nextWorkerToken: 1}
	s.store = schedulerProbe{
		claim: func(id string) (domain.Run, error) {
			if calls.Add(1) == 1 {
				close(empty)
				<-release
				return domain.Run{}, domain.ErrNotFound
			}
			return domain.Run{ID: "queued-during-exit", EnvironmentID: id}, nil
		},
		queued:  func() ([]string, error) { return []string{"env"}, nil },
		running: func(string) (bool, error) { return false, nil },
	}
	s.executor = scheduledExecution(func(run domain.Run) {
		if run.ID != "queued-during-exit" {
			t.Errorf("executed wrong run: %s", run.ID)
		}
		cancel()
		close(executed)
	})
	go func() { s.environmentWorker("env", 1); close(finished) }()
	awaitWorker(t, empty)
	// Enqueue sees the old worker still registered and cannot start another one.
	s.schedule("env")
	close(release)
	awaitWorker(t, finished)
	awaitWorker(t, executed)
	if calls.Load() != 2 {
		t.Fatalf("claims=%d, want one empty claim and one execution", calls.Load())
	}
}

func TestSchedulerStorageFailureWaitsForWatchdogAndRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	done := make(chan struct{})
	s := &RunScheduler{rootCtx: ctx, hub: NewEventHub(), workers: map[string]environmentWorkerState{"env": {token: 1}}, nextWorkerToken: 1}
	s.store = schedulerProbe{
		claim: func(id string) (domain.Run, error) {
			if calls.Add(1) == 1 {
				return domain.Run{}, errors.New("temporary storage failure")
			}
			return domain.Run{ID: "recovered", EnvironmentID: id}, nil
		},
		queued:  func() ([]string, error) { return []string{"env"}, nil },
		running: func(string) (bool, error) { return false, nil },
	}
	s.executor = scheduledExecution(func(domain.Run) { cancel(); close(done) })
	s.environmentWorker("env", 1)
	if calls.Load() != 1 {
		t.Fatal("storage failure immediately rescheduled itself")
	}
	s.mu.Lock()
	_, exists := s.workers["env"]
	s.mu.Unlock()
	if exists {
		t.Fatal("failed worker retained its slot")
	}
	s.reconcileQueue(time.Now())
	awaitWorker(t, done)
	if calls.Load() != 2 {
		t.Fatalf("retry claims=%d", calls.Load())
	}
}

func TestSupersededWorkerExitPreservesReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &RunScheduler{rootCtx: ctx, workers: map[string]environmentWorkerState{"env": {token: 2}}}
	s.environmentWorker("env", 1)
	if s.workers["env"].token != 2 {
		t.Fatal("old worker removed replacement")
	}
}

func TestWatchdogSkipsRunningAndFreshWorkersButRecoversOtherEnvironment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	done := make(chan struct{})
	s := &RunScheduler{rootCtx: ctx, hub: NewEventHub(), nextWorkerToken: 2, workers: map[string]environmentWorkerState{
		"fresh": {token: 1, heartbeat: now}, "stale": {token: 2, heartbeat: now.Add(-time.Minute)},
	}}
	s.store = schedulerProbe{
		claim:   func(id string) (domain.Run, error) { return domain.Run{EnvironmentID: id}, nil },
		queued:  func() ([]string, error) { return []string{"running", "fresh", "stale"}, nil },
		running: func(id string) (bool, error) { return id == "running", nil },
	}
	s.executor = scheduledExecution(func(run domain.Run) {
		if run.EnvironmentID != "stale" {
			t.Errorf("unexpected recovery: %s", run.EnvironmentID)
		}
		cancel()
		close(done)
	})
	s.reconcileQueue(now)
	awaitWorker(t, done)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workers["fresh"].token != 1 || s.nextWorkerToken != 3 {
		t.Fatal("watchdog replaced a fresh worker")
	}
}
