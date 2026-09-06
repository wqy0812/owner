package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
	"strings"
)

func (e *DeliveryService) verifyLockedMedia(ctx context.Context, plan lockedPlan) error {
	seen := map[string]bool{}
	for _, step := range plan.Steps {
		for _, media := range step.Media {
			key := digestValue(media)
			if seen[key] {
				continue
			}
			seen[key] = true
			var err error
			switch media.Kind {
			case "artifact":
				err = e.artifactDelivery.Probe(ctx, ArtifactLocation{URL: media.Location}, ArtifactIdentity{SHA256: strings.TrimPrefix(media.Identity, "sha256:"), SizeBytes: media.SizeBytes})
			case "image":
				err = e.imageDelivery.Probe(ctx, ImageLocation{Ref: media.Location}, ImageDigest{Value: media.Identity})
			default:
				return fmt.Errorf("unknown locked media kind: %s", media.Kind)
			}
			if err != nil {
				return fmt.Errorf("locked media unavailable %s: %w", media.Location, err)
			}
		}
	}
	return nil
}

func (e *DeliveryService) mirrorRunImages(ctx context.Context, runID string, plan *lockedPlan) error {
	for _, transfer := range plan.ImageTransfers {
		_, _ = e.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: fmt.Sprintf("mirroring image from %s to %s", transfer.SourceRegistry, transfer.TargetRegistry), CreatedAt: time.Now().UTC()})
		if err := e.imageDelivery.Probe(ctx, ImageLocation{Ref: transfer.TargetDigest}, ImageDigest{Value: transfer.TargetDigest}); err == nil {
			if resultErr := e.recordDeliveryResult(ctx, runID, plan, transfer.RequirementID, "reused_target", transfer.TargetDigest, "target content appeared while approval was pending"); resultErr != nil {
				return resultErr
			}
			continue
		}
		request := ImageTransfer{
			Source: ImageLocation{Ref: transfer.SourceDigest}, Target: ImageLocation{Ref: transfer.TargetRef},
			Digest: ImageDigest{Value: transfer.TargetDigest},
			Log: func(message string) {
				_, _ = e.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: Redact(message).(string), CreatedAt: time.Now().UTC()})
			},
		}
		if err := e.imageDelivery.Transfer(ctx, request); err != nil {
			_ = e.recordDeliveryResult(context.Background(), runID, plan, transfer.RequirementID, "failed", transfer.TargetDigest, err.Error())
			return err
		}
		if err := e.store.RecordComponentImageMirror(ctx, transfer.TargetRegistry, transfer.SourceDigest, transfer.TargetRef, transfer.TargetDigest, time.Now().UTC()); err != nil {
			return err
		}
		if err := e.recordDeliveryResult(ctx, runID, plan, transfer.RequirementID, "transferred", transfer.TargetDigest, "registry copy completed and digest verified"); err != nil {
			return err
		}
		_, _ = e.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: "image is ready at " + transfer.TargetDigest, CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (e *DeliveryService) mirrorRunArtifacts(ctx context.Context, runID string, plan *lockedPlan) error {
	for _, transfer := range plan.ArtifactTransfers {
		message := fmt.Sprintf("mirroring media %s from %s to %s", transfer.Alias, transfer.SourceURL, transfer.TargetStation)
		_, _ = e.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: message, CreatedAt: time.Now().UTC()})

		location := ArtifactLocation{FileStation: transfer.TargetStation, RelativePath: transfer.RelativePath}
		identity := ArtifactIdentity{SHA256: transfer.SHA256, SizeBytes: transfer.SizeBytes}
		err := e.artifactDelivery.Probe(ctx, location, identity)
		if err != nil && !errors.Is(err, ErrDeliveryTargetMissing) {
			_ = e.recordDeliveryResult(context.Background(), runID, plan, transfer.RequirementID, "failed", artifactURL(transfer.TargetStation, transfer.RelativePath), err.Error())
			return fmt.Errorf("verify target media %s: %w", transfer.Alias, err)
		}
		if errors.Is(err, ErrDeliveryTargetMissing) {
			request := ArtifactTransfer{Source: ArtifactLocation{URL: transfer.SourceURL}, Target: location, Identity: identity}
			if err := e.artifactDelivery.Transfer(ctx, request); err != nil {
				_ = e.recordDeliveryResult(context.Background(), runID, plan, transfer.RequirementID, "failed", artifactURL(transfer.TargetStation, transfer.RelativePath), err.Error())
				return fmt.Errorf("mirror media %s: %w", transfer.Alias, err)
			}
		}
		if err := e.store.RecordComponentArtifactMirror(ctx, transfer.SourceURL, transfer.TargetStation, transfer.RelativePath, transfer.SHA256, time.Now().UTC()); err != nil {
			return fmt.Errorf("record mirrored media %s: %w", transfer.Alias, err)
		}
		status, message := "transferred", "target FSS fetched and verified the content"
		if err == nil {
			status, message = "reused_target", "target content appeared while approval was pending"
		}
		if err := e.recordDeliveryResult(ctx, runID, plan, transfer.RequirementID, status, artifactURL(transfer.TargetStation, transfer.RelativePath), message); err != nil {
			return err
		}
		_, _ = e.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: fmt.Sprintf("media %s is ready on %s (sha256:%s)", transfer.Alias, transfer.TargetStation, transfer.SHA256), CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (e *DeliveryService) recordDeliveryResult(ctx context.Context, runID string, plan *lockedPlan, requirementID, status, location, message string) error {
	now := time.Now().UTC()
	for index := range plan.DeliveryResults {
		if plan.DeliveryResults[index].RequirementID == requirementID {
			plan.DeliveryResults[index].Status = status
			plan.DeliveryResults[index].ActualLocation = location
			plan.DeliveryResults[index].Message = message
			plan.DeliveryResults[index].CompletedAt = &now
			return e.store.UpdateRunDeliveryResults(ctx, runID, plan.DeliveryResults)
		}
	}
	return fmt.Errorf("%w: delivery result %s is missing from locked plan", domain.ErrConflict, requirementID)
}
