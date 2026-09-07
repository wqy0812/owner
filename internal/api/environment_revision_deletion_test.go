package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func revisionDeletionFixture(t *testing.T, count int) (*apiFixture, *http.Cookie, string, []string) {
	t.Helper()
	f := newAPIFixture(t)
	owner := f.session(seed.EnvironmentOwnerID)
	created := f.request(http.MethodPost, "/api/v1/environments", map[string]any{"name": "Version deletion", "facts": completeTestEnvironmentFacts()}, owner)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %s", created.Body.String())
	}
	data := decodeEnvelope(t, created)["data"].(map[string]any)
	id := data["id"].(string)
	ids := []string{data["currentRevisionId"].(string)}
	for i := 1; i < count; i++ {
		response := f.request(http.MethodPut, "/api/v1/environments/"+id+"/facts", map[string]any{"facts": completeTestEnvironmentFacts(), "changeReason": fmt.Sprintf("Revision original note %d", i)}, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("save: %s", response.Body.String())
		}
		ids = append(ids, decodeEnvelope(t, response)["data"].(map[string]any)["currentRevisionId"].(string))
	}
	return f, owner, id, ids
}

func TestEnvironmentRevisionDeletionAPI(t *testing.T) {
	f, owner, id, ids := revisionDeletionFixture(t, 3)
	ctx := context.Background()
	now := time.Now().UTC()
	path := "/api/v1/environments/" + id + "/revisions/" + ids[0]
	for _, actor := range []string{seed.ComponentOwnerRuntimeID, seed.PlatformAdminID, "other-environment-owner"} {
		if actor == "other-environment-owner" {
			if err := f.database.UpsertUser(ctx, domain.User{ID: actor, Name: actor, Role: domain.RoleEnvironmentOwner, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
		cookie := f.session(actor)
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			target := path
			if method == http.MethodGet {
				target += "/deletion-impact"
			}
			if response := f.request(method, target, nil, cookie); response.Code != http.StatusForbidden {
				t.Fatalf("%s %s: %d %s", actor, method, response.Code, response.Body.String())
			}
		}
	}
	if response := f.request(http.MethodDelete, "/api/v1/environments/environment-test/revisions/"+ids[0], nil, owner); response.Code != http.StatusNotFound {
		t.Fatalf("wrong environment: %d", response.Code)
	}
	if response := f.request(http.MethodDelete, path+"-missing", nil, owner); response.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", response.Code)
	}
	if response := f.request(http.MethodDelete, "/api/v1/environments/"+id+"/revisions/"+ids[2], nil, owner); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "当前环境版本") {
		t.Fatalf("current: %s", response.Body.String())
	}
	if err := f.database.SaveEnvironmentHealthCheck(ctx, domain.EnvironmentHealthCheck{ID: "old-tcp", EnvironmentID: id, EnvironmentRevisionID: ids[0], Status: "healthy", Results: []domain.EnvironmentEndpointCheck{}, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.SaveEnvironmentSSHCheck(ctx, domain.EnvironmentSSHCheck{ID: "old-ssh", EnvironmentID: id, EnvironmentRevisionID: ids[0], Status: "healthy", Results: []domain.EnvironmentSSHHostCheck{}, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Another version's run must not prohibit cleaning this unused version.
	if err := f.database.CreateRun(ctx, domain.Run{ID: "another-version-run", Kind: domain.RunComponentTest, Status: domain.RunFailed, RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: id, EnvironmentRevisionID: ids[1], InputSnapshot: map[string]any{}, CreatedAt: now, FinishedAt: &now}, nil); err != nil {
		t.Fatal(err)
	}
	preview := f.request(http.MethodGet, path+"/deletion-impact", nil, owner)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %s", preview.Body.String())
	}
	impact := decodeEnvelope(t, preview)["data"].(map[string]any)
	if impact["canDelete"] != true || impact["runCount"] != float64(0) || impact["healthCheckCount"] != float64(1) || impact["sshCheckCount"] != float64(1) {
		t.Fatalf("impact=%#v", impact)
	}
	if _, leaked := impact["OwnerID"]; leaked {
		t.Fatal("internal owner field leaked")
	}
	if _, err := f.database.GetEnvironmentRevision(ctx, ids[0]); err != nil {
		t.Fatalf("preview deleted data: %v", err)
	}
	if response := f.request(http.MethodDelete, path, nil, owner); response.Code != http.StatusOK {
		t.Fatalf("delete: %s", response.Body.String())
	}
	environment, err := f.database.GetEnvironment(ctx, id, true)
	if err != nil || environment.CurrentRevisionID != ids[2] || len(environment.Revisions) != 2 {
		t.Fatalf("remaining environment=%+v err=%v", environment, err)
	}
	if environment.Revisions[0].Revision != 3 || environment.Revisions[1].Revision != 2 || environment.Revisions[1].ChangeReason != "Revision original note 1" {
		t.Fatalf("history was rewritten: %+v", environment.Revisions)
	}
	for _, table := range []string{"environment_health_checks", "environment_ssh_checks"} {
		var count int
		if err := f.database.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE environment_revision_id=?", ids[0]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var auditCount int
	if err := f.database.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='environment.revision_deleted' AND resource_id=? AND json_extract(metadata_json,'$.healthCheckCount')=1 AND json_extract(metadata_json,'$.sshCheckCount')=1`, ids[0]).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit=%d err=%v", auditCount, err)
	}
	if response := f.request(http.MethodDelete, path, nil, owner); response.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: %d", response.Code)
	}
	if _, err := f.database.GetEnvironmentRevision(ctx, ids[0]); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	for _, operation := range []string{"save", "restore"} {
		target, method := "/api/v1/environments/"+id+"/facts", http.MethodPut
		body := map[string]any{"facts": completeTestEnvironmentFacts(), "changeReason": operation}
		if operation == "restore" {
			target, method = "/api/v1/environments/"+id+"/revisions/"+ids[1]+"/restore", http.MethodPost
			delete(body, "facts")
		}
		response := f.request(method, target, body, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %s", operation, response.Body.String())
		}
		want := 4
		if operation == "restore" {
			want = 5
		}
		actual := decodeEnvelope(t, response)["data"].(map[string]any)["currentRevision"].(map[string]any)["revision"]
		if actual != float64(want) {
			t.Fatalf("%s number=%v", operation, actual)
		}
	}
}

func TestEnvironmentRevisionDeletionRetainsEveryRunStatus(t *testing.T) {
	for _, status := range []string{"queued", "awaiting_approval", "running", "succeeded", "failed", "cancelled", "rejected", "interrupted", "archived", "cleaned"} {
		t.Run(status, func(t *testing.T) {
			f, owner, id, ids := revisionDeletionFixture(t, 2)
			ctx := context.Background()
			now := time.Now().UTC()
			if status == "cleaned" {
				_, err := f.database.DB().ExecContext(ctx, `INSERT INTO run_cleanup_history(id,kind,status,requested_by,environment_id,environment_revision_id,action_kind,created_at,finished_at,cleaned_at,actor_id,reason,identity_json) VALUES('cleaned','component_test','failed',?, ?,?,'install',?,?,?,?,'manual','{}')`, seed.EnvironmentOwnerID, id, ids[0], now, now, now, seed.EnvironmentOwnerID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				runStatus := domain.RunStatus(status)
				if status == "archived" {
					runStatus = domain.RunFailed
				}
				if err := f.database.CreateRun(ctx, domain.Run{ID: "retained", Kind: domain.RunComponentTest, Status: runStatus, RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: id, EnvironmentRevisionID: ids[0], InputSnapshot: map[string]any{}, CreatedAt: now, FinishedAt: &now}, nil); err != nil {
					t.Fatal(err)
				}
				if status == "archived" {
					_, err := f.database.DB().ExecContext(ctx, `INSERT INTO run_archive_files(run_id,relative_path,format_version,size_bytes,sha256,source_digest,log_count,archived_at) VALUES('retained','retained.gz','v1',1,'hash','digest',0,?)`, now)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			path := "/api/v1/environments/" + id + "/revisions/" + ids[0]
			preview := f.request(http.MethodGet, path+"/deletion-impact", nil, owner)
			impact := decodeEnvelope(t, preview)["data"].(map[string]any)
			if impact["canDelete"] != false || impact["runCount"] != float64(1) {
				t.Fatalf("impact=%#v", impact)
			}
			if response := f.request(http.MethodDelete, path, nil, owner); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "environment_revision.run_history") {
				t.Fatalf("delete: %s", response.Body.String())
			}
			if _, err := f.database.GetEnvironmentRevision(ctx, ids[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnvironmentRevisionDeletionRetainsBuildsAndArchivedEnvironment(t *testing.T) {
	for _, status := range []string{"queued", "running", "succeeded", "failed", "cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			f, owner, id, ids := revisionDeletionFixture(t, 2)
			if err := f.database.CreateComponentImageBuild(context.Background(), domain.ComponentImageBuild{ID: "build", ReleaseID: "release-test-runtime-1.1.0", EnvironmentID: id, EnvironmentRevisionID: ids[0], RequestedBy: seed.ComponentOwnerRuntimeID, Status: domain.ImageBuildStatus(status), DockerfileSHA256: "hash", ImageTag: "test", ImageRef: "test:latest", CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/environments/" + id + "/revisions/" + ids[0]
			preview := f.request(http.MethodGet, path+"/deletion-impact", nil, owner)
			impact := decodeEnvelope(t, preview)["data"].(map[string]any)
			if impact["canDelete"] != false || impact["imageBuildCount"] != float64(1) {
				t.Fatalf("impact=%#v", impact)
			}
			if response := f.request(http.MethodDelete, path, nil, owner); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "environment_revision.build_history") {
				t.Fatalf("delete: %s", response.Body.String())
			}
		})
	}
	t.Run("archived environment", func(t *testing.T) {
		f, owner, id, ids := revisionDeletionFixture(t, 2)
		if response := f.request(http.MethodPost, "/api/v1/environments/"+id+"/archive", nil, owner); response.Code != http.StatusOK {
			t.Fatalf("archive: %s", response.Body.String())
		}
		if response := f.request(http.MethodDelete, "/api/v1/environments/"+id+"/revisions/"+ids[0], nil, owner); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "已归档") {
			t.Fatalf("delete: %s", response.Body.String())
		}
	})
}
