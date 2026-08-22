package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

type digestRunner interface {
	Digest(string) (playbookSHA256, treeSHA256 string, err error)
}

type planDigestRunner interface {
	DigestPlan([]string) (playbookSHA256 map[string]string, treeSHA256 string, err error)
}

type workspaceRunner interface {
	PrepareWorkspace(expectedTreeSHA256 string) (*ansiblerunner.Workspace, error)
	RunInWorkspace(context.Context, *ansiblerunner.Workspace, ansiblerunner.Request) (ansiblerunner.Result, error)
}

type lockedStep struct {
	ID                  string            `json:"id"`
	NodeID              string            `json:"nodeId"`
	Name                string            `json:"name"`
	ComponentName       string            `json:"componentName"`
	ReleaseID           string            `json:"releaseId"`
	Action              domain.ActionKind `json:"action"`
	Playbook            string            `json:"playbook"`
	PlaybookDigest      string            `json:"playbookDigest"`
	Tags                []string          `json:"tags"`
	Limit               string            `json:"limit"`
	Variables           map[string]any    `json:"variables"`
	RequiredCredentials []string          `json:"requiredCredentials"`
	TimeoutSeconds      int               `json:"timeoutSeconds"`
	NeedsApproval       bool              `json:"needsApproval"`
}

type lockedPlan struct {
	Steps      []lockedStep `json:"steps"`
	TreeDigest string       `json:"treeDigest"`
}

func (p *Platform) StartComponentTest(ctx context.Context, user domain.User, releaseID, environmentID string, runInput, dependencyFixtures map[string]any) (domain.Run, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return domain.Run{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return domain.Run{}, err
	}
	if user.Role != domain.RoleEnvironmentOwner {
		if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
			return domain.Run{}, err
		}
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return domain.Run{}, err
	}
	if environment.Revision == nil {
		return domain.Run{}, fmt.Errorf("%w: environment has no revision", domain.ErrConflict)
	}
	if err := validateEnvironmentConstraints(release.EnvironmentConstraints, environment.Revision.Facts); err != nil {
		return domain.Run{}, err
	}

	primary, found := primaryActionForComponentTest(release)
	if !found {
		return domain.Run{}, fmt.Errorf("%w: component test requires an executable primary action", domain.ErrInvalid)
	}
	actions := []domain.ActionDefinition{primary}
	if verify, ok := findAction(release, domain.ActionVerify); ok {
		actions = append(actions, verify)
	}
	variables, provenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, environment.Revision.Parameters, runInput, primary.AllowedParameters)
	if err != nil {
		return domain.Run{}, err
	}
	if err := applyDependencyFixtures(release, dependencyFixtures, variables, provenance); err != nil {
		return domain.Run{}, err
	}
	if err := validateResolvedParameters(release.Parameters, variables); err != nil {
		return domain.Run{}, err
	}
	steps := make([]lockedStep, 0, len(actions))
	nodeID := "component-" + component.ID
	for _, action := range actions {
		step, stepErr := p.lockAction(component.Name, nodeID, release, action, variables)
		if stepErr != nil {
			return domain.Run{}, stepErr
		}
		if action.Kind == domain.ActionVerify {
			step.NodeID = nodeID + "-verify"
		}
		steps = append(steps, step)
	}
	return p.createRun(ctx, user, environment, domain.RunComponentTest, release.ID, "", primary.Kind, steps, map[string]map[string]resolvedParameter{nodeID: provenance})
}

func primaryActionForComponentTest(release domain.ComponentRelease) (domain.ActionDefinition, bool) {
	for _, kind := range []domain.ActionKind{domain.ActionUpgrade, domain.ActionInstall, domain.ActionConfigure, domain.ActionPreflight, domain.ActionInspect} {
		if action, found := findAction(release, kind); found {
			return action, true
		}
	}
	return domain.ActionDefinition{}, false
}

func (p *Platform) StartScenarioTest(ctx context.Context, user domain.User, revisionID, environmentID string, runInput map[string]any) (domain.Run, error) {
	return p.startScenario(ctx, user, revisionID, environmentID, runInput, domain.RunScenarioTest)
}

func (p *Platform) StartScenarioRun(ctx context.Context, user domain.User, revisionID, environmentID string, runInput map[string]any) (domain.Run, error) {
	return p.startScenario(ctx, user, revisionID, environmentID, runInput, domain.RunScenario)
}

