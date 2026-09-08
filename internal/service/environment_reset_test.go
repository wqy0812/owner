package service

import (
	"codex/platform-demo/internal/testutil/runfixture"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	ansible "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil"
)

type resetTestStore struct {
	rollbackStore
	revisions     map[string]domain.EnvironmentRevision
	runs          map[string]domain.Run
	installations []domain.EnvironmentComponentInstallation
	receipts      []domain.ActionExecutionReceipt
}

func (s *resetTestStore) GetRun(_ context.Context, id string) (domain.Run, error) {
	run, ok := s.runs[id]
	if !ok {
		return run, domain.ErrNotFound
	}
	return run, nil
}
func (s *resetTestStore) GetEnvironmentRevision(_ context.Context, id string) (domain.EnvironmentRevision, error) {
	revision, ok := s.revisions[id]
	if !ok {
		return revision, domain.ErrNotFound
	}
	return revision, nil
}
func (s *resetTestStore) ListEnvironmentComponentInstallations(context.Context, string) ([]domain.EnvironmentComponentInstallation, error) {
	return append([]domain.EnvironmentComponentInstallation{}, s.installations...), nil
}
func (s *resetTestStore) LatestEnvironmentActionReceipts(context.Context, string) ([]domain.ActionExecutionReceipt, error) {
	return s.receipts, nil
}

func resetFixture(t *testing.T) (*RollbackPlanner, *resetTestStore, domain.EnvironmentRevision, domain.RunExecutionPlan) {
	t.Helper()
	hosts := []domain.RunInventoryHost{
		{Name: "control-a", Address: "192.0.2.1", Groups: []string{"control"}, User: "root"},
		{Name: "control-b", Address: "192.0.2.2", Groups: []string{"control"}, User: "root"},
		{Name: "worker-a", Address: "192.0.2.3", Groups: []string{"workers"}, User: "root"},
		{Name: "worker-b", Address: "192.0.2.4", Groups: []string{"workers"}, User: "root"},
		{Name: "media", Address: "192.0.2.50", Groups: []string{"media"}, User: "root"},
		{Name: "registry", Address: "192.0.2.51", Groups: []string{"registry"}, User: "root"},
	}
	inventory, _ := json.Marshal(InventoryDocument{Hosts: hosts})
	revision := domain.EnvironmentRevision{ID: "env-r1", EnvironmentID: "env", Inventory: inventory, Variables: map[string]string{"FILE_STATION": "192.0.2.50:8080", "IMAGE_REGISTRY": "192.0.2.51:5000/library"}}
	data := &resetTestStore{revisions: map[string]domain.EnvironmentRevision{revision.ID: revision}, runs: map[string]domain.Run{}}
	plan := domain.RunExecutionPlan{ResetTargets: hosts[:4]}
	for _, group := range []string{"control", "workers", "media", "registry"} {
		id := "component-" + group
		backup := domain.BackupMetadata{EnvironmentID: "env", ComponentID: id, ReleaseID: id + "-v1", ActionID: "install-" + group, NodeID: group, InstallRunID: "install-run-" + group, PlaybookSHA256: "sha-" + group}
		installation := domain.EnvironmentComponentInstallation{EnvironmentID: "env", ComponentID: id, ReleaseID: backup.ReleaseID, NodeID: group, InstallRunID: backup.InstallRunID, BackupRef: "backup-" + group, Backup: backup}
		data.installations = append(data.installations, installation)
		sourceStep := domain.RunPlanStep{ID: group, NodeID: group, SourceNodeID: group, ComponentID: id, ReleaseID: backup.ReleaseID, Action: domain.ActionInstall, ActionID: backup.ActionID, Limit: group}
		data.runs[backup.InstallRunID] = domain.Run{ID: backup.InstallRunID, Kind: domain.RunComponentTest, EnvironmentID: "env", EnvironmentRevisionID: revision.ID, Snapshot: runfixture.Snapshot(structToMap(domain.RunExecutionPlan{Steps: []domain.RunPlanStep{sourceStep}}))}
		if group == "control" || group == "workers" {
			step := sourceStep
			step.Action, step.Phase, step.ActionID = domain.ActionRollback, "execute", "rollback-"+group
			step.Backup, step.BackupRef = &backup, installation.BackupRef
			plan.Steps = append(plan.Steps, step)
		}
	}
	plan.ParentSteps = append([]domain.RunPlanStep{}, plan.Steps...)
	var err error
	plan.ResetBoundaryDigest, err = resetBoundaryDigest(context.Background(), revision, plan.ResetTargets)
	if err != nil {
		t.Fatal(err)
	}
	return &RollbackPlanner{store: data, inspector: &testutil.Runner{}}, data, revision, plan
}

