package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) finalizeDeliveryPlan(ctx context.Context, plan lockedPlan, inputs []DeliveryDecisionInput, user domain.User, at time.Time) (lockedPlan, error) {
	if len(plan.DeliveryRequirements) == 0 {
		if len(inputs) != 0 {
			return plan, fmt.Errorf("%w: run has no delivery requirements", domain.ErrInvalid)
		}
		return plan, nil
	}
	// A safe retry carries the original immutable delivery requirements and
	// approved per-item choices. The new approval confirms that locked choice;
	// the executor still probes the target again before any transfer.
	if len(inputs) == 0 && len(plan.DeliveryDecisions) == len(plan.DeliveryRequirements) {
		inputs = make([]DeliveryDecisionInput, 0, len(plan.DeliveryDecisions))
		for _, decision := range plan.DeliveryDecisions {
			inputs = append(inputs, DeliveryDecisionInput{RequirementID: decision.RequirementID, Mode: decision.Mode})
		}
	}
	byID := make(map[string]DeliveryDecisionInput, len(inputs))
	for _, input := range inputs {
		input.RequirementID = strings.TrimSpace(input.RequirementID)
		input.Mode = strings.TrimSpace(input.Mode)
		if input.RequirementID == "" || (input.Mode != "direct" && input.Mode != "transfer") || byID[input.RequirementID].RequirementID != "" {
			return plan, fmt.Errorf("%w: delivery decisions must contain unique requirement ids and direct/transfer modes", domain.ErrInvalid)
		}
		byID[input.RequirementID] = input
	}
	if len(byID) != len(plan.DeliveryRequirements) {
		return plan, fmt.Errorf("%w: every delivery requirement must be decided explicitly", domain.ErrInvalid)
	}
	plan.DeliveryDecisions = nil
	plan.DeliveryResults = nil
	plan.ArtifactTransfers = nil
	plan.ImageTransfers = nil
	for _, requirement := range plan.DeliveryRequirements {
		input, ok := byID[requirement.ID]
		if !ok {
			return plan, fmt.Errorf("%w: delivery requirement %s has no decision", domain.ErrInvalid, requirement.ID)
		}
		decision := DeliveryDecision{RequirementID: requirement.ID, Mode: input.Mode, DecidedBy: user.ID, DecidedAt: at}
		plan.DeliveryDecisions = append(plan.DeliveryDecisions, decision)
		result := DeliveryResult{RequirementID: requirement.ID, Mode: input.Mode, Status: "pending"}
		location := requirement.Source
		if input.Mode == "transfer" {
			if !requirement.TransferAvailable || requirement.Target == "" {
				return plan, fmt.Errorf("%w: delivery requirement %s has no environment target for transfer", domain.ErrInvalid, requirement.ID)
			}
			present, err := p.deliveryTargetPresent(ctx, requirement)
			if err != nil {
				return plan, err
			}
			location = requirement.Target
			if present {
				result.Status, result.ActualLocation, result.CompletedAt = "reused_target", location, &at
			} else if requirement.Kind == "artifact" {
				plan.ArtifactTransfers = append(plan.ArtifactTransfers, lockedArtifactTransfer{
					RequirementID: requirement.ID, Alias: requirement.Name, SourceURL: requirement.Source,
					TargetStation: requirement.TargetStation, RelativePath: requirement.RelativePath,
					SHA256: strings.TrimPrefix(requirement.Identity, "sha256:"), SizeBytes: requirement.SizeBytes,
				})
			} else {
				plan.ImageTransfers = append(plan.ImageTransfers, lockedImageTransfer{
					RequirementID: requirement.ID, SourceRegistry: requirement.SourceRegistry, TargetRegistry: requirement.TargetRegistry,
					SourceDigest: requirement.Source, TargetRef: requirement.TargetRef, TargetDigest: requirement.Target,
				})
			}
		} else {
			result.Status, result.ActualLocation, result.CompletedAt = "direct", location, &at
		}
		if err := bindDeliveryRequirementVariables(&plan, requirement, location); err != nil {
			return plan, err
		}
		plan.DeliveryResults = append(plan.DeliveryResults, result)
	}
	return plan, nil
}

func (p *Platform) deliveryTargetPresent(ctx context.Context, requirement DeliveryRequirement) (bool, error) {
	switch requirement.Kind {
	case "artifact":
		err := p.artifactDelivery.Probe(ctx, ArtifactLocation{FileStation: requirement.TargetStation, RelativePath: requirement.RelativePath}, ArtifactIdentity{SHA256: strings.TrimPrefix(requirement.Identity, "sha256:"), SizeBytes: requirement.SizeBytes})
		if err == nil {
			return true, nil
		}
		if errors.Is(err, ErrDeliveryTargetMissing) {
			return false, nil
		}
		return false, fmt.Errorf("probe artifact target %s: %w", requirement.Target, err)
	case "image":
		if err := p.imageDelivery.Probe(ctx, ImageLocation{Ref: requirement.Target}, ImageDigest{Value: requirement.Identity}); err != nil {
			return false, nil
		}
		return true, nil
	default:
		return false, fmt.Errorf("%w: unknown delivery requirement kind %q", domain.ErrInvalid, requirement.Kind)
	}
}

func bindDeliveryRequirementVariables(plan *lockedPlan, requirement DeliveryRequirement, location string) error {
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
			artifact := domain.ComponentArtifact{Alias: requirement.Name, SHA256: strings.TrimPrefix(requirement.Identity, "sha256:")}
			if err := bindArtifactVariables(step, artifact, relativePath, location); err != nil {
				return err
			}
		} else if err := bindImageVariables(step, requirement.Name, location); err != nil {
			return err
		}
	}
	return nil
}
