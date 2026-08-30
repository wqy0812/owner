package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

type imageDeliveryStub struct {
	resolved string
	err      error
	probes   []ImageLocation
	digests  []ImageDigest
}

func (s *imageDeliveryStub) Probe(_ context.Context, location ImageLocation, digest ImageDigest) error {
	s.probes = append(s.probes, location)
	s.digests = append(s.digests, digest)
	if s.err != nil {
		return s.err
	}
	if location.ObservedDigest != nil {
		*location.ObservedDigest = s.resolved
	}
	return nil
}

func (*imageDeliveryStub) Transfer(context.Context, ImageTransfer) error { return nil }

func TestComponentImageLifecyclePreservesImmutableDigest(t *testing.T) {
	platform, database := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.User{ID: "component-owner", Name: "Owner", Role: domain.RoleComponentOwner, CreatedAt: now}
	release := domain.ComponentRelease{
		ID: "release-image", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
	}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	delivery := &imageDeliveryStub{resolved: "registry.test/runtime@" + digest}
	platform.ConfigureDeliveryAdapters(nil, delivery)

	image, err := platform.RegisterComponentImage(ctx, owner, release.ID, " Main_Image ", "registry.test/runtime:1.0.0", digest)
	if err != nil {
		t.Fatal(err)
	}
	if image.LogicalName != "main_image" || image.Digest != digest || image.SourceRef != "registry.test/runtime:1.0.0" {
		t.Fatalf("registered image=%+v", image)
	}
	if len(delivery.probes) != 1 || delivery.probes[0].Ref != image.SourceRef || delivery.digests[0].Value != "" {
		t.Fatalf("register probes=%+v digests=%+v", delivery.probes, delivery.digests)
	}

	delivery.resolved = "registry.mirror.test/runtime@" + digest
	updated, err := platform.UpdateComponentImageSource(ctx, owner, release.ID, image.LogicalName, "registry.mirror.test/runtime:stable")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Digest != digest || updated.SourceRef != "registry.mirror.test/runtime:stable" {
		t.Fatalf("updated image=%+v", updated)
	}
	if got := delivery.digests[len(delivery.digests)-1].Value; got != digest {
		t.Fatalf("source update probed digest=%q, want %q", got, digest)
	}

	delivery.resolved = "registry.mirror.test/runtime@sha256:" + strings.Repeat("b", 64)
	if _, err := platform.UpdateComponentImageSource(ctx, owner, release.ID, image.LogicalName, "registry.mirror.test/runtime:other"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("mutable source changed content identity: %v", err)
	}
	stored, err := database.GetComponentImage(ctx, release.ID, image.LogicalName)
	if err != nil || stored.SourceRef != updated.SourceRef || stored.Digest != digest {
		t.Fatalf("failed source update changed stored image=%+v err=%v", stored, err)
	}

	if err := platform.DeleteComponentImage(ctx, owner, release.ID, image.LogicalName); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetComponentImage(ctx, release.ID, image.LogicalName); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted image lookup error=%v", err)
	}
}

func TestComponentImageLifecycleRejectsInvalidOrUnsafeChanges(t *testing.T) {
	platform, database := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.User{ID: "component-owner", Role: domain.RoleComponentOwner}
	draft := domain.ComponentRelease{
		ID: "release-draft", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
	}
	releasedAt := now
	released := draft
	released.ID, released.Version, released.Status, released.ReleasedAt = "release-released", "0.9.0", domain.ReleaseReleased, &releasedAt
	for _, release := range []domain.ComponentRelease{draft, released} {
		if err := database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	delivery := &imageDeliveryStub{resolved: "registry.test/runtime@" + digest}
	platform.ConfigureDeliveryAdapters(nil, delivery)

	for name, input := range map[string]struct{ logicalName, sourceRef, expectedDigest string }{
		"logical name": {"bad-name", "registry.test/runtime:1", digest},
		"source URL":   {"main", "https://registry.test/runtime:1", digest},
		"digest":       {"main", "registry.test/runtime:1", "sha256:short"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := platform.RegisterComponentImage(ctx, owner, draft.ID, input.logicalName, input.sourceRef, input.expectedDigest); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid image input error=%v", err)
			}
		})
	}

	delivery.resolved = "registry.test/runtime@" + digest
	mismatchedDigest := "sha256:" + strings.Repeat("d", 64)
	if _, err := platform.RegisterComponentImage(ctx, owner, draft.ID, "mismatch", "registry.test/runtime:1", mismatchedDigest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected digest mismatch error=%v", err)
	}
	if _, err := database.GetComponentImage(ctx, draft.ID, "mismatch"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("digest mismatch persisted image: %v", err)
	}

	if _, err := platform.RegisterComponentImage(ctx, domain.User{ID: "other", Role: domain.RoleComponentOwner}, draft.ID, "main", "registry.test/runtime:1", digest); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-owner registration error=%v", err)
	}
	if _, err := platform.RegisterComponentImage(ctx, owner, released.ID, "main", "registry.test/runtime:1", digest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released image registration error=%v", err)
	}
	if err := platform.DeleteComponentImage(ctx, owner, released.ID, "main"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released image deletion error=%v", err)
	}

	delivery.err = errors.New("registry unavailable")
	if _, err := platform.RegisterComponentImage(ctx, owner, draft.ID, "main", "registry.test/runtime:1", digest); err == nil || !strings.Contains(err.Error(), "probe image source") {
		t.Fatalf("registry failure error=%v", err)
	}
}

func TestImageReferenceNormalizationAndIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("d", 64)
	if got, err := normalizeImageLogicalName(" MAIN_1 "); err != nil || got != "main_1" {
		t.Fatalf("logical name=%q err=%v", got, err)
	}
	if got, err := normalizeOCIDigest(" SHA256:" + strings.Repeat("D", 64) + " "); err != nil || got != digest {
		t.Fatalf("digest=%q err=%v", got, err)
	}
	if got, err := digestFromResolvedImageRef("registry.test/runtime@" + digest); err != nil || got != digest {
		t.Fatalf("resolved digest=%q err=%v", got, err)
	}
	if got := immutableImageSourceRef("registry.test:5000/runtime:latest", digest); got != "registry.test:5000/runtime@"+digest {
		t.Fatalf("immutable ref=%q", got)
	}
	if _, err := digestFromResolvedImageRef("registry.test/runtime:latest"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("mutable resolved ref error=%v", err)
	}
}
