package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Exercise Prepare with the production adapter. A missing-target sentinel from
// an adapter stub cannot prove that Docker command failures are classified.
func TestPrepareWithDockerImageDelivery(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	source := "source.test/runtime@" + digest
	target := "target.test/runtime@" + digest
	tag := "target.test/runtime:1"
	manifest := "manifest inspect --insecure --verbose " + target
	probe := []string{manifest, "pull " + target}
	transferred := append(append([]string{}, probe...), "pull "+source, "tag "+source+" "+tag, "push "+tag, "pull "+target, manifest)
	for _, tc := range []struct {
		name, pullError, presentDigest, afterDigest string
		wantError                                   bool
		wantCommands                                []string
	}{
		{name: "reuse verified target", presentDigest: digest, wantCommands: []string{manifest, manifest}},
		{name: "missing manifest", pullError: "Error response from daemon: manifest for " + target + " not found: manifest unknown: manifest unknown", wantCommands: append(append([]string{}, transferred...), manifest)},
		{name: "missing repository", pullError: "Error response from daemon: name unknown: repository name not known to registry", wantCommands: append(append([]string{}, transferred...), manifest)},
		{name: "containerd missing reference", pullError: fmt.Sprintf("Error response from daemon: failed to resolve reference %q: %s: not found", target, target), wantCommands: append(append([]string{}, transferred...), manifest)},
		{name: "unauthorized fallback", pullError: "Error response from daemon: unauthorized: authentication required", wantError: true, wantCommands: probe},
		{name: "ambiguous private repository", pullError: "Error response from daemon: pull access denied for target.test/runtime, repository does not exist or may require 'docker login': denied: requested access to the resource is denied", wantError: true, wantCommands: probe},
		{name: "network failure", pullError: "Error response from daemon: Get https://target.test/v2/: dial tcp: connection refused", wantError: true, wantCommands: probe},
		{name: "generic not found", pullError: "Error response from daemon: unexpected status: 404 Not Found", wantError: true, wantCommands: probe},
		{name: "missing blob", pullError: "Error response from daemon: blob unknown: blob unknown to registry", wantError: true, wantCommands: probe},
		{name: "different missing reference", pullError: `Error response from daemon: failed to resolve reference "other.test/runtime:1": other.test/runtime:1: not found`, wantError: true, wantCommands: probe},
		{name: "missing message before auth failure", pullError: "manifest unknown: manifest unknown\nunauthorized: authentication required", wantError: true, wantCommands: probe},
		{name: "wrong target identity", presentDigest: "sha256:" + strings.Repeat("b", 64), wantError: true, wantCommands: []string{manifest}},
		{name: "wrong identity after transfer", pullError: "Error response from daemon: manifest unknown: manifest unknown", afterDigest: "sha256:" + strings.Repeat("b", 64), wantError: true, wantCommands: transferred},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, commands := dockerMissingTargetFixture(t, target, digest)
			t.Setenv("DELIVERY_TEST_PULL_ERROR", tc.pullError)
			t.Setenv("DELIVERY_TEST_PRESENT_DIGEST", tc.presentDigest)
			if tc.afterDigest != "" {
				t.Setenv("DELIVERY_TEST_AFTER_DIGEST", tc.afterDigest)
			}
			plan := Plan{
				ImageTransfers:       []PlannedImageTransfer{{RequirementID: "image", SourceDigest: source, TargetRef: tag, TargetDigest: target}},
				DeliveryRequirements: []Requirement{{ID: "image", Kind: "image", Source: source, Target: target, Identity: digest}},
				DeliveryDecisions:    []Decision{{RequirementID: "image", Mode: "transfer"}},
			}
			err := Prepare(context.Background(), plan, nil, adapter)
			if (err != nil) != tc.wantError || errors.Is(err, ErrDeliveryTargetMissing) {
				t.Fatalf("Prepare error=%v, wantError=%v", err, tc.wantError)
			}
			if got := commands(); !reflect.DeepEqual(got, tc.wantCommands) {
				t.Fatalf("commands=%q, want=%q", got, tc.wantCommands)
			}
		})
	}
}

func TestDockerMissingTargetProbePreservesCancellation(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	target := "target.test/runtime@" + digest
	adapter, commands := dockerMissingTargetFixture(t, target, digest)
	t.Setenv("DELIVERY_TEST_PULL_ERROR", "manifest unknown: manifest unknown")
	t.Setenv("DELIVERY_TEST_PULL_SLEEP", "30")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := adapter.Probe(ctx, ImageLocation{Ref: target}, ImageDigest{Value: digest})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrDeliveryTargetMissing) {
		t.Fatalf("cancelled probe error=%v", err)
	}
	if got := commands(); len(got) != 2 || got[1] != "pull "+target {
		t.Fatalf("cancellation did not cover the pull fallback: %q", got)
	}
}

func dockerMissingTargetFixture(t *testing.T, target, digest string) (*DockerImageDelivery, func() []string) {
	t.Helper()
	dir := t.TempDir()
	binary, logPath := filepath.Join(dir, "docker"), filepath.Join(dir, "commands")
	t.Setenv("DELIVERY_TEST_LOG", logPath)
	t.Setenv("DELIVERY_TEST_PUSHED", filepath.Join(dir, "pushed"))
	t.Setenv("DELIVERY_TEST_TARGET", target)
	t.Setenv("DELIVERY_TEST_PRESENT_DIGEST", "")
	t.Setenv("DELIVERY_TEST_AFTER_DIGEST", digest)
	t.Setenv("DELIVERY_TEST_PULL_ERROR", "manifest unknown: manifest unknown")
	t.Setenv("DELIVERY_TEST_PULL_SLEEP", "")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$DELIVERY_TEST_LOG"
case "$1" in
manifest)
  if [ -f "$DELIVERY_TEST_PUSHED" ]; then
    printf '{"Descriptor":{"digest":"%s"}}\n' "$DELIVERY_TEST_AFTER_DIGEST"
  elif [ -n "$DELIVERY_TEST_PRESENT_DIGEST" ]; then
    printf '{"Descriptor":{"digest":"%s"}}\n' "$DELIVERY_TEST_PRESENT_DIGEST"
  else
    printf 'no such manifest: %s\n' "$DELIVERY_TEST_TARGET" >&2
    exit 1
  fi
  ;;
pull)
  if [ "$2" = "$DELIVERY_TEST_TARGET" ] && [ ! -f "$DELIVERY_TEST_PUSHED" ]; then
    printf '%s\n' "$DELIVERY_TEST_PULL_ERROR" >&2
    if [ -n "$DELIVERY_TEST_PULL_SLEEP" ]; then exec sleep "$DELIVERY_TEST_PULL_SLEEP"; fi
    exit 1
  fi
  ;;
push) touch "$DELIVERY_TEST_PUSHED" ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewDockerImageDelivery(binary), func() []string {
		t.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}
