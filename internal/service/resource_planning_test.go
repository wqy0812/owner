package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"testing"
)

type resourceFixtureStore struct {
	releases      map[string]domain.ComponentRelease
	run           domain.Run
	installations []domain.EnvironmentComponentInstallation
}

func (s resourceFixtureStore) GetComponentRelease(_ context.Context, id string) (domain.ComponentRelease, error) {
	return s.releases[id], nil
}
func (s resourceFixtureStore) GetRun(context.Context, string) (domain.Run, error) { return s.run, nil }
func (s resourceFixtureStore) ListEnvironmentComponentInstallations(context.Context, string) ([]domain.EnvironmentComponentInstallation, error) {
	return s.installations, nil
}
func TestResourcePlanUsesOriginalInstallationHostsAndExactUpgrade(t *testing.T) {
	ctx := context.Background()
	contract := &domain.ResourceContract{Version: 1, Claims: []domain.ResourceClaim{{ID: "config", Path: "/etc/kubernetes/config", Scope: "file", Access: "manage"}}}
	original := lockedStep{NodeID: "installed-node", SourceNodeID: "installed-node", ComponentID: "old-component", ReleaseID: "old-release", ActionID: "install", Action: domain.ActionInstall, Phase: "execute", Limit: "old-group", ResourceContract: contract, Resources: []domain.ResourceInstance{{OwnerID: "installed-node", ReleaseID: "old-release", HostGroup: "old-group", Hosts: []string{"192.0.2.1"}, Claim: contract.Claims[0]}}}
	raw, _ := json.Marshal(lockedPlan{Steps: []lockedStep{original}})
	var snapshot map[string]any
	_ = json.Unmarshal(raw, &snapshot)
	data := resourceFixtureStore{releases: map[string]domain.ComponentRelease{"new-release": {ID: "new-release"}}, run: domain.Run{InputSnapshot: snapshot}, installations: []domain.EnvironmentComponentInstallation{{ComponentID: "old-component", ReleaseID: "old-release", NodeID: "installed-node", InstallRunID: "original"}}}
	inventory, _ := json.Marshal(InventoryDocument{Hosts: []InventoryHost{{Name: "renamed", Address: "192.0.2.1", Groups: []string{"new-group"}}, {Name: "other", Address: "192.0.2.2", Groups: []string{"old-group"}}}})
	env := domain.Environment{ID: "env", Revision: &domain.EnvironmentRevision{Inventory: inventory}}
	step := lockedStep{NodeID: "new-node", SourceNodeID: "new-node", ReleaseID: "new-release", ComponentID: "new-component", ActionID: "install", Action: domain.ActionInstall, Phase: "execute", Limit: "new-group", ResourceContract: contract}
	plan := lockedPlan{Steps: []lockedStep{step}}
	if err := validateResourcePlan(ctx, data, env, &plan); err == nil {
		t.Fatal("inventory rename silently moved existing ownership")
	}
	plan.Steps[0].Limit = "old-group"
	if err := validateResourcePlan(ctx, data, env, &plan); err != nil {
		t.Fatal("different actual host blocked", err)
	}
	plan.Steps[0] = step
	plan.Steps[0].Action = domain.ActionUpgrade
	plan.Steps[0].ComponentID = "old-component"
	plan.Steps[0].SourceNodeID = "installed-node"
	plan.Steps[0].FromReleaseID = "old-release"
	if err := validateResourcePlan(ctx, data, env, &plan); err != nil {
		t.Fatal("exact upgrade replacement blocked", err)
	}
	plan.Steps[0].FromReleaseID = "unrelated-release"
	if err := validateResourcePlan(ctx, data, env, &plan); err == nil {
		t.Fatal("unrelated source bypassed ownership")
	}
	plan.Steps[0].ResourceContract = nil
	if err := validateResourcePlan(ctx, data, env, &plan); err == nil {
		t.Fatal("legacy declaration became ready")
	}
}
func TestResourceHostCanonicalIdentity(t *testing.T) {
	inventory := InventoryDocument{Hosts: []InventoryHost{{Address: "::ffff:192.0.2.1", Groups: []string{"a"}}, {Address: "localhost", Groups: []string{"a"}}}}
	got := resourceHostNames(inventory, "a")
	if len(got) != 2 || got[0] != "192.0.2.1" || got[1] != "127.0.0.1" {
		t.Fatal(got)
	}
}
