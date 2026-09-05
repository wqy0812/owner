package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestComponentReadContextPreservesExactReadinessAndHistoricalEvidence(t *testing.T) {
	p, db := readinessTestPlatform(t)
	defer p.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	envOwner := domain.User{ID: "env-owner", Name: "Env Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if err := db.UpsertUser(ctx, envOwner); err != nil {
		t.Fatal(err)
	}
	env := domain.Environment{ID: "env", Name: "Evidence Lab", OwnerID: envOwner.ID, CreatedAt: now, UpdatedAt: now}
	rev := domain.EnvironmentRevision{ID: "env-r1", EnvironmentID: env.ID, Revision: 1, Facts: map[string]any{}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: now}
	if err := db.CreateEnvironment(ctx, env, rev); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "release", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now}
	if err := db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, _ = db.GetComponentRelease(ctx, r.ID)
	digest := componentReleaseSpecDigest(r)
	for i, tc := range []struct {
		id, kind, digest string
		status           domain.RunStatus
		action           domain.ActionKind
	}{
		{"current-install", "install_verify", digest, domain.RunSucceeded, domain.ActionInstall},
		{"current-rollback", "rollback_self_verify", digest, domain.RunSucceeded, domain.ActionRollback},
		{"current-transition", "evolution_round_trip", digest, domain.RunSucceeded, domain.ActionUpgrade},
		{"failed-install", "install_verify", digest, domain.RunFailed, domain.ActionInstall},
		{"stale-rollback", "rollback_verify", "old-contract", domain.RunSucceeded, domain.ActionRollback},
	} {
		at := now.Add(time.Duration(i) * time.Minute)
		run := domain.Run{ID: tc.id, Kind: domain.RunComponentTest, Status: tc.status, RequestedBy: "component-owner", EnvironmentID: env.ID, EnvironmentRevisionID: rev.ID, ComponentReleaseID: r.ID, Action: tc.action, CreatedAt: at, FinishedAt: &at, InputSnapshot: map[string]any{"componentReleaseSpecDigest": tc.digest, "componentTestEvidence": tc.kind, "secretLargeSnapshot": strings.Repeat("x", 10000)}}
		if err := db.CreateRun(ctx, run, nil); err != nil {
			t.Fatal(err)
		}
	}
	r.Readiness = domain.ReleaseReadiness{Status: domain.ReadinessBlocked, InstallEvidenceRunID: "current-install", RollbackEvidenceRunID: "current-rollback", TransitionEvidenceRunID: "current-transition"}
	component, _ := db.GetComponent(ctx, "component-1", false)
	component.Releases = []domain.ComponentRelease{r}
	before := r.Readiness
	for _, user := range []domain.User{{ID: "component-owner", Role: domain.RoleComponentOwner}, envOwner, {ID: "admin", Role: domain.RolePlatformAdmin}, {ID: "scenario-owner", Role: domain.RoleScenarioOwner}} {
		result, err := p.Catalog().ReadContext(ctx, user, component)
		if err != nil {
			t.Fatal(err)
		}
		e := result.Evidence[r.ID]
		if user.Role == domain.RoleScenarioOwner {
			if e.CurrentInstall != nil {
				t.Fatal("invisible evidence exposed")
			}
			continue
		}
		if e.CurrentInstall == nil || e.CurrentInstall.ID != "current-install" || e.CurrentRollback.ID != "current-rollback" || e.CurrentTransition.ID != "current-transition" {
			t.Fatalf("changed current refs: %+v", e)
		}
		if e.HistoricalInstall.ID != "failed-install" || e.HistoricalInstall.Status != domain.RunFailed || !e.HistoricalInstall.MatchesContract || e.HistoricalRollback.ID != "stale-rollback" || e.HistoricalRollback.MatchesContract {
			t.Fatalf("lost historical meaning: %+v", e)
		}
		bytes, _ := json.Marshal(result)
		if strings.Contains(string(bytes), "secretLargeSnapshot") || strings.Contains(string(bytes), `"steps"`) {
			t.Fatal("heavy detail leaked")
		}
		if !reflect.DeepEqual(before, component.Releases[0].Readiness) {
			t.Fatal("summary mutated publish judgment")
		}
	}
}
