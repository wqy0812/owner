package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"codex/platform-demo/internal/domain"
)

func (p *Platform) bindComponentArtifacts(ctx context.Context, revision domain.EnvironmentRevision, plan *lockedPlan) error {
	targetStation := strings.TrimSpace(revision.Variables[fileStationVariable])
	if targetStation != "" {
		var err error
		targetStation, err = normalizeFileStation(targetStation)
		if err != nil {
			return err
		}
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		release, err := p.store.GetComponentRelease(ctx, step.ReleaseID)
		if err != nil {
			return err
		}
		if len(release.Artifacts) == 0 {
			continue
		}
		component, err := p.store.GetComponent(ctx, release.ComponentID, false)
		if err != nil {
			return err
		}
		for _, artifact := range release.Artifacts {
			relativePath := path.Join("components", artifactSegment(component.Slug), artifactSegment(release.Version), artifactSegment(artifact.Filename))
			targetURL := ""
			targetPresent := false
			if targetStation != "" {
				targetURL = artifactURL(targetStation, relativePath)
				targetErr := p.artifactDelivery.Probe(ctx, ArtifactLocation{FileStation: targetStation, RelativePath: relativePath}, ArtifactIdentity{SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes})
				if targetErr == nil {
					targetPresent = true
				} else if !errors.Is(targetErr, ErrDeliveryTargetMissing) {
					return fmt.Errorf("probe artifact target %s: %w", targetURL, targetErr)
				}
			}
			if targetPresent {
				if err := bindArtifactVariables(step, artifact, relativePath, targetURL); err != nil {
					return err
				}
				continue
			}
			if err := p.artifactDelivery.Probe(ctx, ArtifactLocation{URL: artifact.SourceURL}, ArtifactIdentity{SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes}); err != nil {
				return deliverySourceError(component, release, "介质 "+artifact.Alias, artifact.SourceURL, "/components?selected="+component.ID, err)
			}
			appendDeliveryRequirement(plan, DeliveryRequirement{
				ID: "artifact:" + release.ID + ":" + artifact.Alias, Kind: "artifact", Name: artifact.Alias,
				Identity: "sha256:" + artifact.SHA256, Source: artifact.SourceURL, Target: targetURL,
				SourceReadable: true, TargetPresent: false, TransferAvailable: targetStation != "",
				ReleaseID: release.ID, ComponentID: component.ID, ComponentName: component.Name, ComponentOwnerID: component.OwnerID,
				StepIDs: []string{step.ID}, SizeBytes: artifact.SizeBytes, TargetStation: targetStation, RelativePath: relativePath,
			})
		}
	}
	return nil
}

func artifactURL(station, relativePath string) string {
	return (&url.URL{Scheme: "http", Host: station, Path: "/" + strings.TrimPrefix(relativePath, "/")}).String()
}

