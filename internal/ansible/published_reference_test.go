package ansible

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Validate the reference with the same task contract used by Owner authoring.
// This does not execute a cluster install or borrow the source Release's evidence.
func TestPublishedReferenceRoleEntries(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "ansible", "kubernetes-1.17.5")
	contracts, err := filepath.Glob(filepath.Join(root, "roles", "*", "contract.json"))
	if err != nil || len(contracts) != 15 {
		t.Fatalf("reference contracts: count=%d, error=%v", len(contracts), err)
	}
	entries := map[string]bool{}
	for _, path := range contracts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var contract struct {
			Actions []struct{ Entry, Kind string }
		}
		if err := json.Unmarshal(data, &contract); err != nil {
			t.Fatal(err)
		}
		if len(contract.Actions) != 5 {
			t.Fatalf("%s: want all five published lifecycle entries", path)
		}
		for _, action := range contract.Actions {
			entries[filepath.Join(filepath.Dir(path), action.Entry)] = action.Kind == "check"
		}
	}
	entries[filepath.Join(root, "acceptance", "tasks", "acceptance.yml")] = false // mayMutate
	if len(entries) != 76 {
		t.Fatalf("reference entries: %d", len(entries))
	}
	for path, check := range entries {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateRoleTasks(data, check); err != nil {
				t.Fatal(err)
			}
		})
	}
}
