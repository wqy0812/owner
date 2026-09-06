package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *RunScheduler) schedule(environmentID string) {
	s.mu.Lock()
	if _, running := s.workers[environmentID]; running {
		s.mu.Unlock()
		return
	}
	s.nextWorkerToken++
	token := s.nextWorkerToken
	s.workers[environmentID] = environmentWorkerState{token: token, heartbeat: time.Now()}
	s.mu.Unlock()
	go s.environmentWorker(environmentID, token)
}

const (
	queueWatchdogInterval = 2 * time.Second
	queueWorkerStaleAfter = 10 * time.Second
)

func (s *RunScheduler) queueWatchdog() {
	ticker := time.NewTicker(queueWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.rootCtx.Done():
			return
		case now := <-ticker.C:
			if count, err := s.store.FailInvalidActiveRuns(s.rootCtx, now.UTC()); err != nil {
				s.hub.Publish("run.worker_error", map[string]any{"error": err.Error()})
			} else if count > 0 {
				s.hub.Publish("run.updated", map[string]any{"status": domain.RunFailed, "reconciled": count})
			}
			environments, err := s.store.ListQueuedEnvironmentIDs(s.rootCtx)
			if err != nil {
				s.hub.Publish("run.worker_error", map[string]any{"error": err.Error()})
				continue
			}
			for _, environmentID := range environments {
				running, runErr := s.store.HasRunningRun(s.rootCtx, environmentID)
				if runErr != nil || running {
					continue
				}
				s.recoverEnvironmentWorker(environmentID, now.Add(-queueWorkerStaleAfter))
			}
		}
	}
}

func (s *RunScheduler) recoverEnvironmentWorker(environmentID string, staleBefore time.Time) {
	s.mu.Lock()
	state, exists := s.workers[environmentID]
	if exists && state.heartbeat.After(staleBefore) {
		s.mu.Unlock()
		return
	}
	s.nextWorkerToken++
	token := s.nextWorkerToken
	s.workers[environmentID] = environmentWorkerState{token: token, heartbeat: time.Now()}
	s.mu.Unlock()
	go s.environmentWorker(environmentID, token)
}

func (s *RunScheduler) touchEnvironmentWorker(environmentID string, token uint64) {
	s.mu.Lock()
	if state, ok := s.workers[environmentID]; ok && state.token == token {
		state.heartbeat = time.Now()
		s.workers[environmentID] = state
	}
	s.mu.Unlock()
}

func (s *RunScheduler) environmentWorker(environmentID string, token uint64) {
	defer func() {
		s.mu.Lock()
		if state, ok := s.workers[environmentID]; ok && state.token == token {
			delete(s.workers, environmentID)
		}
		s.mu.Unlock()
		// Close the narrow hand-off window where a run is queued after this
		// worker's final empty claim but before it removes itself. Any enqueue
		// after the removal schedules its own worker; an enqueue before removal
		// is discovered here.
		if s.rootCtx.Err() == nil {
			running, err := s.store.HasRunningRun(context.Background(), environmentID)
			if err != nil || running {
				return
			}
			queued, err := s.store.ListQueuedEnvironmentIDs(context.Background())
			if err == nil {
				for _, id := range queued {
					if id == environmentID {
						s.schedule(environmentID)
						break
					}
				}
			}
		}
	}()
	for {
		if s.rootCtx.Err() != nil {
			return
		}
		s.touchEnvironmentWorker(environmentID, token)
		run, err := s.store.ClaimNextRun(s.rootCtx, environmentID, time.Now().UTC())
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			return
		}
		if err != nil {
			s.hub.Publish("run.worker_error", map[string]any{"environmentId": environmentID, "error": err.Error()})
			return
		}
		s.executor.executeRun(run)
		s.touchEnvironmentWorker(environmentID, token)
	}
}

func (s *RunScheduler) Start(ctx context.Context) error {
	if _, err := s.store.MarkRunningInterrupted(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	}
	if _, err := s.store.FailInvalidActiveRuns(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("reconcile invalid active runs: %w", err)
	}
	environments, err := s.store.ListQueuedEnvironmentIDs(ctx)
	if err != nil {
		return fmt.Errorf("list queued runs: %w", err)
	}
	for _, id := range environments {
		s.schedule(id)
	}
	go s.queueWatchdog()
	return nil
}

type environmentWorkerState struct {
	token     uint64
	heartbeat time.Time
}
