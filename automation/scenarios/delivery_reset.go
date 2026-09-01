package scenarios

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (h *harness) resetDeliveryTargets(ctx context.Context) error {
	if !h.cfg.ResetDelivery {
		return fmt.Errorf("delivery reset was not explicitly confirmed")
	}
	if err := verifyHTTPArtifact(ctx, h.cfg.Fixture.ArtifactSourceURL, h.cfg.Fixture.ArtifactSHA256, http.StatusOK); err != nil {
		return fmt.Errorf("verify source artifact: %w", err)
	}
	if err := h.resetArtifactTarget(ctx); err != nil {
		return err
	}
	if err := h.resetImageTarget(ctx); err != nil {
		return err
	}
	if err := verifyHTTPArtifact(ctx, h.cfg.Fixture.ArtifactTargetURL, "", http.StatusNotFound); err != nil {
		return fmt.Errorf("artifact target remained after reset: %w", err)
	}
	if status, _, err := h.registryManifest(ctx, http.MethodHead); err != nil {
		return err
	} else if status != http.StatusNotFound {
		return fmt.Errorf("image target remained after reset: status=%d", status)
	}
	h.report.Checks = append(h.report.Checks, "dedicated Artifact/Image targets reset")
	return nil
}

func (h *harness) resetArtifactTarget(ctx context.Context) error {
	const script = `set -eu
target="$1"
expected="$2"
if [ -e "$target" ]; then
  [ -f "$target" ]
  actual=$(sha256sum "$target" | awk '{print $1}')
  [ "$actual" = "$expected" ] || { echo "target digest mismatch: $actual" >&2; exit 65; }
  rm -- "$target"
fi
`
	output, err := commandWithInput(ctx, script, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", h.cfg.Fixture.FSSSSHHost, "bash", "-s", "--", h.cfg.Fixture.ArtifactTargetPath, h.cfg.Fixture.ArtifactSHA256)
	if err != nil {
		return fmt.Errorf("reset dedicated FSS target: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (h *harness) resetImageTarget(ctx context.Context) error {
	status, observed, err := h.registryManifest(ctx, http.MethodHead)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("inspect target image manifest: status=%d", status)
	}
	if observed != h.cfg.Fixture.ImageDigest {
		return fmt.Errorf("refusing Registry reset: observed digest %s, want %s", observed, h.cfg.Fixture.ImageDigest)
	}
	status, _, err = h.registryManifest(ctx, http.MethodDelete)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted && status != http.StatusNotFound {
		return fmt.Errorf("delete dedicated image manifest: status=%d", status)
	}
	return nil
}

func (h *harness) registryManifest(ctx context.Context, method string) (int, string, error) {
	endpoint := strings.TrimRight(h.cfg.Fixture.ImageTargetRegistry, "/") + "/v2/" + h.cfg.Fixture.ImageTargetRepository + "/manifests/" + h.cfg.Fixture.ImageDigest
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, response.Header.Get("Docker-Content-Digest"), nil
}

func verifyHTTPArtifact(ctx context.Context, endpoint, expectedSHA string, expectedStatus int) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("GET %s returned %s, want %d", endpoint, response.Status, expectedStatus)
	}
	if expectedSHA == "" || response.StatusCode != http.StatusOK {
		return nil
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, response.Body); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedSHA {
		return fmt.Errorf("SHA-256=%s, want %s", actual, expectedSHA)
	}
	return nil
}
