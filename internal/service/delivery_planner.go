package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	mediadelivery "codex/platform-demo/internal/delivery"
	"codex/platform-demo/internal/domain"
)

func (p *DeliveryService) finalizeDeliveryPlan(ctx context.Context, plan domain.RunExecutionPlan, at time.Time) (domain.RunExecutionPlan, error) {
	if len(plan.DeliveryRequirements) == 0 {
		return plan, nil
	}
	// Automatic target transfer is the platform policy. No Owner decision is needed.
	seen := make(map[string]bool, len(plan.DeliveryRequirements))
	for _, requirement := range plan.DeliveryRequirements {
		if strings.TrimSpace(requirement.ID) == "" || seen[requirement.ID] {
			return plan, fmt.Errorf("%w: delivery requirements must have unique nonempty ids", domain.ErrInvalid)
		}
		seen[requirement.ID] = true
	}
	plan.DeliveryDecisions = nil
	plan.DeliveryResults = nil
	plan.ArtifactTransfers = nil
	plan.ImageTransfers = nil
	for _, requirement := range plan.DeliveryRequirements {
		decision := domain.RunDeliveryDecision{RequirementID: requirement.ID, Mode: "transfer", Policy: domain.DeliveryPolicyTransferToTarget}
		plan.DeliveryDecisions = append(plan.DeliveryDecisions, decision)
		result := domain.RunDeliveryResult{RequirementID: requirement.ID, Mode: "transfer", Status: "pending"}
		location := requirement.Target
		if !requirement.TransferAvailable || requirement.Target == "" {
			return plan, fmt.Errorf("%w: delivery requirement %s has no environment target for transfer", domain.ErrInvalid, requirement.ID)
		}
		present, err := p.deliveryTargetPresent(ctx, requirement)
		if err != nil {
			return plan, err
		}
		if present {
			result.Status, result.ActualLocation, result.CompletedAt = "reused_target", location, &at
		} else if requirement.Kind == "artifact" {
			plan.ArtifactTransfers = append(plan.ArtifactTransfers, domain.RunArtifactTransfer{
				RequirementID: requirement.ID, Alias: requirement.Name, SourceURL: requirement.Source,
				TargetStation: requirement.TargetStation, RelativePath: requirement.RelativePath,
				SHA256: strings.TrimPrefix(requirement.Identity, "sha256:"), SizeBytes: requirement.SizeBytes,
			})
		} else {
			plan.ImageTransfers = append(plan.ImageTransfers, domain.RunImageTransfer{
				RequirementID: requirement.ID, SourceRegistry: requirement.SourceRegistry, TargetRegistry: requirement.TargetRegistry,
				SourceDigest: requirement.Source, TargetRef: requirement.TargetRef, TargetDigest: requirement.Target,
			})
		}
		if err := bindDeliveryRequirementVariables(&plan, requirement, location); err != nil {
			return plan, err
		}
		plan.DeliveryResults = append(plan.DeliveryResults, result)
	}
	refreshParentSteps(&plan)
	return plan, nil
}

func (p *DeliveryService) deliveryTargetPresent(ctx context.Context, requirement domain.RunDeliveryRequirement) (bool, error) {
	switch requirement.Kind {
	case "artifact":
		err := p.artifactDelivery.Probe(ctx, mediadelivery.ArtifactLocation{FileStation: requirement.TargetStation, RelativePath: requirement.RelativePath}, mediadelivery.ArtifactIdentity{SHA256: strings.TrimPrefix(requirement.Identity, "sha256:"), SizeBytes: requirement.SizeBytes})
		if err == nil {
			return true, nil
		}
		if errors.Is(err, mediadelivery.ErrDeliveryTargetMissing) {
			return false, nil
		}
		return false, fmt.Errorf("probe artifact target %s: %w", requirement.Target, err)
	case "image":
		if err := p.imageDelivery.Probe(ctx, mediadelivery.ImageLocation{Ref: requirement.Target}, mediadelivery.ImageDigest{Value: requirement.Identity}); err != nil {
			return false, nil
		}
		return true, nil
	default:
		return false, fmt.Errorf("%w: unknown delivery requirement kind %q", domain.ErrInvalid, requirement.Kind)
	}
}

func bindDeliveryRequirementVariables(plan *domain.RunExecutionPlan, requirement domain.RunDeliveryRequirement, location string) error {
	stepSet := make(map[string]bool, len(requirement.StepIDs))
	for _, id := range requirement.StepIDs {
		stepSet[id] = true
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if !stepSet[step.ID] {
			continue
		}
		if requirement.Kind == "artifact" {
			relativePath := requirement.RelativePath
			if location == requirement.Source {
				if parsed, err := url.Parse(location); err == nil {
					relativePath = strings.TrimPrefix(parsed.Path, "/")
				}
			}
			artifact := domain.ComponentArtifact{Alias: requirement.Name, SHA256: strings.TrimPrefix(requirement.Identity, "sha256:"), SizeBytes: requirement.SizeBytes}
			if err := bindArtifactVariables(step, artifact, relativePath, location); err != nil {
				return err
			}
		} else if err := bindImageVariables(step, requirement.Name, location, requirement.Identity); err != nil {
			return err
		}
	}
	return nil
}
