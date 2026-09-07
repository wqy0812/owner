package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mediadelivery "codex/platform-demo/internal/delivery"
)

func TestPreparationMediaDeduplicatesOnlyWithinSourceAndContext(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	checks := []PreparationCheck{}
	ctx := withMediaObservation(context.Background(), func(c PreparationCheck) { mu.Lock(); defer mu.Unlock(); checks = append(checks, c) })
	probe := func(ctx context.Context) (int64, string, error) { calls.Add(1); return 7, "digest", nil }
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			n, d, e := observedProbe(ctx, "source-a/identity/context-a", "https://user:password@files.example/a?token=private", probe)
			if n != 7 || d != "digest" || e != nil {
				t.Errorf("cached result=%d %s %v", n, d, e)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("source checked %d times", calls.Load())
	}
	if len(checks) != 2 || checks[0].Source != "https://files.example/a" {
		t.Fatalf("unsafe or duplicate observations: %+v", checks)
	}
	_, _, _ = observedProbe(ctx, "source-b/identity/context-a", "https://other.example/a", probe)
	_, _, _ = observedProbe(withMediaObservation(context.Background(), func(PreparationCheck) {}), "source-a/identity/context-a", "https://files.example/a", probe)
	if calls.Load() != 3 {
		t.Fatalf("different sources or sessions were incorrectly reused: %d", calls.Load())
	}
}
func TestPreparationMediaCancellationStopsProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(withMediaObservation(context.Background(), func(PreparationCheck) {}))
	done := make(chan error, 1)
	go func() {
		_, _, e := observedProbe(ctx, "key", "source", func(c context.Context) (int64, string, error) { <-c.Done(); return 0, "", c.Err() })
		done <- e
	}()
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("probe ignored cancellation")
	}
}

func TestPreparationPlannedTransferIsProvidedNeverPassed(t *testing.T) {
	checks := map[string]PreparationCheck{}
	ctx := withMediaObservation(context.Background(), func(c PreparationCheck) { checks[c.ID] = c })
	_, _, _ = observedProbe(ctx, "target", "files.test/components/a", func(context.Context) (int64, string, error) { return 0, "", mediadelivery.ErrDeliveryTargetMissing })
	_, _, _ = observedProbe(ctx, "source", "https://source.test/a", func(context.Context) (int64, string, error) { return 0, "", errors.New("source failed") })
	observePlannedMediaTransfer(ctx, "http://files.test/components/a")
	for _, c := range checks {
		if c.Source == "files.test/components/a" && c.Status != "provided" {
			t.Fatal(c)
		}
		if c.Source == "https://source.test/a" && c.Status != "failed" {
			t.Fatal("unrelated source failure suppressed", c)
		}
	}
}

func TestMediaDecoratorsIsolateAdaptersAndReturnObservedValues(t *testing.T) {
	checks := []PreparationCheck{}
	ctx := withMediaObservation(context.Background(), func(c PreparationCheck) { checks = append(checks, c) })
	first := &imageDeliveryStub{resolved: "registry.test/a@sha256:first"}
	second := &imageDeliveryStub{resolved: "registry.test/a@sha256:second"}
	a, b := observeImageDelivery(first), observeImageDelivery(second)
	if observeImageDelivery(a) != a {
		t.Fatal("adapter wrapped twice")
	}
	for _, item := range []struct {
		adapter mediadelivery.ImageDelivery
		want    string
	}{{a, first.resolved}, {a, first.resolved}, {b, second.resolved}} {
		var digest string
		if err := item.adapter.Probe(ctx, mediadelivery.ImageLocation{Ref: "registry.test/a:1", ObservedDigest: &digest}, mediadelivery.ImageDigest{}); err != nil || digest != item.want {
			t.Fatalf("digest=%s error=%v", digest, err)
		}
	}
	if len(first.probes) != 1 || len(second.probes) != 1 || len(checks) != 4 {
		t.Fatalf("probes=%d/%d checks=%d", len(first.probes), len(second.probes), len(checks))
	}
}
