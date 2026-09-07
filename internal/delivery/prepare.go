package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Prepare reuses verified targets, performs planned transfers, then verifies the
// selected media locations. Approval and execution timing belong to the caller.
func Prepare(ctx context.Context, plan Plan, artifacts ArtifactDelivery, images ImageDelivery) error {
	for _, transfer := range plan.ArtifactTransfers {
		target := ArtifactLocation{FileStation: transfer.TargetStation, RelativePath: transfer.RelativePath}
		identity := ArtifactIdentity{SHA256: transfer.SHA256, SizeBytes: transfer.SizeBytes}
		if err := artifacts.Probe(ctx, target, identity); err == nil {
			continue
		} else if !errors.Is(err, ErrDeliveryTargetMissing) {
			return err
		}
		if err := artifacts.Transfer(ctx, ArtifactTransfer{Source: ArtifactLocation{URL: transfer.SourceURL}, Target: target, Identity: identity}); err != nil {
			return err
		}
		if err := artifacts.Probe(ctx, target, identity); err != nil {
			return err
		}
	}
	for _, transfer := range plan.ImageTransfers {
		if err := images.Probe(ctx, ImageLocation{Ref: transfer.TargetDigest}, ImageDigest{Value: transfer.TargetDigest}); err == nil {
			continue
		} else if !errors.Is(err, ErrDeliveryTargetMissing) {
			return err
		}
		if err := images.Transfer(ctx, ImageTransfer{Source: ImageLocation{Ref: transfer.SourceDigest}, Target: ImageLocation{Ref: transfer.TargetRef}, Digest: ImageDigest{Value: transfer.TargetDigest}}); err != nil {
			return err
		}
		if err := images.Probe(ctx, ImageLocation{Ref: transfer.TargetDigest}, ImageDigest{Value: transfer.TargetDigest}); err != nil {
			return err
		}
	}
	for _, requirement := range plan.DeliveryRequirements {
		location := requirement.Source
		for _, choice := range plan.DeliveryDecisions {
			if choice.RequirementID == requirement.ID && choice.Mode == "transfer" {
				location = requirement.Target
			}
		}
		switch requirement.Kind {
		case "artifact":
			if err := artifacts.Probe(ctx, ArtifactLocation{URL: location}, ArtifactIdentity{SHA256: strings.TrimPrefix(requirement.Identity, "sha256:"), SizeBytes: requirement.SizeBytes}); err != nil {
				return fmt.Errorf("media %s: %w", requirement.Name, err)
			}
		case "image":
			if err := images.Probe(ctx, ImageLocation{Ref: location}, ImageDigest{Value: requirement.Identity}); err != nil {
				return err
			}
		}
	}
	return nil
}
