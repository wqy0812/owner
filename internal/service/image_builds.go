package service

import (
	"codex/platform-demo/internal/imagebuild"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"codex/platform-demo/internal/domain"
)

const maxDockerfileBytes = 1 << 20

const imageRegistryVariable = "IMAGE_REGISTRY"

var imageTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func (p *CatalogService) StartImageBuild(ctx context.Context, user domain.User, releaseID, environmentID, tag string, dockerfile []byte) (domain.ComponentImageBuild, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return domain.ComponentImageBuild{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return domain.ComponentImageBuild{}, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return domain.ComponentImageBuild{}, err
	}
	if release.Status != domain.ReleaseDraft {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: images can only be built for a draft release", domain.ErrConflict)
	}
	if strings.TrimSpace(environmentID) == "" {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: image build requires an environment", domain.ErrInvalid)
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return domain.ComponentImageBuild{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return domain.ComponentImageBuild{}, err
	}
	if environment.Revision == nil {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: selected environment has no current revision", domain.ErrConflict)
	}
	registry, configured := environment.Revision.Variables[imageRegistryVariable]
	if !configured {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: selected environment does not define %s", domain.ErrConflict, imageRegistryVariable)
	}
	registry, err = normalizeImageRegistry(registry)
	if err != nil {
		return domain.ComponentImageBuild{}, err
	}
	tag = strings.ToLower(strings.TrimSpace(tag))
	if !imageTagPattern.MatchString(tag) {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: image tag must contain lowercase letters, digits, dots, underscores or hyphens", domain.ErrInvalid)
	}
	if len(dockerfile) == 0 || len(dockerfile) > maxDockerfileBytes || !utf8.Valid(dockerfile) {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: Dockerfile must be UTF-8 and no larger than 1 MiB", domain.ErrInvalid)
	}
	if !containsDockerfileFrom(dockerfile) {
		return domain.ComponentImageBuild{}, fmt.Errorf("%w: Dockerfile requires a FROM instruction", domain.ErrInvalid)
	}
	digest := sha256.Sum256(dockerfile)
	now := time.Now().UTC()
	build := domain.ComponentImageBuild{
		ID: newID("image-build"), ReleaseID: releaseID, RequestedBy: user.ID,
		EnvironmentID: environment.ID, EnvironmentRevisionID: environment.CurrentRevisionID,
		Status: domain.ImageBuildQueued, DockerfileSHA256: fmt.Sprintf("%x", digest[:]),
		ImageTag: tag, ImageRef: registry + "/components/" + component.Slug + ":" + tag, CreatedAt: now,
	}
	if err := p.store.CreateComponentImageBuild(ctx, build); err != nil {
		return domain.ComponentImageBuild{}, err
	}
	p.audit.Record(ctx, user, "component.image_build_requested", "component_release", releaseID, map[string]any{"buildId": build.ID, "environmentId": environment.ID, "environmentRevisionId": environment.CurrentRevisionID, "imageRef": build.ImageRef, "dockerfileSha256": build.DockerfileSHA256})
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": releaseID, "status": build.Status})
	contents := append([]byte(nil), dockerfile...)
	go p.executeComponentImageBuild(build, contents)
	return build, nil
}

func normalizeImageRegistry(value string) (string, error) {
	return domain.NormalizeImageRegistry(value)
}

func containsDockerfileFrom(dockerfile []byte) bool {
	for _, line := range strings.Split(string(dockerfile), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			return true
		}
	}
	return false
}

func (p *CatalogService) GetImageBuild(ctx context.Context, user domain.User, id string) (domain.ComponentImageBuild, error) {
	build, err := p.store.GetComponentImageBuild(ctx, id)
	if err != nil {
		return build, err
	}
	if err := p.requireImageBuildVisible(ctx, user, build); err != nil {
		return build, err
	}
	return build, nil
}

