package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codex/platform-demo/internal/domain"
)

const maxDockerfileBytes = 1 << 20

const imageRegistryVariable = "IMAGE_REGISTRY"

var imageTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
var imageRegistryPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

func (p *Platform) StartComponentImageBuild(ctx context.Context, user domain.User, releaseID, environmentID, tag string, dockerfile []byte) (domain.ComponentImageBuild, error) {
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
	p.audit(ctx, user, "component.image_build_requested", "component_release", releaseID, map[string]any{"buildId": build.ID, "environmentId": environment.ID, "environmentRevisionId": environment.CurrentRevisionID, "imageRef": build.ImageRef, "dockerfileSha256": build.DockerfileSHA256})
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": releaseID, "status": build.Status})
	contents := append([]byte(nil), dockerfile...)
	go p.executeComponentImageBuild(build, contents)
	return build, nil
}

func normalizeImageRegistry(value string) (string, error) {
	registry := strings.TrimSuffix(strings.TrimSpace(value), "/")
	if registry == "" || strings.Contains(registry, "://") || strings.ContainsAny(registry, "@?#") || !imageRegistryPattern.MatchString(registry) {
		return "", fmt.Errorf("%w: %s must be a Docker registry prefix without a URL scheme, tag or digest", domain.ErrInvalid, imageRegistryVariable)
	}
	host := registry
	if slash := strings.IndexByte(host, '/'); slash >= 0 {
		host = host[:slash]
	}
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		port, err := strconv.Atoi(host[colon+1:])
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("%w: %s contains an invalid registry port", domain.ErrInvalid, imageRegistryVariable)
		}
	}
	return registry, nil
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

func (p *Platform) GetComponentImageBuild(ctx context.Context, user domain.User, id string) (domain.ComponentImageBuild, error) {
	build, err := p.store.GetComponentImageBuild(ctx, id)
	if err != nil {
		return build, err
	}
	if err := p.requireImageBuildVisible(ctx, user, build); err != nil {
		return build, err
	}
	return build, nil
}

func (p *Platform) ListComponentImageBuilds(ctx context.Context, user domain.User, releaseID string) ([]domain.ComponentImageBuild, error) {
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

func (p *Platform) requireImageBuildVisible(ctx context.Context, user domain.User, build domain.ComponentImageBuild) error {
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

func (p *Platform) executeComponentImageBuild(build domain.ComponentImageBuild, dockerfile []byte) {
	ctx := p.rootCtx
	now := time.Now().UTC()
	if err := p.store.UpdateComponentImageBuildStatus(context.Background(), build.ID, []domain.ImageBuildStatus{domain.ImageBuildQueued}, domain.ImageBuildRunning, "", "", now); err != nil {
		return
	}
	p.emitImageBuildLog(build, "system", "starting isolated Dockerfile build")
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "status": domain.ImageBuildRunning})

	root := p.imageBuildRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "newplatform-image-builds")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		p.failImageBuild(build, err)
		return
	}
	workdir, err := os.MkdirTemp(root, "image-build-")
	if err != nil {
		p.failImageBuild(build, err)
		return
	}
	defer os.RemoveAll(workdir)
	if err := os.Chmod(workdir, 0o700); err != nil {
		p.failImageBuild(build, err)
		return
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), dockerfile, 0o600); err != nil {
		p.failImageBuild(build, err)
		return
	}

	docker := p.dockerBinary
	if docker == "" {
		docker = "docker"
	}
	steps := [][]string{
		{"build", "--progress=plain", "--tag", build.ImageRef, "--file", "Dockerfile", "."},
		{"push", build.ImageRef},
	}
	for _, args := range steps {
		p.emitImageBuildLog(build, "system", "docker "+args[0]+" started")
		if err := p.runImageBuildCommand(ctx, workdir, docker, args, build); err != nil {
			p.failImageBuild(build, err)
			return
		}
	}

	output, err := exec.CommandContext(ctx, docker, "image", "inspect", "--format={{index .RepoDigests 0}}", build.ImageRef).Output()
	if err != nil {
		p.failImageBuild(build, fmt.Errorf("inspect pushed image digest: %w", err))
		return
	}
	pushedDigest := strings.TrimSpace(string(output))
	if !strings.Contains(pushedDigest, "@sha256:") {
		p.failImageBuild(build, errors.New("registry did not return an immutable image digest"))
		return
	}
	digest, err := digestFromResolvedImageRef(pushedDigest)
	if err != nil {
		p.failImageBuild(build, err)
		return
	}
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
	p.audit(context.Background(), actor, "component.image_saved", "component_release", build.ReleaseID, map[string]any{"logicalName": image.LogicalName, "digest": image.Digest, "sourceRef": image.SourceRef, "buildId": build.ID})
	p.emitImageBuildLog(build, "system", "published "+pushedDigest)
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "status": domain.ImageBuildSucceeded, "imageDigest": pushedDigest})
}

func (p *Platform) runImageBuildCommand(ctx context.Context, workdir, binary string, args []string, build domain.ComponentImageBuild) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workdir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var readers sync.WaitGroup
	readers.Add(2)
	go p.captureImageBuildOutput(stdout, "stdout", build, &readers)
	go p.captureImageBuildOutput(stderr, "stderr", build, &readers)
	waitErr := cmd.Wait()
	readers.Wait()
	return waitErr
}

func (p *Platform) captureImageBuildOutput(reader io.Reader, stream string, build domain.ComponentImageBuild, wait *sync.WaitGroup) {
	defer wait.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		p.emitImageBuildLog(build, stream, scanner.Text())
	}
}

func (p *Platform) emitImageBuildLog(build domain.ComponentImageBuild, stream, message string) {
	if len(message) > 16*1024 {
		message = message[:16*1024] + " [truncated]"
	}
	log := domain.ImageBuildLog{BuildID: build.ID, Stream: stream, Message: message, CreatedAt: time.Now().UTC()}
	_ = p.store.AppendComponentImageBuildLog(context.Background(), log)
	p.hub.Publish("image_build.log", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "stream": stream, "line": message})
}

func (p *Platform) failImageBuild(build domain.ComponentImageBuild, cause error) {
	message := Redact(cause.Error()).(string)
	status := domain.ImageBuildFailed
	if errors.Is(p.rootCtx.Err(), context.Canceled) {
		status = domain.ImageBuildInterrupted
	}
	_ = p.store.UpdateComponentImageBuildStatus(context.Background(), build.ID, []domain.ImageBuildStatus{domain.ImageBuildRunning, domain.ImageBuildQueued}, status, "", message, time.Now().UTC())
	p.emitImageBuildLog(build, "system", "image build failed: "+message)
	p.hub.Publish("image_build.updated", map[string]any{"buildId": build.ID, "releaseId": build.ReleaseID, "status": status})
}
