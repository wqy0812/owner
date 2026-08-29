package backup

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

type Scheduler struct {
	managerMu      sync.RWMutex
	manager        *Manager
	snapshotMu     sync.Mutex
	enabled        bool
	delay          time.Duration
	requests       chan string
	cancel         context.CancelFunc
	done           chan struct{}
	onError        func(error)
	onDone         func(Manifest)
	once           sync.Once
	statusMu       sync.RWMutex
	lastError      string
	lastErrorAt    *time.Time
	lastSuccessAt  *time.Time
	onStatusChange func()
}

type SchedulerStatus struct {
	LastError     string
	LastErrorAt   *time.Time
	LastSuccessAt *time.Time
}

func (s *Scheduler) Manager() *Manager {
	s.managerMu.RLock()
	defer s.managerMu.RUnlock()
	return s.manager
}

func (s *Scheduler) Enabled() bool {
	s.managerMu.RLock()
	defer s.managerMu.RUnlock()
	return s.enabled
}

func (s *Scheduler) SetEnabled(enabled bool) {
	s.managerMu.Lock()
	s.enabled = enabled
	s.managerMu.Unlock()
}

// SetManager switches future snapshots to another validated Catalog repository.
// An in-flight snapshot keeps using the manager it started with.
func (s *Scheduler) SetManager(manager *Manager) {
	s.managerMu.Lock()
	s.manager = manager
	s.managerMu.Unlock()
}

func (s *Scheduler) Status() SchedulerStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return SchedulerStatus{LastError: s.lastError, LastErrorAt: cloneTime(s.lastErrorAt), LastSuccessAt: cloneTime(s.lastSuccessAt)}
}

func (s *Scheduler) SetStatusChangeHandler(handler func()) {
	s.statusMu.Lock()
	s.onStatusChange = handler
	s.statusMu.Unlock()
}

func (s *Scheduler) notifyStatusChange() {
	s.statusMu.RLock()
	handler := s.onStatusChange
	s.statusMu.RUnlock()
	if handler != nil {
		handler()
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Scheduler) recordFailure(err error) {
	now := time.Now().UTC()
	s.statusMu.Lock()
	s.lastError, s.lastErrorAt = err.Error(), &now
	s.statusMu.Unlock()
	s.notifyStatusChange()
}

func (s *Scheduler) recordSuccess() {
	now := time.Now().UTC()
	s.statusMu.Lock()
	s.lastError, s.lastErrorAt, s.lastSuccessAt = "", nil, &now
	s.statusMu.Unlock()
	s.notifyStatusChange()
}

func NewScheduler(manager *Manager, delay time.Duration) *Scheduler {
	if delay <= 0 {
		delay = 30 * time.Second
	}
	return &Scheduler{
		manager: manager, enabled: true, delay: delay, requests: make(chan string, 64), done: make(chan struct{}),
		onError: func(err error) { log.Printf("catalog backup failed: %v", err) },
		onDone: func(manifest Manifest) {
			log.Printf("catalog backup %s completed at Git commit %s", manifest.BackupID, manifest.GitCommit)
		},
	}
}

func (s *Scheduler) Start(parent context.Context) {
	s.once.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		s.cancel = cancel
		go s.run(ctx)
	})
}

func (s *Scheduler) Request(reason string) {
	if !s.Enabled() {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "publication"
	}
	select {
	case s.requests <- reason:
	default:
		// A full snapshot covers every publication generation. A saturated queue
		// therefore only loses duplicate reasons, never published Catalog state.
	}
}

func (s *Scheduler) RequestIfBehind(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	needed, generation, err := s.Manager().NeedsSnapshot(ctx)
	if err != nil {
		return err
	}
	if needed {
		s.Request(fmt.Sprintf("startup-catch-up:generation-%d", generation))
	}
	return nil
}

// SnapshotNow creates a recovery point immediately and returns its immutable
// identity to the caller. It shares serialization and health reporting with
// publication-triggered snapshots so UI requests cannot race the scheduler.
func (s *Scheduler) SnapshotNow(ctx context.Context, reason string) (Manifest, error) {
	if !s.Enabled() {
		return Manifest{}, fmt.Errorf("Catalog backup is not configured")
	}
	return s.executeSnapshot(ctx, reason)
}

func (s *Scheduler) executeSnapshot(ctx context.Context, reason string) (Manifest, error) {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	manifest, err := s.Manager().Snapshot(ctx, reason)
	if err != nil {
		s.recordFailure(err)
		s.onError(err)
		return manifest, err
	}
	s.recordSuccess()
	s.onDone(manifest)
	return manifest, nil
}

func (s *Scheduler) Close() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
}

func (s *Scheduler) run(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-s.requests:
			reasons := map[string]bool{reason: true}
			timer := time.NewTimer(s.delay)
		collect:
			for {
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case reason := <-s.requests:
					reasons[reason] = true
				case <-timer.C:
					break collect
				}
			}
			ordered := make([]string, 0, len(reasons))
			for item := range reasons {
				ordered = append(ordered, item)
			}
			sort.Strings(ordered)
			_, err := s.executeSnapshot(ctx, strings.Join(ordered, ","))
			if err != nil {
				retry := time.NewTimer(s.delay)
				select {
				case <-ctx.Done():
					retry.Stop()
					return
				case <-retry.C:
					s.Request("retry-after-failure")
				}
				continue
			}
		}
	}
}