func (p *Platform) startScenario(ctx context.Context, user domain.User, revisionID, environmentID string, runInput map[string]any, kind domain.RunKind) (domain.Run, error) {
	revision, err := p.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return domain.Run{}, err
	}
	scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
	if err != nil {
		return domain.Run{}, err
	}
	if user.Role != domain.RoleEnvironmentOwner {
		if err := requireOwner(user, domain.RoleScenarioOwner, scenario.OwnerID); err != nil {
			return domain.Run{}, err
		}
	}
	if kind == domain.RunScenario && revision.Status != domain.RevisionReleased {
		return domain.Run{}, fmt.Errorf("%w: only a released scenario can run outside testing", domain.ErrConflict)
	}
	if kind == domain.RunScenarioTest && revision.Status == domain.RevisionReleased {
		return domain.Run{}, fmt.Errorf("%w: released scenario revisions are immutable; test a draft revision", domain.ErrConflict)
	}
	if kind == domain.RunScenarioTest && scenario.CurrentRevisionID != revisionID {
		return domain.Run{}, fmt.Errorf("%w: only the current scenario revision can be tested", domain.ErrConflict)
	}
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return domain.Run{}, err
	}
	if environment.Revision == nil {
		return domain.Run{}, fmt.Errorf("%w: environment has no revision", domain.ErrConflict)
	}
	validator := user
	if user.Role == domain.RoleEnvironmentOwner {
		validator = domain.User{ID: scenario.OwnerID, Role: domain.RoleScenarioOwner}
	}
	issues, validationErr := p.ValidateScenario(ctx, validator, revisionID)
	if validationErr != nil {
		return domain.Run{}, validationErr
	}
	if len(issues) > 0 {
		return domain.Run{}, &domain.ValidationError{Message: "scenario validation failed", Details: issues}
	}
	ordered, err := topologicalNodes(revision.Graph)
	if err != nil {
		return domain.Run{}, err
	}
	if err := validateScenarioRunInput(ordered, runInput); err != nil {
		return domain.Run{}, err
	}
	steps := make([]lockedStep, 0, len(ordered))
	releaseByNode := map[string]domain.ComponentRelease{}
	resolvedByNode := map[string]map[string]any{}
	provenanceByNode := map[string]map[string]resolvedParameter{}
	for _, node := range ordered {
		release, releaseErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil {
			return domain.Run{}, releaseErr
		}
		releaseByNode[node.ID] = release
	}
	for _, node := range ordered {
		release := releaseByNode[node.ID]
		if constraintErr := validateEnvironmentConstraints(release.EnvironmentConstraints, environment.Revision.Facts); constraintErr != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, constraintErr)
		}
		component, componentErr := p.store.GetComponent(ctx, release.ComponentID, false)
		if componentErr != nil {
			return domain.Run{}, componentErr
		}
		action, actionErr := actionFor(release, node.Action)
		if actionErr != nil {
			return domain.Run{}, actionErr
		}
		variables, provenance, resolveErr := resolveOwnParameters(release, node, environment.Revision.Parameters, runInputForNode(node, runInput), node.RunInputs)
		if resolveErr != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, resolveErr)
		}
		if mapErr := applyParameterMappings(release, node, revision.Graph, releaseByNode, resolvedByNode, variables, provenance); mapErr != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, mapErr)
		}
		if err := validateResolvedParameters(release.Parameters, variables); err != nil {
			return domain.Run{}, fmt.Errorf("node %s: %w", node.ID, err)
		}
		resolvedByNode[node.ID] = variables
		provenanceByNode[node.ID] = provenance
		if node.HostGroup != "" {
			action.Limit = node.HostGroup
		}
		step, stepErr := p.lockAction(component.Name, node.ID, release, action, variables)
		if stepErr != nil {
			return domain.Run{}, stepErr
		}
		steps = append(steps, step)
		if node.Action != domain.ActionVerify {
			verifyRelease, verifyVariables := release, variables
			if node.Action == domain.ActionRollback {
				// A rollback without version endpoints is the compensating job for
				// this release's installation. It verifies its own postcondition and
				// intentionally does not append the install-state verify action.
				if action.FromReleaseID == "" && action.ToReleaseID == "" {
					continue
				}
				target, targetErr := p.store.GetComponentRelease(ctx, action.ToReleaseID)
				if targetErr != nil || target.ComponentID != release.ComponentID || (target.Status != domain.ReleaseReleased && target.Status != domain.ReleaseDeprecated) {
					return domain.Run{}, fmt.Errorf("%w: rollback target must be a retained release of the same component", domain.ErrInvalid)
				}
				verifyRelease = target
				verifyVariables, _, targetErr = resolveOwnParameters(target, node, environment.Revision.Parameters, runInputForNode(node, runInput), node.RunInputs)
				if targetErr != nil {
					return domain.Run{}, fmt.Errorf("node %s rollback target: %w", node.ID, targetErr)
				}
				if mapErr := applyParameterMappings(target, node, revision.Graph, releaseByNode, resolvedByNode, verifyVariables, map[string]resolvedParameter{}); mapErr != nil {
					return domain.Run{}, fmt.Errorf("node %s rollback target: %w", node.ID, mapErr)
				}
				if err := validateResolvedParameters(target.Parameters, verifyVariables); err != nil {
					return domain.Run{}, fmt.Errorf("node %s rollback target: %w", node.ID, err)
				}
			}
			if verify, ok := findAction(verifyRelease, domain.ActionVerify); ok {
				verify.Limit = action.Limit
				verifyStep, verifyErr := p.lockAction(component.Name, node.ID+"-verify", verifyRelease, verify, verifyVariables)
				if verifyErr != nil {
					return domain.Run{}, verifyErr
				}
				steps = append(steps, verifyStep)
			}
		}
	}
	if kind == domain.RunScenarioTest {
		now := time.Now().UTC()
		if err := p.store.SetScenarioRevisionStatus(ctx, revisionID, []domain.RevisionStatus{domain.RevisionDraft, domain.RevisionTestPassed}, domain.RevisionTesting, now); err != nil {
			return domain.Run{}, err
		}
	}
	run, err := p.createRun(ctx, user, environment, kind, "", revision.ID, "", steps, provenanceByNode)
	if err != nil && kind == domain.RunScenarioTest {
		_ = p.store.SetScenarioRevisionStatus(ctx, revisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
	}
	return run, err
}

