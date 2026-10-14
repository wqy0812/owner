package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestMissingDeliveryTargetExplainsEnvironmentConfigurationBeforeProbing(t *testing.T) {
	for _, variable := range []string{fileStationVariable, imageRegistryVariable} {
		t.Run(variable, func(t *testing.T) {
			_, db := readinessTestPlatform(t)
			ctx := context.Background()
			now := time.Now().UTC()
			release := domain.ComponentRelease{ID: "release-media", ComponentID: "component-1", Version: "1.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, CreatedAt: now}
			if err := db.CreateComponentRelease(ctx, release); err != nil {
				t.Fatal(err)
			}
			if variable == fileStationVariable {
				err := db.UpsertDraftComponentArtifactAndInvalidate(ctx, domain.ComponentArtifact{ID: "artifact", ReleaseID: release.ID, Alias: "media", Filename: "media.tgz", SHA256: strings.Repeat("a", 64), SourceURL: "https://source.invalid/media", CreatedBy: "component-owner", SourceUpdatedBy: "component-owner", CreatedAt: now, SourceUpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				err := db.UpsertDraftComponentImage(ctx, domain.ComponentImage{ID: "image", ReleaseID: release.ID, LogicalName: "main", Digest: "sha256:" + strings.Repeat("a", 64), SourceRef: "source.invalid/image:1", CreatedBy: "component-owner", SourceUpdatedBy: "component-owner", CreatedAt: now, SourceUpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
			}
			// Nil adapters make an accidental probe fail immediately.
			delivery := &DeliveryService{store: db}
			plan := domain.RunExecutionPlan{Steps: []domain.RunPlanStep{{ID: "step", ReleaseID: release.ID, Variables: map[string]any{}}}}
			revision := domain.EnvironmentRevision{EnvironmentID: "target-env", Variables: map[string]string{}}
			var err error
			if variable == fileStationVariable {
				err = delivery.bindComponentArtifacts(ctx, revision, &plan)
			} else {
				err = delivery.bindComponentImages(ctx, revision, &plan)
			}
			var actionable *domain.ActionableError
			if !errors.Is(err, domain.ErrInvalid) || !errors.As(err, &actionable) || !strings.Contains(err.Error(), variable) {
				t.Fatalf("missing actionable target error: %v", err)
			}
			if actionable.Explanation.PrimaryAction.Href != "/environments?selected=target-env&tab=variables&focus="+variable {
				t.Fatalf("wrong configuration action: %+v", actionable.Explanation)
			}
		})
	}
}

func TestRetryPreservesRiskWhenDeliveryTargetWasReused(t *testing.T) {
	p, db, _, revision, environment, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	releaseID := revision.Graph.Nodes[0].ReleaseID
	release, err := db.GetComponentRelease(ctx, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	component, err := db.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.User{ID: component.OwnerID, Role: domain.RoleComponentOwner}
	prepared, err := p.planner.prepareComponentTest(ctx, owner, releaseID, ComponentTestRequest{EnvironmentID: environment.ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	plan, _, _, err := p.planner.prepareLockedPlan(ctx, environment, domain.RunComponentTest, "reused-risk", now, prepared.steps)
	if err != nil {
		t.Fatal(err)
	}
	plan.DeliveryRequirements = []domain.RunDeliveryRequirement{{ID: "media", Kind: "artifact", Name: "media", Source: "https://source.invalid/media", Target: "http://target/media", TargetStation: "target", RelativePath: "media", TransferAvailable: true}}
	delivery := &DeliveryService{artifactDelivery: presentArtifactDelivery{}}
	plan, err = delivery.finalizeDeliveryPlan(ctx, plan, now)
	if err != nil || len(plan.ArtifactTransfers) != 0 || !plan.HighRisk() {
		t.Fatalf("reused plan=%+v err=%v", plan, err)
	}
	p.scheduler.mu.Lock()
	p.scheduler.workers[environment.ID] = environmentWorkerState{token: 99, heartbeat: now}
	p.scheduler.mu.Unlock()
	source, err := p.execution.creator.createRun(ctx, owner, environment, domain.RunComponentTest, releaseID, "", prepared.action, lockedRunPreparation{ID: "reused-risk", CapturedAt: now, Plan: plan, Destructive: plan.HighRisk()}, prepared.provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRunStatus(ctx, source.ID, []domain.RunStatus{domain.RunQueued}, domain.RunFailed, "precheck failed", now); err != nil {
		t.Fatal(err)
	}
	preview, err := p.execution.PreviewRetry(ctx, owner, source.ID)
	if err != nil || !preview.HighRisk {
		t.Fatalf("retry risk=%v err=%v", preview.HighRisk, err)
	}
	retry, err := p.execution.Retry(ctx, owner, source.ID, RunRetryRequest{ExpectedPlanDigest: preview.PlanDigest})
	if err != nil || !retry.Destructive {
		t.Fatalf("retry destructive=%v err=%v", retry.Destructive, err)
	}
}