func TestEnvironmentResetReconstructsAllTargetsAndPreservesSharedRecords(t *testing.T) {
	r, data, revision, plan := resetFixture(t)
	before, _ := json.Marshal(data.installations)
	state, err := r.environmentResetState(context.Background(), revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.installations) != 2 || len(state.targetHosts) != 4 {
		t.Fatalf("unexpected reset state: %+v", state)
	}
	for _, host := range state.targetHosts {
		if host.Name == "media" || host.Name == "registry" {
			t.Fatal("shared service became a reset target")
		}
	}
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(data.installations)
	if string(before) != string(after) {
		t.Fatal("preview changed installation records")
	}
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, true); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("claimed clean with remaining installations: %v", err)
	}
	shared := append([]domain.EnvironmentComponentInstallation{}, data.installations[2:]...)
	data.installations = shared
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(data.installations, shared) {
		t.Fatal("shared records changed")
	}
}

func TestEnvironmentResetUsesCurrentGroupMembersAndLocksPreview(t *testing.T) {
	for _, change := range []string{"add", "remove", "replace", "connection"} {
		t.Run(change, func(t *testing.T) {
			r, _, revision, plan := resetFixture(t)
			var inventory InventoryDocument
			if err := json.Unmarshal(revision.Inventory, &inventory); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "add":
				inventory.Hosts = append(inventory.Hosts, domain.RunInventoryHost{Name: "control-new", Address: "192.0.2.99", Groups: []string{"control"}, User: "testops"})
			case "remove":
				inventory.Hosts = inventory.Hosts[1:]
			case "replace":
				inventory.Hosts[0] = domain.RunInventoryHost{Name: "control-new", Address: "192.0.2.99", Groups: []string{"control"}, User: "testops"}
			case "connection":
				inventory.Hosts[0].Address, inventory.Hosts[0].User, inventory.Hosts[0].Port = "192.0.2.99", "testops", 2222
			}
			revision.Inventory, _ = json.Marshal(inventory)
			if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("stale preview accepted: %v", err)
			}
			state, err := r.environmentResetState(context.Background(), revision)
			if err != nil {
				t.Fatal(err)
			}
			want := []domain.RunInventoryHost{}
			for _, host := range inventory.Hosts {
				if host.Groups[0] == "control" || host.Groups[0] == "workers" {
					want = append(want, host)
				}
			}
			sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
			if !reflect.DeepEqual(state.targetHosts, want) {
				t.Fatalf("targets=%+v want=%+v", state.targetHosts, want)
			}
			plan.ResetTargets = state.targetHosts
			plan.ResetBoundaryDigest, err = resetBoundaryDigest(context.Background(), revision, plan.ResetTargets)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); err != nil {
				t.Fatalf("new preview rejected: %v", err)
			}
			plan.ResetTargets = plan.ResetTargets[1:]
			plan.ResetBoundaryDigest, err = resetBoundaryDigest(context.Background(), revision, plan.ResetTargets)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("partial locked targets accepted: %v", err)
			}
		})
	}
}

func TestEnvironmentResetRejectsMissingCurrentGroup(t *testing.T) {
	r, _, revision, _ := resetFixture(t)
	var inventory InventoryDocument
	_ = json.Unmarshal(revision.Inventory, &inventory)
	inventory.Hosts = inventory.Hosts[2:]
	revision.Inventory, _ = json.Marshal(inventory)
	if _, err := r.environmentResetState(context.Background(), revision); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("empty current group accepted: %v", err)
	}
}

func TestEnvironmentResetRejectsPartialAndChangedPlans(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*resetTestStore, *domain.EnvironmentRevision, *domain.RunExecutionPlan)
	}{
		{"partial", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.Steps = p.Steps[:1]
			p.ParentSteps = p.Steps
		}},
		{"shared target", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.ResetTargets[0].Address = "192.0.2.50"
		}},
		{"unlocked targets", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.ResetBoundaryDigest = ""
		}},
		{"shared step", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.Steps[0].Limit = "media"
		}},
		{"mixed step", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.Steps[0].Limit = "all"
		}},
		{"partial hosts", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.Steps[0].Limit = "control-a"
			p.ParentSteps = append([]domain.RunPlanStep{}, p.Steps...)
		}},
		{"missing target", func(_ *resetTestStore, _ *domain.EnvironmentRevision, p *domain.RunExecutionPlan) {
			p.Steps[0].Limit = "unknown"
		}},
		{"unresolved endpoint", func(_ *resetTestStore, rev *domain.EnvironmentRevision, _ *domain.RunExecutionPlan) {
			rev.Variables["FILE_STATION"] = ":bad:endpoint"
		}},
		{"multi-level baseline", func(s *resetTestStore, _ *domain.EnvironmentRevision, _ *domain.RunExecutionPlan) {
			s.installations[0].Backup.Previous = &domain.EnvironmentComponentInstallation{}
		}},
		{"changed group", func(_ *resetTestStore, rev *domain.EnvironmentRevision, _ *domain.RunExecutionPlan) {
			var inventory InventoryDocument
			_ = json.Unmarshal(rev.Inventory, &inventory)
			inventory.Hosts[0].Address = "192.0.2.99"
			rev.Inventory, _ = json.Marshal(inventory)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, data, revision, plan := resetFixture(t)
			tc.change(data, &revision, &plan)
			if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("unsafe reset accepted: %v", err)
			}
		})
	}
}

