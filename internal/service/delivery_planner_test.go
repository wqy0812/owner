package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func TestLockedCachedMediaRecheckedAndDeduplicated(t *testing.T) {
	probe := &stubArtifactDelivery{}
	delivery := &DeliveryService{artifactDelivery: probe}
	step := lockedStep{Variables: map[string]any{}}
	if err := bindArtifactVariables(&step, domain.ComponentArtifact{Alias: "runtime", SHA256: strings.Repeat("a", 64), SizeBytes: 42}, "runtime.tgz", "http://fss.test/runtime.tgz"); err != nil {
		t.Fatal(err)
	}
	if len(step.Media) != 1 || step.Media[0].SizeBytes != 42 {
		t.Fatal("cached content was not locked")
	}
	plan := lockedPlan{Steps: []lockedStep{step, step}}
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
	if !reflect.DeepEqual(job.Steps[0].Media, []ansible.JobMedia{step.Media[0]}) {
		t.Fatal("export lost cached media identities")
	}
}

type stubArtifactDelivery struct {
	probeErr error
	probes   []ArtifactLocation
}

func (s *stubArtifactDelivery) Probe(_ context.Context, location ArtifactLocation, _ ArtifactIdentity) error {
	s.probes = append(s.probes, location)
	return s.probeErr
}

func (*stubArtifactDelivery) Transfer(context.Context, ArtifactTransfer) error { return nil }

type stubImageDelivery struct{ probeErr error }

func (s stubImageDelivery) Probe(context.Context, ImageLocation, ImageDigest) error {
	return s.probeErr
}

func (stubImageDelivery) Transfer(context.Context, ImageTransfer) error { return nil }

