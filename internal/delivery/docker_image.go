package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type DockerImageDelivery struct{ binary string }

var _ ArtifactDelivery = (*HTTPArtifactDelivery)(nil)
var _ ImageDelivery = (*DockerImageDelivery)(nil)

func NewDockerImageDelivery(binary string) *DockerImageDelivery {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = "docker"
	}
	return &DockerImageDelivery{binary: binary}
}

func (d *DockerImageDelivery) Probe(ctx context.Context, location ImageLocation, digest ImageDigest) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ref := location.Ref
	if ref == "" {
		ref = digest.Value
	}
	output, err := exec.CommandContext(ctx, d.binary, "manifest", "inspect", "--insecure", "--verbose", ref).CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var manifest struct {
		Descriptor struct {
			Digest string `json:"digest"`
		} `json:"Descriptor"`
	}
	observedDigest := ""
	if err == nil {
		if decodeErr := json.Unmarshal(output, &manifest); decodeErr == nil && strings.HasPrefix(manifest.Descriptor.Digest, "sha256:") {
			observedDigest = manifest.Descriptor.Digest
		}
	}
	if observedDigest == "" {
		observedDigest, err = d.probeByPull(ctx, ref)
		if err != nil {
			manifestError := strings.TrimSpace(string(output))
			if manifestError == "" {
				manifestError = "invalid remote response"
			}
			return fmt.Errorf("docker manifest inspect failed (%s); OCI pull probe failed: %w", manifestError, err)
		}
	}
	if expected := imageDigestValue(digest.Value); expected != "" && observedDigest != expected {
		return fmt.Errorf("registry returned digest %s, expected %s", observedDigest, expected)
	}
	if location.ObservedDigest != nil {
		*location.ObservedDigest = imageRepository(ref) + "@" + observedDigest
	}
	return nil
}

func (d *DockerImageDelivery) probeByPull(ctx context.Context, ref string) (string, error) {
	if output, err := exec.CommandContext(ctx, d.binary, "pull", ref).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() > 0 && dockerPullTargetMissing(output, ref) {
			return "", fmt.Errorf("%w: docker pull failed: %w: %s", ErrDeliveryTargetMissing, err, strings.TrimSpace(string(output)))
		}
		return "", fmt.Errorf("docker pull failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	output, err := exec.CommandContext(ctx, d.binary, "image", "inspect", "--format", "{{json .RepoDigests}}", ref).CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("docker image inspect failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var repoDigests []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &repoDigests); err != nil {
		return "", fmt.Errorf("decode pulled image RepoDigests: %w", err)
	}
	repository := imageRepository(ref)
	for _, resolved := range repoDigests {
		digest, digestErr := DigestFromResolvedImageRef(resolved)
		if digestErr == nil && strings.HasPrefix(resolved, repository+"@") {
			return digest, nil
		}
	}
	for _, resolved := range repoDigests {
		if digest, digestErr := DigestFromResolvedImageRef(resolved); digestErr == nil {
			return digest, nil
		}
	}
	return "", fmt.Errorf("pulled image has no immutable RepoDigest")
}

// Classify only the terminal pull error, after the manifest probe has fallen
// back to the daemon. Earlier output and a generic "not found" can describe
// credentials, network endpoints or blobs rather than a missing image.
func dockerPullTargetMissing(output []byte, ref string) bool {
	message := strings.TrimSpace(string(output))
	if index := strings.LastIndexByte(message, '\n'); index >= 0 {
		message = strings.TrimSpace(message[index+1:])
	}
	message = strings.TrimPrefix(message, "Error response from daemon: ")
	message = strings.TrimPrefix(message, "manifest for "+ref+" not found: ")
	for _, code := range []string{"manifest unknown", "name unknown"} {
		if message == code || strings.HasPrefix(message, code+": ") {
			return true
		}
	}
	return message == "no such manifest: "+ref ||
		message == fmt.Sprintf("failed to resolve reference %q: %s: not found", ref, ref)
}

func imageDigestValue(value string) string {
	if index := strings.LastIndex(value, "@sha256:"); index >= 0 {
		return value[index+1:]
	}
	if strings.HasPrefix(value, "sha256:") {
		return value
	}
	return ""
}

func imageRepository(ref string) string {
	if index := strings.LastIndex(ref, "@"); index >= 0 {
		return ref[:index]
	}
	lastSlash := strings.LastIndex(ref, "/")
	if index := strings.LastIndex(ref, ":"); index > lastSlash {
		return ref[:index]
	}
	return ref
}

func (d *DockerImageDelivery) Transfer(ctx context.Context, transfer ImageTransfer) error {
	for _, args := range [][]string{{"pull", transfer.Source.Ref}, {"tag", transfer.Source.Ref, transfer.Target.Ref}, {"push", transfer.Target.Ref}, {"pull", transfer.Digest.Value}} {
		if err := d.run(ctx, transfer.Log, args...); err != nil {
			return err
		}
	}
	return nil
}

func (d *DockerImageDelivery) run(ctx context.Context, logOutput func(string), args ...string) error {
	output, err := exec.CommandContext(ctx, d.binary, args...).CombinedOutput()
	message := strings.TrimSpace(string(output))
	if len(message) > 16*1024 {
		message = message[len(message)-(16*1024):]
	}
	if message != "" && logOutput != nil {
		logOutput(message)
	}
	if err != nil {
		return fmt.Errorf("docker %s failed: %w", args[0], err)
	}
	return nil
}
