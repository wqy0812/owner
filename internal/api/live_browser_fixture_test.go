package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/store"
)

// Opt-in browser data belongs only to the temporary API started by the E2E
// script. The production seeder continues to create an empty business catalog.
func TestLiveAPIComponentFixture(t *testing.T) {
	destination := os.Getenv("CLUSTERFORGE_LIVE_API_FIXTURE")
	if destination == "" {
		t.Skip("set CLUSTERFORGE_LIVE_API_FIXTURE for isolated browser data")
	}
	if !filepath.IsAbs(destination) {
		t.Fatal("fixture destination must be absolute")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(destination, "playbooks"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(destination, "platform.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := (seed.Seeder{Store: db}).SeedUsers(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"browser-upstream", "browser-consumer"} {
		if err := db.CreateComponent(ctx, domain.Component{ID: id, Slug: id, Name: id, OwnerID: seed.ComponentOwnerRuntimeID, Layer: domain.LayerRuntimeState, Tags: []string{}, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	upstream := domain.ComponentRelease{ID: "browser-upstream-r1", ComponentID: "browser-upstream", Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
		Parameters: []domain.ParameterDefinition{{Name: "runtime_version", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, FixedValue: "1.0.0"}},
	}
	if err := db.CreateComponentRelease(ctx, upstream); err != nil {
		t.Fatal(err)
	}
	consumer := domain.ComponentRelease{ID: "browser-consumer-r1", ComponentID: "browser-consumer", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now,
		Parameters:   []domain.ParameterDefinition{{Name: "required_runtime", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner}},
		Dependencies: []domain.ComponentDependency{{Kind: "execution", ID: "browser-runtime-dependency", ReleaseID: "browser-consumer-r1", UpstreamComponentID: upstream.ComponentID, UpstreamReleaseID: upstream.ID, Purpose: "Browser parameter lineage", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "runtime_version", TargetParameter: "required_runtime"}}}},
	}
	if err := db.CreateComponentRelease(ctx, consumer); err != nil {
		t.Fatal(err)
	}
	// Populated desktop acceptance data never runs Ansible or touches real hosts.
	for index, group := range []string{"control_plane", "worker"} {
		if err := db.EnsurePlatformOption(ctx, "hostGroup", domain.PlatformOption{ID: fmt.Sprintf("browser-group-%d", index), Value: group, Label: group, CreatedBy: seed.PlatformAdminID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	env := domain.Environment{ID: "browser-environment", Name: "桌面验收环境 · 多主机与长说明", Description: strings.Repeat("用于验证环境配置、版本记录及宽表格排版。", 8), OwnerID: seed.EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	revision := domain.EnvironmentRevision{ID: "browser-environment-r1", EnvironmentID: env.ID, Revision: 1, Facts: completeTestEnvironmentFacts(), Inventory: json.RawMessage(`{"hosts":[{"name":"control-plane-long-hostname-01.example.internal","address":"192.0.2.10","groups":["control_plane"],"user":"deploy","port":22},{"name":"worker-01","address":"192.0.2.11","groups":["worker"],"user":"deploy","port":2222}]}`), Parameters: map[string]any{}, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{}, CreatedBy: seed.EnvironmentOwnerID, ChangeReason: "创建隔离浏览器测试数据", CreatedAt: now}
	if err := db.CreateEnvironment(ctx, env, revision); err != nil {
		t.Fatal(err)
	}
	second := revision
	second.ID = "browser-environment-r2"
	second.Revision = 2
	second.ChangeReason = strings.Repeat("更新主机连接配置并保留历史版本。", 10)
	if err := db.CreateEnvironmentRevision(ctx, second); err != nil {
		t.Fatal(err)
	}
	scenario := domain.Scenario{ID: "browser-scenario", Slug: "browser-scenario", Name: "Kubernetes 集群交付 · 桌面验收", Description: strings.Repeat("验证组件依赖、执行顺序与业务验收配置。", 6), OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", Name: "容器运行时与公共参数来源", ReleaseID: upstream.ID, Action: domain.ActionInstall, HostGroup: "all", ParameterValues: map[string]any{}, Position: domain.GraphPosition{X: 80, Y: 120}}}, Edges: []domain.ScenarioEdge{}}
	sceneRevision := domain.ScenarioRevision{ID: "browser-scenario-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, CreatedAt: now, Graph: graph}
	if err := db.CreateScenario(ctx, scenario, sceneRevision); err != nil {
		t.Fatal(err)
	}
	for index, status := range []domain.RunStatus{domain.RunSucceeded, domain.RunFailed} {
		id := fmt.Sprintf("browser-run-%d", index)
		run := domain.Run{ID: id, Kind: domain.RunComponentTest, Status: status, RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: env.ID, EnvironmentRevisionID: revision.ID, ComponentReleaseID: upstream.ID, Action: domain.ActionInstall, CreatedAt: now, StartedAt: &now, FinishedAt: &now, InputSnapshot: map[string]any{"componentReleaseSpecDigest": domain.ComponentReleaseSpecDigest(upstream)}}
		if status == domain.RunFailed {
			run.Error = "浏览器测试数据：后置检查未通过，请检查完整日志和诊断信息。"
		}
		if err := db.CreateRun(ctx, run, nil); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			step := domain.RunStep{ID: fmt.Sprintf("%s-step-%d", id, j), RunID: id, Name: []string{"安装前条件检查", "部署容器运行时与依赖组件", "安装后业务检查"}[j], Status: status, StartedAt: &now, FinishedAt: &now}
			if err := db.CreateRunStep(ctx, step); err != nil {
				t.Fatal(err)
			}
			for line := 0; line < 35; line++ {
				if _, err := db.AppendRunLog(ctx, domain.RunLog{RunID: id, StepID: step.ID, Stream: "stdout", Message: fmt.Sprintf("TASK [%s] %s", step.Name, strings.Repeat("long_output=value ", 20)), CreatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, owner := range []string{seed.ComponentOwnerRuntimeID, seed.EnvironmentOwnerID, seed.ScenarioOwnerID} {
		if err := db.CreateNotification(ctx, domain.Notification{ID: "browser-notification-" + owner, UserID: owner, Type: "release.published", Title: "上游组件已发布新版本 · 请评估下游影响", Body: strings.Repeat("当前锁定版本保持有效。请核对参数合同、测试证据以及目标环境适配范围。", 5), ResourceURL: "/components?selected=browser-consumer", Payload: map[string]any{}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

}
