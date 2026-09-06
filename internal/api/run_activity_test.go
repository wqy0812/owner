package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestRunActivityContractVisibilityAndRedaction(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.session(seed.EnvironmentOwnerID)
	env, err := f.database.GetEnvironment(ctx, "environment-test", false)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "activity-contract", Kind: domain.RunScenario, Status: domain.RunRunning, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, RequestedBy: seed.EnvironmentOwnerID, CreatedAt: time.Now().UTC(), InputSnapshot: map[string]any{}}
	if err = f.database.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{`{"kind":"waiting","stepId":"s","host":"h","task":"ready","result":{"waiting":{"observed":"Pending"}}}`, `{"kind":"result","stepId":"x","result":{"password":"DO-NOT-EXPOSE"}}`} {
		if _, err = f.database.AppendRunLog(ctx, domain.RunLog{RunID: run.ID, Stream: "event", Message: message, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	response := f.request(http.MethodGet, "/api/v1/runs/"+run.ID+"/activity", nil, owner)
	if response.Code != 200 || strings.Contains(response.Body.String(), "DO-NOT-EXPOSE") {
		t.Fatal(response.Code, response.Body.String())
	}
	data := decodeEnvelope(t, response)["data"].(map[string]any)
	if data["runId"] != run.ID || data["status"] != "running" || len(data["logs"].([]any)) != 2 || len(data["waitingObservations"].([]any)) != 1 || data["hasMore"] != false || data["archived"] != false {
		t.Fatal(data)
	}
	for _, query := range []string{"?afterId=-1", "?afterId=x", "?afterId=1.5", "?limit=0", "?limit=2001"} {
		response = f.request(http.MethodGet, "/api/v1/runs/"+run.ID+"/activity"+query, nil, owner)
		if response.Code != 400 {
			t.Fatal(query, response.Code, response.Body.String())
		}
	}
	response = f.request(http.MethodGet, "/api/v1/runs/"+run.ID+"/activity?afterId=0&limit=1", nil, owner)
	data = decodeEnvelope(t, response)["data"].(map[string]any)
	if data["hasMore"] != true || len(data["logs"].([]any)) != 1 {
		t.Fatal(data)
	}
	response = f.request(http.MethodGet, "/api/v1/runs/"+run.ID, nil, owner)
	if response.Code != 200 || strings.Contains(response.Body.String(), "logTail") || strings.Contains(response.Body.String(), "waitingObservations") {
		t.Fatal(response.Code, response.Body.String())
	}
}
