package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func adapterResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestHTTPArtifactDeliveryProbeDistinguishesMissingTarget(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/register" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		return adapterResponse(http.StatusNotFound, "missing"), nil
	})}
	delivery := NewHTTPArtifactDelivery(client)
	err := delivery.Probe(context.Background(), ArtifactLocation{FileStation: "fss.test", RelativePath: "components/runtime.tgz"}, ArtifactIdentity{SHA256: strings.Repeat("a", 64)})
	if !errors.Is(err, ErrDeliveryTargetMissing) {
		t.Fatalf("probe error=%v, want target missing", err)
	}
}

func TestHTTPArtifactDeliveryTransfersAndVerifiesMetadata(t *testing.T) {
	contents := []byte("artifact")
	checksum := strings.Repeat("b", 64)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && request.URL.Host == "target.test" && request.URL.Path == "/api/v1/fetch":
			body, err := io.ReadAll(request.Body)
			if err != nil || !strings.Contains(string(body), `"sourceUrl":"https://source.test/runtime.tgz"`) || !strings.Contains(string(body), `"path":"components/runtime.tgz"`) || !strings.Contains(string(body), checksum) {
				t.Fatalf("target body=%q err=%v", body, err)
			}
			return adapterResponse(http.StatusCreated, `{"relativePath":"components/runtime.tgz","filename":"runtime.tgz","sha256":"`+checksum+`","sizeBytes":8}`), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
			return nil, nil
		}
	})}
	delivery := NewHTTPArtifactDelivery(client)
	err := delivery.Transfer(context.Background(), ArtifactTransfer{
		Source:   ArtifactLocation{URL: "https://source.test/runtime.tgz"},
		Target:   ArtifactLocation{FileStation: "target.test", RelativePath: "components/runtime.tgz"},
		Identity: ArtifactIdentity{SHA256: checksum, SizeBytes: int64(len(contents))},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPArtifactDeliveryProbeVerifiesSourceIdentityAndSize(t *testing.T) {
	contents := "immutable artifact"
	digest := sha256.Sum256([]byte(contents))
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://source.test/runtime.tgz" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		return adapterResponse(http.StatusOK, contents), nil
	})}
	delivery := NewHTTPArtifactDelivery(client)
	var observed int64
	err := delivery.Probe(context.Background(), ArtifactLocation{URL: "https://source.test/runtime.tgz", ObservedSize: &observed}, ArtifactIdentity{SHA256: hex.EncodeToString(digest[:])})
	if err != nil || observed != int64(len(contents)) {
		t.Fatalf("probe observed=%d err=%v", observed, err)
	}
	if err := delivery.Probe(context.Background(), ArtifactLocation{URL: "https://source.test/runtime.tgz"}, ArtifactIdentity{SHA256: strings.Repeat("0", 64)}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("hash mismatch error=%v", err)
	}
}

func TestHTTPArtifactDeliveryProbeRejectsIncompleteAndFailedLocations(t *testing.T) {
	delivery := NewHTTPArtifactDelivery(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return adapterResponse(http.StatusServiceUnavailable, "offline"), nil
	})})
	if err := delivery.Probe(context.Background(), ArtifactLocation{URL: "https://source.test/runtime.tgz"}, ArtifactIdentity{}); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Fatalf("failed source error=%v", err)
	}
	if err := delivery.Probe(context.Background(), ArtifactLocation{}, ArtifactIdentity{}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete target error=%v", err)
	}
}

func TestHTTPArtifactDeliveryTransferRejectsMismatchedTargetMetadata(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	delivery := NewHTTPArtifactDelivery(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return adapterResponse(http.StatusCreated, `{"relativePath":"components/other.tgz","sha256":"`+checksum+`"}`), nil
	})})
	err := delivery.Transfer(context.Background(), ArtifactTransfer{
		Source: ArtifactLocation{URL: "https://source.test/runtime.tgz"}, Target: ArtifactLocation{FileStation: "target.test", RelativePath: "components/runtime.tgz"},
		Identity: ArtifactIdentity{SHA256: checksum},
	})
	if err == nil || !strings.Contains(err.Error(), "mismatched metadata") {
		t.Fatalf("metadata mismatch error=%v", err)
	}
}
