package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceBoundariesAndExactSharing(t *testing.T) {
	parent := ResourceInstance{OwnerID: "cni", ReleaseID: "cni-v1", HostGroup: "control", Hosts: []string{"192.0.2.1"}, Claim: ResourceClaim{ID: "plugins", Path: "/opt/cni/bin", Scope: "tree", Access: "manage", SharedPaths: []string{"/opt/cni/bin/flannel"}}}
	downstream := ResourceInstance{OwnerID: "flannel", ReleaseID: "flannel-v1", HostGroup: "workers", Hosts: []string{"192.0.2.1"}, Claim: ResourceClaim{ID: "plugin", Path: "/opt/cni/bin/flannel", Scope: "file", Access: "manage", SharedWith: &ResourceReference{ReleaseID: "cni-v1", ClaimID: "plugins"}}}
	for _, tc := range []struct {
		name   string
		change func(*ResourceInstance, *ResourceInstance)
		want   bool
	}{
		{"exact child handoff", func(a, b *ResourceInstance) {}, false},
		{"unbound child", func(a, b *ResourceInstance) { b.Claim.SharedWith = nil }, true},
		{"wrong version", func(a, b *ResourceInstance) { b.Claim.SharedWith.ReleaseID = "cni-v2" }, true},
		{"whole directory ownership", func(a, b *ResourceInstance) { a.Claim.SharedPaths = nil }, true},
		{"exclusive directory verify", func(a, b *ResourceInstance) { a.Claim.Exclusive = true; a.Claim.Access = "verify" }, true},
		{"same file double writer", func(a, b *ResourceInstance) { a.Claim.Path = b.Claim.Path; a.Claim.Scope = "file" }, true},
		{"different actual hosts", func(a, b *ResourceInstance) { b.Hosts = []string{"192.0.2.2"}; b.Claim.SharedWith = nil }, false},
		{"read bound to managed source", func(a, b *ResourceInstance) { a.Claim.SharedPaths = nil; b.Claim.Access = "read" }, false},
		{"read exceeds provider tree", func(a, b *ResourceInstance) {
			b.Claim.Access = "read"
			b.Claim.Path = "/opt/cni"
			b.Claim.Scope = "tree"
		}, true},
		{"file provider cannot cover tree", func(a, b *ResourceInstance) {
			a.Claim.Path = b.Claim.Path
			a.Claim.Scope = "file"
			a.Claim.SharedPaths = nil
			b.Claim.Access = "read"
			b.Claim.Scope = "tree"
		}, true},
		{"read includes excluded subtree", func(a, b *ResourceInstance) {
			a.Claim.SharedPaths = nil
			a.Claim.Excludes = []string{"/opt/cni/bin/private"}
			b.Claim.Access = "read"
			b.Claim.Path = a.Claim.Path
			b.Claim.Scope = "tree"
		}, true},
		{"read respects provider exclusion", func(a, b *ResourceInstance) {
			a.Claim.SharedPaths = nil
			a.Claim.Excludes = []string{"/opt/cni/bin/private"}
			b.Claim.Access = "read"
			b.Claim.Path = a.Claim.Path
			b.Claim.Scope = "tree"
			b.Claim.Excludes = []string{"/opt/cni/bin/private"}
		}, false},
		{"read without binding", func(a, b *ResourceInstance) {
			a.Claim.SharedPaths = nil
			b.Claim.Access = "read"
			b.Claim.SharedWith = nil
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := parent, downstream
			ref := *downstream.Claim.SharedWith
			b.Claim.SharedWith = &ref
			tc.change(&a, &b)
			issues := ResourceConflicts([]ResourceInstance{a, b})
			if (len(issues) > 0) != tc.want {
				t.Fatalf("issues=%+v", issues)
			}
		})
	}
}
func TestResourceDeclarationsNeverInventLegacyConsent(t *testing.T) {
	if ValidateResourceContract(nil, nil, false) != nil {
		t.Fatal("historic read must remain valid")
	}
	if ValidateResourceContract(nil, nil, true) == nil {
		t.Fatal("new operation accepted missing declaration")
	}
	explicit := &ResourceContract{Version: 1, NoManagedPaths: true, Claims: []ResourceClaim{}}
	if err := ValidateResourceContract(explicit, nil, true); err != nil {
		t.Fatal(err)
	}
	action := ActionDefinition{ID: "legacy"}
	data, _ := json.Marshal(action)
	if strings.Contains(string(data), "resourceContract") {
		t.Fatal("nil resource field changed old serialized action")
	}
	contract := &ResourceContract{Version: 1, Claims: []ResourceClaim{{ID: "bin", Path: "${root}/bin", Scope: "tree", Access: "manage"}}}
	parameters := []ParameterDefinition{{Name: "root", Type: ParameterTypeString, ValueProvider: ParameterProviderEnvironmentOwner}}
	if err := ValidateResourceContract(contract, parameters, true); err != nil {
		t.Fatal(err)
	}
	for _, root := range []any{nil, "/opt/../etc", "/opt/*", "/opt/${other}"} {
		if _, err := ResolveResourceContract(contract, map[string]any{"root": root}); err == nil {
			t.Fatalf("unsafe/unresolved path accepted: %v", root)
		}
	}
	claims, err := ResolveResourceContract(contract, map[string]any{"root": "/opt/cni"})
	if err != nil || claims[0].Path != "/opt/cni/bin" {
		t.Fatalf("resolve=%+v %v", claims, err)
	}
}
