package ansible

import "testing"

func TestComponentAndScenarioJobRollbackRetainsDependencySafeScope(t *testing.T) {
	steps := []JobStep{}
	for _, node := range []string{"consumer", "provider"} {
		for _, phase := range []string{"pre", "execute", "post"} {
			steps = append(steps, JobStep{ID: node + "-" + phase, ComponentID: node, NodeID: node, Phase: phase})
		}
	}
	selected, err := SelectRollbackStages(steps, []string{"consumer/consumer"})
	if err != nil || len(selected) != 3 || selected[2].Phase != "post" {
		t.Fatalf("component/scenario partial recovery lost its checks: %+v %v", selected, err)
	}
	if _, err := SelectRollbackStages(steps, []string{"provider/provider"}); err == nil {
		t.Fatal("allowed removing the provider while its consumer remains")
	}
	all, err := SelectRollbackStages(steps, nil)
	if err != nil || len(all) != 6 {
		t.Fatalf("full job recovery changed: %+v %v", all, err)
	}
}
