package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

const fileStationVariable = "FILE_STATION"

var artifactAliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type fssFileMetadata struct {
	RelativePath string `json:"relativePath"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"sizeBytes"`
}

func normalizeFileStation(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "/"))
	if value == "" || strings.Contains(value, "://") || strings.ContainsAny(value, "/@?#") {
		return "", fmt.Errorf("%w: %s must be a host:port without scheme or path", domain.ErrInvalid, fileStationVariable)
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("%w: %s must be a host:port without scheme or path", domain.ErrInvalid, fileStationVariable)
	}
	return net.JoinHostPort(host, port), nil
}

func normalizeArtifactAlias(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !artifactAliasPattern.MatchString(value) {
		return "", fmt.Errorf("%w: artifact alias must use lowercase letters, digits and underscores", domain.ErrInvalid)
	}
	return value, nil
}

func normalizeSHA256(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("%w: SHA-256 must contain 64 hexadecimal characters", domain.ErrInvalid)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("%w: SHA-256 must contain 64 hexadecimal characters", domain.ErrInvalid)
	}
	return value, nil
}

func parseSHA256File(contents []byte) (string, error) {
	fields := strings.Fields(string(contents))
	if len(fields) == 0 {
		return "", fmt.Errorf("%w: checksum file is empty", domain.ErrInvalid)
	}
	return normalizeSHA256(fields[0])
}

func artifactSegment(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._-", char) {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	clean := strings.Trim(builder.String(), ".-")
	if clean == "" {
		return "artifact"
	}
	return clean
}

func (p *Platform) artifactContext(ctx context.Context, user domain.User, releaseID, environmentID, alias string) (domain.ComponentRelease, domain.Component, domain.Environment, string, string, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return release, domain.Component{}, domain.Environment{}, "", "", err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, component, domain.Environment{}, "", "", err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, component, domain.Environment{}, "", "", err
	}
	if release.Status != domain.ReleaseDraft {
		return release, component, domain.Environment{}, "", "", fmt.Errorf("%w: artifacts can only be changed on a draft release", domain.ErrConflict)
	}
	if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
		return release, component, domain.Environment{}, "", "", activeErr
	} else if active {
		return release, component, domain.Environment{}, "", "", fmt.Errorf("%w: wait for the active component test before changing artifacts", domain.ErrConflict)
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return release, component, environment, "", "", err
	}
	if environment.Revision == nil {
		return release, component, environment, "", "", fmt.Errorf("%w: selected environment has no current revision", domain.ErrConflict)
	}
	station, ok := environment.Revision.Variables[fileStationVariable]
	if !ok {
		return release, component, environment, "", "", fmt.Errorf("%w: selected environment does not define %s", domain.ErrConflict, fileStationVariable)
	}
	station, err = normalizeFileStation(station)
	if err != nil {
		return release, component, environment, "", "", err
	}
	alias, err = normalizeArtifactAlias(alias)
	if err != nil {
		return release, component, environment, "", "", err
	}
	for _, artifact := range release.Artifacts {
		if artifact.FileStation != station {
			return release, component, environment, "", "", fmt.Errorf("%w: release artifacts are locked to %s; remove them before choosing another file station", domain.ErrConflict, artifact.FileStation)
		}
	}
	return release, component, environment, station, alias, nil
}

