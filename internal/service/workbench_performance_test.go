package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil/perf"
)

func workbenchPerformanceFixture(t testing.TB, runCount int) (*Platform, *store.Store, []domain.User) {
	p, db := readinessTestPlatform(t)
	t.Cleanup(p.Close)
	p.ConfigurePlaybookRoot(t.TempDir())
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	users := []domain.User{{ID: "component-owner", Name: "Owner", Role: domain.RoleComponentOwner}, {ID: "scenario-owner", Name: "Scenario", Role: domain.RoleScenarioOwner}, {ID: "environment-owner", Name: "Environment", Role: domain.RoleEnvironmentOwner}, {ID: "platform-admin", Name: "Admin", Role: domain.RolePlatformAdmin}}
	for _, u := range users {
		if err := db.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	env, err := p.environments.Create(ctx, users[2], domain.Environment{Name: "Workbench benchmark"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	releases := []domain.ComponentRelease{}
	for i := 0; i < 18; i++ {
		id := fmt.Sprintf("component-%d", i+1)
		if i != 0 {
			if err = db.CreateComponent(ctx, domain.Component{ID: id, Slug: id, Name: id, OwnerID: users[0].ID, Layer: domain.LayerRuntimeState, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
		versions := 3
		if i < 10 {
			versions = 4
		}
		for v := 0; v < versions; v++ {
			r := domain.ComponentRelease{ID: fmt.Sprintf("%s-r%d", id, v), ComponentID: id, Version: fmt.Sprintf("%d.0.0", v), Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now, Parameters: []domain.ParameterDefinition{{Name: "description", Type: domain.ParameterTypeString, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: strings.Repeat("metadata-", 128)}}}
			if v == versions-1 {
				r.Status = domain.ReleaseDraft
				r.ReleasedAt = nil
			}
			if err = db.CreateComponentRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
			r, err = db.GetComponentRelease(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			releases = append(releases, r)
		}
	}
	sc, err := p.scenarios.Create(ctx, users[1], domain.Scenario{Name: "Workbench scenario", Slug: "workbench-scenario"})
	if err != nil {
		t.Fatal(err)
	}
	_ = sc
	tx, err := db.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,action_kind,input_snapshot_json,created_at,finished_at) VALUES(?,'component_test',?,?,?,?,?,'install',?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runCount; i++ {
		r := releases[i%len(releases)]
		status := "succeeded"
		if i%17 == 0 {
			status = "failed"
		}
		payload, _ := json.Marshal(map[string]any{"componentReleaseSpecDigest": componentReleaseSpecDigest(r), "componentTestEvidence": "install_verify", "steps": []any{map[string]any{"nodeId": "node", "releaseId": r.ID, "componentId": r.ComponentID, "releaseSpecDigest": componentReleaseSpecDigest(r), "variables": map[string]any{"payload": strings.Repeat("locked execution data ", 512)}}}})
		at := now.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		if _, err = stmt.ExecContext(ctx, fmt.Sprintf("history-%05d", i), status, users[0].ID, env.ID, env.CurrentRevisionID, r.ID, string(payload), at, at); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveEnvironmentHealthCheck(ctx, domain.EnvironmentHealthCheck{ID: "health", EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, Status: "degraded", CheckedAt: now, Results: []domain.EnvironmentEndpointCheck{{Reachable: false}, {Reachable: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveEnvironmentSSHCheck(ctx, domain.EnvironmentSSHCheck{ID: "ssh", EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, Status: "degraded", CheckedAt: now, Results: []domain.EnvironmentSSHHostCheck{{Status: "failed"}, {Status: "passed"}}}); err != nil {
		t.Fatal(err)
	}
	if runCount == 3050 {
		// Scale unrelated published versions and environment history as well.
		for i := 0; i < 576; i++ {
			r := domain.ComponentRelease{ID: fmt.Sprintf("unrelated-release-%d", i), ComponentID: fmt.Sprintf("component-%d", i%18+1), Version: fmt.Sprintf("historic-%d", i), Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now, Parameters: releases[0].Parameters}
			if err = db.CreateComponentRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		for i := 2; i <= 10; i++ {
			if _, err = db.DB().ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,variables_json,created_at,created_by) SELECT ?,environment_id,?,facts_json,inventory_json,variables_json,created_at,created_by FROM environment_revisions WHERE id=?`, fmt.Sprintf("historical-env-%d", i), i, env.CurrentRevisionID); err != nil {
				t.Fatal(err)
			}
		}
	}
	return p, db, users
}

func TestWorkbenchPerformanceSamples(t *testing.T) {
	path := os.Getenv("CLUSTERFORGE_WORKBENCH_PERFORMANCE_REPORT")
	if path == "" {
		t.Skip("opt-in isolated performance measurement")
	}
	var samples []perf.PerformanceSample
	for _, count := range []int{305, 3050} {
		p, _, users := workbenchPerformanceFixture(t, count)
		for _, user := range users {
			for _, legacy := range []bool{true, false} {
				name := "dedicated"
				if legacy {
					name = "legacy"
				}
				samples = append(samples, perf.MeasureRequests(t, fmt.Sprintf("%d/%s/%s", count, user.Role, name), 31, func() error {
					if legacy {
						_, err := p.readModel.legacyWorkbench(context.Background(), user)
						return err
					}
					_, err := p.readModel.Workbench(context.Background(), user)
					return err
				}))
			}
		}
	}
	body, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkbenchScenarioEvidenceSearchBeyondBatch(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	valid := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	for i := 0; i < 130; i++ {
		stale := valid
		stale.ID = fmt.Sprintf("stale-success-%03d", i)
		stale.CreatedAt = valid.CreatedAt.Add(time.Duration(i+1) * time.Second)
		raw, _ := json.Marshal(valid.InputSnapshot)
		stale.InputSnapshot = nil
		if err := json.Unmarshal(raw, &stale.InputSnapshot); err != nil {
			t.Fatal(err)
		}
		for _, raw := range stale.InputSnapshot["steps"].([]any) {
			step := raw.(map[string]any)
			if step["sourceType"] != "scenario_acceptance" {
				step["releaseSpecDigest"] = "stale-locked-release"
			}
		}
		raw, _ = json.Marshal(stale.InputSnapshot)
		if _, err := db.DB().ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,created_at,finished_at) VALUES(?,'scenario_test','succeeded',?,?,?,?,?,?,?)`, stale.ID, owner.ID, env.ID, env.CurrentRevisionID, revision.ID, string(raw), stale.CreatedAt.Format(time.RFC3339Nano), stale.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	want, err := p.readModel.legacyWorkbench(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.readModel.Workbench(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	want.GeneratedAt = got.GeneratedAt
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("evidence result changed: old=%+v new=%+v", want, got)
	}
	found := false
	for _, item := range got.Items {
		for _, reason := range item.Reasons {
			if reason.Code == "scenario.ready_to_publish" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("valid evidence behind 130 invalid successes not found: %+v", got)
	}
	// Cleanup retains Release identities, not the acceptance job's empty Release
	// ID. The newer tombstone must still suppress the older current failure.
	raw, _ := json.Marshal(valid.InputSnapshot)
	for i, id := range []string{"scene-older-failure", "scene-newest-failure"} {
		at := valid.CreatedAt.Add(time.Duration(200+i) * time.Second).Format(time.RFC3339Nano)
		if _, err = db.DB().ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,created_at,finished_at) VALUES(?,'scenario_test','failed',?,?,?,?,?,?,?)`, id, owner.ID, env.ID, env.CurrentRevisionID, revision.ID, string(raw), at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.CleanupRuns(ctx, []string{"scene-newest-failure"}, "system", "manual", valid.CreatedAt.Add(200*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err = p.readModel.Workbench(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "scene-older-failure") || strings.Contains(string(encoded), "scene-newest-failure") {
		t.Fatalf("cleaned scene failure resurfaced: %s", encoded)
	}
}

func TestWorkbenchProjectionMatchesFullAggregation(t *testing.T) {
	p, _, users := workbenchPerformanceFixture(t, 305)
	for _, u := range users {
		t.Run(string(u.Role), func(t *testing.T) {
			old, err := p.readModel.legacyWorkbench(context.Background(), u)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.readModel.Workbench(context.Background(), u)
			if err != nil {
				t.Fatal(err)
			}
			old.GeneratedAt = got.GeneratedAt
			if !reflect.DeepEqual(old, got) {
				a, _ := json.MarshalIndent(old, "", "  ")
				b, _ := json.MarshalIndent(got, "", "  ")
				t.Fatalf("workbench changed:\nold=%s\nnew=%s", a, b)
			}
		})
	}
}

func TestWorkbenchDoesNotHydrateUnrelatedHistory(t *testing.T) {
	p, db, users := workbenchPerformanceFixture(t, 3050)
	subjects, err := db.WorkbenchSubjects(context.Background(), users[0])
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, c := range subjects.Components {
		for _, r := range c.Releases {
			count++
			if r.Status != domain.ReleaseDraft {
				t.Fatal("published history hydrated")
			}
		}
	}
	if count != 18 {
		t.Fatalf("draft contracts=%d", count)
	}
	runs, err := p.readModel.workbenchRuns(context.Background(), users[0], subjects.Components, subjects.Scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) > 82 {
		t.Fatalf("unbounded historical snapshots: %d", len(runs))
	}
}

func BenchmarkWorkbenchReadModels(b *testing.B) {
	for _, count := range []int{305, 3050} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			p, _, users := workbenchPerformanceFixture(b, count)
			for _, u := range users {
				b.Run(string(u.Role), func(b *testing.B) {
					for _, legacy := range []bool{true, false} {
						name := "dedicated"
						if legacy {
							name = "legacy"
						}
						b.Run(name, func(b *testing.B) {
							b.ReportAllocs()
							b.ResetTimer()
							for i := 0; i < b.N; i++ {
								var err error
								if legacy {
									_, err = p.readModel.legacyWorkbench(context.Background(), u)
								} else {
									_, err = p.readModel.Workbench(context.Background(), u)
								}
								if err != nil {
									b.Fatal(err)
								}
							}
						})
					}
				})
			}
		})
	}
}
