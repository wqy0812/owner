package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPreparationSchedulesYAMLWithoutRunningComponentChecks(t *testing.T) {
	inventory, _ := json.Marshal(InventoryDocument{Hosts: []InventoryHost{{Name: "local", Address: "127.0.0.1", Groups: []string{"nodes"}}}})
	env := domain.Environment{Revision: &domain.EnvironmentRevision{Inventory: inventory, Variables: map[string]string{"ansible_python_interpreter": "sh"}}}
	plan := lockedPlan{Steps: []lockedStep{{ID: "pre", ReleaseID: "consumer", Phase: "pre", Limit: "nodes", Playbook: "tasks/checks/pre.yml"}, {ID: "rollback-post", Action: domain.ActionCheck, Phase: "post", SourceParametersFrozen: true, Playbook: "tasks/checks/source-pre.yml"}}}
	var checks []PreparationCheck
	err := (&EnvironmentService{}).probeForPreparation(context.Background(), env, plan, func(c PreparationCheck) { checks = append(checks, c) })
	if err != nil {
		t.Fatal(err)
	}
	found := false
	postFound := false
	for _, c := range checks {
		if c.ID == "pre:yaml" {
			found = c.Status == "scheduled" && c.StartedAt.IsZero() && c.Source == "tasks/checks/pre.yml"
		}
		if c.ID == "rollback-post:yaml" {
			postFound = c.Status == "scheduled" && c.Source == "tasks/checks/source-pre.yml" && strings.Contains(c.Message, "动作完成后")
		}
		if c.Category == "residual" && c.Status == "passed" {
			t.Fatal("unperformed residual check passed")
		}
	}
	if !found || !postFound {
		t.Fatalf("missing scheduled YAML: %+v", checks)
	}
}

func TestPreparationChecksOmitUnobservedTimes(t *testing.T) {
	data, err := json.Marshal(PreparationCheck{ID: "provider", Status: "provided"})
	if err != nil || strings.Contains(string(data), "startedAt") {
		t.Fatalf("unobserved check invented a timestamp: %s %v", data, err)
	}
	data, err = json.Marshal(PreparationCheck{ID: "ssh", Status: "passed", StartedAt: time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)})
	if err != nil || !strings.Contains(string(data), `"startedAt":"2026-09-06T09:00:00Z"`) {
		t.Fatalf("observed timestamp missing: %s %v", data, err)
	}
}
