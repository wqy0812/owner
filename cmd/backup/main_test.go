package main

import (
	"context"
	"strings"
	"testing"
)

func TestManualSnapshotCommandIsNotAvailable(t *testing.T) {
	err := run(context.Background(), []string{"snapshot", "--reason", "manual"})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("manual snapshot command error=%v", err)
	}
}

func TestAutomationSnapshotRequiresKnownSource(t *testing.T) {
	for _, source := range []string{"manual", "scheduled"} {
		err := run(context.Background(), []string{"automation-snapshot", "--source", source})
		if err == nil || !strings.Contains(err.Error(), "--source must be") {
			t.Fatalf("automation snapshot source %q error=%v", source, err)
		}
	}
}

func TestAutomationSnapshotAcceptsGuardedSources(t *testing.T) {
	for _, source := range []string{"before-deploy", "after-v1-rebuild", "after-schema-migration"} {
		if !validAutomationSource(source) {
			t.Fatalf("guarded automation source %q was rejected", source)
		}
	}
}
