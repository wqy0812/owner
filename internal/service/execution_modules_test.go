package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

type recordingStore struct {
	recorderStore
	fail  string
	calls []string
}

func (s *recordingStore) record(operation string) error {
	s.calls = append(s.calls, operation)
	if s.fail == operation {
		return errors.New(operation)
	}
	return nil
}
func (s *recordingStore) MarkScenarioMutation(context.Context, domain.Run) error {
	return s.record("mutation")
}
func (s *recordingStore) CreateRunStep(context.Context, domain.RunStep) error {
	return s.record("step")
}
func (s *recordingStore) RecordActionExecution(_ context.Context, r store.ActionExecutionReceipt) error {
	return s.record(r.Status)
}
func (s *recordingStore) UpsertEnvironmentComponentInstallation(context.Context, domain.EnvironmentComponentInstallation) error {
	return s.record("baseline")
}
func (s *recordingStore) UpdateRunStep(context.Context, domain.RunStep) error {
	return s.record("completed")
}
func (s *recordingStore) AppendRunLog(context.Context, domain.RunLog) (int64, error) {
	return 1, s.record("log")
}

func TestRecorderStopsAtFailedMutationStepOrReceipt(t *testing.T) {
	for _, tc := range []struct {
		fail      string
		calls     []string
		stepSaved bool
	}{
		{"mutation", []string{"mutation"}, false},
		{"step", []string{"mutation", "step"}, false},
		{"started", []string{"mutation", "step", "started"}, true},
		{"", []string{"mutation", "step", "started"}, true},
	} {
		t.Run(tc.fail, func(t *testing.T) {
			data, hub := &recordingStore{fail: tc.fail}, NewEventHub()
			events, unsubscribe := hub.Subscribe()
			defer unsubscribe()
			recorder := &LifecycleRecorder{store: data, hub: hub}
			step, err := recorder.beginStep(context.Background(), domain.Run{ID: "run-one"}, lockedStep{Phase: "execute", Backup: &domain.BackupMetadata{}}, ansiblerunner.JobStepResult{StartedAt: time.Now()})
			if (err != nil) != (tc.fail != "") || (step.ID != "") != tc.stepSaved || !reflect.DeepEqual(data.calls, tc.calls) {
				t.Fatalf("step=%+v err=%v calls=%v", step, err, data.calls)
			}
			if tc.fail != "" && len(events) != 0 {
				t.Fatal("published a step before its persistence succeeded")
			}
		})
	}
}

func TestRecorderPostcheckRequiresBaselineAndVerifiedReceipt(t *testing.T) {
	parent := lockedStep{ID: "main", ActionID: "install", SourceNodeID: "node-one", Phase: "execute", Action: domain.ActionInstall, BackupRef: "backup-one", Backup: &domain.BackupMetadata{InstallRunID: "run-one"}}
	post := lockedStep{ParentActionID: parent.ActionID, SourceNodeID: parent.SourceNodeID, Phase: "post"}
	for _, tc := range []struct {
		fail  string
		calls []string
	}{
		{"baseline", []string{"baseline"}},
		{"verified", []string{"baseline", "verified"}},
		{"completed", []string{"baseline", "verified", "completed"}},
		{"", []string{"baseline", "verified", "completed"}},
	} {
		t.Run(tc.fail, func(t *testing.T) {
			data, hub := &recordingStore{fail: tc.fail}, NewEventHub()
			events, unsubscribe := hub.Subscribe()
			defer unsubscribe()
			recorder := &LifecycleRecorder{store: data, hub: hub}
			_, err := recorder.completeStep(context.Background(), domain.Run{ID: "run-one"}, post, []lockedStep{parent}, domain.RunStep{ID: "post-one"}, ansiblerunner.JobStepResult{FinishedAt: time.Now()})
			if (err != nil) != (tc.fail != "") || !reflect.DeepEqual(data.calls, tc.calls) {
				t.Fatalf("err=%v calls=%v", err, data.calls)
			}
			if tc.fail != "" && len(events) != 0 {
				t.Fatal("published success after incomplete lifecycle persistence")
			}
		})
	}
}

