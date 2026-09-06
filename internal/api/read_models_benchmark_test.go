package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

// Reproducible same-scale fixture: 18 components, 64 Releases, 305 Runs.
// No executors are started and the listener is opt-in and loopback-only.
func readModelScaleFixture(t *testing.T) *Handler {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := (seed.Seeder{Store: db}).SeedUsers(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	env := domain.Environment{ID: "perf-environment", Name: "Six node evidence lab", OwnerID: seed.EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	rev := domain.EnvironmentRevision{ID: "perf-environment-r1", EnvironmentID: env.ID, Revision: 1, Facts: completeTestEnvironmentFacts(), Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: now}
	if err := db.CreateEnvironment(ctx, env, rev); err != nil {
		t.Fatal(err)
	}
	releases := []domain.ComponentRelease{}
	for i := 0; i < 18; i++ {
		c := domain.Component{ID: fmt.Sprintf("perf-component-%02d", i), Slug: fmt.Sprintf("perf-component-%02d", i), Name: fmt.Sprintf("Component %02d", i), Description: strings.Repeat("组件合同与依赖验证。", 8), Layer: domain.LayerRuntimeState, Tags: []string{"runtime", "performance"}, OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now}
		if err := db.CreateComponent(ctx, c); err != nil {
			t.Fatal(err)
		}
		count := 3
		if i < 10 {
			count = 4
		}
		for j := 0; j < count; j++ {
			r := domain.ComponentRelease{ID: fmt.Sprintf("%s-r%d", c.ID, j), ComponentID: c.ID, Version: fmt.Sprintf("1.%d.0", j), LineID: "line-" + c.ID, LineName: "稳定发布线", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now.Add(time.Duration(j) * time.Minute), ReleasedAt: &now, EnvironmentConstraints: map[string]any{"architecture": []string{"amd64"}}}
			if j == count-1 {
				r.Status = domain.ReleaseDraft
				r.ReleasedAt = nil
			}
			for k := 0; k < 12; k++ {
				r.Parameters = append(r.Parameters, domain.ParameterDefinition{Name: fmt.Sprintf("parameter_%02d", k), Type: "string", Description: "合同参数说明", Required: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: "fixture-value"})
			}
			if i == 1 {
				r.Parameters[0].ValueProvider = domain.ParameterProviderUpstreamMapping
				r.Parameters[0].FixedValue = nil
				r.Dependencies = []domain.ComponentDependency{{ID: r.ID + "-dependency", ReleaseID: r.ID, UpstreamComponentID: "perf-component-00", UpstreamReleaseID: "perf-component-00-r0", Purpose: "参数来源", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "parameter_00", TargetParameter: "parameter_00"}}}}
			}
			for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionVerify, domain.ActionRollback} {
				r.Actions = append(r.Actions, domain.ActionDefinition{ID: r.ID + "-" + string(kind), Name: string(kind), Kind: kind, Playbook: "fixtures/" + string(kind) + ".yml", HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow})
			}
			if err := db.CreateComponentRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
			r, _ = db.GetComponentRelease(ctx, r.ID)
			releases = append(releases, r)
		}
	}
	scenario := domain.Scenario{ID: "perf-scenario", Slug: "perf-scenario", Name: "Read model composition", OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	scenarioRevision := domain.ScenarioRevision{ID: "perf-scenario-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, CreatedAt: now, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "source", Name: "Source", ReleaseID: "perf-component-00-r0", Action: domain.ActionInstall, HostGroup: "all", ParameterValues: map[string]any{}, Position: domain.GraphPosition{X: 50, Y: 50}},
		{ID: "consumer", Name: "Consumer", ReleaseID: "perf-component-01-r0", Action: domain.ActionInstall, HostGroup: "all", ParameterValues: map[string]any{}, Position: domain.GraphPosition{X: 350, Y: 50}},
	}, Edges: []domain.ScenarioEdge{{ID: "dependency", Source: "source", Target: "consumer", Kind: domain.ScenarioEdgeDependency, DependencyID: "perf-component-01-r0-dependency"}}}}
	if err := db.CreateScenario(ctx, scenario, scenarioRevision); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 305; i++ {
		r := releases[i%len(releases)]
		at := now.Add(time.Duration(i) * time.Minute)
		status := domain.RunSucceeded
		if i >= 245 {
			status = domain.RunAwaitingApproval
		} else if i%9 == 0 {
			status = domain.RunFailed
		}
		run := domain.Run{ID: fmt.Sprintf("perf-run-%03d", i), Kind: domain.RunComponentTest, Status: status, RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: env.ID, EnvironmentRevisionID: rev.ID, ComponentReleaseID: r.ID, Action: domain.ActionInstall, CreatedAt: at, FinishedAt: &at, InputSnapshot: map[string]any{"componentReleaseSpecDigest": domain.ComponentReleaseSpecDigest(r), "componentTestEvidence": "install_verify", "payload": strings.Repeat("x", 16*1024)}}
		if err := db.CreateRun(ctx, run, nil); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"install", "verify"} {
			if err := db.CreateRunStep(ctx, domain.RunStep{ID: run.ID + "-" + kind, RunID: run.ID, Name: kind, Status: domain.RunSucceeded}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.DB().Exec(`INSERT INTO run_logs(run_id,message,created_at) VALUES(?,?,?)`, run.ID, strings.Repeat("fixture log ", 1024), at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if status == domain.RunAwaitingApproval {
			if _, err := db.DB().Exec(`INSERT INTO approvals(id,run_id,requested_at) VALUES(?,?,?)`, "approval-"+run.ID, run.ID, at.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := newAPITestPlatform(t, db, &fakeRunner{}, service.NewEventHub())
	t.Cleanup(p.Close)
	root, err := filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	static := http.FileServer(http.Dir(root))
	spa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(root, filepath.Clean(r.URL.Path))); err != nil {
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		static.ServeHTTP(w, r)
	})
	return NewHandler(p, spa)
}

func TestReadModelPayloadBudget(t *testing.T) {
	h := readModelScaleFixture(t)
	response := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/session/switch", strings.NewReader(fmt.Sprintf(`{"userId":%q}`, seed.ComponentOwnerRuntimeID)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	read := func(path string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		return w.Body.Len()
	}
	directory, detail, runs := read("/api/v1/components"), read("/api/v1/components/perf-component-00"), read("/api/v1/runs?page=1&pageSize=50&filter=all")
	t.Logf("18 components / 64 releases / 305 runs: directory=%d detail=%d runs50=%d bytes", directory, detail, runs)
	if directory+detail >= 150000 || runs >= 100000 {
		t.Fatal("read model payload budget exceeded")
	}
}

func TestServeReadModelBrowserFixture(t *testing.T) {
	if os.Getenv("CLUSTERFORGE_SERVE_READ_FIXTURE") != "1" {
		t.Skip("opt-in local browser fixture")
	}
	server := &http.Server{Addr: "127.0.0.1:18089", Handler: readModelScaleFixture(t), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = server.Close() })
	t.Log("serving isolated fixture at http://127.0.0.1:18089")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