func findAction(release domain.ComponentRelease, kind domain.ActionKind) (domain.ActionDefinition, bool) {
	for _, action := range release.Actions {
		if action.Kind == kind {
			return action, true
		}
	}
	return domain.ActionDefinition{}, false
}

func matchesParameterType(value any, expected string) bool {
	if value == nil {
		return expected == "null"
	}
	kind := reflect.TypeOf(value).Kind()
	switch expected {
	case "string":
		return kind == reflect.String
	case "boolean":
		return kind == reflect.Bool
	case "object":
		return kind == reflect.Map
	case "array":
		return kind == reflect.Array || kind == reflect.Slice
	case "number":
		return isNumericKind(kind)
	case "integer":
		if !isNumericKind(kind) {
			return false
		}
		switch number := value.(type) {
		case float32:
			return math.Trunc(float64(number)) == float64(number)
		case float64:
			return math.Trunc(number) == number
		default:
			return true
		}
	case "null":
		return false
	default:
		// Parameter types are validated when the release contract is saved.
		return true
	}
}

func isNumericKind(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Float64
}

func containsParameterValue(values []any, value any) bool {
	for _, candidate := range values {
		if parameterValuesEqual(candidate, value) {
			return true
		}
	}
	return false
}

func parameterValuesEqual(left, right any) bool {
	if left != nil && right != nil && isNumericKind(reflect.TypeOf(left).Kind()) && isNumericKind(reflect.TypeOf(right).Kind()) {
		return numericValue(left) == numericValue(right)
	}
	return reflect.DeepEqual(left, right)
}

func numericValue(value any) float64 {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(reflected.Uint())
	default:
		return reflected.Float()
	}
}

func validateScenarioRunInput(nodes []domain.ScenarioNode, runInput map[string]any) error {
	allowed := map[string]struct{}{}
	for _, node := range nodes {
		for _, key := range node.RunInputs {
			allowed[key] = struct{}{}
		}
	}
	for key := range runInput {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%w: run input %q was not declared by the scenario", domain.ErrInvalid, key)
		}
	}
	return nil
}

func runInputForNode(node domain.ScenarioNode, runInput map[string]any) map[string]any {
	selected := map[string]any{}
	for _, key := range node.RunInputs {
		if value, ok := runInput[key]; ok {
			selected[key] = value
		}
	}
	return selected
}

func resolveEnvironmentBinding(environment map[string]any, key string) (any, bool) {
	if value, ok := environment[key]; ok {
		return value, true
	}
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return nil, false
	}
	var current any = environment
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return deepCopy(current), true
}

