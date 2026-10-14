package delivery

import (
	"codex/platform-demo/internal/domain"
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
)

type artifactRecorder struct {
	events                   []string
	initial, after, transfer error
	transferred              bool
}

func (a *artifactRecorder) Probe(_ context.Context, l ArtifactLocation, _ ArtifactIdentity) error {
	if l.URL != "" {
		a.events = append(a.events, "probe:"+l.URL)
		return nil
	}
	a.events = append(a.events, "target")
	if a.transferred {
		return a.after
	}
	return a.initial
}
func (a *artifactRecorder) Transfer(_ context.Context, transfer ArtifactTransfer) error {
	a.events = append(a.events, "transfer:"+transfer.Source.URL)
	a.transferred = true
	return a.transfer
}

type imageRecorder struct {
	refs        []string
	transferred bool
	targetError error
}

func (i *imageRecorder) Probe(_ context.Context, l ImageLocation, _ ImageDigest) error {
	i.refs = append(i.refs, l.Ref)
	if !i.transferred {
		return i.targetError
	}
	return nil
}
func (i *imageRecorder) Transfer(context.Context, ImageTransfer) error {
	i.transferred = true
	return nil
}
func TestPrepareArtifactsVerifyBeforeAndAfterTransfer(t *testing.T) {
	failure := errors.New("transfer failed")
	for _, tc := range []struct {
		name                     string
		initial, after, transfer error
		want                     []string
		err                      error
	}{
		{"reuse", nil, nil, nil, []string{"target", "probe:http://target/a"}, nil},
		{"transfer", ErrDeliveryTargetMissing, nil, nil, []string{"target", "transfer:https://source/a", "target", "probe:http://target/a"}, nil},
		{"mismatch", ErrArtifactIdentityMismatch, nil, nil, []string{"target"}, ErrArtifactIdentityMismatch},
		{"copy fails", ErrDeliveryTargetMissing, nil, failure, []string{"target", "transfer:https://source/a"}, failure},
		{"copied wrong bytes", ErrDeliveryTargetMissing, ErrArtifactIdentityMismatch, nil, []string{"target", "transfer:https://source/a", "target"}, ErrArtifactIdentityMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &artifactRecorder{initial: tc.initial, after: tc.after, transfer: tc.transfer}
			plan := Plan{
				DeliveryRequirements: []domain.RunDeliveryRequirement{{ID: "a", Kind: "artifact", Source: "https://source/a", Target: "http://target/a"}},
				DeliveryDecisions:    []domain.RunDeliveryDecision{{RequirementID: "a", Mode: "transfer", Policy: domain.DeliveryPolicyTransferToTarget}},
				ArtifactTransfers:    []domain.RunArtifactTransfer{{RequirementID: "a", SourceURL: "https://source/a", TargetStation: "target", RelativePath: "a", SHA256: "abc"}},
			}
			err := Prepare(context.Background(), plan, adapter, &imageRecorder{})
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(adapter.events, tc.want) {
				t.Fatalf("events=%v err=%v", adapter.events, err)
			}
		})
	}
}
func TestPrepareVerifiesChosenLocationsAndImageTransfers(t *testing.T) {
	artifacts := &artifactRecorder{}
	images := &imageRecorder{targetError: ErrDeliveryTargetMissing}
	plan := Plan{ImageTransfers: []domain.RunImageTransfer{{RequirementID: "i", SourceDigest: "source@sha256:abc", TargetRef: "target:1", TargetDigest: "target@sha256:abc"}},
		DeliveryRequirements: []domain.RunDeliveryRequirement{{ID: "a", Kind: "artifact", Source: "https://source/a", Target: "https://target/a"}, {ID: "i", Kind: "image", Source: "source@sha256:abc", Target: "target@sha256:abc"}},
		DeliveryDecisions:    []domain.RunDeliveryDecision{{RequirementID: "a", Mode: "transfer", Policy: domain.DeliveryPolicyTransferToTarget}, {RequirementID: "i", Mode: "transfer", Policy: domain.DeliveryPolicyTransferToTarget}}}
	if err := Prepare(context.Background(), plan, artifacts, images); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(artifacts.events, []string{"probe:https://target/a"}) || !images.transferred || !reflect.DeepEqual(images.refs, []string{"target@sha256:abc", "target@sha256:abc", "target@sha256:abc"}) {
		t.Fatalf("artifacts=%v images=%+v", artifacts.events, images)
	}
	if err := Prepare(context.Background(), Plan{}, nil, nil); err != nil {
		t.Fatal(err)
	}
}
func TestRawProbeHasDeadlineAndHonorsParentCancellation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 3*time.Minute {
			t.Error("probe deadline missing")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewHTTPArtifactDelivery(client).Probe(ctx, ArtifactLocation{URL: "https://source/a"}, ArtifactIdentity{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPrepareRejectsInvalidPlansBeforeMediaSideEffects(t *testing.T) {
	for name, change := range map[string]func(*Plan){
		"missing decision":      func(p *Plan) { p.DeliveryDecisions = nil },
		"direct":                func(p *Plan) { p.DeliveryDecisions[0].Mode = "direct" },
		"missing policy":        func(p *Plan) { p.DeliveryDecisions[0].Policy = "" },
		"unknown policy":        func(p *Plan) { p.DeliveryDecisions[0].Policy = "other" },
		"missing target":        func(p *Plan) { p.DeliveryRequirements[0].Target = " " },
		"duplicate decision":    func(p *Plan) { p.DeliveryDecisions = append(p.DeliveryDecisions, p.DeliveryDecisions[0]) },
		"duplicate requirement": func(p *Plan) { p.DeliveryRequirements = append(p.DeliveryRequirements, p.DeliveryRequirements[0]) },
		"unknown kind":          func(p *Plan) { p.DeliveryRequirements[0].Kind = "unknown" },
		"orphan transfer":       func(p *Plan) { p.ArtifactTransfers[0].RequirementID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			plan := Plan{
				DeliveryRequirements: []domain.RunDeliveryRequirement{{ID: "a", Kind: "artifact", Source: "https://source/a", Target: "https://target/a"}},
				DeliveryDecisions:    []domain.RunDeliveryDecision{{RequirementID: "a", Mode: "transfer", Policy: domain.DeliveryPolicyTransferToTarget}},
				ArtifactTransfers:    []domain.RunArtifactTransfer{{RequirementID: "a", SourceURL: "https://source/a", TargetStation: "target", RelativePath: "a"}},
			}
			change(&plan)
			artifacts, images := &artifactRecorder{}, &imageRecorder{}
			if err := Prepare(context.Background(), plan, artifacts, images); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid plan accepted: %v", err)
			}
			if len(artifacts.events) != 0 || len(images.refs) != 0 || images.transferred {
				t.Fatalf("invalid plan caused media side effects: %v %+v", artifacts.events, images)
			}
		})
	}
}
