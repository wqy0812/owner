package service

import (
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestAllExecutableKindsCanDeclareRetrySafety(t *testing.T) {
	platform, _ := readinessTestPlatform(t)
	component := domain.Component{ID: "component-1", Name: "component"}
	release := domain.ComponentRelease{ID: "release-1", Version: "1.0.0"}
	for _, kind := range []domain.ActionKind{domain.ActionUpgrade, domain.ActionRollback} {
		step, err := platform.actions.lockAction(component, "node", release, domain.ActionDefinition{ID: "action", Kind: kind, Playbook: "action.yml", Idempotent: true}, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if !step.RetrySafe {
			t.Fatalf("%s lost its declared retry capability", kind)
		}
	}
}
