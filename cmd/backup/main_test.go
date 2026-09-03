package main

import (
	"context"
	"strings"
	"testing"
)

func TestManualSnapshotCommandIsNotAvailable(t *testing.T) {
	for _, args := range [][]string{{"snapshot", "--reason", "manual"}, {"convert-legacy-k8s1175"}} {
		err := run(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("removed command %s error=%v", args[0], err)
		}
	}
}

func TestAutomationSnapshotRequiresKnownSource(t *testing.T) {
	for _, source := range []string{"manual", "scheduled", "after-schema-migration"} {
		err := run(context.Background(), []string{"automation-snapshot", "--source", source})
		if err == nil || !strings.Contains(err.Error(), "--source must be") {
			t.Fatalf("automation snapshot source %q error=%v", source, err)
		}
	}
}

func TestAutomationSnapshotAcceptsGuardedSources(t *testing.T) {
	for _, source := range []string{"before-deploy", "after-v1-rebuild"} {
		if !validAutomationSource(source) {
			t.Fatalf("guarded automation source %q was rejected", source)
		}
	}
}
