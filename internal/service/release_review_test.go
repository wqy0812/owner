package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestPlatformAdminWorkbenchReviewPreviewAndDigestGuard(t *testing.T) {
	platform, database := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	admin := domain.User{ID: "platform-admin", Name: "Admin", Role: domain.RolePlatformAdmin, CreatedAt: now}
	if err := database.UpsertUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	owner, err := database.GetUser(ctx, "component-owner")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	platform.ConfigurePlaybookRoot(root)
	contents := map[domain.ActionKind][]byte{
		domain.ActionInstall:  []byte("---\n- hosts: all\n  tasks: []\n"),
		domain.ActionVerify:   []byte("---\n- hosts: all\n  gather_facts: false\n"),
		domain.ActionRollback: []byte("---\n- hosts: all\n  tasks: []\n"),
	}
	release := domain.ComponentRelease{
		ID: "release-review", ComponentID: "component-1", LineName: "baseline", Version: "1.0.0-p1",
		Compatibility: domain.CompatibilityNotApplicable, Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
	}
	for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionVerify, domain.ActionRollback} {
		path := "managed/component-1/release-review/" + string(kind) + ".yml"
		digest := sha256.Sum256(contents[kind])
		release.Actions = append(release.Actions, domain.ActionDefinition{
			ID: "action-" + string(kind), ReleaseID: release.ID, Name: string(kind), Kind: kind, Playbook: path,
			PlaybookSHA256: hex.EncodeToString(digest[:]), HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow,
		})
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, contents[kind], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, err = database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	submittedAt := now.Add(time.Minute)
	if err := database.SubmitComponentReleaseReview(ctx, release.ID, componentReleaseSpecDigest(release), release.PublicationGeneration, submittedAt); err != nil {
		t.Fatal(err)
	}

	adminWorkbench, err := platform.Workbench(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminWorkbench.Items) != 1 || adminWorkbench.Items[0].Kind != "component_review" || adminWorkbench.Items[0].PrimaryAction.Label != "预览并审核" || adminWorkbench.Summary.ActionRequired != 1 {
		t.Fatalf("admin workbench=%+v", adminWorkbench)
	}
	ownerWorkbench, err := platform.Workbench(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range ownerWorkbench.Items {
		if item.Kind == "component_review" {
			t.Fatalf("component owner can see admin review item: %+v", item)
		}
	}
	if _, err := platform.Catalog().PreviewReleaseReview(ctx, owner, release.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner preview error=%v", err)
	}
	preview, err := platform.Catalog().PreviewReleaseReview(ctx, admin, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ComponentName != "Component" || preview.OwnerName != "Owner" || len(preview.Playbooks) != 3 || preview.PreviewDigest == "" {
		t.Fatalf("preview=%+v", preview)
	}
	if _, err := platform.Catalog().DecideReleaseReview(ctx, admin, release.ID, true, "", ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing digest error=%v", err)
	}
	if _, err := platform.Catalog().DecideReleaseReview(ctx, admin, release.ID, true, "", "stale"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale digest error=%v", err)
	}
	verifyPath := filepath.Join(root, "managed/component-1/release-review/verify.yml")
	if err := os.WriteFile(verifyPath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.Catalog().PreviewReleaseReview(ctx, admin, release.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("tampered preview error=%v", err)
	}
	if err := os.Remove(verifyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.Catalog().PreviewReleaseReview(ctx, admin, release.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("missing playbook preview error=%v", err)
	}
	if err := os.WriteFile(verifyPath, contents[domain.ActionVerify], 0o600); err != nil {
		t.Fatal(err)
	}
	approved, err := platform.Catalog().DecideReleaseReview(ctx, admin, release.ID, true, "", preview.PreviewDigest)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Review.Status != domain.ReleaseReviewApproved {
		t.Fatalf("approved review=%+v", approved.Review)
	}
	adminWorkbench, err = platform.Workbench(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminWorkbench.Items) != 0 || adminWorkbench.Summary.ActionRequired != 0 {
		t.Fatalf("handled review remains in workbench: %+v", adminWorkbench)
	}
}

func TestPlatformAdminWorkbenchSortsReviewsBySubmissionTime(t *testing.T) {
	platform, database := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	admin := domain.User{ID: "platform-admin", Name: "Admin", Role: domain.RolePlatformAdmin, CreatedAt: now}
	if err := database.UpsertUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"release-review-older", "release-review-newer"} {
		release := domain.ComponentRelease{
			ID: id, ComponentID: "component-1", LineID: "line-" + id, LineName: id, Version: "1.0." + string(rune('0'+index)),
			Compatibility: domain.CompatibilityNotApplicable, Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
		}
		if err := database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
		release, err := database.GetComponentRelease(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.SubmitComponentReleaseReview(ctx, id, componentReleaseSpecDigest(release), release.PublicationGeneration, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	workbench, err := platform.Workbench(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(workbench.Items) != 2 || workbench.Items[0].Subject.ID != "release-review-newer" || workbench.Items[1].Subject.ID != "release-review-older" {
		t.Fatalf("review order=%+v", workbench.Items)
	}
}
