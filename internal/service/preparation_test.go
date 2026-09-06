package service

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestPreparationRejectsBlockedScenarioPreview(t *testing.T) {
	p, db, owner, revision, environment, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	_, err := p.scenarios.SaveAcceptance(ctx, owner, revision.ID, ScenarioAcceptanceInput{ExpectedRevisionDigest: domain.ScenarioRevisionSpecDigest(revision), Jobs: []domain.ScenarioAcceptanceJob{}})
	if err != nil {
		t.Fatal(err)
	}
	p.ConfigureEnvironmentHealthDialer(func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	})
	input := PreparationInput{Kind: "scenario_execution", SubjectID: revision.ID, EnvironmentID: environment.ID, Scenario: ScenarioExecutionRequest{EnvironmentID: environment.ID, TestOnly: true}}
	raw, _ := json.Marshal(input)
	now := time.Now().UTC()
	session, err := db.CreateWorkflowSession(ctx, domain.WorkflowSession{ID: "blocked-preparation", Kind: "preparation", OwnerID: owner.ID, RequestKey: "blocked", RequestDigest: "blocked", Status: "queued", Input: raw, Output: []byte(`{"checks":[]}`), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	work, cancel := context.WithCancel(ctx)
	p.preparations.run(work, cancel, owner, session, input)
	saved, err := db.GetWorkflowSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var output PreparationResult
	if err := json.Unmarshal(saved.Output, &output); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || !strings.Contains(output.Error, "业务验收") || output.Plan != nil {
		t.Fatalf("blocked scenario reported as prepared: status=%s output=%s", saved.Status, saved.Output)
	}
}
