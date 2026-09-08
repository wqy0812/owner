package store

import (
	"codex/platform-demo/internal/testutil/runfixture"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"codex/platform-demo/internal/domain"
)

func emptyComponentForDeletion(t *testing.T, s *Store, id string) (domain.Component, domain.AuditEvent) {
	t.Helper()
	c := componentFixture(id, "component-owner-a")
	if err := s.CreateComponent(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c, domain.AuditEvent{ID: id + "-deleted", ActorID: c.OwnerID, Action: "component.deleted", ResourceType: "component", ResourceID: c.ID, CreatedAt: testNow}
}

func TestEmptyComponentDeletionRetainsAuditAndRemovesEmptyLines(t *testing.T) {
	s, _ := catalogStorePair(t)
	ctx := context.Background()
	c, audit := emptyComponentForDeletion(t, s, "empty")
	created := audit
	created.ID, created.Action = "created", "component.created"
	if err := s.AppendAudit(ctx, created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('empty-line',?,'empty',?)`, c.ID, timeText(testNow)); err != nil {
		t.Fatal(err)
	}
	if can, err := s.CanDeleteComponent(ctx, c.ID, c.OwnerID); err != nil || !can {
		t.Fatalf("eligibility=%v %v", can, err)
	}
	epoch := publicationEpoch(t, s)
	if err := s.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetComponent(ctx, c.ID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted component=%v", err)
	}
	var lines, audits int
	var metadata string
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM component_release_lines WHERE component_id=?`, c.ID).Scan(&lines); err != nil || lines != 0 {
		t.Fatalf("lines=%d %v", lines, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE resource_id=?`, c.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit history=%d %v", audits, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT metadata_json FROM audit_events WHERE id=?`, audit.ID).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil || meta["componentName"] != c.Name || meta["componentSlug"] != c.Slug || meta["ownerId"] != c.OwnerID {
		t.Fatalf("audit metadata=%s %v", metadata, err)
	}
	if publicationEpoch(t, s) != epoch+1 {
		t.Fatal("deletion must advance the catalog epoch once")
	}
	if err := s.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("repeat delete=%v", err)
	}
}

func TestEmptyComponentDeletionRechecksVersionsAndOwner(t *testing.T) {
	for _, status := range []domain.ReleaseStatus{domain.ReleaseDraft, domain.ReleaseReleased, domain.ReleaseDeprecated} {
		t.Run(string(status), func(t *testing.T) {
			a, b := catalogStorePair(t)
			ctx := context.Background()
			c, audit := emptyComponentForDeletion(t, a, "version-guard")
			if can, err := a.CanDeleteComponent(ctx, c.ID, c.OwnerID); err != nil || !can {
				t.Fatalf("before=%v %v", can, err)
			}
			r := releaseFixture("guard-release", c.ID, "1.0.0", status)
			if err := b.CreateComponentRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
			if can, err := a.CanDeleteComponent(ctx, c.ID, c.OwnerID); err != nil || can {
				t.Fatalf("after=%v %v", can, err)
			}
			epoch := publicationEpoch(t, a)
			err := a.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit)
			var conflict *ComponentDeletionConflict
			if !errors.As(err, &conflict) || conflict.Code != "component.not_empty" {
				t.Fatalf("delete=%v", err)
			}
			if _, err := a.GetComponentRelease(ctx, r.ID); err != nil || publicationEpoch(t, a) != epoch {
				t.Fatalf("blocked deletion changed version/epoch: %v", err)
			}
			if status == domain.ReleaseDeprecated {
				releaseAudit := audit
				releaseAudit.ID, releaseAudit.Action, releaseAudit.ResourceID = "release-deleted", "component_release.deleted", r.ID
				if err := b.DeleteComponentRelease(ctx, r.ID, releaseAudit); err != nil {
					t.Fatal(err)
				}
				if can, err := a.CanDeleteComponent(ctx, c.ID, c.OwnerID); err != nil || !can {
					t.Fatalf("emptied eligibility=%v %v", can, err)
				}
				if err := a.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("changed owner", func(t *testing.T) {
		a, b := catalogStorePair(t)
		ctx := context.Background()
		c, audit := emptyComponentForDeletion(t, a, "owner-guard")
		if _, err := b.DB().ExecContext(ctx, `UPDATE components SET owner_id='environment-owner-a' WHERE id=?`, c.ID); err != nil {
			t.Fatal(err)
		}
		if can, err := a.CanDeleteComponent(ctx, c.ID, c.OwnerID); err != nil || can {
			t.Fatalf("eligibility=%v %v", can, err)
		}
		if err := a.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("delete=%v", err)
		}
	})
}

func TestEmptyComponentDeletionBlocksReferences(t *testing.T) {
	for _, reference := range []string{"dependency", "installation", "receipt", "nonempty-line"} {
		t.Run(reference, func(t *testing.T) {
			s, _ := catalogStorePair(t)
			ctx := context.Background()
			c, audit := emptyComponentForDeletion(t, s, "referenced-empty")
			other, _ := emptyComponentForDeletion(t, s, "other")
			r := releaseFixture("other-release", other.ID, "1.0.0", domain.ReleaseDraft)
			if err := s.CreateComponentRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
			e := domain.Environment{ID: "environment", Name: "Environment", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
			er := domain.EnvironmentRevision{ID: "environment-r1", EnvironmentID: e.ID, Revision: 1, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
			if err := s.CreateEnvironment(ctx, e, er); err != nil {
				t.Fatal(err)
			}
			if err := insertStoredRunForTest(ctx, s, domain.Run{ID: "run", Kind: domain.RunComponentTest, Status: domain.RunFailed, RequestedBy: c.OwnerID, EnvironmentID: e.ID, EnvironmentRevisionID: er.ID, ComponentReleaseID: r.ID, Snapshot: runfixture.Snapshot(map[string]any{}), CreatedAt: testNow}); err != nil {
				t.Fatal(err)
			}
			// The schema stores component and Release references separately. Even
			// an inconsistent historical pair must never be silently cascaded.
			queries := map[string]string{
				"dependency":    `INSERT INTO component_dependencies(kind,id,release_id,upstream_component_id,upstream_release_id) VALUES('execution','dep','other-release',?,'other-release')`,
				"installation":  `INSERT INTO environment_component_installations(environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,installed_at) VALUES('environment',?,'other-release','run','backup','{}','2026-09-07')`,
				"receipt":       `INSERT INTO action_execution_receipts(run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at) VALUES('run','step','environment',?,'other-release','action','node','started','backup','{}','2026-09-07','2026-09-07')`,
				"nonempty-line": `UPDATE component_release_lines SET component_id=? WHERE id='line-other-release'`,
			}
			if _, err := s.DB().ExecContext(ctx, queries[reference], c.ID); err != nil {
				t.Fatal(err)
			}
			if can, err := s.CanDeleteComponent(ctx, c.ID, c.OwnerID); err != nil || can {
				t.Fatalf("eligibility=%v %v", can, err)
			}
			if err := s.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("delete=%v", err)
			}
			if _, err := s.GetComponent(ctx, c.ID, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetComponentRelease(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEmptyComponentDeletionAuditFailureRollsBack(t *testing.T) {
	s, _ := catalogStorePair(t)
	ctx := context.Background()
	c, audit := emptyComponentForDeletion(t, s, "audit-rollback")
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('empty-line',?,'empty','2026-09-07')`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `CREATE TRIGGER reject_component_delete_audit BEFORE INSERT ON audit_events WHEN NEW.action='component.deleted' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	epoch := publicationEpoch(t, s)
	if err := s.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit); err == nil {
		t.Fatal("expected audit failure")
	}
	if _, err := s.GetComponent(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	var lines, audits int
	if err := s.DB().QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM component_release_lines WHERE component_id=?),(SELECT COUNT(*) FROM audit_events WHERE id=?)`, c.ID, audit.ID).Scan(&lines, &audits); err != nil || lines != 1 || audits != 0 || publicationEpoch(t, s) != epoch {
		t.Fatalf("partial deletion: lines=%d audits=%d err=%v", lines, audits, err)
	}
}

func TestEmptyComponentDeletionConcurrentFirstRelease(t *testing.T) {
	a, b := catalogStorePair(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		c, audit := emptyComponentForDeletion(t, a, fmt.Sprintf("race-%d", i))
		r := releaseFixture(c.ID+"-r1", c.ID, "1.0.0", domain.ReleaseDraft)
		start := make(chan struct{})
		deleted, created := make(chan error, 1), make(chan error, 1)
		go func() { <-start; deleted <- a.DeleteEmptyComponent(ctx, c.ID, c.OwnerID, audit) }()
		go func() { <-start; created <- b.CreateComponentRelease(ctx, r) }()
		close(start)
		deleteErr, createErr := <-deleted, <-created
		if (deleteErr == nil) == (createErr == nil) {
			t.Fatalf("exactly one writer must succeed: delete=%v create=%v", deleteErr, createErr)
		}
		if createErr == nil {
			if !errors.Is(deleteErr, domain.ErrConflict) {
				t.Fatalf("delete conflict=%v", deleteErr)
			}
			if _, err := a.GetComponentRelease(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
		} else if _, err := a.GetComponent(ctx, c.ID, true); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("deleted component=%v", err)
		}
	}
	rows, err := a.DB().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("concurrent deletion left orphaned records")
	}
}