func (p *CatalogService) ListImageBuilds(ctx context.Context, user domain.User, releaseID string) ([]domain.ComponentImageBuild, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return nil, err
	}
	if user.Role == domain.RoleComponentOwner && user.ID != component.OwnerID && release.Status != domain.ReleaseReleased {
		return nil, domain.ErrForbidden
	}
	return p.store.ListComponentImageBuilds(ctx, releaseID, 20)
}

func (p *CatalogService) requireImageBuildVisible(ctx context.Context, user domain.User, build domain.ComponentImageBuild) error {
	release, err := p.store.GetComponentRelease(ctx, build.ReleaseID)
	if err != nil {
		return err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return err
	}
	if user.Role == domain.RoleComponentOwner && user.ID != component.OwnerID && release.Status != domain.ReleaseReleased {
		return domain.ErrForbidden
	}
	return nil
}

func (p *CatalogService) executeComponentImageBuild(build domain.ComponentImageBuild, dockerfile []byte) {
	ctx := p.rootCtx
	now := time.Now().UTC()
	if err := p.store.UpdateComponentImageBuildStatus(context.Background(), build.ID, []domain.ImageBuildStatus{domain.ImageBuildQueued}, domain.ImageBuildRunning, "", "", now); err != nil {
		return
	}
	p.emitImageBuildLog(build, "system", "starting isolated Dockerfile build")
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "status": domain.ImageBuildRunning})

	result, err := p.imageBuilder.Build(ctx, imagebuild.Request{ImageRef: build.ImageRef, Dockerfile: dockerfile}, func(event imagebuild.LogEvent) {
		p.emitImageBuildLog(build, event.Stream, event.Message)
	})
	if err != nil {
		p.failImageBuild(build, err)
		return
	}
	pushedDigest, digest := result.ResolvedRef, result.Digest
	finished := time.Now().UTC()
	image := domain.ComponentImage{
		ID: newID("image"), ReleaseID: build.ReleaseID, LogicalName: "main", Digest: digest,
		SourceRef: build.ImageRef, SourceUpdatedBy: build.RequestedBy, SourceUpdatedAt: finished,
		CreatedBy: build.RequestedBy, CreatedAt: finished,
	}
	if err := p.store.CompleteComponentImageBuild(context.Background(), build.ID, pushedDigest, image, finished); err != nil {
		p.failImageBuild(build, err)
		return
	}
	actor, _ := p.store.GetUser(context.Background(), build.RequestedBy)
	p.audit.Record(context.Background(), actor, "component.image_saved", "component_release", build.ReleaseID, map[string]any{"logicalName": image.LogicalName, "digest": image.Digest, "sourceRef": image.SourceRef, "buildId": build.ID})
	p.emitImageBuildLog(build, "system", "published "+pushedDigest)
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "status": domain.ImageBuildSucceeded, "imageDigest": pushedDigest})
}

func (p *CatalogService) emitImageBuildLog(build domain.ComponentImageBuild, stream, message string) {
	if len(message) > 16*1024 {
		message = message[:16*1024] + " [truncated]"
	}
	log := domain.ImageBuildLog{BuildID: build.ID, Stream: stream, Message: message, CreatedAt: time.Now().UTC()}
	_ = p.store.AppendComponentImageBuildLog(context.Background(), log)
	p.hub.Publish("image_build.log", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "stream": stream, "line": message})
}

func (p *CatalogService) failImageBuild(build domain.ComponentImageBuild, cause error) {
	message := Redact(cause.Error()).(string)
	status := domain.ImageBuildFailed
	if errors.Is(p.rootCtx.Err(), context.Canceled) {
		status = domain.ImageBuildInterrupted
	}
	_ = p.store.UpdateComponentImageBuildStatus(context.Background(), build.ID, []domain.ImageBuildStatus{domain.ImageBuildRunning, domain.ImageBuildQueued}, status, "", message, time.Now().UTC())
	p.emitImageBuildLog(build, "system", "image build failed: "+message)
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "status": status})
}
