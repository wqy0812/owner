package service

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func inventoryJSON(t *testing.T, hosts ...InventoryHost) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(InventoryDocument{Hosts: hosts})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTopologicalNodesAreStableAndRejectCycles(t *testing.T) {
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "z"}, {ID: "b"}, {ID: "a"}, {ID: "c"}},
		Edges: []domain.ScenarioEdge{{Source: "a", Target: "c"}, {Source: "b", Target: "c"}},
	}
	nodes, err := topologicalNodes(graph)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(nodes))
	for _, node := range nodes {
		got = append(got, node.ID)
	}
	if want := []string{"a", "b", "c", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("topological order=%v want=%v", got, want)
	}

	graph.Edges = append(graph.Edges, domain.ScenarioEdge{Source: "c", Target: "a"})
	if _, err := topologicalNodes(graph); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("cycle error=%v", err)
	}
}

func TestLockedPlanMapRoundTripAndEmptyPlanRejection(t *testing.T) {
	original := lockedPlan{Steps: []lockedStep{{ID: "step-1", Variables: map[string]any{"replicas": 3}}}, TreeDigest: "tree"}
	mapped := structToMap(original)
	decoded, err := mapToPlan(mapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Steps) != 1 || decoded.Steps[0].ID != "step-1" || decoded.TreeDigest != "tree" {
		t.Fatalf("decoded plan=%+v", decoded)
	}
	if _, err := mapToPlan(map[string]any{"steps": []any{}}); err == nil || !strings.Contains(err.Error(), "no steps") {
		t.Fatalf("empty plan error=%v", err)
	}
}

func TestValidatePlanHostGroups(t *testing.T) {
	raw := inventoryJSON(t,
		InventoryHost{Name: "control-1", Address: "10.0.0.10", Groups: []string{"control_plane"}},
		InventoryHost{Name: "worker-1", Address: "10.0.0.20", Groups: []string{"workers"}},
	)
	for _, limit := range []string{"all", "control-1", "workers", "10.0.0.0/24"} {
		err := validatePlanHostGroups(raw, []lockedStep{{Name: "install", Limit: limit}})
		if limit == "10.0.0.0/24" {
			if err != nil {
				t.Fatalf("literal Ansible limit %q rejected: %v", limit, err)
			}
		} else if err != nil {
			t.Fatalf("known inventory target %q rejected: %v", limit, err)
		}
	}
	if err := validatePlanHostGroups(raw, []lockedStep{{Name: "install", Limit: "missing_group"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing group error=%v", err)
	}
	if err := validatePlanHostGroups(json.RawMessage(`{"hosts":`), nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("malformed inventory error=%v", err)
	}
}

func TestRenderInventorySortsGroupsAndProtectsTokens(t *testing.T) {
	raw := inventoryJSON(t,
		InventoryHost{Name: "local", Address: "127.0.0.1", Groups: []string{"zeta", "all"}},
		InventoryHost{Name: "worker-1", Address: "10.0.0.20", Groups: []string{"alpha"}, User: "deploy", Port: 2222},
	)
	got, err := renderInventory(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "[all]\nlocal ansible_connection=local\nworker-1 ansible_host=10.0.0.20 ansible_user=deploy ansible_port=2222\n\n[alpha]\nworker-1\n\n[zeta]\nlocal\n"
	if string(got) != want {
		t.Fatalf("inventory:\n%s\nwant:\n%s", got, want)
	}
	for name, document := range map[string]json.RawMessage{
		"empty":   inventoryJSON(t),
		"host":    inventoryJSON(t, InventoryHost{Name: "bad host", Address: "10.0.0.1"}),
		"user":    inventoryJSON(t, InventoryHost{Name: "host", Address: "10.0.0.1", User: "bad user"}),
		"group":   inventoryJSON(t, InventoryHost{Name: "host", Address: "10.0.0.1", Groups: []string{"bad group"}}),
		"address": inventoryJSON(t, InventoryHost{Name: "host", Address: "10.0.0.1;touch"}),
	} {
		if _, err := renderInventory(document); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s unsafe inventory error=%v", name, err)
		}
	}
}

func TestResolveCredentialRefsUsesReferencesWithoutPersistingSecrets(t *testing.T) {
	t.Setenv("CLUSTERFORGE_TEST_PASSWORD", "sensitive-value")
	keyPath := t.TempDir() + "/id_test"
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	variables, secrets, err := resolveCredentialRefs([]domain.CredentialRef{
		{Name: "password", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_PASSWORD"},
		{Name: "ssh_key", Kind: "sshKeyPath", Reference: keyPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if variables["password"] != "sensitive-value" || variables["ssh_key"] != keyPath || !reflect.DeepEqual(secrets, []string{"sensitive-value"}) {
		t.Fatalf("resolved variables=%v secrets=%v", variables, secrets)
	}
	if _, _, err := resolveCredentialRefs([]domain.CredentialRef{{Name: "missing", Kind: "envVarRef", Reference: "CLUSTERFORGE_TEST_MISSING"}}); err == nil {
		t.Fatal("missing environment credential accepted")
	}
	if _, _, err := resolveCredentialRefs([]domain.CredentialRef{{Name: "raw", Kind: "literal", Reference: "secret"}}); err == nil {
		t.Fatal("unsupported literal credential accepted")
	}
}

func TestRecapSummaryAggregatesHosts(t *testing.T) {
	if got := recapSummary(nil); got != "Ansible completed successfully" {
		t.Fatalf("empty recap=%q", got)
	}
	got := recapSummary(map[string]ansiblerunner.HostRecap{
		"control": {OK: 3, Changed: 1},
		"worker":  {OK: 5, Changed: 2},
	})
	if got != "recap: ok=8 changed=3 hosts=2" {
		t.Fatalf("recap=%q", got)
	}
}
