package delivery

import (
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
		{"reuse", nil, nil, nil, []string{"target"}, nil},
		{"transfer", ErrDeliveryTargetMissing, nil, nil, []string{"target", "transfer:https://source/a", "target"}, nil},
		{"mismatch", ErrArtifactIdentityMismatch, nil, nil, []string{"target"}, ErrArtifactIdentityMismatch},
		{"copy fails", ErrDeliveryTargetMissing, nil, failure, []string{"target", "transfer:https://source/a"}, failure},
		{"copied wrong bytes", ErrDeliveryTargetMissing, ErrArtifactIdentityMismatch, nil, []string{"target", "transfer:https://source/a", "target"}, ErrArtifactIdentityMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &artifactRecorder{initial: tc.initial, after: tc.after, transfer: tc.transfer}
			plan := Plan{ArtifactTransfers: []PlannedArtifactTransfer{{SourceURL: "https://source/a", TargetStation: "target", RelativePath: "a", SHA256: "abc"}}}
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
	plan := Plan{ImageTransfers: []PlannedImageTransfer{{SourceDigest: "source@sha256:abc", TargetRef: "target:1", TargetDigest: "target@sha256:abc"}},
		DeliveryRequirements: []Requirement{{ID: "a", Kind: "artifact", Source: "https://source/a", Target: "https://target/a"}, {ID: "b", Kind: "artifact", Source: "https://source/b"}, {ID: "i", Kind: "image", Source: "source@sha256:abc", Target: "target@sha256:abc"}},
		DeliveryDecisions:    []Decision{{RequirementID: "a", Mode: "transfer"}, {RequirementID: "b", Mode: "direct"}, {RequirementID: "i", Mode: "transfer"}}}
	if err := Prepare(context.Background(), plan, artifacts, images); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(artifacts.events, []string{"probe:https://target/a", "probe:https://source/b"}) || !images.transferred || !reflect.DeepEqual(images.refs, []string{"target@sha256:abc", "target@sha256:abc", "target@sha256:abc"}) {
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
