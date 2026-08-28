package service

import (
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

func normalizeArtifactSourceURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: artifact sourceUrl must be an HTTP or HTTPS URL", domain.ErrInvalid)
	}
	parsed.Path = path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	return parsed.String(), nil
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

func (p *Platform) ownedReleaseForArtifact(ctx context.Context, user domain.User, releaseID, alias string, draftOnly bool) (domain.ComponentRelease, domain.Component, string, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return release, domain.Component{}, "", err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, component, "", err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, component, "", err
	}
	if draftOnly && release.Status != domain.ReleaseDraft {
		return release, component, "", fmt.Errorf("%w: artifact content can only be changed on a draft release", domain.ErrConflict)
	}
	if draftOnly {
		if active, activeErr := p.store.HasActiveComponentTest(ctx, release.ID); activeErr != nil {
			return release, component, "", activeErr
		} else if active {
			return release, component, "", fmt.Errorf("%w: wait for the active component test before changing artifact content", domain.ErrConflict)
		}
	}
	alias, err = normalizeArtifactAlias(alias)
	return release, component, alias, err
}

func (p *Platform) artifactUploadContext(ctx context.Context, user domain.User, releaseID, environmentID, alias string) (domain.ComponentRelease, domain.Component, domain.Environment, string, string, error) {
	release, component, alias, err := p.ownedReleaseForArtifact(ctx, user, releaseID, alias, true)
	if err != nil {
		return release, component, domain.Environment{}, "", "", err
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return release, component, environment, "", "", err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
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
	return release, component, environment, station, alias, nil
}

func (p *Platform) UploadComponentArtifact(ctx context.Context, user domain.User, releaseID, environmentID, alias, filename, expectedSHA string, input io.Reader) (domain.ComponentArtifact, error) {
	release, component, _, station, alias, err := p.artifactUploadContext(ctx, user, releaseID, environmentID, alias)
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
	sourceURL := artifactURL(station, metadata.RelativePath)
	return p.saveComponentArtifact(ctx, user, release, alias, sourceURL, metadata)
}

func (p *Platform) RegisterComponentArtifact(ctx context.Context, user domain.User, releaseID, alias, filename, sourceURL, expectedSHA string) (domain.ComponentArtifact, error) {
	release, _, alias, err := p.ownedReleaseForArtifact(ctx, user, releaseID, alias, true)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	expectedSHA, err = normalizeSHA256(expectedSHA)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	sourceURL, err = normalizeArtifactSourceURL(sourceURL)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	filename = path.Base(strings.TrimSpace(filename))
	if filename == "." || filename == "/" || filename == "" {
		return domain.ComponentArtifact{}, fmt.Errorf("%w: artifact filename is required", domain.ErrInvalid)
	}
	size := int64(0)
	if err := p.artifactDelivery.Probe(ctx, ArtifactLocation{URL: sourceURL, ObservedSize: &size}, ArtifactIdentity{SHA256: expectedSHA}); err != nil {
		return domain.ComponentArtifact{}, fmt.Errorf("probe artifact source: %w", err)
	}
	return p.saveComponentArtifact(ctx, user, release, alias, sourceURL, fssFileMetadata{Filename: filename, SHA256: expectedSHA, SizeBytes: size})
}

func (p *Platform) saveComponentArtifact(ctx context.Context, user domain.User, release domain.ComponentRelease, alias, sourceURL string, metadata fssFileMetadata) (domain.ComponentArtifact, error) {
	if metadata.SHA256 == "" || metadata.SHA256 != strings.ToLower(metadata.SHA256) {
		return domain.ComponentArtifact{}, fmt.Errorf("%w: file station returned invalid artifact metadata", domain.ErrConflict)
	}
	now := time.Now().UTC()
	artifact := domain.ComponentArtifact{ID: newID("artifact"), ReleaseID: release.ID, Alias: alias, Filename: metadata.Filename, SHA256: metadata.SHA256, SizeBytes: metadata.SizeBytes, SourceURL: sourceURL, SourceUpdatedBy: user.ID, SourceUpdatedAt: now, CreatedBy: user.ID, CreatedAt: now}
	if err := p.store.UpsertDraftComponentArtifactAndInvalidate(ctx, artifact); err != nil {
		return domain.ComponentArtifact{}, err
	}
	items, err := p.store.ListComponentArtifacts(ctx, release.ID)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	for _, saved := range items {
		if saved.Alias == alias {
			artifact = saved
			break
		}
	}
	p.audit(ctx, user, "component.artifact_saved", "component_release", release.ID, map[string]any{"alias": alias, "filename": metadata.Filename, "sha256": metadata.SHA256, "sourceUrl": sourceURL})
	return artifact, nil
}

func (p *Platform) UpdateComponentArtifactSource(ctx context.Context, user domain.User, releaseID, alias, sourceURL string) (domain.ComponentArtifact, error) {
	release, _, alias, err := p.ownedReleaseForArtifact(ctx, user, releaseID, alias, false)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	sourceURL, err = normalizeArtifactSourceURL(sourceURL)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	currentArtifacts, err := p.store.ListComponentArtifacts(ctx, release.ID)
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	var current domain.ComponentArtifact
	for _, artifact := range currentArtifacts {
		if artifact.Alias == alias {
			current = artifact
			break
		}
	}
	if current.ID == "" {
		return domain.ComponentArtifact{}, domain.ErrNotFound
	}
	if err := p.artifactDelivery.Probe(ctx, ArtifactLocation{URL: sourceURL}, ArtifactIdentity{SHA256: current.SHA256, SizeBytes: current.SizeBytes}); err != nil {
		return domain.ComponentArtifact{}, fmt.Errorf("probe artifact source: %w", err)
	}
	updated, err := p.store.UpdateComponentArtifactSource(ctx, release.ID, alias, sourceURL, user.ID, time.Now().UTC())
	if err != nil {
		return domain.ComponentArtifact{}, err
	}
	p.audit(ctx, user, "component.artifact_source_updated", "component_release", release.ID, map[string]any{"alias": alias, "sourceUrl": sourceURL, "sha256": updated.SHA256})
	return updated, nil
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
	if err := p.store.DeleteDraftComponentArtifactAndInvalidate(ctx, releaseID, alias); err != nil {
		return err
	}
	p.audit(ctx, user, "component.artifact_detached", "component_release", release.ID, map[string]any{"alias": alias})
	return nil
}
