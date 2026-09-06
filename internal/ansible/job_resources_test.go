package ansible

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestNativeResourcePolicyMatchesPlatform(t *testing.T) {
	provider := domain.ResourceInstance{OwnerID: "provider-a", ReleaseID: "provider", Hosts: []string{"192.0.2.1"}, Claim: domain.ResourceClaim{ID: "root", Path: "/opt/example", Scope: "tree", Access: "manage"}}
	consumer := domain.ResourceInstance{OwnerID: "consumer", ReleaseID: "consumer", Hosts: []string{"192.0.2.1"}, Claim: domain.ResourceClaim{ID: "read", Path: "/opt/example", Scope: "tree", Access: "read", SharedWith: &domain.ResourceReference{ReleaseID: "provider", ClaimID: "root"}}}
	for _, tc := range []struct {
		name   string
		change func(*domain.ResourceInstance, *domain.ResourceInstance) []domain.ResourceInstance
		valid  bool
	}{
		{"exact source", func(a, b *domain.ResourceInstance) []domain.ResourceInstance { return nil }, true},
		{"broader read", func(a, b *domain.ResourceInstance) []domain.ResourceInstance { b.Claim.Path = "/opt"; return nil }, false},
		{"file cannot provide tree", func(a, b *domain.ResourceInstance) []domain.ResourceInstance { a.Claim.Scope = "file"; return nil }, false},
		{"excluded descendant", func(a, b *domain.ResourceInstance) []domain.ResourceInstance {
			a.Claim.Excludes = []string{"/opt/example/private"}
			return nil
		}, false},
		{"matching exclusion", func(a, b *domain.ResourceInstance) []domain.ResourceInstance {
			a.Claim.Excludes = []string{"/opt/example/private"}
			b.Claim.Excludes = a.Claim.Excludes
			return nil
		}, true},
		{"distributed hosts", func(a, b *domain.ResourceInstance) []domain.ResourceInstance {
			extra := *a
			extra.OwnerID = "provider-b"
			extra.Hosts = []string{"192.0.2.2"}
			b.Hosts = []string{"192.0.2.1", "192.0.2.2"}
			return []domain.ResourceInstance{extra}
		}, true},
		{"uncovered host", func(a, b *domain.ResourceInstance) []domain.ResourceInstance {
			b.Hosts = []string{"192.0.2.1", "192.0.2.2"}
			return nil
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := provider, consumer
			instances := tc.change(&a, &b)
			instances = append(instances, a, b)
			if valid := len(domain.ResourceConflicts(instances)) == 0; valid != tc.valid {
				t.Errorf("platform valid=%v, want %v", valid, tc.valid)
			}
			source, err := jobPlugins.ReadFile("job_plugins/cf_resources.py")
			if err != nil {
				t.Fatal(err)
			}
			input, err := json.Marshal(map[string]any{"metadata": map[string]any{"resourcePolicyVersion": 1, "existingResources": instances}, "steps": []any{}})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command("python3", "-B", "-c", string(source)+"\nimport json, sys\nvalidate(json.load(sys.stdin))\n")
			command.Stdin = bytes.NewReader(input)
			output, err := command.CombinedOutput()
			if valid := err == nil; valid != tc.valid {
				t.Fatalf("native valid=%v, want %v: %s", valid, tc.valid, output)
			}
		})
	}
}
