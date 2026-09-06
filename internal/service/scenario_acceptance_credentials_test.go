package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func TestScenarioAcceptanceCredentialIsolationRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("set CLUSTERFORGE_JOB_TEST_ANSIBLE for isolated local execution")
	}
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	const secret = "scenario-private-token-7bdf293c"
	const reference = "CLUSTERFORGE_SCENARIO_ACCEPTANCE_TEST_TOKEN"
	t.Setenv(reference, secret)
	job := revision.AcceptanceJobs[0]
	job.RequiredCredentials = []string{"business_token"}
	if _, err := p.scenarios.SaveAcceptance(ctx, owner, revision.ID, ScenarioAcceptanceInput{ExpectedRevisionDigest: domain.ScenarioRevisionSpecDigest(revision), Jobs: []domain.ScenarioAcceptanceJob{job}}); err != nil {
		t.Fatal(err)
	}
	contents := []byte("- name: Fail with credential to exercise controller redaction\n  fail:\n    msg: '{{ business_token }}'\n")
	if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, "tasks/acceptance/business.yml", contents, acceptanceExpectation(t, p, owner, revision.ID, "tasks/acceptance/business.yml")); err != nil {
		t.Fatal(err)
	}
	credentialEnv := env
	credentialEnv.ID = "credential-acceptance-env"
	credentialEnv.Name = credentialEnv.ID
	credentialEnv.CurrentRevisionID = ""
	envRevision := *env.Revision
	envRevision.ID = credentialEnv.ID + "-r1"
	envRevision.EnvironmentID = credentialEnv.ID
	envRevision.CredentialRefs = []domain.CredentialRef{{Name: "business_token", Kind: "envVarRef", Reference: reference}}
	if err := db.CreateEnvironment(ctx, credentialEnv, envRevision); err != nil {
		t.Fatal(err)
	}
	setTestRunner(t, p, &ansible.Runner{AllowedRoot: p.catalog.workspace.root, Binary: binary})
	input := ScenarioExecutionRequest{EnvironmentID: credentialEnv.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "private-acceptance"}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
	if err != nil || !preview.Ready {
		t.Fatalf("credential preview=%+v %v", preview, err)
	}
	previewJSON, _ := json.Marshal(preview)
	if strings.Contains(string(previewJSON), secret) || strings.Contains(string(previewJSON), reference) {
		t.Fatal("preview exposed credential value/reference")
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	p.scheduler.mu.Lock()
	p.scheduler.workers[credentialEnv.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
	p.scheduler.mu.Unlock()
	run, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run.Status = domain.RunRunning
	p.executor.executeRun(run)
	finished, err := db.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.RunFailed {
		t.Fatalf("credential failure status=%s error=%s", finished.Status, finished.Error)
	}
	var acceptanceFailed bool
	for _, step := range finished.Steps {
		if step.NodeID == "acceptance:business" {
			acceptanceFailed = step.Status == domain.RunFailed
		}
	}
	if !acceptanceFailed {
		t.Fatalf("test never reached credential acceptance: error=%s steps=%+v", finished.Error, finished.Steps)
	}
	logs, err := db.ListRunLogs(ctx, run.ID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(struct {
		Run  domain.Run
		Logs []domain.RunLog
	}{finished, logs})
	if strings.Contains(string(data), secret) || strings.Contains(string(data), reference) {
		t.Fatal("credential value or environment-variable reference leaked into persisted Run/logs")
	}
	_, archive, _, err := db.GetRunJob(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(archive), secret) || strings.Contains(string(archive), reference) {
		t.Fatal("job archive contains credential value/reference")
	}
	if _, err := db.ScenarioTestEvidence(ctx, revision.ID); err == nil {
		t.Fatal("failed credential acceptance became publish evidence")
	}
}