func (p *Platform) bindComponentImages(ctx context.Context, revision domain.EnvironmentRevision, plan *lockedPlan) error {
	targetRegistry := strings.TrimSpace(revision.Variables[imageRegistryVariable])
	if targetRegistry != "" {
		var err error
		targetRegistry, err = normalizeImageRegistry(targetRegistry)
		if err != nil {
			return err
		}
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		release, err := p.store.GetComponentRelease(ctx, step.ReleaseID)
		if err != nil {
			return err
		}
		if len(release.Images) == 0 {
			continue
		}
		component, err := p.store.GetComponent(ctx, release.ComponentID, false)
		if err != nil {
			return err
		}
		for _, image := range release.Images {
			sourceRegistry := strings.SplitN(image.SourceRef, "/", 2)[0]
			sourceDigest := immutableImageSourceRef(image.SourceRef, image.Digest)
			targetRef, targetDigest := "", ""
			targetPresent := false
			if targetRegistry != "" {
				targetRepository := targetRegistry + "/components/" + artifactSegment(component.Slug) + "/" + artifactSegment(image.LogicalName)
				targetRef = targetRepository + ":sha256-" + strings.TrimPrefix(image.Digest, "sha256:")[:12]
				targetDigest = targetRepository + "@" + image.Digest
				targetPresent = p.imageDelivery.Probe(ctx, ImageLocation{Ref: targetDigest}, ImageDigest{Value: image.Digest}) == nil
			}
			if targetPresent {
				if err := bindImageVariables(step, image.LogicalName, targetDigest, image.Digest); err != nil {
					return err
				}
				continue
			}
			observed := ""
			if err := p.imageDelivery.Probe(ctx, ImageLocation{Ref: image.SourceRef, ObservedDigest: &observed}, ImageDigest{Value: image.Digest}); err != nil {
				return deliverySourceError(component, release, "镜像 "+image.LogicalName, image.SourceRef, "/components?selected="+component.ID, err)
			}
			if observedDigest, err := digestFromResolvedImageRef(observed); err != nil || observedDigest != image.Digest {
				if err == nil {
					err = fmt.Errorf("resolved digest is %s, expected %s", observedDigest, image.Digest)
				}
				return deliverySourceError(component, release, "镜像 "+image.LogicalName, image.SourceRef, "/components?selected="+component.ID, err)
			}
			appendDeliveryRequirement(plan, DeliveryRequirement{
				ID: "image:" + release.ID + ":" + image.LogicalName, Kind: "image", Name: image.LogicalName,
				Identity: image.Digest, Source: sourceDigest, Target: targetDigest,
				SourceReadable: true, TargetPresent: false, TransferAvailable: targetRegistry != "",
				ReleaseID: release.ID, ComponentID: component.ID, ComponentName: component.Name, ComponentOwnerID: component.OwnerID,
				StepIDs: []string{step.ID}, SourceRegistry: sourceRegistry, TargetRegistry: targetRegistry, TargetRef: targetRef,
			})
		}
	}
	return nil
}

func bindArtifactVariables(step *lockedStep, artifact domain.ComponentArtifact, relativePath, location string) error {
	for suffix, value := range map[string]any{"_path": relativePath, "_url": location, "_sha256": artifact.SHA256} {
		name := artifact.Alias + suffix
		if _, exists := step.Variables[name]; exists {
			return fmt.Errorf("%w: generated artifact variable %q conflicts with a component parameter", domain.ErrInvalid, name)
		}
		step.Variables[name] = value
	}
	return nil
}

func bindImageVariables(step *lockedStep, logicalName, location, digest string) error {
	variables := map[string]any{logicalName + "_image_ref": location, logicalName + "_image_digest": digest}
	if logicalName == "main" {
		variables["component_image_ref"], variables["component_image_digest"] = location, digest
	}
	for name, value := range variables {
		if _, exists := step.Variables[name]; exists {
			return fmt.Errorf("%w: generated image variable %q conflicts with a component parameter", domain.ErrInvalid, name)
		}
		step.Variables[name] = value
	}
	return nil
}

func appendDeliveryRequirement(plan *lockedPlan, requirement DeliveryRequirement) {
	for index := range plan.DeliveryRequirements {
		if plan.DeliveryRequirements[index].ID == requirement.ID {
			plan.DeliveryRequirements[index].StepIDs = append(plan.DeliveryRequirements[index].StepIDs, requirement.StepIDs...)
			return
		}
	}
	plan.DeliveryRequirements = append(plan.DeliveryRequirements, requirement)
}

func deliverySourceError(component domain.Component, release domain.ComponentRelease, item, source, actionURL string, cause error) error {
	base := fmt.Errorf("%w: %s %s %s 的当前来源 %s 不可读（Component Owner: %s）: %v", domain.ErrConflict, component.Name, release.Version, item, source, component.OwnerID, cause)
	return actionableExistingError(base, "delivery.source_unreadable", "内容来源不可读；请由 Component Owner 修复同一内容身份的来源地址", "更新内容来源", actionURL)
}
