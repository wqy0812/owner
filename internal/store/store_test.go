package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

var testNow = time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, u := range []domain.User{
		{ID: "component-alice", Name: "Alice", Role: domain.RoleComponentOwner, CreatedAt: testNow},
		{ID: "component-bob", Name: "Bob", Role: domain.RoleComponentOwner, CreatedAt: testNow},
		{ID: "scenario-carol", Name: "Carol", Role: domain.RoleScenarioOwner, CreatedAt: testNow},
		{ID: "environment-dave", Name: "Dave", Role: domain.RoleEnvironmentOwner, CreatedAt: testNow},
	} {
		if err := s.UpsertUser(context.Background(), u); err != nil {
			t.Fatalf("UpsertUser: %v", err)
		}
	}
	return s
}

func componentFixture(id, owner string) domain.Component {
	return domain.Component{
		ID: id, Slug: id, Name: id, Layer: domain.LayerRuntimeState, Category: domain.CategoryRuntime,
		Kind: domain.ComponentSoftware, Requiredness: domain.RequiredProfile,
		OwnerID: owner, CreatedAt: testNow, UpdatedAt: testNow,
	}
}

func releaseFixture(id, component, version string, status domain.ReleaseStatus) domain.ComponentRelease {
	r := domain.ComponentRelease{
		ID: id, ComponentID: component, Version: version, Type: domain.ReleaseAtomic,
		Status: status, RiskLevel: domain.RiskLow, CreatedAt: testNow,
		EnvironmentConstraints: map[string]any{"arch": "amd64"},
		ParameterSchema:        map[string]any{"type": "object"},
		Actions: []domain.ActionDefinition{{
			ID: id + "-install", Name: "install", Kind: domain.ActionInstall,
			Playbook: "demo/install.yml", HostGroup: "workers", TimeoutSeconds: 60, RiskLevel: domain.RiskLow,
		}},
	}
	if status == domain.ReleaseReleased {
		r.ReleasedAt = ptr(testNow)
	}
	return r
}

func ptr[T any](v T) *T { return &v }

func TestComponentVisibilityAndReleaseImmutability(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for _, c := range []domain.Component{componentFixture("alice-private", "component-alice"), componentFixture("bob-private", "component-bob"), componentFixture("bob-public", "component-bob")} {
		if err := s.CreateComponent(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("alice-draft", "alice-private", "1.0.0", domain.ReleaseDraft)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("bob-draft", "bob-private", "1.0.0", domain.ReleaseDraft)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("bob-release", "bob-public", "1.0.0", domain.ReleaseReleased)); err != nil {
		t.Fatal(err)
	}

	alice, _ := s.GetUser(ctx, "component-alice")
	got, err := s.ListComponents(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Alice sees %d components, want own private plus Bob public", len(got))
	}
	for _, c := range got {
		if c.ID == "bob-private" {
			t.Fatal("Alice can see Bob's draft-only component")
		}
		if c.ID == "bob-public" && len(c.Releases) != 1 {
			t.Fatalf("expected one released Bob version, got %d", len(c.Releases))
		}
	}

	draft, _ := s.GetComponentRelease(ctx, "alice-draft")
	if err := s.PublishComponentRelease(ctx, draft.ID, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	draft.ReleaseNotes = "illegal mutation"
	if err := s.UpdateDraftRelease(ctx, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released update = %v, want conflict", err)
	}
}

func TestComponentClassificationDatabaseConstraint(t *testing.T) {
	s := newTestStore(t)
	component := componentFixture("invalid-classification", "component-alice")
	component.Category = domain.CategoryDNS
	if err := s.CreateComponent(context.Background(), component); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("invalid layer/category insert=%v, want conflict", err)
	}
}

func TestSQLiteConnectionPragmasApplyAcrossPool(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	connections := make([]*sql.Conn, 0, 8)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		connection, err := s.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var foreignKeys, busyTimeout int
		if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("connection %d pragmas foreign_keys=%d busy_timeout=%d", i, foreignKeys, busyTimeout)
		}
	}
}

