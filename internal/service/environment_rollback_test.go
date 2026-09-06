package service

import (
	"errors"
	"testing"

	"codex/platform-demo/internal/domain"
)

type rollbackDigestRunner map[string]string

func (r rollbackDigestRunner) Digest(playbook string) (string, string, error) {
	digest, ok := r[playbook]
	if !ok {
		return "", "", errors.New("missing Playbook")
	}
	return digest, "tree", nil
}

func TestCurrentReleaseContainsCapturedInstallPlaybookRejectsRotatedActionID(t *testing.T) {
	release := domain.ComponentRelease{ID: "release-1", Actions: []domain.ActionDefinition{
		{ID: "new-install-action", Kind: domain.ActionInstall, Playbook: "install.yml"},
		{ID: "new-verify-action", Kind: domain.ActionVerify, Playbook: "verify.yml"},
	}}

	matched, err := currentReleaseContainsCapturedInstallPlaybook(release, "captured-install-action", "install-digest", rollbackDigestRunner{
		"install.yml": "install-digest",
		"verify.yml":  "verify-digest",
	})
	if !errors.Is(err, domain.ErrConflict) || matched {
		t.Fatalf("rotated action accepted: matched=%v err=%v", matched, err)
	}

	matched, err = currentReleaseContainsCapturedInstallPlaybook(release, "new-install-action", "different-digest", rollbackDigestRunner{
		"install.yml": "install-digest",
		"verify.yml":  "verify-digest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("expected a changed install Playbook digest to remain blocked")
	}
}

func TestRollbackVariablesFromInstallRemovesGeneratedDeliveryVariables(t *testing.T) {
	values := map[string]any{
		"keep":                          "value",
		"IMAGE_REGISTRY":                "registry.example:5000",
		"component_image_ref":           "registry.example/component@sha256:main",
		"component_image_digest":        "sha256:main",
		"coredns_image_ref":             "registry.example/coredns@sha256:dns",
		"coredns_image_digest":          "sha256:dns",
		"flannel_manifest_url":          "http://files.example/flannel.yml",
		"flannel_manifest_sha256":       "manifest-sha",
		"clusterforge_backup_operation": "capture",
	}
	release := domain.ComponentRelease{
		Artifacts: []domain.ComponentArtifact{{Alias: "flannel_manifest"}},
		Images:    []domain.ComponentImage{{LogicalName: "coredns"}},
	}
	source := domain.EnvironmentRevision{Variables: map[string]string{"IMAGE_REGISTRY": "registry.example:5000"}}
	current := domain.EnvironmentRevision{Variables: map[string]string{"FILE_STATION": "files.example"}}

	got := rollbackVariablesFromInstall(values, source, current, release)
	if got["keep"] != "value" {
		t.Fatalf("expected component input to be preserved, got %#v", got)
	}
	for _, name := range []string{
		"IMAGE_REGISTRY", "component_image_ref", "component_image_digest",
		"coredns_image_ref", "coredns_image_digest",
		"flannel_manifest_url", "flannel_manifest_sha256",
		"clusterforge_backup_operation",
	} {
		if _, exists := got[name]; exists {
			t.Fatalf("expected generated variable %q to be removed, got %#v", name, got)
		}
	}
}
