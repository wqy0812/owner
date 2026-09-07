package service

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	mediadelivery "codex/platform-demo/internal/delivery"
)

type mediaObservationKey struct{}
type observedMedia struct {
	source string
	check  PreparationCheck
	done   chan struct{}
	err    error
	size   int64
	digest string
}
type mediaObservation struct {
	mu      sync.Mutex
	entries map[string]*observedMedia
	report  func(PreparationCheck)
}

func withMediaObservation(ctx context.Context, report func(PreparationCheck)) context.Context {
	return context.WithValue(ctx, mediaObservationKey{}, &mediaObservation{entries: map[string]*observedMedia{}, report: report})
}
func observedProbe(ctx context.Context, key, source string, probe func(context.Context) (int64, string, error)) (int64, string, error) {
	state, _ := ctx.Value(mediaObservationKey{}).(*mediaObservation)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if state == nil {
		return probe(ctx)
	}
	state.mu.Lock()
	entry, ok := state.entries[key]
	if ok {
		state.mu.Unlock()
		select {
		case <-entry.done:
			return entry.size, entry.digest, entry.err
		case <-ctx.Done():
			return 0, "", ctx.Err()
		}
	}
	entry = &observedMedia{done: make(chan struct{}), source: source}
	state.entries[key] = entry
	state.mu.Unlock()
	started := time.Now().UTC()
	label := source
	if u, e := url.Parse(source); e == nil && u.Host != "" {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		source = u.String()
		label = path.Base(u.Path)
	}
	check := PreparationCheck{ID: "media-" + digestValue(key)[:20], Category: "media", Label: label, Status: "running", Source: source, StartedAt: started}
	state.report(check)
	entry.size, entry.digest, entry.err = probe(ctx)
	check.Status = "passed"
	if entry.err != nil {
		check.Status = "failed"
		check.Message = safePreparationError(entry.err)
	}
	check.ElapsedMS = time.Since(started).Milliseconds()
	state.mu.Lock()
	entry.check = check
	state.mu.Unlock()
	state.report(check)
	close(entry.done)
	return entry.size, entry.digest, entry.err
}

// A validated source plus an explicit transfer requirement supplies a missing
// target at execution approval. It does not turn a failed probe into a pass.
func observePlannedMediaTransfer(ctx context.Context, target string) {
	state, _ := ctx.Value(mediaObservationKey{}).(*mediaObservation)
	if state == nil {
		return
	}
	normalize := func(v string) string { return strings.TrimPrefix(strings.TrimPrefix(v, "http://"), "https://") }
	updates := []PreparationCheck{}
	state.mu.Lock()
	for _, entry := range state.entries {
		if normalize(entry.source) == normalize(target) && entry.check.Status == "failed" {
			check := entry.check
			check.Status = "provided"
			check.Message = "目标介质尚未就绪；已验证当前来源，需在环境审批后按计划同步，并在使用前复核摘要"
			updates = append(updates, check)
		}
	}
	state.mu.Unlock()
	for _, check := range updates {
		state.report(check)
	}
}
func (d *observedArtifactDelivery) Probe(ctx context.Context, location mediadelivery.ArtifactLocation, identity mediadelivery.ArtifactIdentity) error {
	source := location.URL
	if source == "" {
		source = location.FileStation + "/" + location.RelativePath
	}
	key := digestValue(struct {
		Source        string
		AccessContext string
		Identity      mediadelivery.ArtifactIdentity
	}{source, fmt.Sprintf("%p", d), identity})
	size, _, err := observedProbe(ctx, key, source, func(ctx context.Context) (int64, string, error) {
		var size int64
		probeLocation := location
		probeLocation.ObservedSize = &size
		err := d.ArtifactDelivery.Probe(ctx, probeLocation, identity)
		return size, "", err
	})
	if location.ObservedSize != nil {
		*location.ObservedSize = size
	}
	return err
}
func (d *observedImageDelivery) Probe(ctx context.Context, location mediadelivery.ImageLocation, identity mediadelivery.ImageDigest) error {
	key := digestValue(struct{ Ref, Digest, AccessContext string }{location.Ref, identity.Value, fmt.Sprintf("%p", d)})
	_, digest, err := observedProbe(ctx, key, location.Ref, func(ctx context.Context) (int64, string, error) {
		var digest string
		probeLocation := location
		probeLocation.ObservedDigest = &digest
		err := d.ImageDelivery.Probe(ctx, probeLocation, identity)
		return 0, digest, err
	})
	if location.ObservedDigest != nil {
		*location.ObservedDigest = digest
	}
	return err
}

// One wrapper instance is shared by Catalog and Delivery. Its identity isolates
// access configuration, even when two adapters probe the same source and digest.
type observedArtifactDelivery struct{ mediadelivery.ArtifactDelivery }
type observedImageDelivery struct{ mediadelivery.ImageDelivery }

func observeArtifactDelivery(adapter mediadelivery.ArtifactDelivery) mediadelivery.ArtifactDelivery {
	if _, ok := adapter.(*observedArtifactDelivery); ok {
		return adapter
	}
	return &observedArtifactDelivery{ArtifactDelivery: adapter}
}
func observeImageDelivery(adapter mediadelivery.ImageDelivery) mediadelivery.ImageDelivery {
	if _, ok := adapter.(*observedImageDelivery); ok {
		return adapter
	}
	return &observedImageDelivery{ImageDelivery: adapter}
}
