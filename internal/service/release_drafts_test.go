package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil"
)

func TestReleaseDraftModesKeepTemplateAndEvolutionRelationshipsSeparate(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	owner := domain.User{ID: "component-owner", Name: "Owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	root := domain.ComponentRelease{
		ID: "release-root", ComponentID: "component-1", LineID: "line-stable", LineName: "Stable",
		Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now, ReleasedAt: &now,
	}
	if err := database.CreateComponentRelease(ctx, root); err != nil {
		t.Fatal(err)
	}
	child := domain.ComponentRelease{
		ID: "release-child", ComponentID: "component-1", LineID: root.LineID, LineName: root.LineName,
		ParentReleaseID: root.ID, TemplateSourceReleaseID: root.ID,
		Version: "2.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityBreaking,
		RiskLevel: domain.RiskHigh, EnvironmentConstraints: map[string]any{"architecture": []any{"amd64"}}, CreatedAt: now.Add(time.Second), ReleasedAt: &now,
		Actions: []domain.ActionDefinition{
			{ID: "install-child", ReleaseID: "release-child", Name: "Install", Kind: domain.ActionInstall, Playbook: "install.yml", HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
			{ID: "upgrade-child", ReleaseID: "release-child", Name: "Upgrade", Kind: domain.ActionUpgrade, Playbook: "upgrade.yml", HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskHigh, FromReleaseID: root.ID, ToReleaseID: "release-child"},
			{ID: "rollback-child", ReleaseID: "release-child", Name: "Rollback", Kind: domain.ActionRollback, Playbook: "rollback.yml", HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskHigh, FromReleaseID: "release-child", ToReleaseID: root.ID},
		},
	}
	if err := database.CreateComponentRelease(ctx, child); err != nil {
		t.Fatal(err)
	}

	workspaceRoot := t.TempDir()
	testutil.Workspaces(t, database, workspaceRoot, child.ID)
	platform.ConfigurePlaybookRoot(workspaceRoot)

	newLineRequest := ReleaseDraftRequest{
		Mode: ReleaseDraftNewLine, LineName: "Next baseline", TemplateSourceReleaseID: child.ID,
		Version: "3.0-template", ReleaseNotes: "independent baseline", RiskLevel: domain.RiskLow,
	}
	newLinePlan, err := platform.PreviewReleaseDraft(ctx, owner, "component-1", newLineRequest)
	if err != nil {
		t.Fatal(err)
	}
	newLineRequest.ExpectedPlanDigest = "stale"
	if _, err := platform.CreateReleaseDraft(ctx, owner, "component-1", newLineRequest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale plan error=%v", err)
	}
	newLineRequest.ExpectedPlanDigest = newLinePlan.PlanDigest
	baseline, err := platform.CreateReleaseDraft(ctx, owner, "component-1", newLineRequest)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.LineID == child.LineID || baseline.ParentReleaseID != "" || baseline.TemplateSourceReleaseID != child.ID || baseline.Compatibility != domain.CompatibilityNotApplicable {
		t.Fatalf("new baseline lineage=%+v", baseline)
	}
	for _, action := range baseline.Actions {
		if action.Kind == domain.ActionUpgrade {
			t.Fatalf("new baseline retained Upgrade: %+v", action)
		}
		if action.Kind == domain.ActionRollback && (action.FromReleaseID != "" || action.ToReleaseID != "") {
			t.Fatalf("new baseline retained bound Rollback: %+v", action)
		}
	}
	impact, err := platform.PublicationImpact(ctx, owner, baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if impact.ChangeKind != "new_line" || len(impact.Recipients) != 0 {
		t.Fatalf("new baseline impact=%+v", impact)
	}

	evolutionRequest := ReleaseDraftRequest{
		Mode: ReleaseDraftEvolution, ParentReleaseID: child.ID, Compatibility: domain.CompatibilityCompatible,
		Version: "3.0.0", ReleaseNotes: "line evolution", RiskLevel: domain.RiskMedium,
	}
	evolutionPlan, err := platform.PreviewReleaseDraft(ctx, owner, "component-1", evolutionRequest)
	if err != nil {
		t.Fatal(err)
	}
	evolutionRequest.ExpectedPlanDigest = evolutionPlan.PlanDigest
	evolution, err := platform.CreateReleaseDraft(ctx, owner, "component-1", evolutionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if evolution.LineID != child.LineID || evolution.ParentReleaseID != child.ID || evolution.TemplateSourceReleaseID != child.ID || evolution.Compatibility != domain.CompatibilityCompatible {
		t.Fatalf("evolution lineage=%+v", evolution)
	}
	for _, action := range evolution.Actions {
		switch action.Kind {
		case domain.ActionUpgrade:
			if action.FromReleaseID != child.ID || action.ToReleaseID != evolution.ID {
				t.Fatalf("rewritten Upgrade=%+v", action)
			}
		case domain.ActionRollback:
			if action.FromReleaseID != evolution.ID || action.ToReleaseID != child.ID {
				t.Fatalf("rewritten Rollback=%+v", action)
			}
		}
	}
}

func TestReleaseDraftPreviewRejectsDeprecatedPublishedSuccessor(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	owner := domain.User{ID: "component-owner", Name: "Owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	root := domain.ComponentRelease{
		ID: "release-preview-root", ComponentID: "component-1", LineID: "line-preview", LineName: "Preview",
		Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now, ReleasedAt: &now,
	}
	childReleasedAt := now.Add(time.Minute)
	child := domain.ComponentRelease{
		ID: "release-preview-child", ComponentID: "component-1", LineID: root.LineID, LineName: root.LineName,
		ParentReleaseID: root.ID, TemplateSourceReleaseID: root.ID,
		Version: "2.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityCompatible,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: childReleasedAt, ReleasedAt: &childReleasedAt,
	}
	if err := database.CreateComponentRelease(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateComponentRelease(ctx, child); err != nil {
		t.Fatal(err)
	}
	if err := database.DeprecateComponentRelease(ctx, child.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, err := platform.PreviewReleaseDraft(ctx, owner, "component-1", ReleaseDraftRequest{
		Mode: ReleaseDraftEvolution, ParentReleaseID: root.ID, Compatibility: domain.CompatibilityCompatible,
		Version: "3.0.0", ReleaseNotes: "must not preview", EnvironmentConstraints: map[string]any{},
	})
	if !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "Deprecated") {
		t.Fatalf("preview error=%v, want explicit Deprecated successor conflict", err)
	}
	retained, err := database.HasRetainedSuccessor(ctx, root.ID)
	if err != nil || !retained {
		t.Fatalf("retained successor=%t err=%v", retained, err)
	}
}
