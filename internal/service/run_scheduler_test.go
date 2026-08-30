package service

import (
	"testing"
	"time"
)

func TestRecoverEnvironmentWorkerOnlyReplacesStaleWorker(t *testing.T) {
	platform, _ := readinessTestPlatform(t)
	platform.cancel()
	now := time.Now()
	platform.nextWorkerToken = 7
	platform.workers["environment-1"] = environmentWorkerState{token: 7, heartbeat: now}

	platform.recoverEnvironmentWorker("environment-1", now.Add(-time.Second))
	if platform.nextWorkerToken != 7 || platform.workers["environment-1"].token != 7 {
		t.Fatalf("fresh worker was replaced: token=%d state=%+v", platform.nextWorkerToken, platform.workers["environment-1"])
	}

	platform.workers["environment-1"] = environmentWorkerState{token: 7, heartbeat: now.Add(-2 * time.Minute)}
	platform.recoverEnvironmentWorker("environment-1", now.Add(-time.Minute))
	if platform.nextWorkerToken != 8 {
		t.Fatalf("stale worker replacement token=%d", platform.nextWorkerToken)
	}
}

func TestTouchEnvironmentWorkerRejectsSupersededToken(t *testing.T) {
	platform, _ := readinessTestPlatform(t)
	original := time.Now().Add(-time.Minute)
	platform.workers["environment-1"] = environmentWorkerState{token: 4, heartbeat: original}

	platform.touchEnvironmentWorker("environment-1", 3)
	if got := platform.workers["environment-1"].heartbeat; !got.Equal(original) {
		t.Fatalf("superseded worker changed heartbeat from %s to %s", original, got)
	}
	platform.touchEnvironmentWorker("environment-1", 4)
	if got := platform.workers["environment-1"].heartbeat; !got.After(original) {
		t.Fatalf("current worker heartbeat was not refreshed: %s", got)
	}
}
