package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
)

type ArtifactLocation struct {
	URL          string
	FileStation  string
	RelativePath string
	ObservedSize *int64
}

type ArtifactIdentity struct {
	SHA256    string
	SizeBytes int64
}

type ArtifactTransfer struct {
	Source   ArtifactLocation
	Target   ArtifactLocation
	Identity ArtifactIdentity
}

type ImageLocation struct {
	Ref            string
	ObservedDigest *string
}
type ImageDigest struct{ Value string }
type ImageTransfer struct {
	Source ImageLocation
	Target ImageLocation
	Digest ImageDigest
	Log    func(string)
}

type ArtifactDelivery interface {
	Probe(context.Context, ArtifactLocation, ArtifactIdentity) error
	Transfer(context.Context, ArtifactTransfer) error
}

type ImageDelivery interface {
	Probe(context.Context, ImageLocation, ImageDigest) error
	Transfer(context.Context, ImageTransfer) error
}

var (
	ErrArtifactIdentityMismatch = errors.New("artifact source SHA-256 does not match content identity")
	ErrDeliveryTargetMissing    = errors.New("delivery target missing")
)

type HTTPArtifactDelivery struct{ client *http.Client }

func NewHTTPArtifactDelivery(client *http.Client) *HTTPArtifactDelivery {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPArtifactDelivery{client: client}
}

func (d *HTTPArtifactDelivery) probe(ctx context.Context, location ArtifactLocation, identity ArtifactIdentity) error {
	if location.URL != "" {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.URL, nil)
		if err != nil {
			return err
		}
		response, err := d.client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			return fmt.Errorf("artifact source returned %s", response.Status)
		}
		hash := sha256.New()
		size, err := io.Copy(hash, response.Body)
		if err != nil {
			return fmt.Errorf("read artifact source: %w", err)
		}
		if identity.SHA256 != "" && hex.EncodeToString(hash.Sum(nil)) != identity.SHA256 {
			return ErrArtifactIdentityMismatch
		}
		if location.ObservedSize != nil {
			*location.ObservedSize = size
		}
		return nil
	}
	if location.FileStation == "" || location.RelativePath == "" {
		return fmt.Errorf("artifact target location is incomplete")
	}
	payload, _ := json.Marshal(map[string]string{"path": location.RelativePath, "sha256": identity.SHA256})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+location.FileStation+"/api/v1/register", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return nil
	}
	if response.StatusCode == http.StatusNotFound {
		return ErrDeliveryTargetMissing
	}
	if response.StatusCode == http.StatusUnprocessableEntity {
		return ErrArtifactIdentityMismatch
	}
	message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	return fmt.Errorf("file station returned %s: %s", response.Status, strings.TrimSpace(string(message)))
}

func (d *HTTPArtifactDelivery) Transfer(ctx context.Context, transfer ArtifactTransfer) error {
	payload, _ := json.Marshal(map[string]string{"sourceUrl": transfer.Source.URL, "path": transfer.Target.RelativePath, "sha256": transfer.Identity.SHA256})
	targetURL := "http://" + transfer.Target.FileStation + "/api/v1/fetch"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return fmt.Errorf("target file station returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var metadata fssFileMetadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return err
	}
	if metadata.SHA256 != transfer.Identity.SHA256 || metadata.RelativePath != transfer.Target.RelativePath {
		return fmt.Errorf("target file station returned mismatched metadata")
	}
	return nil
}

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

func (d *DockerImageDelivery) probe(ctx context.Context, location ImageLocation, digest ImageDigest) error {
	ref := location.Ref
	if ref == "" {
		ref = digest.Value
	}
	output, err := exec.CommandContext(ctx, d.binary, "manifest", "inspect", "--insecure", "--verbose", ref).CombinedOutput()
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
		return "", fmt.Errorf("docker pull failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	output, err := exec.CommandContext(ctx, d.binary, "image", "inspect", "--format", "{{json .RepoDigests}}", ref).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker image inspect failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var repoDigests []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &repoDigests); err != nil {
		return "", fmt.Errorf("decode pulled image RepoDigests: %w", err)
	}
	repository := imageRepository(ref)
	for _, resolved := range repoDigests {
		digest, digestErr := digestFromResolvedImageRef(resolved)
		if digestErr == nil && strings.HasPrefix(resolved, repository+"@") {
			return digest, nil
		}
	}
	for _, resolved := range repoDigests {
		if digest, digestErr := digestFromResolvedImageRef(resolved); digestErr == nil {
			return digest, nil
		}
	}
	return "", fmt.Errorf("pulled image has no immutable RepoDigest")
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
