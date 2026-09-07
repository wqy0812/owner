package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPArtifactDelivery struct{ client *http.Client }

func NewHTTPArtifactDelivery(client *http.Client) *HTTPArtifactDelivery {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPArtifactDelivery{client: client}
}

func (d *HTTPArtifactDelivery) Probe(ctx context.Context, location ArtifactLocation, identity ArtifactIdentity) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
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
	var metadata struct {
		SHA256       string `json:"sha256"`
		RelativePath string `json:"relativePath"`
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return err
	}
	if metadata.SHA256 != transfer.Identity.SHA256 || metadata.RelativePath != transfer.Target.RelativePath {
		return fmt.Errorf("target file station returned mismatched metadata")
	}
	return nil
}
