package service

import (
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestNormalizeEnvironmentVariablesAndRegistry(t *testing.T) {
	normalized, err := normalizeEnvironmentVariables(map[string]string{
		"IMAGE_REGISTRY": " 192.168.88.54:5000/ ",
		"REGION":         "cn-east",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if normalized["IMAGE_REGISTRY"] != "192.168.88.54:5000" {
		t.Fatalf("registry=%q", normalized["IMAGE_REGISTRY"])
	}
	for name, variables := range map[string]map[string]string{
		"lowercase": {"image_registry": "registry.invalid"},
		"sensitive": {"REGISTRY_TOKEN": "secret"},
		"scheme":    {"IMAGE_REGISTRY": "http://registry.invalid"},
		"tag":       {"IMAGE_REGISTRY": "registry.invalid/project:tag"},
		"port":      {"IMAGE_REGISTRY": "registry.invalid:70000"},
	} {
		if _, err := normalizeEnvironmentVariables(variables, nil); err == nil {
			t.Fatalf("%s variable was accepted", name)
		}
	}
	if _, err := normalizeEnvironmentVariables(map[string]string{"REGION": "cn"}, []domain.CredentialRef{{Name: "REGION", Kind: "envVarRef", Reference: "REGION_SECRET"}}); err == nil || !strings.Contains(err.Error(), "CredentialRef") {
		t.Fatalf("credential collision err=%v", err)
	}
}

func TestInjectEnvironmentVariablesDirectlyAndRejectsCollisions(t *testing.T) {
	revision := domain.EnvironmentRevision{Variables: map[string]string{"IMAGE_REGISTRY": "192.168.88.54:5000"}}
	steps := []lockedStep{{Variables: map[string]any{"component_version": "1.0.0"}}}
	if err := injectEnvironmentVariables(revision, steps); err != nil {
		t.Fatal(err)
	}
	if steps[0].Variables["IMAGE_REGISTRY"] != "192.168.88.54:5000" {
		t.Fatalf("variables=%#v", steps[0].Variables)
	}
	if err := injectEnvironmentVariables(revision, []lockedStep{{Variables: map[string]any{"IMAGE_REGISTRY": "override"}}}); err == nil || !strings.Contains(err.Error(), "component parameter") {
		t.Fatalf("component collision err=%v", err)
	}
	revision.CredentialRefs = []domain.CredentialRef{{Name: "IMAGE_REGISTRY", Kind: "envVarRef", Reference: "REGISTRY_REF"}}
	if err := injectEnvironmentVariables(revision, []lockedStep{{Variables: map[string]any{}}}); err == nil || !strings.Contains(err.Error(), "CredentialRef") {
		t.Fatalf("credential collision err=%v", err)
	}
}
