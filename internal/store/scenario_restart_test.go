package store

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestScenarioRestartQueueAcceptsOnlyValidTypedAcceptanceSteps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		bad    bool
	}{
		{name: "valid"},
		{name: "other revision", bad: true, change: func(step map[string]any) { step["scenarioRevisionId"] = "other" }},
		{name: "unknown job", bad: true, change: func(step map[string]any) { step["acceptanceJobId"] = "missing" }},
		{name: "missing job", bad: true, change: func(step map[string]any) { delete(step, "acceptanceJobId") }},
		{name: "spoofed component", bad: true, change: func(step map[string]any) { step["componentId"] = "other" }},
		{name: "wrong node identity", bad: true, change: func(step map[string]any) { step["nodeId"] = "component-node" }},
		{name: "wrong action", bad: true, change: func(step map[string]any) { step["action"] = "install" }},
		{name: "missing phase", bad: true, change: func(step map[string]any) { delete(step, "phase") }},
		{name: "wrong source", bad: true, change: func(step map[string]any) { step["playbook"] = "unregistered.yml" }},
		{name: "wrong digest", bad: true, change: func(step map[string]any) { step["playbookDigest"] = "stale" }},
		{name: "unknown type", bad: true, change: func(step map[string]any) { step["sourceType"] = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, _, revision := scenarioLifecycleFixture(t)
			step := map[string]any{"id": "acceptance-business", "nodeId": "acceptance:business", "sourceType": "scenario_acceptance", "scenarioRevisionId": revision.ID, "acceptanceJobId": "business", "stage": "acceptance", "phase": "acceptance", "action": "acceptance", "actionId": "business", "playbook": "acceptance.yml", "playbookDigest": "source-sha"}
			if tc.change != nil {
				tc.change(step)
			}
			insertScenarioSourceRun(t, db, revision, "queued", "scenario_test", "queued", "install")
			snapshot := map[string]any{"scenarioContractVersion": 2, "scenarioRevisionSpecDigest": domain.ScenarioRevisionSpecDigest(revision), "steps": []any{step}}
			if _, err := db.db.Exec(`UPDATE runs SET input_snapshot_json=? WHERE id='queued'`, jsonText(snapshot)); err != nil {
				t.Fatal(err)
			}
			count, err := db.FailInvalidActiveRuns(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if (count == 1) != tc.bad {
				t.Fatalf("invalid count=%d wantBad=%v", count, tc.bad)
			}
			run, err := db.GetRun(ctx, "queued")
			if err != nil {
				t.Fatal(err)
			}
			want := domain.RunQueued
			if tc.bad {
				want = domain.RunFailed
			}
			if run.Status != want {
				t.Fatalf("queue status=%s want=%s", run.Status, want)
			}
		})
	}
}

func TestScenarioRestartPreservesPartialAndInvalidatesUnmutatedBaseline(t *testing.T) {
	for _, state := range []string{"complete", "test", "partial"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			db, scenario, revision := scenarioLifecycleFixture(t)
			insertScenarioSourceRun(t, db, revision, "baseline", "scenario_run", "succeeded", "install")
			insertScenarioSourceRun(t, db, revision, "running", "scenario_test", "running", "upgrade")
			if _, err := db.db.Exec(`UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.scenarioContractVersion',2) WHERE id='running'`); err != nil {
				t.Fatal(err)
			}
			mutating := ""
			if state == "partial" {
				mutating = "running"
			}
			installation := domain.ScenarioInstallation{EnvironmentID: "env", ScenarioID: scenario.ID, RevisionID: revision.ID, RunID: "baseline", MutatingRunID: mutating, State: state, TestOnly: state == "test", InstallationDigest: "exact-baseline", UpdatedAt: time.Now().UTC()}
			if err := db.SaveScenarioInstallation(ctx, installation, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.Exec(`UPDATE scenario_revisions SET status='testing' WHERE id=?`, revision.ID); err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			if err := db.CreateRunStep(ctx, domain.RunStep{ID: "inflight", RunID: "running", NodeID: "runtime", Name: "runtime", Status: domain.RunRunning, StartedAt: &started}); err != nil {
				t.Fatal(err)
			}
			count, err := db.MarkRunningInterrupted(ctx, started.Add(time.Second))
			if err != nil || count != 1 {
				t.Fatalf("interrupt count=%d err=%v", count, err)
			}
			got, err := db.GetScenarioInstallation(ctx, "env", scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantState, wantGeneration := "unverified", int64(2)
			if state == "partial" {
				wantState, wantGeneration = "partial", 1
			}
			if got.State != wantState || got.Generation != wantGeneration || got.RevisionID != revision.ID || got.RunID != "baseline" || got.MutatingRunID != mutating || got.TestOnly != installation.TestOnly || got.InstallationDigest != "exact-baseline" {
				t.Fatalf("restart lost recovery identity: %+v", got)
			}
			run, err := db.GetRun(ctx, "running")
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != domain.RunInterrupted || len(run.Steps) != 1 || run.Steps[0].Status != domain.RunInterrupted {
				t.Fatalf("restart did not retain interrupted steps: %+v", run)
			}
			if _, err := db.ScenarioTestEvidence(ctx, revision.ID); err == nil {
				t.Fatal("interrupted run produced test evidence")
			}
		})
	}
}
