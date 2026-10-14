package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	mediadelivery "codex/platform-demo/internal/delivery"
	"codex/platform-demo/internal/domain"
)

func TestLockedCachedMediaRecheckedAndDeduplicated(t *testing.T) {
	probe := &stubArtifactDelivery{}
	delivery := &DeliveryService{artifactDelivery: probe}
	step := domain.RunPlanStep{Variables: map[string]any{}}
	if err := bindArtifactVariables(&step, domain.ComponentArtifact{Alias: "runtime", SHA256: strings.Repeat("a", 64), SizeBytes: 42}, "runtime.tgz", "http://fss.test/runtime.tgz"); err != nil {
		t.Fatal(err)
	}
	if len(step.Media) != 1 || step.Media[0].SizeBytes != 42 {
		t.Fatal("cached content was not locked")
	}
	plan := domain.RunExecutionPlan{Steps: []domain.RunPlanStep{step, step}}
	if err := delivery.verifyLockedMedia(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(probe.probes) != 1 {
		t.Fatal("duplicate media downloaded per phase")
	}
	probe.probeErr = errors.New("content disappeared after preview")
	if err := delivery.verifyLockedMedia(context.Background(), plan); err == nil {
		t.Fatal("missing cached media accepted")
	}
	job := jobPlanFromLocked("env", plan, nil)
	if !reflect.DeepEqual(job.Steps[0].Media, nativeMedia([]domain.RunMedia{step.Media[0]})) {
		t.Fatal("export lost cached media identities")
	}
}

type stubArtifactDelivery struct {
	probeErr error
	probes   []mediadelivery.ArtifactLocation
}

func (s *stubArtifactDelivery) Probe(_ context.Context, location mediadelivery.ArtifactLocation, _ mediadelivery.ArtifactIdentity) error {
	s.probes = append(s.probes, location)
	return s.probeErr
}

func (*stubArtifactDelivery) Transfer(context.Context, mediadelivery.ArtifactTransfer) error {
	return nil
}

type stubImageDelivery struct{ probeErr error }

func (s stubImageDelivery) Probe(context.Context, mediadelivery.ImageLocation, mediadelivery.ImageDigest) error {
	return s.probeErr
}

func (stubImageDelivery) Transfer(context.Context, mediadelivery.ImageTransfer) error { return nil }

func TestFinalizeDeliveryPlanValidatesUniqueRequirements(t *testing.T) {
	p := &DeliveryService{}
	for name, requirements := range map[string][]domain.RunDeliveryRequirement{
		"missing":   {{ID: ""}},
		"duplicate": {{ID: "artifact:a"}, {ID: "artifact:a"}},
	} {
		plan := domain.RunExecutionPlan{DeliveryRequirements: requirements}
		if _, err := p.finalizeDeliveryPlan(context.Background(), plan, time.Time{}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s requirement error=%v", name, err)
		}
	}
}

func TestFinalizeArtifactTransferLocksWorkAndBindsTarget(t *testing.T) {
	artifactDelivery := &stubArtifactDelivery{probeErr: mediadelivery.ErrDeliveryTargetMissing}
	p := &DeliveryService{artifactDelivery: artifactDelivery}
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	checksum := strings.Repeat("a", 64)
	plan := domain.RunExecutionPlan{
		Steps: []domain.RunPlanStep{{ID: "install", Variables: map[string]any{}}, {ID: "unrelated", Variables: map[string]any{}}},
		DeliveryRequirements: []domain.RunDeliveryRequirement{{
			ID: "artifact:release:runtime", Kind: "artifact", Name: "runtime", Identity: "sha256:" + checksum,
			Source: "https://source.test/runtime.tgz", Target: "http://fss.test/components/runtime.tgz", TransferAvailable: true,
			StepIDs: []string{"install"}, TargetStation: "fss.test", RelativePath: "components/runtime.tgz", SizeBytes: 42,
		}},
	}
	got, err := p.finalizeDeliveryPlan(context.Background(), plan, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DeliveryDecisions) != 1 || got.DeliveryDecisions[0].Mode != "transfer" || got.DeliveryDecisions[0].Policy != domain.DeliveryPolicyTransferToTarget {
		t.Fatalf("automatic policy=%+v", got.DeliveryDecisions)
	}
	if len(got.ArtifactTransfers) != 1 || got.ArtifactTransfers[0].SHA256 != checksum || got.ArtifactTransfers[0].SizeBytes != 42 {
		t.Fatalf("artifact transfers=%+v", got.ArtifactTransfers)
	}
	if len(got.DeliveryResults) != 1 || got.DeliveryResults[0].Status != "pending" || got.DeliveryResults[0].ActualLocation != "" {
		t.Fatalf("delivery results=%+v", got.DeliveryResults)
	}
	wantVariables := map[string]any{
		"runtime_path": "components/runtime.tgz", "runtime_url": "http://fss.test/components/runtime.tgz", "runtime_sha256": checksum,
	}
	if !reflect.DeepEqual(got.Steps[0].Variables, wantVariables) || len(got.Steps[1].Variables) != 0 {
		t.Fatalf("bound variables=%v unrelated=%v", got.Steps[0].Variables, got.Steps[1].Variables)
	}
	if len(artifactDelivery.probes) != 1 || artifactDelivery.probes[0].RelativePath != "components/runtime.tgz" {
		t.Fatalf("target probes=%+v", artifactDelivery.probes)
	}
}

func TestAutomaticDeliveryRejectsMissingTarget(t *testing.T) {
	p := &DeliveryService{}
	plan := domain.RunExecutionPlan{DeliveryRequirements: []domain.RunDeliveryRequirement{{ID: "image", Kind: "image", Source: "source/image@sha256:abc"}}}
	if _, err := p.finalizeDeliveryPlan(context.Background(), plan, time.Time{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing target error=%v", err)
	}
}

func TestDeliveryVariableBindingRejectsParameterCollisions(t *testing.T) {
	artifactStep := domain.RunPlanStep{Variables: map[string]any{"runtime_url": "user-value"}}
	err := bindArtifactVariables(&artifactStep, domain.ComponentArtifact{Alias: "runtime", SHA256: "sum"}, "runtime.tgz", "https://source.test/runtime.tgz")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("artifact collision error=%v", err)
	}
	imageStep := domain.RunPlanStep{Variables: map[string]any{"component_image_ref": "user-value"}}
	if err := bindImageVariables(&imageStep, "main", "registry.test/runtime@sha256:abc", "sha256:abc"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("image collision error=%v", err)
	}
}

func TestDeliveryRequirementDeduplicatesSharedReleaseAcrossSteps(t *testing.T) {
	plan := domain.RunExecutionPlan{}
	appendDeliveryRequirement(&plan, domain.RunDeliveryRequirement{ID: "artifact:r:a", StepIDs: []string{"step-1"}})
	appendDeliveryRequirement(&plan, domain.RunDeliveryRequirement{ID: "artifact:r:a", StepIDs: []string{"step-2"}})
	if len(plan.DeliveryRequirements) != 1 || !reflect.DeepEqual(plan.DeliveryRequirements[0].StepIDs, []string{"step-1", "step-2"}) {
		t.Fatalf("requirements=%+v", plan.DeliveryRequirements)
	}
}

func TestDeliveryTargetPresenceDistinguishesKindsAndProbeFailures(t *testing.T) {
	p := &DeliveryService{artifactDelivery: &stubArtifactDelivery{probeErr: errors.New("fss unavailable")}, imageDelivery: stubImageDelivery{probeErr: errors.New("missing")}}
	if _, err := p.deliveryTargetPresent(context.Background(), domain.RunDeliveryRequirement{Kind: "artifact", Target: "http://fss.test/file"}); err == nil || !strings.Contains(err.Error(), "probe artifact target") {
		t.Fatalf("artifact probe error=%v", err)
	}
	if present, err := p.deliveryTargetPresent(context.Background(), domain.RunDeliveryRequirement{Kind: "image", Target: "registry.test/image@sha256:abc"}); err != nil || present {
		t.Fatalf("missing image present=%v err=%v", present, err)
	}
	if _, err := p.deliveryTargetPresent(context.Background(), domain.RunDeliveryRequirement{Kind: "archive"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown kind error=%v", err)
	}
}