func validateEnvironmentConstraints(constraints, facts map[string]any) error {
	aliases := map[string][]string{
		"architecture":    {"architecture", "arch"},
		"arch":            {"architecture", "arch"},
		"operatingSystem": {"operatingSystem", "os", "distribution"},
		"os":              {"operatingSystem", "os", "distribution"},
		"ipFamily":        {"ipFamily", "network"},
		"network":         {"ipFamily", "network"},
	}
	for key, expected := range constraints {
		factKeys := aliases[key]
		if len(factKeys) == 0 {
			factKeys = []string{key}
		}
		var actual any
		found := false
		for _, factKey := range factKeys {
			if value, ok := facts[factKey]; ok {
				actual, found = value, true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: environment fact %q is required by the component", domain.ErrInvalid, key)
		}
		if !constraintMatches(expected, actual) {
			return fmt.Errorf("%w: environment fact %q=%v does not satisfy %v", domain.ErrInvalid, key, actual, expected)
		}
	}
	return nil
}

func constraintMatches(expected, actual any) bool {
	normalize := func(value any) string { return strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) }
	switch values := expected.(type) {
	case []any:
		for _, value := range values {
			if normalize(value) == normalize(actual) {
				return true
			}
		}
		return false
	case []string:
		for _, value := range values {
			if normalize(value) == normalize(actual) {
				return true
			}
		}
		return false
	default:
		return normalize(expected) == normalize(actual)
	}
}

func (p *Platform) lockAction(componentName, nodeID string, release domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) (lockedStep, error) {
	step := lockedStep{
		ID: newID("locked-step"), NodeID: nodeID, Name: componentName + " · " + string(action.Kind), ComponentName: componentName,
		ReleaseID: release.ID, Action: action.Kind, Playbook: action.Playbook, Tags: append([]string(nil), action.Tags...),
		Limit: valueOr(action.Limit, action.HostGroup), Variables: cloneMap(variables),
		RequiredCredentials: append([]string(nil), action.RequiredCredentials...), TimeoutSeconds: action.TimeoutSeconds,
		NeedsApproval: action.NeedsApproval(),
	}
	return step, nil
}

func (p *Platform) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, steps []lockedStep, resolvedParametersByNode map[string]map[string]resolvedParameter) (domain.Run, error) {
	if p.runner == nil {
		return domain.Run{}, fmt.Errorf("%w: Ansible runner is not configured", domain.ErrConflict)
	}
	if environment.Revision == nil {
		return domain.Run{}, fmt.Errorf("%w: environment revision is required", domain.ErrInvalid)
	}
	for _, step := range steps {
		if err := rejectSensitiveMap(step.Variables, "run parameter"); err != nil {
			return domain.Run{}, err
		}
	}
	if err := validateRequiredCredentials(environment.Revision.CredentialRefs, steps); err != nil {
		return domain.Run{}, err
	}
	if err := validatePlanHostGroups(environment.Revision.Inventory, steps); err != nil {
		return domain.Run{}, err
	}
	plan := lockedPlan{Steps: append([]lockedStep(nil), steps...)}
	if digester, ok := p.runner.(planDigestRunner); ok {
		playbooks := make([]string, 0, len(plan.Steps))
		for _, step := range plan.Steps {
			playbooks = append(playbooks, step.Playbook)
		}
		digests, treeDigest, err := digester.DigestPlan(playbooks)
		if err != nil {
			return domain.Run{}, err
		}
		plan.TreeDigest = treeDigest
		for i := range plan.Steps {
			plan.Steps[i].PlaybookDigest = digests[plan.Steps[i].Playbook]
		}
	} else if digester, ok := p.runner.(digestRunner); ok {
		for i := range plan.Steps {
			playbookDigest, treeDigest, err := digester.Digest(plan.Steps[i].Playbook)
			if err != nil {
				return domain.Run{}, err
			}
			if plan.TreeDigest != "" && plan.TreeDigest != treeDigest {
				return domain.Run{}, fmt.Errorf("%w: playbooks resolved to different executable trees", domain.ErrConflict)
			}
			plan.Steps[i].PlaybookDigest = playbookDigest
			plan.TreeDigest = treeDigest
		}
	}
	snapshot := structToMap(plan)
	if len(resolvedParametersByNode) > 0 {
		snapshot["resolvedParametersByNode"] = provenanceSnapshot(resolvedParametersByNode)
	}
	if releaseID != "" {
		release, err := p.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return domain.Run{}, err
		}
		snapshot["componentReleaseSpecDigest"] = componentReleaseSpecDigest(release)
	}
	redactedRefs := make([]domain.CredentialRef, 0)
	if environment.Revision != nil {
		redactedRefs = domain.RedactCredentialRefs(environment.Revision.CredentialRefs, false)
	}
	snapshot["environmentRevisionId"] = environment.CurrentRevisionID
	snapshot["credentialRefs"] = redactedRefs
	destructive := false
	for _, step := range plan.Steps {
		if step.NeedsApproval {
			destructive = true
			break
		}
	}
	status := domain.RunQueued
	var approval *domain.Approval
	now := time.Now().UTC()
	run := domain.Run{
		ID: newID("run"), Kind: kind, Status: status, RequestedBy: user.ID,
		EnvironmentID: environment.ID, EnvironmentRevisionID: environment.CurrentRevisionID,
		ComponentReleaseID: releaseID, ScenarioRevisionID: revisionID, Action: action,
		Destructive: destructive, InputSnapshot: snapshot, ArtifactDigest: plan.TreeDigest, CreatedAt: now,
	}
	if destructive {
		run.Status = domain.RunAwaitingApproval
		approval = &domain.Approval{ID: newID("approval"), RunID: run.ID, Status: "pending", RequestedAt: now}
		run.Approval = approval
	}
	if err := p.store.CreateRun(ctx, run, approval); err != nil {
		return run, err
	}
	p.audit(ctx, user, "run.created", "run", run.ID, map[string]any{
		"kind": kind, "environmentId": environment.ID, "environmentRevisionId": environment.CurrentRevisionID,
		"componentReleaseId": releaseID, "scenarioRevisionId": revisionID, "artifactDigest": plan.TreeDigest, "destructive": destructive,
	})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	if !destructive {
		p.schedule(environment.ID)
	}
	return run, nil
}

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

