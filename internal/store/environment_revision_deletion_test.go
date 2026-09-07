package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"codex/platform-demo/internal/domain"
)

func deletionEnvironment(t *testing.T, s *Store, id string) (domain.Environment, domain.EnvironmentRevision, domain.AuditEvent) {
	t.Helper()
	ctx := context.Background()
	e := domain.Environment{ID: id, Name: id, OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	r := domain.EnvironmentRevision{ID: id + "-r1", EnvironmentID: id, Revision: 1, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, e, r); err != nil {
		t.Fatal(err)
	}
	r2 := r
	r2.ID, r2.Revision = id+"-r2", 2
	if err := s.CreateEnvironmentRevision(ctx, r2, EnvironmentRevisionWrite{ExpectedCurrentRevisionID: r.ID}); err != nil {
		t.Fatal(err)
	}
	audit := domain.AuditEvent{ID: id + "-deleted", ActorID: e.OwnerID, Action: "environment.revision_deleted", ResourceType: "environment_revision", ResourceID: r.ID, CreatedAt: testNow}
	return e, r, audit
}

func TestEnvironmentRevisionDeletionAuditFailureRollsBack(t *testing.T) {
	a, _ := catalogStorePair(t)
	ctx := context.Background()
	e, r, audit := deletionEnvironment(t, a, "audit-rollback")
	if err := a.SaveEnvironmentHealthCheck(ctx, domain.EnvironmentHealthCheck{ID: "tcp", EnvironmentID: e.ID, EnvironmentRevisionID: r.ID, Status: "healthy", Results: []domain.EnvironmentEndpointCheck{}, CheckedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveEnvironmentSSHCheck(ctx, domain.EnvironmentSSHCheck{ID: "ssh", EnvironmentID: e.ID, EnvironmentRevisionID: r.ID, Status: "healthy", Results: []domain.EnvironmentSSHHostCheck{}, CheckedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	// Make only the final audit write fail, after the three DELETE statements.
	if _, err := a.DB().ExecContext(ctx, `CREATE TRIGGER reject_version_delete_audit BEFORE INSERT ON audit_events WHEN NEW.action='environment.revision_deleted' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteEnvironmentRevision(ctx, e.ID, r.ID, e.OwnerID, audit); err == nil {
		t.Fatal("expected audit failure")
	}
	impact, err := a.EnvironmentRevisionDeletionImpact(ctx, e.ID, r.ID)
	if err != nil || impact.HealthCheckCount != 1 || impact.SSHCheckCount != 1 {
		t.Fatalf("partial deletion: %+v %v", impact, err)
	}
	var count int
	if err := a.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE id=?`, audit.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit=%d %v", count, err)
	}
}

func TestEnvironmentRevisionDeletionRechecksLifecycleAfterPreview(t *testing.T) {
	for _, change := range []string{"owner", "current", "archived"} {
		t.Run(change, func(t *testing.T) {
			a, b := catalogStorePair(t)
			ctx := context.Background()
			e, r, audit := deletionEnvironment(t, a, "stale-preview")
			if _, err := a.EnvironmentRevisionDeletionImpact(ctx, e.ID, r.ID); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE environments SET owner_id='component-owner-a' WHERE id=?`
			if change == "current" {
				query = `UPDATE environments SET current_revision_id='stale-preview-r1' WHERE id=?`
			}
			if change == "archived" {
				query = `UPDATE environments SET archived_at='2026-09-07' WHERE id=?`
			}
			if _, err := b.DB().ExecContext(ctx, query, e.ID); err != nil {
				t.Fatal(err)
			}
			err := a.DeleteEnvironmentRevision(ctx, e.ID, r.ID, e.OwnerID, audit)
			want := domain.ErrConflict
			if change == "owner" {
				want = domain.ErrForbidden
			}
			if !errors.Is(err, want) {
				t.Fatalf("delete=%v want=%v", err, want)
			}
			if _, err := a.GetEnvironmentRevision(ctx, r.ID); err != nil {
				t.Fatalf("version missing: %v", err)
			}
		})
	}
}

func TestEnvironmentRevisionDeletionConcurrentRunReference(t *testing.T) {
	a, b := catalogStorePair(t)
	ctx := context.Background()
	// Independent connection pools contend on the same SQLite writer lock.
	for i := 0; i < 8; i++ {
		e, r, audit := deletionEnvironment(t, a, fmt.Sprintf("concurrent-%d", i))
		if _, err := a.EnvironmentRevisionDeletionImpact(ctx, e.ID, r.ID); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		deleted, created := make(chan error, 1), make(chan error, 1)
		go func() { <-start; deleted <- a.DeleteEnvironmentRevision(ctx, e.ID, r.ID, e.OwnerID, audit) }()
		go func() {
			<-start
			created <- b.CreateRun(ctx, domain.Run{ID: e.ID + "-run", Kind: domain.RunComponentTest, Status: domain.RunFailed, RequestedBy: e.OwnerID, EnvironmentID: e.ID, EnvironmentRevisionID: r.ID, InputSnapshot: map[string]any{}, CreatedAt: testNow, FinishedAt: &testNow}, nil)
		}()
		close(start)
		deleteErr, createErr := <-deleted, <-created
		if (deleteErr == nil) == (createErr == nil) {
			t.Fatalf("exactly one operation must succeed: delete=%v create=%v", deleteErr, createErr)
		}
		if createErr == nil {
			if !errors.Is(deleteErr, domain.ErrConflict) {
				t.Fatalf("reference conflict: %v", deleteErr)
			}
			if _, err := a.GetEnvironmentRevision(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
		} else if _, err := a.GetEnvironmentRevision(ctx, r.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("deleted version exists: %v", err)
		}
	}
	rows, err := a.DB().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("deletion left a dangling reference")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
