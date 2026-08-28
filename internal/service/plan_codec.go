package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

func validateRequiredCredentials(refs []domain.CredentialRef, steps []lockedStep) error {
	configured := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		configured[ref.Name] = struct{}{}
	}
	missing := map[string]struct{}{}
	for _, step := range steps {
		for _, name := range step.RequiredCredentials {
			if _, ok := configured[name]; !ok {
				missing[name] = struct{}{}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Errorf("%w: environment is missing required CredentialRefs: %s", domain.ErrInvalid, strings.Join(names, ", "))
}

func validatePlanHostGroups(raw json.RawMessage, steps []lockedStep) error {
	var document InventoryDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("%w: invalid environment inventory: %v", domain.ErrInvalid, err)
	}
	available := map[string]bool{"all": len(document.Hosts) > 0}
	for _, host := range document.Hosts {
		available[host.Name] = true
		for _, group := range host.Groups {
			available[group] = true
		}
	}
	for _, step := range steps {
		if step.Limit != "" && inventoryToken(step.Limit) && !available[step.Limit] {
			return fmt.Errorf("%w: host group or host %q required by %s is absent from the environment", domain.ErrInvalid, step.Limit, step.Name)
		}
	}
	return nil
}

func structToMap(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	var output map[string]any
	_ = json.Unmarshal(encoded, &output)
	return output
}

func mapToPlan(value map[string]any) (lockedPlan, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return lockedPlan{}, err
	}
	var plan lockedPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return plan, err
	}
	if len(plan.Steps) == 0 {
		return plan, errors.New("locked run plan contains no steps")
	}
	return plan, nil
}

func topologicalNodes(graph domain.ScenarioGraph) ([]domain.ScenarioNode, error) {
	byID := map[string]domain.ScenarioNode{}
	indegree := map[string]int{}
	adjacency := map[string][]string{}
	for _, node := range graph.Nodes {
		byID[node.ID], indegree[node.ID] = node, 0
	}
	for _, edge := range graph.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
		indegree[edge.Target]++
	}
	queue := make([]string, 0)
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	result := make([]domain.ScenarioNode, 0, len(graph.Nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		result = append(result, byID[id])
		for _, next := range adjacency[id] {
			indegree[next]--
			if indegree[next] == 0 {
				idx := sort.SearchStrings(queue, next)
				queue = append(queue, "")
				copy(queue[idx+1:], queue[idx:])
				queue[idx] = next
			}
		}
	}
	if len(result) != len(graph.Nodes) {
		return nil, fmt.Errorf("%w: scenario graph contains a cycle", domain.ErrInvalid)
	}
	return result, nil
}