func (p *Platform) schedule(environmentID string) {
	p.mu.Lock()
	if _, running := p.workers[environmentID]; running {
		p.mu.Unlock()
		return
	}
	p.workers[environmentID] = struct{}{}
	p.mu.Unlock()
	go p.environmentWorker(environmentID)
}

func (p *Platform) environmentWorker(environmentID string) {
	defer func() {
		p.mu.Lock()
		delete(p.workers, environmentID)
		p.mu.Unlock()
		// Close the narrow hand-off window where a run is queued after this
		// worker's final empty claim but before it removes itself. Any enqueue
		// after the removal schedules its own worker; an enqueue before removal
		// is discovered here.
		if p.rootCtx.Err() == nil {
			queued, err := p.store.ListQueuedEnvironmentIDs(context.Background())
			if err == nil {
				for _, id := range queued {
					if id == environmentID {
						p.schedule(environmentID)
						break
					}
				}
			}
		}
	}()
	for {
		if p.rootCtx.Err() != nil {
			return
		}
		run, err := p.store.ClaimNextRun(p.rootCtx, environmentID, time.Now().UTC())
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			return
		}
		if err != nil {
			p.hub.Publish("run.worker_error", map[string]any{"environmentId": environmentID, "error": err.Error()})
			return
		}
		p.executeRun(run)
	}
}