func (p *Platform) UploadComponentArtifact(ctx context.Context, user domain.User, releaseID, environmentID, alias, filename, expectedSHA string, input io.Reader) (domain.ComponentArtifact, error) {
	release, component, environment, station, alias, err := p.artifactContext(ctx, user, releaseID, environmentID, alias)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	expectedSHA, err = normalizeSHA256(expectedSHA)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	filename = path.Base(strings.TrimSpace(filename))
	if filename == "." || filename == "/" || filename == "" {
		return domain.ComponentArtifact{}, fmt.Errorf("%w: artifact filename is required", domain.ErrInvalid)
	}
	relativePath := path.Join("components", artifactSegment(component.Slug), artifactSegment(release.Version), artifactSegment(filename))
	endpoint := "http://" + station + "/api/v1/files?path=" + url.QueryEscape(relativePath) + "&sha256=" + expectedSHA
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, input)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return domain.ComponentArtifact{}, fmt.Errorf("publish artifact to file station: %w", err)
	}
	defer response.Body.Close()
	var metadata fssFileMetadata
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return domain.ComponentArtifact{}, fmt.Errorf("%w: file station upload failed (%s): %s", domain.ErrConflict, response.Status, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return domain.ComponentArtifact{}, fmt.Errorf("decode file station response: %w", err)
	}
	return p.saveComponentArtifact(ctx, user, release, environment, station, alias, "upload", metadata)
}

func (p *Platform) RegisterComponentArtifact(ctx context.Context, user domain.User, releaseID, environmentID, alias, relativePath, expectedSHA string) (domain.ComponentArtifact, error) {
	release, _, environment, station, alias, err := p.artifactContext(ctx, user, releaseID, environmentID, alias)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	expectedSHA, err = normalizeSHA256(expectedSHA)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	payload, _ := json.Marshal(map[string]string{"path": strings.TrimSpace(relativePath), "sha256": expectedSHA})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+station+"/api/v1/register", bytes.NewReader(payload))
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return domain.ComponentArtifact{}, fmt.Errorf("verify artifact on file station: %w", err)
	}
	defer response.Body.Close()
	var metadata fssFileMetadata
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return domain.ComponentArtifact{}, fmt.Errorf("%w: file station registration failed (%s): %s", domain.ErrConflict, response.Status, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return domain.ComponentArtifact{}, fmt.Errorf("decode file station response: %w", err)
	}
	return p.saveComponentArtifact(ctx, user, release, environment, station, alias, "register", metadata)
}

func (p *Platform) saveComponentArtifact(ctx context.Context, user domain.User, release domain.ComponentRelease, environment domain.Environment, station, alias, mode string, metadata fssFileMetadata) (domain.ComponentArtifact, error) {
	if metadata.SHA256 == "" || metadata.SHA256 != strings.ToLower(metadata.SHA256) {
		return domain.ComponentArtifact{}, fmt.Errorf("%w: file station returned invalid artifact metadata", domain.ErrConflict)
	}
	artifact := domain.ComponentArtifact{
		ID: newID("artifact"), ReleaseID: release.ID, Alias: alias, FileStation: station,
		RelativePath: metadata.RelativePath, Filename: metadata.Filename, SHA256: metadata.SHA256,
		SizeBytes: metadata.SizeBytes, SourceMode: mode, EnvironmentID: environment.ID,
		EnvironmentRevisionID: environment.CurrentRevisionID, CreatedBy: user.ID, CreatedAt: time.Now().UTC(),
	}
	if err := p.store.UpsertComponentArtifact(ctx, artifact); err != nil {
		return domain.ComponentArtifact{}, err
	}
	_ = p.store.MarkReleaseVerified(ctx, release.ID, false)
	p.audit(ctx, user, "component.artifact_saved", "component_release", release.ID, map[string]any{"alias": alias, "fileStation": station, "path": metadata.RelativePath, "sha256": metadata.SHA256, "sourceMode": mode})
	return artifact, nil
}

func (p *Platform) DeleteComponentArtifact(ctx context.Context, user domain.User, releaseID, alias string) error {
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
		return fmt.Errorf("%w: artifacts can only be changed on a draft release", domain.ErrConflict)
	}
	alias, err = normalizeArtifactAlias(alias)
	if err != nil {
		return err
	}
	if err := p.store.DeleteComponentArtifact(ctx, releaseID, alias); err != nil {
		return err
	}
	_ = p.store.MarkReleaseVerified(ctx, release.ID, false)
	p.audit(ctx, user, "component.artifact_detached", "component_release", release.ID, map[string]any{"alias": alias})
	return nil
}