func TestEnvironmentResetRejectsMixedSourceRunWithoutTrimming(t *testing.T) {
	r, data, revision, _ := resetFixture(t)
	source := data.runs["install-run-control"]
	plan, _ := planFromRun(source)
	plan.Steps[0].Limit = "all"
	source.Snapshot = domain.SnapshotFromExecutionPlan(plan)
	data.runs[source.ID] = source
	if _, err := r.environmentResetState(context.Background(), revision); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "混合主机组") {
		t.Fatalf("mixed source accepted: %v", err)
	}
}

func TestEnvironmentResetDoesNotReclassifyClusterBaselinesAsShared(t *testing.T) {
	r, _, revision, _ := resetFixture(t)
	// Both control-plane hosts become service endpoints. They must not disappear
	// from the reset simply because the current variables now mark them shared.
	revision.Variables = map[string]string{"FILE_STATION": "192.0.2.1:8080", "IMAGE_REGISTRY": "192.0.2.2:5000"}
	if _, err := r.environmentResetState(context.Background(), revision); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "边界冲突") {
		t.Fatalf("cluster baselines silently excluded: %v", err)
	}
}

func TestEnvironmentResetContinuationRequiresAllRemainingBaselinesIncludingPostcheck(t *testing.T) {
	r, data, revision, plan := resetFixture(t)
	// The first component finished; the next failed its postcheck after the body.
	data.installations = data.installations[1:]
	parent := plan.Steps[1]
	plan.Steps = []domain.RunPlanStep{{ID: "post", SourceNodeID: parent.SourceNodeID, ComponentID: parent.ComponentID, ParentActionID: parent.ActionID, Phase: "post", Limit: parent.Limit}}
	data.receipts = []domain.ActionExecutionReceipt{{ComponentID: parent.ComponentID, SourceNodeID: parent.SourceNodeID, BackupRef: parent.BackupRef, Backup: *parent.Backup, Status: "executed", StartedAt: time.Now()}}
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); err != nil {
		t.Fatalf("postcheck continuation lost recovery coverage: %v", err)
	}
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, true); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("pending postcheck counted as clean")
	}
	data.installations = data.installations[1:]
	data.receipts[0].Status = "verified"
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, true); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentResetValidatesTaskTargetsAgainstFullLockedInventory(t *testing.T) {
	r, _, revision, plan := resetFixture(t)
	plan.Steps[0].Playbook = "managed/component/tasks/rollback.yml"
	plan.ParentSteps = append([]domain.RunPlanStep{}, plan.Steps...)
	called := false
	r.inspector = &testutil.Runner{TargetFunc: func(playbooks []string, scope ansible.TaskTargetScope) error {
		called = true
		if len(playbooks) == 0 || playbooks[0] != plan.Steps[0].Playbook {
			t.Fatalf("missing recovery source: %v", playbooks)
		}
		if !reflect.DeepEqual(scope.Hosts, []string{"control-a", "control-b", "worker-a", "worker-b"}) {
			t.Fatalf("invalid permitted hosts: %v", scope.Hosts)
		}
		if !reflect.DeepEqual(scope.Groups["media"], []string{"media"}) || len(scope.Groups["all"]) != 6 {
			t.Fatalf("scope lost protected inventory membership: %v", scope.Groups)
		}
		return errors.New("outside locked reset hosts")
	}}
	if err := r.validateEnvironmentReset(context.Background(), revision, plan, false); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "outside locked reset hosts") {
		t.Fatalf("unsafe tasks were accepted: %v", err)
	}
	if !called {
		t.Fatal("recovery task validation skipped")
	}
}

func TestEnvironmentResetRetryAfterPostcheckContinuation(t *testing.T) {
	planner, data, revision, plan := resetFixture(t)
	original := append([]domain.RunPlanStep{}, plan.Steps...)
	plan.Steps = nil
	for _, body := range original {
		body.RetrySafe = true
		plan.Steps = append(plan.Steps, body)
		post := domain.RunPlanStep{ID: body.ID + "-post", NodeID: body.ID + "-post", SourceNodeID: body.SourceNodeID, ComponentID: body.ComponentID, ReleaseID: body.ReleaseID, ActionID: body.ActionID + "-post", ParentActionID: body.ActionID, Phase: "post", Action: domain.ActionCheck, Limit: body.Limit, RetrySafe: true}
		plan.Steps = append(plan.Steps, post)
	}
	// Original attempt completed control body but failed its postcheck.
	first, err := retryLockedSteps(plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan.Steps = first
	// Retry completed control postcheck, then failed the workers body.
	data.installations = data.installations[1:]
	second, err := retryLockedSteps(plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan.Steps = second
	if len(second) != 2 || second[0].Limit != "workers" || second[0].Phase != "execute" {
		t.Fatalf("completed teardown replayed: %+v", second)
	}
	if err := planner.validateEnvironmentReset(context.Background(), revision, plan, false); err != nil {
		t.Fatalf("second continuation rejected after completed control cleanup: %v", err)
	}
}