func (p *Platform) executeRun(run domain.Run) {
	ctx, cancel := context.WithCancel(p.rootCtx)
	p.mu.Lock()
	p.active[run.ID] = cancel
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.active, run.ID)
		p.mu.Unlock()
	}()

	plan, err := mapToPlan(run.InputSnapshot)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	environmentRevision, err := p.store.GetEnvironmentRevision(ctx, run.EnvironmentRevisionID)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	inventory, err := renderInventory(environmentRevision.Inventory)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	credentialVariables, secrets, err := resolveCredentialRefs(environmentRevision.CredentialRefs)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	var sharedWorkspace *ansiblerunner.Workspace
	preparedRunner, supportsSharedWorkspace := p.runner.(workspaceRunner)
	if supportsSharedWorkspace {
		sharedWorkspace, err = preparedRunner.PrepareWorkspace(run.ArtifactDigest)
		if err != nil {
			p.finishRun(run, domain.RunFailed, err)
			return
		}
		defer sharedWorkspace.Close()
	}

	for _, locked := range plan.Steps {
		if ctx.Err() != nil {
			p.finishRun(run, domain.RunCancelled, ctx.Err())
			return
		}
		now := time.Now().UTC()
		step := domain.RunStep{ID: newID("step"), RunID: run.ID, NodeID: locked.NodeID, Name: locked.Name, Status: domain.RunRunning, StartedAt: &now}
		if err := p.store.CreateRunStep(ctx, step); err != nil {
			p.finishRun(run, domain.RunFailed, err)
			return
		}
		variables := cloneMap(locked.Variables)
		mergeMap(variables, credentialVariables)
		request := ansiblerunner.Request{
			Playbook: locked.Playbook, Inventory: inventory, Variables: variables, SecretValues: secrets,
			Limit: locked.Limit, Tags: locked.Tags, Timeout: time.Duration(locked.TimeoutSeconds) * time.Second,
			ExpectedPlaybookSHA256: locked.PlaybookDigest, ExpectedTreeSHA256: run.ArtifactDigest,
			LogSink: func(event ansiblerunner.LogEvent) {
				_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{
					RunID: run.ID, StepID: step.ID, Stream: string(event.Stream), Message: event.Line, CreatedAt: event.Time,
				})
				p.hub.Publish("run.log", map[string]any{"runId": run.ID, "stepId": step.ID, "stream": event.Stream, "line": event.Line})
			},
		}
		var result ansiblerunner.Result
		var runErr error
		if supportsSharedWorkspace {
			result, runErr = preparedRunner.RunInWorkspace(ctx, sharedWorkspace, request)
		} else {
			result, runErr = p.runner.Run(ctx, request)
		}
		exitCode := 0
		if len(result.Phases) > 0 {
			exitCode = result.Phases[len(result.Phases)-1].ExitCode
		}
		step.ExitCode = &exitCode
		finished := time.Now().UTC()
		step.FinishedAt = &finished
		if runErr != nil {
			step.Status = domain.RunFailed
			step.Summary = runErr.Error()
			if errors.Is(ctx.Err(), context.Canceled) {
				step.Status = domain.RunCancelled
			}
			if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
				log.Printf("run %s: update step %s: %v", run.ID, step.ID, stepErr)
			}
			if step.Status == domain.RunCancelled {
				p.finishRun(run, domain.RunCancelled, ctx.Err())
			} else {
				p.finishRun(run, domain.RunFailed, runErr)
			}
			return
		}
		if run.ArtifactDigest != "" && result.TreeSHA256 != run.ArtifactDigest {
			step.Status, step.Summary = domain.RunFailed, "playbook tree digest changed after the run was queued"
			if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
				log.Printf("run %s: update step %s: %v", run.ID, step.ID, stepErr)
			}
			p.finishRun(run, domain.RunFailed, errors.New(step.Summary))
			return
		}
		step.Status = domain.RunSucceeded
		step.Summary = recapSummary(result.Recap)
		if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
			log.Printf("run %s: update step %s: %v", run.ID, step.ID, stepErr)
		}
		p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "stepId": step.ID, "status": step.Status})
	}
	p.finishRun(run, domain.RunSucceeded, nil)
}

func (p *Platform) finishRun(run domain.Run, status domain.RunStatus, cause error) {
	errText := ""
	if cause != nil {
		errText = Redact(cause.Error()).(string)
	}
	if err := p.store.UpdateRunStatus(context.Background(), run.ID, []domain.RunStatus{domain.RunRunning}, status, errText, time.Now().UTC()); err != nil {
		log.Printf("finishRun %s: update status to %s: %v", run.ID, status, err)
	}
	if run.Kind == domain.RunComponentTest && status == domain.RunSucceeded {
		lockedDigest, _ := run.InputSnapshot["componentReleaseSpecDigest"].(string)
		current, currentErr := p.store.GetComponentRelease(context.Background(), run.ComponentReleaseID)
		if currentErr == nil && lockedDigest != "" && componentReleaseSpecDigest(current) == lockedDigest {
			if err := p.store.MarkReleaseVerified(context.Background(), run.ComponentReleaseID, true); err != nil {
				log.Printf("finishRun %s: mark release verified: %v", run.ID, err)
			}
		}
	}
	if run.Kind == domain.RunScenarioTest {
		if status == domain.RunSucceeded {
			if err := p.store.SetScenarioRevisionStatus(context.Background(), run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionTestPassed, time.Now().UTC()); err != nil {
				log.Printf("finishRun %s: set scenario revision test_passed: %v", run.ID, err)
			}
		} else {
			if err := p.store.SetScenarioRevisionStatus(context.Background(), run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC()); err != nil {
				log.Printf("finishRun %s: reset scenario revision to draft: %v", run.ID, err)
			}
		}
	}
	actor, _ := p.store.GetUser(context.Background(), run.RequestedBy)
	p.audit(context.Background(), actor, "run.finished", "run", run.ID, map[string]any{"status": status, "error": errText})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": status})
}