func TestScenarioEnvironmentRunApprovalAndFIFO(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateComponent(ctx, componentFixture("runtime", "component-alice")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("runtime-1", "runtime", "1.0.0", domain.ReleaseReleased)); err != nil {
		t.Fatal(err)
	}

	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", Name: "Runtime", ReleaseID: "runtime-1", Action: domain.ActionInstall, HostGroup: "workers", Values: map[string]any{}}}, Edges: []domain.ScenarioEdge{}}
	scenario := domain.Scenario{ID: "cluster", Slug: "cluster", Name: "Cluster", OwnerID: "scenario-carol", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "cluster-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, Graph: graph, ExecutionPolicy: map[string]any{}, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionTestPassed}, domain.RevisionReleased, testNow); err != nil {
		t.Fatal(err)
	}

	inventory, _ := json.Marshal(map[string]any{"all": map[string]any{"hosts": map[string]any{"localhost": map[string]any{"ansible_connection": "local"}}}})
	env := domain.Environment{ID: "lab", Name: "Lab", OwnerID: "environment-dave", CreatedAt: testNow, UpdatedAt: testNow}
	envRev := domain.EnvironmentRevision{ID: "lab-r1", EnvironmentID: env.ID, Revision: 1, Facts: map[string]any{"arch": "amd64"}, Inventory: inventory, Parameters: map[string]any{}, CredentialRefs: []domain.CredentialRef{{Name: "ssh", Kind: "envVarRef", Reference: "TEST_KEY"}}, MaxConcurrent: 1, CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, env, envRev); err != nil {
		t.Fatal(err)
	}

	approval := domain.Approval{ID: "approval-1", RunID: "run-1", Status: "pending", RequestedAt: testNow}
	run1 := domain.Run{ID: "run-1", Kind: domain.RunScenarioTest, Status: domain.RunAwaitingApproval, RequestedBy: "scenario-carol", EnvironmentID: env.ID, EnvironmentRevisionID: envRev.ID, ScenarioRevisionID: revision.ID, Destructive: true, InputSnapshot: map[string]any{}, CreatedAt: testNow}
	if err := s.CreateRun(ctx, run1, &approval); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideApproval(ctx, approval.ID, "environment-dave", "approved", "safe lab", testNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	run2 := run1
	run2.ID = "run-2"
	run2.Status = domain.RunQueued
	run2.Destructive = false
	run2.CreatedAt = testNow.Add(time.Minute)
	if err := s.CreateRun(ctx, run2, nil); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimNextRun(ctx, env.ID, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "run-1" {
		t.Fatalf("claimed %s, want FIFO run-1", claimed.ID)
	}
	if _, err := s.ClaimNextRun(ctx, env.ID, testNow.Add(3*time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second claim=%v, want environment conflict", err)
	}
	if err := s.UpdateRunStatus(ctx, claimed.ID, []domain.RunStatus{domain.RunRunning}, domain.RunSucceeded, "", testNow.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimNextRun(ctx, env.ID, testNow.Add(5*time.Minute))
	if err != nil || claimed.ID != "run-2" {
		t.Fatalf("second FIFO claim=%+v err=%v", claimed, err)
	}

	cancelledRun := run1
	cancelledRun.ID = "run-cancelled"
	cancelledRun.Status = domain.RunAwaitingApproval
	cancelledRun.CreatedAt = testNow.Add(10 * time.Minute)
	cancelledApproval := domain.Approval{ID: "approval-cancelled", RunID: cancelledRun.ID, Status: "pending", RequestedAt: cancelledRun.CreatedAt}
	if err := s.CreateRun(ctx, cancelledRun, &cancelledApproval); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRunStatus(ctx, cancelledRun.ID, []domain.RunStatus{domain.RunAwaitingApproval}, domain.RunCancelled, "cancelled", testNow.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideApproval(ctx, cancelledApproval.ID, "environment-dave", "approved", "too late", testNow.Add(12*time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("approval of cancelled run = %v, want conflict", err)
	}
	gotApproval, err := s.GetApproval(ctx, cancelledApproval.ID)
	if err != nil || gotApproval.Status != "pending" {
		t.Fatalf("cancelled approval mutated despite rollback: %+v err=%v", gotApproval, err)
	}
	componentTest := domain.Run{
		ID: "component-test-active", Kind: domain.RunComponentTest, Status: domain.RunQueued,
		RequestedBy: "component-alice", EnvironmentID: env.ID, EnvironmentRevisionID: envRev.ID,
		ComponentReleaseID: "runtime-1", InputSnapshot: map[string]any{}, CreatedAt: testNow.Add(20 * time.Minute),
	}
	if err := s.CreateRun(ctx, componentTest, nil); err != nil {
		t.Fatal(err)
	}
	if active, err := s.HasActiveComponentTest(ctx, componentTest.ComponentReleaseID); err != nil || !active {
		t.Fatalf("active component test=%v err=%v", active, err)
	}
	if err := s.UpdateRunStatus(ctx, componentTest.ID, []domain.RunStatus{domain.RunQueued}, domain.RunCancelled, "cancelled", testNow.Add(21*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active, err := s.HasActiveComponentTest(ctx, componentTest.ComponentReleaseID); err != nil || active {
		t.Fatalf("terminal component test active=%v err=%v", active, err)
	}
}

func TestRestartInterruptsRunAndReleasesScenarioTestingState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	scenario := domain.Scenario{ID: "restart-scenario", Slug: "restart-scenario", Name: "Restart", OwnerID: "scenario-carol", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "restart-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionTesting, Graph: domain.ScenarioGraph{}, ExecutionPolicy: map[string]any{}, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	environment := domain.Environment{ID: "restart-env", Name: "Restart Env", OwnerID: "environment-dave", CreatedAt: testNow, UpdatedAt: testNow}
	environmentRevision := domain.EnvironmentRevision{ID: "restart-env-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{}, Inventory: json.RawMessage(`{"hosts":[]}`), Parameters: map[string]any{}, MaxConcurrent: 1, CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, environment, environmentRevision); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "restart-run", Kind: domain.RunScenarioTest, Status: domain.RunRunning, RequestedBy: "scenario-carol", EnvironmentID: environment.ID, EnvironmentRevisionID: environmentRevision.ID, ScenarioRevisionID: revision.ID, InputSnapshot: map[string]any{"steps": []any{map[string]any{"id": "step"}}}, CreatedAt: testNow}
	if err := s.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	started := testNow.Add(30 * time.Second)
	step := domain.RunStep{ID: "restart-step", RunID: run.ID, NodeID: "node", Name: "running", Status: domain.RunRunning, StartedAt: &started}
	if err := s.CreateRunStep(ctx, step); err != nil {
		t.Fatal(err)
	}
	if count, err := s.MarkRunningInterrupted(ctx, testNow.Add(time.Minute)); err != nil || count != 1 {
		t.Fatalf("MarkRunningInterrupted count=%d err=%v", count, err)
	}
	gotRun, _ := s.GetRun(ctx, run.ID)
	gotRevision, _ := s.GetScenarioRevision(ctx, revision.ID)
	steps, _ := s.ListRunSteps(ctx, run.ID)
	if gotRun.Status != domain.RunInterrupted || gotRevision.Status != domain.RevisionDraft || len(steps) != 1 || steps[0].Status != domain.RunInterrupted || steps[0].FinishedAt == nil {
		t.Fatalf("restart state run=%s revision=%s steps=%+v", gotRun.Status, gotRevision.Status, steps)
	}
}

func TestNotificationsAuditSessionAndLogs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateSession(ctx, "hash", "component-alice", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if u, err := s.UserBySession(ctx, "hash"); err != nil || u.ID != "component-alice" {
		t.Fatalf("session user=%+v err=%v", u, err)
	}

	n := domain.Notification{ID: "n1", UserID: "component-alice", Type: "component_released", Title: "Runtime updated", Body: "1.1.0", Payload: map[string]any{"path": []any{"runtime", "kubernetes"}}, CreatedAt: testNow}
	if err := s.CreateNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListNotifications(ctx, n.UserID, true); err != nil || len(got) != 1 {
		t.Fatalf("notifications=%+v err=%v", got, err)
	}
	if err := s.MarkNotificationRead(ctx, n.ID, n.UserID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListNotifications(ctx, n.UserID, true); len(got) != 0 {
		t.Fatalf("unread notifications=%d", len(got))
	}

	e := domain.AuditEvent{ID: "audit-1", ActorID: n.UserID, Action: "component.release", ResourceType: "component_release", ResourceID: "r1", Metadata: map[string]any{"secret": "[REDACTED]"}, CreatedAt: testNow}
	if err := s.AppendAudit(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListAudit(ctx, 10); err != nil || len(got) != 1 {
		t.Fatalf("audit=%+v err=%v", got, err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE audit_events SET action='tampered' WHERE id=?`, e.ID); err == nil {
		t.Fatal("append-only audit event was updated")
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM audit_events WHERE id=?`, e.ID); err == nil {
		t.Fatal("append-only audit event was deleted")
	}
}

func TestScenarioGraphEditInvalidatesTestAndReleasedIsImmutable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	scenario := domain.Scenario{ID: "editable", Slug: "editable", Name: "Editable", OwnerID: "scenario-carol", CreatedAt: testNow, UpdatedAt: testNow}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: "release", Action: domain.ActionInstall, HostGroup: "all"}}, Edges: []domain.ScenarioEdge{}}
	revision := domain.ScenarioRevision{ID: "editable-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, Graph: graph, ExecutionPolicy: map[string]any{}, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, testNow); err != nil {
		t.Fatal(err)
	}
	graph.Nodes[0].Name = "edited"
	if err := s.SaveScenarioGraph(ctx, revision.ID, graph, map[string]any{"waves": 2}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetScenarioRevision(ctx, revision.ID)
	if got.Status != domain.RevisionDraft || got.TestPassedAt != nil {
		t.Fatalf("edit did not invalidate test: %+v", got)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionTestPassed}, domain.RevisionReleased, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScenarioGraph(ctx, revision.ID, graph, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released graph edit=%v, want conflict", err)
	}
}
