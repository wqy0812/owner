package api

import (
	"codex/platform-demo/internal/testutil"
	"codex/platform-demo/internal/testutil/runfixture"
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestInvalidRunSnapshotKeepsDetailDiagnosticsAndLogDownloadReadable(t *testing.T) {
	for _, corruption := range []string{
		`execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.steps[0].timeoutSeconds','wrong')`,
		`delivery_results_json='[{"requirementId":"image","mode":"direct","status":42}]'`,
	} {
		t.Run(corruption, func(t *testing.T) {
			f := newAPIFixture(t)
			ctx := context.Background()
			owner := f.session(seed.EnvironmentOwnerID)
			env, err := f.database.GetEnvironment(ctx, "environment-test", false)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			run := domain.Run{ID: "invalid-diagnostic-api", Kind: domain.RunComponentTest, Status: domain.RunFailed, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, RequestedBy: seed.EnvironmentOwnerID, CreatedAt: now, FinishedAt: &now, Error: "invalid active run: execution input is damaged", Snapshot: runfixture.Snapshot(map[string]any{"steps": []any{map[string]any{"nodeId": "node", "variables": map[string]any{"private_value": "DO-NOT-EXPORT"}}}})}
			if err = testutil.InsertRunRecord(ctx, f.database.DB(), run); err != nil {
				t.Fatal(err)
			}
			if err = f.database.CreateRunStep(ctx, domain.RunStep{ID: "actual", RunID: run.ID, NodeID: "node", Name: "Persisted failure", Status: domain.RunFailed}); err != nil {
				t.Fatal(err)
			}
			if _, err = runfixture.CorruptSnapshot(ctx, f.database.DB(), `UPDATE runs SET `+corruption+` WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.database.GetRun(ctx, run.ID); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("execution read accepted damage: %v", err)
			}
			for _, suffix := range []string{"", "/diagnostics", "/log-bundle", "/activity"} {
				response := f.request(http.MethodGet, "/api/v1/runs/"+run.ID+suffix, nil, owner)
				if response.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", suffix, response.Code, response.Body.String())
				}
				if suffix == "" && (!strings.Contains(response.Body.String(), "snapshotError") || !strings.Contains(response.Body.String(), "Persisted failure") || !strings.Contains(response.Body.String(), "invalid active run") || strings.Contains(response.Body.String(), "DO-NOT-EXPORT")) {
					t.Fatal(response.Body.String())
				}
				outsider := f.request(http.MethodGet, "/api/v1/runs/"+run.ID+suffix, nil, f.session(seed.ComponentOwnerK8sID))
				if outsider.Code != http.StatusForbidden {
					t.Fatalf("diagnostic permission bypass %s: %d", suffix, outsider.Code)
				}
			}
			response := f.request(http.MethodPost, "/api/v1/runs/"+run.ID+"/retry-plan", nil, owner)
			if response.Code != http.StatusBadRequest && response.Code != http.StatusConflict {
				t.Fatalf("invalid Run retry: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRunLogEndpointsAndRemovedReferenceRebuild(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.session(seed.EnvironmentOwnerID)
	now := time.Now().UTC()
	env, err := f.database.GetEnvironment(ctx, "environment-test", false)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "run-api-logs", Kind: domain.RunScenario, Status: domain.RunFailed, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, RequestedBy: seed.EnvironmentOwnerID, CreatedAt: now, FinishedAt: &now, Error: "executor timed out", Snapshot: runfixture.Snapshot(map[string]any{})}
	if err = testutil.InsertRunRecord(ctx, f.database.DB(), run, nil); err != nil {
		t.Fatal(err)
	}
	r := f.request(http.MethodGet, "/api/v1/runs/"+run.ID+"/diagnostics", nil, owner)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "executor timed out") {
		t.Fatal(r.Code, r.Body.String())
	}
	r = f.request(http.MethodGet, "/api/v1/runs/"+run.ID+"/log-bundle", nil, owner)
	_, params, err := mime.ParseMediaType(r.Header().Get("Content-Disposition"))
	if r.Code != 200 || r.Header().Get("Content-Type") != "application/gzip" || err != nil || params["filename"] != run.ID+"-logs.tar.gz" || len(r.Body.Bytes()) < 2 || r.Body.Bytes()[0] != 0x1f {
		t.Fatal(r.Code, r.Header(), err)
	}
	outsider := f.session(seed.ComponentOwnerK8sID)
	for _, suffix := range []string{"diagnostics", "log-bundle", "activity"} {
		r = f.request(http.MethodGet, "/api/v1/runs/"+run.ID+"/"+suffix, nil, outsider)
		if r.Code != 403 {
			t.Fatal(suffix, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/reference-rebuilds", "/api/v1/reference-rebuilds/analyze", "/api/v1/reference-rebuilds/old/confirm"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
			r = f.request(method, path, map[string]any{}, owner)
			if r.Code != 404 {
				t.Fatal(method, path, r.Code)
			}
		}
	}
}