func componentReleaseSpecDigest(release domain.ComponentRelease) string {
	type dependencySpec struct {
		UpstreamComponentID string                    `json:"upstreamComponentId"`
		UpstreamReleaseID   string                    `json:"upstreamReleaseId"`
		Purpose             string                    `json:"purpose"`
		ParameterMappings   []domain.ParameterMapping `json:"parameterMappings"`
	}
	type actionSpec struct {
		Name                string            `json:"name"`
		Kind                domain.ActionKind `json:"kind"`
		Playbook            string            `json:"playbook"`
		Tags                []string          `json:"tags"`
		Limit               string            `json:"limit"`
		HostGroup           string            `json:"hostGroup"`
		AllowedParameters   []string          `json:"allowedParameters"`
		RequiredCredentials []string          `json:"requiredCredentials"`
		TimeoutSeconds      int               `json:"timeoutSeconds"`
		RiskLevel           domain.RiskLevel  `json:"riskLevel"`
		Destructive         bool              `json:"destructive"`
		FromReleaseID       string            `json:"fromReleaseId"`
		ToReleaseID         string            `json:"toReleaseId"`
	}
	spec := struct {
		Version                string                       `json:"version"`
		Type                   domain.ReleaseType           `json:"type"`
		ReleaseNotes           string                       `json:"releaseNotes"`
		Breaking               bool                         `json:"breaking"`
		RiskLevel              domain.RiskLevel             `json:"riskLevel"`
		EnvironmentConstraints map[string]any               `json:"environmentConstraints"`
		Parameters             []domain.ParameterDefinition `json:"parameters"`
		Dependencies           []dependencySpec             `json:"dependencies"`
		Actions                []actionSpec                 `json:"actions"`
	}{
		Version: release.Version, Type: release.Type, ReleaseNotes: release.ReleaseNotes,
		Breaking: release.Breaking, RiskLevel: release.RiskLevel,
		EnvironmentConstraints: release.EnvironmentConstraints, Parameters: release.Parameters,
	}
	for _, dependency := range release.Dependencies {
		spec.Dependencies = append(spec.Dependencies, dependencySpec{
			UpstreamComponentID: dependency.UpstreamComponentID, UpstreamReleaseID: dependency.UpstreamReleaseID, Purpose: dependency.Purpose,
			ParameterMappings: dependency.ParameterMappings,
		})
	}
	for _, action := range release.Actions {
		spec.Actions = append(spec.Actions, actionSpec{
			Name: action.Name, Kind: action.Kind, Playbook: action.Playbook, Tags: action.Tags,
			Limit: action.Limit, HostGroup: action.HostGroup, AllowedParameters: action.AllowedParameters,
			RequiredCredentials: action.RequiredCredentials,
			TimeoutSeconds:      action.TimeoutSeconds, RiskLevel: action.RiskLevel, Destructive: action.Destructive,
			FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		})
	}
	encoded, _ := json.Marshal(spec)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func (p *Platform) CancelRun(ctx context.Context, user domain.User, runID string) (domain.Run, error) {
	run, err := p.store.GetRun(ctx, runID)
	if err != nil {
		return run, err
	}
	if user.ID != run.RequestedBy {
		environment, environmentErr := p.store.GetEnvironment(ctx, run.EnvironmentID, false)
		if environmentErr != nil {
			return run, environmentErr
		}
		if user.Role != domain.RoleEnvironmentOwner || environment.OwnerID != user.ID {
			return run, domain.ErrForbidden
		}
	}
	switch run.Status {
	case domain.RunQueued, domain.RunAwaitingApproval:
		if err := p.store.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{run.Status}, domain.RunCancelled, "cancelled by user", time.Now().UTC()); err != nil {
			return run, err
		}
		run.Status = domain.RunCancelled
		if run.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
	case domain.RunRunning:
		p.mu.Lock()
		cancel := p.active[run.ID]
		p.mu.Unlock()
		if cancel == nil {
			return run, fmt.Errorf("%w: active process is not attached to this server", domain.ErrConflict)
		}
		cancel()
	default:
		return run, fmt.Errorf("%w: run is already terminal", domain.ErrConflict)
	}
	p.audit(ctx, user, "run.cancel_requested", "run", run.ID, nil)
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	return run, nil
}

