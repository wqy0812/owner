package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (m *Manager) VerifyExternal(ctx context.Context, backupID string) error {
	manifest, err := m.Verify(ctx, backupID, true)
	if err != nil {
		return err
	}
	catalog, _, err := m.catalogAt(ctx, manifest.GitCommit)
	if err != nil {
		return err
	}
	tables := map[string]TableDump{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	artifacts := tables["component_release_artifacts"]
	for _, row := range artifacts.Rows {
		source, _ := row[columnIndex(artifacts.Columns, "source_url")].Value().(string)
		expectedSHA, _ := row[columnIndex(artifacts.Columns, "sha256")].Value().(string)
		expectedSize, _ := row[columnIndex(artifacts.Columns, "size_bytes")].Value().(int64)
		parsed, err := url.Parse(source)
		if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("artifact source is not a credential-free HTTP(S) URL")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("probe artifact host %s: %w", parsed.Host, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, response.Body)
		closeErr := response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("artifact host %s returned HTTP %d", parsed.Host, response.StatusCode)
		}
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		actualSHA := hex.EncodeToString(hash.Sum(nil))
		if actualSHA != expectedSHA || size != expectedSize {
			return fmt.Errorf("artifact from host %s does not match its Catalog identity", parsed.Host)
		}
	}
	images := tables["component_release_images"]
	for _, row := range images.Rows {
		source, _ := row[columnIndex(images.Columns, "source_ref")].Value().(string)
		digest, _ := row[columnIndex(images.Columns, "digest")].Value().(string)
		base := strings.Split(source, "@")[0]
		if base == "" || !strings.HasPrefix(digest, "sha256:") {
			return fmt.Errorf("image source or digest is invalid")
		}
		if _, err := runCommand(ctx, "", "docker", "manifest", "inspect", base+"@"+digest); err != nil {
			return fmt.Errorf("probe image %s: %w", base, err)
		}
	}
	return nil
}