func TestFinalizeDeliveryPlanValidatesCompleteUniqueDecisions(t *testing.T) {
	p := &DeliveryService{}
	if _, err := p.finalizeDeliveryPlan(context.Background(), lockedPlan{}, []DeliveryDecisionInput{{RequirementID: "extra", Mode: "direct"}}, domain.User{}, time.Time{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("decision without requirements error=%v", err)
	}
	plan := lockedPlan{DeliveryRequirements: []DeliveryRequirement{{ID: "artifact:a"}, {ID: "image:b"}}}
	for name, inputs := range map[string][]DeliveryDecisionInput{
		"missing":   {{RequirementID: "artifact:a", Mode: "direct"}},
		"duplicate": {{RequirementID: "artifact:a", Mode: "direct"}, {RequirementID: "artifact:a", Mode: "transfer"}},
		"mode":      {{RequirementID: "artifact:a", Mode: "copy"}, {RequirementID: "image:b", Mode: "direct"}},
		"unknown":   {{RequirementID: "artifact:a", Mode: "direct"}, {RequirementID: "other", Mode: "direct"}},
	} {
		if _, err := p.finalizeDeliveryPlan(context.Background(), plan, inputs, domain.User{}, time.Time{}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s decision error=%v", name, err)
		}
	}
}

func TestFinalizeArtifactTransferLocksWorkAndBindsTarget(t *testing.T) {
	artifactDelivery := &stubArtifactDelivery{probeErr: ErrDeliveryTargetMissing}
	p := &DeliveryService{artifactDelivery: artifactDelivery}
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	checksum := strings.Repeat("a", 64)
	plan := lockedPlan{
		Steps: []lockedStep{{ID: "install", Variables: map[string]any{}}, {ID: "unrelated", Variables: map[string]any{}}},
		DeliveryRequirements: []DeliveryRequirement{{
			ID: "artifact:release:runtime", Kind: "artifact", Name: "runtime", Identity: "sha256:" + checksum,
			Source: "https://source.test/runtime.tgz", Target: "http://fss.test/components/runtime.tgz", TransferAvailable: true,
			StepIDs: []string{"install"}, TargetStation: "fss.test", RelativePath: "components/runtime.tgz", SizeBytes: 42,
		}},
	}
	got, err := p.finalizeDeliveryPlan(context.Background(), plan, []DeliveryDecisionInput{{RequirementID: "artifact:release:runtime", Mode: "transfer"}}, domain.User{ID: "environment-owner"}, at)
	if err != nil {
		t.Fatal(err)
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

func TestFinalizeDirectImageBindsImmutableSource(t *testing.T) {
	p := &DeliveryService{}
	digest := "sha256:" + strings.Repeat("b", 64)
	plan := lockedPlan{
		Steps: []lockedStep{{ID: "install", Variables: map[string]any{}}},
		DeliveryRequirements: []DeliveryRequirement{{
			ID: "image:release:main", Kind: "image", Name: "main", Identity: digest,
			Source: "registry.test/runtime@" + digest, StepIDs: []string{"install"},
		}},
	}
	got, err := p.finalizeDeliveryPlan(context.Background(), plan, []DeliveryDecisionInput{{RequirementID: "image:release:main", Mode: "direct"}}, domain.User{ID: "owner"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main_image_ref", "component_image_ref"} {
		if got.Steps[0].Variables[name] != plan.DeliveryRequirements[0].Source {
			t.Fatalf("%s=%v", name, got.Steps[0].Variables[name])
		}
	}
	for _, name := range []string{"main_image_digest", "component_image_digest"} {
		if got.Steps[0].Variables[name] != digest {
			t.Fatalf("%s=%v", name, got.Steps[0].Variables[name])
		}
	}
	if got.DeliveryResults[0].Status != "direct" || got.DeliveryResults[0].CompletedAt == nil {
		t.Fatalf("direct result=%+v", got.DeliveryResults[0])
	}
}

func TestDeliveryVariableBindingRejectsParameterCollisions(t *testing.T) {
	artifactStep := lockedStep{Variables: map[string]any{"runtime_url": "user-value"}}
	err := bindArtifactVariables(&artifactStep, domain.ComponentArtifact{Alias: "runtime", SHA256: "sum"}, "runtime.tgz", "https://source.test/runtime.tgz")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("artifact collision error=%v", err)
	}
	imageStep := lockedStep{Variables: map[string]any{"component_image_ref": "user-value"}}
	if err := bindImageVariables(&imageStep, "main", "registry.test/runtime@sha256:abc", "sha256:abc"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("image collision error=%v", err)
	}
}

func TestDeliveryRequirementDeduplicatesSharedReleaseAcrossSteps(t *testing.T) {
	plan := lockedPlan{}
	appendDeliveryRequirement(&plan, DeliveryRequirement{ID: "artifact:r:a", StepIDs: []string{"step-1"}})
	appendDeliveryRequirement(&plan, DeliveryRequirement{ID: "artifact:r:a", StepIDs: []string{"step-2"}})
	if len(plan.DeliveryRequirements) != 1 || !reflect.DeepEqual(plan.DeliveryRequirements[0].StepIDs, []string{"step-1", "step-2"}) {
		t.Fatalf("requirements=%+v", plan.DeliveryRequirements)
	}
}

func TestDeliveryTargetPresenceDistinguishesKindsAndProbeFailures(t *testing.T) {
	p := &DeliveryService{artifactDelivery: &stubArtifactDelivery{probeErr: errors.New("fss unavailable")}, imageDelivery: stubImageDelivery{probeErr: errors.New("missing")}}
	if _, err := p.deliveryTargetPresent(context.Background(), DeliveryRequirement{Kind: "artifact", Target: "http://fss.test/file"}); err == nil || !strings.Contains(err.Error(), "probe artifact target") {
		t.Fatalf("artifact probe error=%v", err)
	}
	if present, err := p.deliveryTargetPresent(context.Background(), DeliveryRequirement{Kind: "image", Target: "registry.test/image@sha256:abc"}); err != nil || present {
		t.Fatalf("missing image present=%v err=%v", present, err)
	}
	if _, err := p.deliveryTargetPresent(context.Background(), DeliveryRequirement{Kind: "archive"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown kind error=%v", err)
	}
}
