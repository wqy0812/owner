package api

import (
	"context"
	"mime"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestRunLogEndpointsAndRemovedReferenceRebuild(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.session(seed.EnvironmentOwnerID)
	now := time.Now().UTC()
	env, err := f.database.GetEnvironment(ctx, "environment-test", false)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "run-api-logs", Kind: domain.RunScenario, Status: domain.RunFailed, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, RequestedBy: seed.EnvironmentOwnerID, CreatedAt: now, FinishedAt: &now, Error: "executor timed out", InputSnapshot: map[string]any{}}
	if err = f.database.CreateRun(ctx, run, nil); err != nil {
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
