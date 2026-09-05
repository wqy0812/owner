package ansible

import "fmt"

// SelectRollbackStages selects a prefix of the reverse dependency order. Keeping
// its dependent suffix intact is conservative even for manually ordered nodes.
func SelectRollbackStages(stages []JobStep, nodes []string) ([]JobStep, error) {
	if len(nodes) == 0 {
		return stages, nil
	}
	requested := map[string]bool{}
	for _, node := range nodes {
		if node == "" || requested[node] {
			return nil, fmt.Errorf("rollback nodes must be nonempty and unique")
		}
		requested[node] = true
	}
	selected := []JobStep{}
	seen := map[string]bool{}
	omitted := false
	for _, step := range stages {
		key := step.ComponentID + "/" + step.NodeID
		include := requested[key]
		if !include {
			omitted = true
			continue
		}
		if omitted {
			return nil, fmt.Errorf("rollback scope must include all preceding dependent nodes in the reverse plan")
		}
		seen[key] = true
		selected = append(selected, step)
	}
	if len(seen) != len(requested) {
		return nil, fmt.Errorf("rollback scope contains an unknown component instance")
	}
	return selected, nil
}