func TestRecorderDoesNotPublishUnstoredOutput(t *testing.T) {
	hub := NewEventHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	recorder := &LifecycleRecorder{store: &recordingStore{fail: "log"}, hub: hub}
	if err := recorder.recordOutput("run-one", "step-one", ansiblerunner.LogEvent{}); err == nil || len(events) != 0 {
		t.Fatalf("unstored log published: %v", err)
	}
	if err := recorder.recordEvent(context.Background(), "run-one", "step-one", ansiblerunner.JobEvent{}); err == nil || len(events) != 0 {
		t.Fatalf("unstored event published: %v", err)
	}
	data := &recordingStore{}
	recorder.store = data
	if err := recorder.recordOutput("run-one", "step-one", ansiblerunner.LogEvent{Line: "body must stay in SQLite"}); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.Type != "run.log" || !reflect.DeepEqual(event.Data, map[string]any{"runId": "run-one", "logId": int64(1)}) || !reflect.DeepEqual(data.calls, []string{"log"}) {
		t.Fatalf("unexpected committed log event: %+v", event)
	}
}

type runCreationStore struct {
	creatorStore
	fail     bool
	saved    domain.Run
	approval *domain.Approval
}

func (s *runCreationStore) CreateRun(_ context.Context, run domain.Run, approval *domain.Approval) error {
	s.saved, s.approval = run, approval
	if s.fail {
		return errors.New("create failed")
	}
	return nil
}

type recordingScheduler struct{ environments []string }

func (s *recordingScheduler) schedule(id string) { s.environments = append(s.environments, id) }

func TestRunCreatorPersistsBeforeSchedulingAndHonorsApproval(t *testing.T) {
	for _, tc := range []struct{ destructive, fail bool }{{false, false}, {true, false}, {false, true}} {
		data, scheduler, audit := &runCreationStore{fail: tc.fail}, &recordingScheduler{}, &creationAudit{}
		creator := &RunCreator{store: data, scheduler: scheduler, audit: audit, hub: NewEventHub()}
		prepared := lockedRunPreparation{ID: "locked-run", CapturedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), Plan: lockedPlan{TreeDigest: "locked-tree"}, Destructive: tc.destructive}
		run, err := creator.createRun(context.Background(), domain.User{ID: "owner-one"}, domain.Environment{ID: "environment-one", CurrentRevisionID: "revision-one"}, domain.RunComponentTest, "", "", domain.ActionCheck, prepared, nil)
		if (err != nil) != tc.fail {
			t.Fatal(err)
		}
		wantSchedule := 1
		if tc.destructive || tc.fail {
			wantSchedule = 0
		}
		if len(scheduler.environments) != wantSchedule || (data.approval != nil) != tc.destructive {
			t.Fatalf("approval=%v scheduled=%v", data.approval, scheduler.environments)
		}
		if tc.fail && len(audit.actions) != 0 {
			t.Fatal("failed creation emitted audit")
		}
		if run.ID != prepared.ID || run.ArtifactDigest != "locked-tree" || run.InputSnapshot["environmentRevisionId"] != "revision-one" || !run.CreatedAt.Equal(prepared.CapturedAt) {
			t.Fatalf("prepared snapshot changed: %+v", run)
		}
	}
}

type rejectedJobStore struct {
	executorStore
	attempted bool
}

func (s *rejectedJobStore) SaveRunJob(context.Context, string, string, []byte) error {
	s.attempted = true
	return errors.New("archive write failed")
}

type noRecoverySteps struct{ rollbackPort }

func (noRecoverySteps) addRecoverySteps(context.Context, *ansiblerunner.JobPlan, lockedPlan) error {
	return nil
}

func TestExecutorNeverRunsAnUnpersistedBundle(t *testing.T) {
	testutil.CompanionCLI(t)
	data := &rejectedJobStore{}
	runner := &testutil.Runner{RunFunc: func(context.Context, *ansiblerunner.JobBundle, ansiblerunner.JobRequest) (ansiblerunner.JobResult, error) {
		t.Fatal("ran bundle after persistence failed")
		return ansiblerunner.JobResult{}, nil
	}}
	executor := &RunExecutor{jobs: runner, store: data, rollback: noRecoverySteps{}}
	_, err := executor.executeLockedJob(context.Background(), domain.Run{ID: "run-one"}, ansiblerunner.JobRequest{Plan: ansiblerunner.JobPlan{Contract: ansiblerunner.JobContract}})
	if err == nil || err.Error() != "archive write failed" || !data.attempted {
		t.Fatalf("bundle persistence was not required: %v", err)
	}
}
