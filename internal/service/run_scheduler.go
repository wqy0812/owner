package service

import (
	"context"
	"errors"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *RunScheduler) schedule(environmentID string) {
	p := s.platform
	p.mu.Lock()
	if _, running := p.workers[environmentID]; running {
		p.mu.Unlock()
		return
	}
	p.nextWorkerToken++
	token := p.nextWorkerToken
	p.workers[environmentID] = environmentWorkerState{token: token, heartbeat: time.Now()}
	p.mu.Unlock()
	go p.environmentWorker(environmentID, token)
}

const (
	queueWatchdogInterval = 2 * time.Second
	queueWorkerStaleAfter = 10 * time.Second
)

func (s *RunScheduler) queueWatchdog() {
	p := s.platform
	ticker := time.NewTicker(queueWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.rootCtx.Done():
			return
		case now := <-ticker.C:
			if count, err := p.store.FailInvalidActiveRuns(p.rootCtx, now.UTC()); err != nil {
				p.hub.Publish("run.worker_error", map[string]any{"error": err.Error()})
			} else if count > 0 {
				p.hub.Publish("run.updated", map[string]any{"status": domain.RunFailed, "reconciled": count})
			}
			environments, err := p.store.ListQueuedEnvironmentIDs(p.rootCtx)
			if err != nil {
				p.hub.Publish("run.worker_error", map[string]any{"error": err.Error()})
				continue
			}
			for _, environmentID := range environments {
				running, runErr := p.store.HasRunningRun(p.rootCtx, environmentID)
				if runErr != nil || running {
					continue
				}
				p.recoverEnvironmentWorker(environmentID, now.Add(-queueWorkerStaleAfter))
			}
		}
	}
}

func (s *RunScheduler) recoverEnvironmentWorker(environmentID string, staleBefore time.Time) {
	p := s.platform
	p.mu.Lock()
	state, exists := p.workers[environmentID]
	if exists && state.heartbeat.After(staleBefore) {
		p.mu.Unlock()
		return
	}
	p.nextWorkerToken++
	token := p.nextWorkerToken
	p.workers[environmentID] = environmentWorkerState{token: token, heartbeat: time.Now()}
	p.mu.Unlock()
	go p.environmentWorker(environmentID, token)
}

func (s *RunScheduler) touchEnvironmentWorker(environmentID string, token uint64) {
	p := s.platform
	p.mu.Lock()
	if state, ok := p.workers[environmentID]; ok && state.token == token {
		state.heartbeat = time.Now()
		p.workers[environmentID] = state
	}
	p.mu.Unlock()
}

func (s *RunScheduler) environmentWorker(environmentID string, token uint64) {
	p := s.platform
	defer func() {
		p.mu.Lock()
		if state, ok := p.workers[environmentID]; ok && state.token == token {
			delete(p.workers, environmentID)
		}
		p.mu.Unlock()
		// Close the narrow hand-off window where a run is queued after this
		// worker's final empty claim but before it removes itself. Any enqueue
		// after the removal schedules its own worker; an enqueue before removal
		// is discovered here.
		if p.rootCtx.Err() == nil {
			running, err := p.store.HasRunningRun(context.Background(), environmentID)
			if err != nil || running {
				return
			}
			queued, err := p.store.ListQueuedEnvironmentIDs(context.Background())
			if err == nil {
				for _, id := range queued {
					if id == environmentID {
						p.schedule(environmentID)
						break
					}
				}
			}
		}
	}()
	for {
		if p.rootCtx.Err() != nil {
			return
		}
		p.touchEnvironmentWorker(environmentID, token)
		run, err := p.store.ClaimNextRun(p.rootCtx, environmentID, time.Now().UTC())
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			return
		}
		if err != nil {
			p.hub.Publish("run.worker_error", map[string]any{"environmentId": environmentID, "error": err.Error()})
			return
		}
		p.executeRun(run)
		p.touchEnvironmentWorker(environmentID, token)
	}
}
