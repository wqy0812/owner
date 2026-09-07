package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	mediadelivery "codex/platform-demo/internal/delivery"
	"codex/platform-demo/internal/domain"
)

var imageLogicalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func normalizeImageLogicalName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !imageLogicalNamePattern.MatchString(value) {
		return "", fmt.Errorf("%w: image logicalName must start with a lowercase letter and contain only lowercase letters, digits or underscores", domain.ErrInvalid)
	}
	return value, nil
}

func normalizeImageSourceRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") || strings.ContainsAny(value, " ?#") || !strings.Contains(value, "/") {
		return "", fmt.Errorf("%w: image sourceRef must be an OCI registry reference without a URL scheme", domain.ErrInvalid)
	}
	return value, nil
}

func (p *CatalogService) RegisterImage(ctx context.Context, user domain.User, releaseID, logicalName, sourceRef, expectedDigest string) (domain.ComponentImage, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return domain.ComponentImage{}, err
	}
	if release.Status != domain.ReleaseDraft {
		return domain.ComponentImage{}, fmt.Errorf("%w: image content can only be changed on a draft release", domain.ErrConflict)
	}
	if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
		return domain.ComponentImage{}, activeErr
	} else if active {
		return domain.ComponentImage{}, fmt.Errorf("%w: wait for the active component test before changing image content", domain.ErrConflict)
	}
	logicalName, err = normalizeImageLogicalName(logicalName)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	sourceRef, err = normalizeImageSourceRef(sourceRef)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	observed := ""
	if err := p.imageDelivery.Probe(ctx, mediadelivery.ImageLocation{Ref: sourceRef, ObservedDigest: &observed}, mediadelivery.ImageDigest{}); err != nil {
		return domain.ComponentImage{}, fmt.Errorf("probe image source: %w", err)
	}
	digest, err := mediadelivery.DigestFromResolvedImageRef(observed)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	if strings.TrimSpace(expectedDigest) != "" {
		expectedDigest, err = mediadelivery.NormalizeOCIDigest(expectedDigest)
		if err != nil {
			return domain.ComponentImage{}, err
		}
		if expectedDigest != digest {
			return domain.ComponentImage{}, fmt.Errorf("%w: image source resolved to %s, expected %s", domain.ErrConflict, digest, expectedDigest)
		}
	}
	now := time.Now().UTC()
	image := domain.ComponentImage{ID: newID("image"), ReleaseID: releaseID, LogicalName: logicalName, Digest: digest, SourceRef: sourceRef, SourceUpdatedBy: user.ID, SourceUpdatedAt: now, CreatedBy: user.ID, CreatedAt: now}
	if err := p.store.UpsertDraftComponentImage(ctx, image); err != nil {
		return domain.ComponentImage{}, err
	}
	image, err = p.store.GetComponentImage(ctx, releaseID, logicalName)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	p.audit.Record(ctx, user, "component.image_saved", "component_release", releaseID, map[string]any{"logicalName": logicalName, "digest": digest, "sourceRef": sourceRef})
	return image, nil
}

func (p *CatalogService) UpdateImageSource(ctx context.Context, user domain.User, releaseID, logicalName, sourceRef string) (domain.ComponentImage, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return domain.ComponentImage{}, err
	}
	logicalName, err = normalizeImageLogicalName(logicalName)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	current, err := p.store.GetComponentImage(ctx, releaseID, logicalName)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	sourceRef, err = normalizeImageSourceRef(sourceRef)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	observed := ""
	if err := p.imageDelivery.Probe(ctx, mediadelivery.ImageLocation{Ref: sourceRef, ObservedDigest: &observed}, mediadelivery.ImageDigest{Value: current.Digest}); err != nil {
		return domain.ComponentImage{}, fmt.Errorf("probe image source: %w", err)
	}
	digest, err := mediadelivery.DigestFromResolvedImageRef(observed)
	if err != nil {
		return domain.ComponentImage{}, err
	}
	if digest != current.Digest {
		return domain.ComponentImage{}, fmt.Errorf("%w: source update resolved to %s but content identity is %s", domain.ErrConflict, digest, current.Digest)
	}
	updated, err := p.store.UpdateComponentImageSource(ctx, releaseID, logicalName, sourceRef, user.ID, time.Now().UTC())
	if err != nil {
		return domain.ComponentImage{}, err
	}
	p.audit.Record(ctx, user, "component.image_source_updated", "component_release", releaseID, map[string]any{"logicalName": logicalName, "digest": current.Digest, "sourceRef": sourceRef})
	return updated, nil
}

func (p *CatalogService) DeleteImage(ctx context.Context, user domain.User, releaseID, logicalName string) error {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return err
	}
	if release.Status != domain.ReleaseDraft {
		return fmt.Errorf("%w: image content can only be removed from a draft release", domain.ErrConflict)
	}
	logicalName, err = normalizeImageLogicalName(logicalName)
	if err != nil {
		return err
	}
	if err := p.store.DeleteDraftComponentImage(ctx, releaseID, logicalName); err != nil {
		return err
	}
	p.audit.Record(ctx, user, "component.image_deleted", "component_release", releaseID, map[string]any{"logicalName": logicalName})
	return nil
}
