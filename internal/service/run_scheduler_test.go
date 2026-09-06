package service

import (
	"context"
	"testing"
	"time"
)

func TestRecoverEnvironmentWorkerOnlyReplacesStaleWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler := &RunScheduler{rootCtx: ctx, workers: make(map[string]environmentWorkerState)}
	now := time.Now()
	scheduler.nextWorkerToken = 7
	scheduler.workers["environment-1"] = environmentWorkerState{token: 7, heartbeat: now}

	scheduler.recoverEnvironmentWorker("environment-1", now.Add(-time.Second))
	if scheduler.nextWorkerToken != 7 || scheduler.workers["environment-1"].token != 7 {
		t.Fatalf("fresh worker was replaced: token=%d state=%+v", scheduler.nextWorkerToken, scheduler.workers["environment-1"])
	}

	scheduler.workers["environment-1"] = environmentWorkerState{token: 7, heartbeat: now.Add(-2 * time.Minute)}
	scheduler.recoverEnvironmentWorker("environment-1", now.Add(-time.Minute))
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.nextWorkerToken != 8 {
		t.Fatalf("stale worker replacement token=%d", scheduler.nextWorkerToken)
	}
}

func TestTouchEnvironmentWorkerRejectsSupersededToken(t *testing.T) {
	scheduler := &RunScheduler{workers: make(map[string]environmentWorkerState)}
	original := time.Now().Add(-time.Minute)
	scheduler.workers["environment-1"] = environmentWorkerState{token: 4, heartbeat: original}

	scheduler.touchEnvironmentWorker("environment-1", 3)
	if got := scheduler.workers["environment-1"].heartbeat; !got.Equal(original) {
		t.Fatalf("superseded worker changed heartbeat from %s to %s", original, got)
	}
	scheduler.touchEnvironmentWorker("environment-1", 4)
	if got := scheduler.workers["environment-1"].heartbeat; !got.After(original) {
		t.Fatalf("current worker heartbeat was not refreshed: %s", got)
	}
}