func (p *Platform) DecideApproval(ctx context.Context, user domain.User, approvalID, decision, reason string) (domain.Run, error) {
	if user.Role != domain.RoleEnvironmentOwner {
		return domain.Run{}, fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
	}
	approval, err := p.store.GetApproval(ctx, approvalID)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := p.store.GetRun(ctx, approval.RunID)
	if err != nil {
		return run, err
	}
	environment, err := p.store.GetEnvironment(ctx, run.EnvironmentID, false)
	if err != nil {
		return run, err
	}
	if environment.OwnerID != user.ID {
		return run, fmt.Errorf("%w: approval belongs to another owner's environment", domain.ErrForbidden)
	}
	if err := p.store.DecideApproval(ctx, approvalID, user.ID, decision, reason, time.Now().UTC()); err != nil {
		return run, err
	}
	if decision == "approved" {
		run.Status = domain.RunQueued
		p.schedule(run.EnvironmentID)
	} else {
		run.Status = domain.RunRejected
		if run.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
	}
	p.audit(ctx, user, "approval."+decision, "approval", approvalID, map[string]any{"runId": run.ID, "reason": reason})
	p.hub.Publish("approval.updated", map[string]any{"approvalId": approvalID, "runId": run.ID, "decision": decision})
	return p.store.GetRun(ctx, run.ID)
}

func renderInventory(raw json.RawMessage) ([]byte, error) {
	var document InventoryDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%w: invalid environment inventory: %v", domain.ErrInvalid, err)
	}
	if len(document.Hosts) == 0 {
		return nil, fmt.Errorf("%w: inventory has no hosts", domain.ErrInvalid)
	}
	groups := map[string][]string{}
	var builder strings.Builder
	builder.WriteString("[all]\n")
	for _, host := range document.Hosts {
		if !inventoryToken(host.Name) || !inventoryToken(host.Address) {
			return nil, fmt.Errorf("%w: inventory host contains unsafe characters", domain.ErrInvalid)
		}
		builder.WriteString(host.Name)
		if host.Address == "localhost" || host.Address == "127.0.0.1" || host.Address == "::1" {
			builder.WriteString(" ansible_connection=local")
		} else {
			builder.WriteString(" ansible_host=" + host.Address)
			if host.User != "" {
				if !inventoryToken(host.User) {
					return nil, fmt.Errorf("%w: unsafe inventory user", domain.ErrInvalid)
				}
				builder.WriteString(" ansible_user=" + host.User)
			}
			if host.Port > 0 {
				builder.WriteString(" ansible_port=" + strconv.Itoa(host.Port))
			}
		}
		builder.WriteByte('\n')
		for _, group := range host.Groups {
			if !inventoryToken(group) {
				return nil, fmt.Errorf("%w: unsafe inventory group", domain.ErrInvalid)
			}
			groups[group] = append(groups[group], host.Name)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		if key != "all" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, group := range keys {
		builder.WriteString("\n[" + group + "]\n")
		for _, host := range groups[group] {
			builder.WriteString(host + "\n")
		}
	}
	return []byte(builder.String()), nil
}

func inventoryToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func resolveCredentialRefs(refs []domain.CredentialRef) (map[string]any, []string, error) {
	variables := map[string]any{}
	var secrets []string
	for _, ref := range refs {
		switch ref.Kind {
		case "envVarRef":
			value, ok := os.LookupEnv(ref.Reference)
			if !ok || value == "" {
				return nil, nil, fmt.Errorf("credential %q envVarRef is not configured", ref.Name)
			}
			variables[ref.Name] = value
			secrets = append(secrets, value)
		case "sshKeyPath":
			if _, err := os.Stat(ref.Reference); err != nil {
				return nil, nil, fmt.Errorf("credential SSH key %s: %w", ref.Name, err)
			}
			variables[ref.Name] = ref.Reference
		default:
			return nil, nil, fmt.Errorf("unsupported credential reference kind %q", ref.Kind)
		}
	}
	return variables, secrets, nil
}

func recapSummary(recap map[string]ansiblerunner.HostRecap) string {
	if len(recap) == 0 {
		return "Ansible completed successfully"
	}
	changed, okCount := 0, 0
	for _, host := range recap {
		changed += host.Changed
		okCount += host.OK
	}
	return fmt.Sprintf("recap: ok=%d changed=%d hosts=%d", okCount, changed, len(recap))
}
