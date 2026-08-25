package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
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
	ID                  string                 `json:"id"`
	NodeID              string                 `json:"nodeId"`
	Name                string                 `json:"name"`
	ComponentID         string                 `json:"componentId"`
	ComponentName       string                 `json:"componentName"`
	ReleaseID           string                 `json:"releaseId"`
	ReleaseVersion      string                 `json:"releaseVersion"`
	ReleaseSpecDigest   string                 `json:"releaseSpecDigest"`
	ActionID            string                 `json:"actionId"`
	Action              domain.ActionKind      `json:"action"`
	FromReleaseID       string                 `json:"fromReleaseId,omitempty"`
	ToReleaseID         string                 `json:"toReleaseId,omitempty"`
	Playbook            string                 `json:"playbook"`
	PlaybookDigest      string                 `json:"playbookDigest"`
	Tags                []string               `json:"tags"`
	Limit               string                 `json:"limit"`
	Variables           map[string]any         `json:"variables"`
	RequiredCredentials []string               `json:"requiredCredentials"`
	TimeoutSeconds      int                    `json:"timeoutSeconds"`
	NeedsApproval       bool                   `json:"needsApproval"`
	BackupRef           string                 `json:"backupRef,omitempty"`
	Backup              *domain.BackupMetadata `json:"backup,omitempty"`
}

type lockedPlan struct {
	Steps                      []lockedStep                 `json:"steps"`
	ArtifactTransfers          []lockedArtifactTransfer     `json:"artifactTransfers,omitempty"`
	ImageTransfers             []lockedImageTransfer        `json:"imageTransfers,omitempty"`
	TreeDigest                 string                       `json:"treeDigest"`
	InstallationBaseline       []lockedInstallationBaseline `json:"installationBaseline,omitempty"`
	InstallationBaselineDigest string                       `json:"installationBaselineDigest,omitempty"`
}

type lockedInstallationBaseline struct {
	ComponentID    string `json:"componentId"`
	ReleaseID      string `json:"releaseId"`
	InstallRunID   string `json:"installRunId"`
	BackupRef      string `json:"backupRef"`
	PlaybookSHA256 string `json:"playbookSha256"`
}

type lockedArtifactTransfer struct {
	Alias         string `json:"alias"`
	SourceStation string `json:"sourceStation"`
	TargetStation string `json:"targetStation"`
	RelativePath  string `json:"relativePath"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
}

type lockedImageTransfer struct {
	SourceRegistry string `json:"sourceRegistry"`
	TargetRegistry string `json:"targetRegistry"`
	SourceDigest   string `json:"sourceDigest"`
	TargetRef      string `json:"targetRef"`
	TargetDigest   string `json:"targetDigest"`
}

type ComponentTestMode string

type RollbackVerificationKind string

const (
	ComponentTestInstallVerify ComponentTestMode = "install_verify"
	ComponentTestRollback      ComponentTestMode = "rollback"

	RollbackVerificationTargetRelease RollbackVerificationKind = "target_release"
	RollbackVerificationOnly          RollbackVerificationKind = "rollback_only"
)

type RollbackVerification struct {
	Kind      RollbackVerificationKind `json:"kind"`
	ReleaseID string                   `json:"releaseId,omitempty"`
}

type ComponentTestRequest struct {
	EnvironmentID        string               `json:"environmentId"`
	Mode                 ComponentTestMode    `json:"mode"`
	RollbackVerification RollbackVerification `json:"rollbackVerification"`
	RunInput             map[string]any       `json:"runInput"`
	DependencyFixtures   map[string]any       `json:"dependencyFixtures"`
	ExpectedPlanDigest   string               `json:"expectedPlanDigest,omitempty"`
}

type ComponentTestPlanStep struct {
	Order              int               `json:"order"`
	ComponentID        string            `json:"componentId"`
	ComponentName      string            `json:"componentName"`
	ReleaseID          string            `json:"releaseId"`
	ReleaseVersion     string            `json:"releaseVersion"`
	Action             domain.ActionKind `json:"action"`
	Playbook           string            `json:"playbook"`
	Limit              string            `json:"limit,omitempty"`
	NeedsApproval      bool              `json:"needsApproval"`
	FromReleaseID      string            `json:"fromReleaseId,omitempty"`
	FromReleaseVersion string            `json:"fromReleaseVersion,omitempty"`
	ToReleaseID        string            `json:"toReleaseId,omitempty"`
	ToReleaseVersion   string            `json:"toReleaseVersion,omitempty"`
	BackupRef          string            `json:"backupRef,omitempty"`
	BackupInstallRunID string            `json:"backupInstallRunId,omitempty"`
	BackupCapturedAt   *time.Time        `json:"backupCapturedAt,omitempty"`
	BackupPlaybookSHA  string            `json:"backupPlaybookSha256,omitempty"`
}

type ComponentTestPlan struct {
	EnvironmentID         string                  `json:"environmentId"`
	EnvironmentRevisionID string                  `json:"environmentRevisionId"`
	Destructive           bool                    `json:"destructive"`
	RequiresApproval      bool                    `json:"requiresApproval"`
	PlanDigest            string                  `json:"planDigest"`
	Steps                 []ComponentTestPlanStep `json:"steps"`
}

type preparedComponentTest struct {
	release     domain.ComponentRelease
	environment domain.Environment
	action      domain.ActionKind
	steps       []lockedStep
	provenance  map[string]map[string]resolvedParameter
}

func (p *Platform) PreviewComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (ComponentTestPlan, error) {
	prepared, err := p.prepareComponentTest(ctx, user, releaseID, input)
	if err != nil {
		return ComponentTestPlan{}, err
	}
	plan, digest, destructive, err := p.prepareLockedPlan(ctx, prepared.environment, domain.RunComponentTest, "preview", time.Time{}, prepared.steps)
	if err != nil {
		return ComponentTestPlan{}, err
	}
	return p.componentTestPlanDTO(ctx, prepared.environment, plan, digest, destructive), nil
}

func (p *Platform) StartComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (domain.Run, error) {
	prepared, err := p.prepareComponentTest(ctx, user, releaseID, input)
	if err != nil {
		return domain.Run{}, err
	}
	return p.createRun(ctx, user, prepared.environment, domain.RunComponentTest, prepared.release.ID, "", prepared.action, prepared.steps, prepared.provenance, input.ExpectedPlanDigest)
}

func (p *Platform) prepareComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (preparedComponentTest, error) {
	release, err := p.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return preparedComponentTest{}, err
	}
	component, err := p.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if user.Role != domain.RoleEnvironmentOwner {
		if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
			return preparedComponentTest{}, err
		}
	}
	environment, err := p.store.GetEnvironment(ctx, input.EnvironmentID, false)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if environment.Revision == nil {
		return preparedComponentTest{}, fmt.Errorf("%w: environment has no revision", domain.ErrConflict)
	}
	if err := validateEnvironmentConstraints(release.EnvironmentConstraints, environment.Revision.Facts); err != nil {
		return preparedComponentTest{}, err
	}

	if input.Mode == "" {
		input.Mode = ComponentTestInstallVerify
	}
	var selected domain.ActionDefinition
	switch input.Mode {
	case ComponentTestInstallVerify:
		if input.RollbackVerification.Kind != "" || input.RollbackVerification.ReleaseID != "" {
			return preparedComponentTest{}, fmt.Errorf("%w: rollbackVerification is only valid for rollback tests", domain.ErrInvalid)
		}
		selected, _ = primaryActionForComponentTest(release)
		if selected.Kind == "" {
			return preparedComponentTest{}, fmt.Errorf("%w: install verification requires an executable primary action", domain.ErrInvalid)
		}
	case ComponentTestRollback:
		selected, _ = findAction(release, domain.ActionRollback)
		if selected.Kind == "" {
			return preparedComponentTest{}, fmt.Errorf("%w: rollback verification requires a rollback action", domain.ErrInvalid)
		}
	default:
		return preparedComponentTest{}, fmt.Errorf("%w: unsupported component test mode %q", domain.ErrInvalid, input.Mode)
	}
	variables, provenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, input.RunInput, selected.AllowedParameters)
	if err != nil {
		return preparedComponentTest{}, err
	}
	if err := applyDependencyFixtures(release, input.DependencyFixtures, variables, provenance); err != nil {
		return preparedComponentTest{}, err
	}
	if err := validateResolvedParameters(release.Parameters, variables); err != nil {
		return preparedComponentTest{}, err
	}
	steps := make([]lockedStep, 0, 2)
	nodeID := "component-" + component.ID
	step, err := p.lockAction(component, nodeID, release, selected, variables)
	if err != nil {
		return preparedComponentTest{}, err
	}
	steps = append(steps, step)
	if input.Mode == ComponentTestInstallVerify {
		if verify, ok := findAction(release, domain.ActionVerify); ok {
			verifyStep, stepErr := p.lockAction(component, nodeID+"-verify", release, verify, variables)
			if stepErr != nil {
				return preparedComponentTest{}, stepErr
			}
			steps = append(steps, verifyStep)
		}
	} else {
		if (selected.FromReleaseID == "") != (selected.ToReleaseID == "") {
			return preparedComponentTest{}, fmt.Errorf("%w: rollback action must define both fromReleaseId and toReleaseId, or neither", domain.ErrInvalid)
		}
		if selected.FromReleaseID != "" && (selected.FromReleaseID != release.ID || selected.ToReleaseID == release.ID) {
			return preparedComponentTest{}, fmt.Errorf("%w: rollback action must point from this draft to an earlier release", domain.ErrInvalid)
		}
		verification := input.RollbackVerification
		if verification.Kind == "" {
			if selected.ToReleaseID == "" {
				verification.Kind = RollbackVerificationOnly
			} else {
				verification.Kind, verification.ReleaseID = RollbackVerificationTargetRelease, selected.ToReleaseID
			}
		}
		switch verification.Kind {
		case RollbackVerificationOnly:
			if verification.ReleaseID != "" {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback_only must not include a releaseId", domain.ErrInvalid)
			}
		case RollbackVerificationTargetRelease:
			if verification.ReleaseID == "" {
				return preparedComponentTest{}, fmt.Errorf("%w: target_release requires a releaseId", domain.ErrInvalid)
			}
			target, targetErr := p.store.GetComponentRelease(ctx, verification.ReleaseID)
			if targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback verify target must be a retained release of the same component", domain.ErrInvalid)
			}
			if target.ComponentID != release.ComponentID || (target.Status != domain.ReleaseReleased && target.Status != domain.ReleaseDeprecated) {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback verify target must be a retained release of the same component", domain.ErrInvalid)
			}
			verify, ok := findAction(target, domain.ActionVerify)
			if !ok {
				return preparedComponentTest{}, fmt.Errorf("%w: rollback verify target must define a verify action", domain.ErrInvalid)
			}
			targetVariables, _, targetErr := resolveOwnParameters(target, domain.ScenarioNode{}, input.RunInput, selected.AllowedParameters)
			if targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("rollback target: %w", targetErr)
			}
			targetProvenance := map[string]resolvedParameter{}
			if targetErr = applyDependencyFixtures(target, input.DependencyFixtures, targetVariables, targetProvenance); targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("rollback target: %w", targetErr)
			}
			if targetErr = validateResolvedParameters(target.Parameters, targetVariables); targetErr != nil {
				return preparedComponentTest{}, fmt.Errorf("rollback target: %w", targetErr)
			}
			verify.Limit = selected.Limit
			verifyStep, stepErr := p.lockAction(component, nodeID+"-verify", target, verify, targetVariables)
			if stepErr != nil {
				return preparedComponentTest{}, stepErr
			}
			steps = append(steps, verifyStep)
		default:
			return preparedComponentTest{}, fmt.Errorf("%w: unsupported rollback verification kind %q", domain.ErrInvalid, verification.Kind)
		}
	}
	return preparedComponentTest{
		release: release, environment: environment, action: selected.Kind, steps: steps,
		provenance: map[string]map[string]resolvedParameter{nodeID: provenance},
	}, nil
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
		base := &domain.ValidationError{Message: "scenario validation failed", Details: issues}
		return domain.Run{}, actionableExistingError(base, "scenario.graph_invalid", "当前 DAG 或锁定 Release 未通过校验", "检查场景问题", fmt.Sprintf("/scenarios?selected=%s&revision=%s&action=inspect", scenario.ID, revision.ID))
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
		variables, provenance, resolveErr := resolveOwnParameters(release, node, runInputForNode(node, runInput), node.RunInputs)
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
		step, stepErr := p.lockAction(component, node.ID, release, action, variables)
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
				verifyVariables, _, targetErr = resolveOwnParameters(target, node, runInputForNode(node, runInput), node.RunInputs)
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
				verifyStep, verifyErr := p.lockAction(component, node.ID+"-verify", verifyRelease, verify, verifyVariables)
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
	run, err := p.createRun(ctx, user, environment, kind, "", revision.ID, "", steps, provenanceByNode, "")
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

func validateEnvironmentConstraints(constraints, facts map[string]any) error {
	for key, expected := range constraints {
		actual, found := facts[key]
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
	switch values := expected.(type) {
	case []any:
		for _, value := range values {
			if reflect.DeepEqual(value, actual) {
				return true
			}
		}
		return false
	case []string:
		for _, value := range values {
			if reflect.DeepEqual(value, actual) {
				return true
			}
		}
		return false
	default:
		return reflect.DeepEqual(expected, actual)
	}
}

func (p *Platform) lockAction(component domain.Component, nodeID string, release domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) (lockedStep, error) {
	step := lockedStep{
		ID: newID("locked-step"), NodeID: nodeID, Name: component.Name + " · " + string(action.Kind),
		ComponentID: component.ID, ComponentName: component.Name, ReleaseID: release.ID, ReleaseVersion: release.Version,
		ReleaseSpecDigest: componentReleaseSpecDigest(release), ActionID: action.ID,
		Action: action.Kind, FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		Playbook: action.Playbook, Tags: append([]string(nil), action.Tags...),
		Limit: valueOr(action.Limit, action.HostGroup), Variables: cloneMap(variables),
		RequiredCredentials: append([]string(nil), action.RequiredCredentials...), TimeoutSeconds: action.TimeoutSeconds,
		NeedsApproval: action.NeedsApproval(),
	}
	return step, nil
}

func (p *Platform) prepareLockedPlan(ctx context.Context, environment domain.Environment, kind domain.RunKind, runID string, capturedAt time.Time, steps []lockedStep) (lockedPlan, string, bool, error) {
	if p.runner == nil {
		return lockedPlan{}, "", false, fmt.Errorf("%w: Ansible runner is not configured", domain.ErrConflict)
	}
	if environment.Revision == nil {
		return lockedPlan{}, "", false, fmt.Errorf("%w: environment revision is required", domain.ErrInvalid)
	}
	plan := lockedPlan{Steps: append([]lockedStep(nil), steps...)}
	for index := range plan.Steps {
		plan.Steps[index].Variables = cloneMap(plan.Steps[index].Variables)
	}
	if err := p.bindComponentArtifacts(ctx, *environment.Revision, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := p.bindComponentImages(ctx, *environment.Revision, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := injectEnvironmentVariables(*environment.Revision, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	for _, step := range plan.Steps {
		if err := rejectSensitiveMap(step.Variables, "run parameter"); err != nil {
			return lockedPlan{}, "", false, err
		}
	}
	if err := validateRequiredCredentials(environment.Revision.CredentialRefs, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	if err := validatePlanHostGroups(environment.Revision.Inventory, plan.Steps); err != nil {
		return lockedPlan{}, "", false, err
	}
	if digester, ok := p.runner.(planDigestRunner); ok {
		playbooks := make([]string, 0, len(plan.Steps))
		for _, step := range plan.Steps {
			playbooks = append(playbooks, step.Playbook)
		}
		digests, treeDigest, err := digester.DigestPlan(playbooks)
		if err != nil {
			return lockedPlan{}, "", false, err
		}
		plan.TreeDigest = treeDigest
		for i := range plan.Steps {
			plan.Steps[i].PlaybookDigest = digests[plan.Steps[i].Playbook]
		}
	} else if digester, ok := p.runner.(digestRunner); ok {
		for i := range plan.Steps {
			playbookDigest, treeDigest, err := digester.Digest(plan.Steps[i].Playbook)
			if err != nil {
				return lockedPlan{}, "", false, err
			}
			if plan.TreeDigest != "" && plan.TreeDigest != treeDigest {
				return lockedPlan{}, "", false, fmt.Errorf("%w: playbooks resolved to different executable trees", domain.ErrConflict)
			}
			plan.Steps[i].PlaybookDigest = playbookDigest
			plan.TreeDigest = treeDigest
		}
	}
	if err := p.bindBackupPlan(ctx, environment.ID, runID, kind, capturedAt, &plan); err != nil {
		return lockedPlan{}, "", false, err
	}
	if kind == domain.RunEnvironmentRollback {
		plan.InstallationBaseline = installationBaselineFromSteps(plan.Steps)
		plan.InstallationBaselineDigest = installationBaselineDigest(plan.InstallationBaseline)
	}
	planDigest := componentTestPlanDigest(environment.CurrentRevisionID, plan)
	destructive := len(plan.ArtifactTransfers) > 0 || len(plan.ImageTransfers) > 0
	for _, step := range plan.Steps {
		if step.NeedsApproval {
			destructive = true
			break
		}
	}
	return plan, planDigest, destructive, nil
}

func componentTestPlanDigest(environmentRevisionID string, plan lockedPlan) string {
	type digestStep struct {
		ComponentID         string
		ReleaseID           string
		ReleaseVersion      string
		ReleaseSpecDigest   string
		ActionID            string
		Action              domain.ActionKind
		FromReleaseID       string
		ToReleaseID         string
		Playbook            string
		PlaybookDigest      string
		Tags                []string
		Limit               string
		Variables           map[string]any
		RequiredCredentials []string
		TimeoutSeconds      int
		NeedsApproval       bool
		BackupRef           string
		BackupInstallRunID  string
		BackupPlaybookSHA   string
		BackupCapturedAt    string
	}
	digestSteps := make([]digestStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		variables := cloneMap(step.Variables)
		for _, name := range []string{
			"clusterforge_backup_ref", "clusterforge_backup_marker", "clusterforge_backup_operation",
			"clusterforge_backup_cleanup_on_success", "clusterforge_backup_metadata",
		} {
			delete(variables, name)
		}
		backupRef, backupInstallRunID, backupPlaybookSHA, backupCapturedAt := "", "", "", ""
		if (step.Action == domain.ActionRollback || step.Action == domain.ActionUninstall) && step.Backup != nil {
			backupRef, backupInstallRunID, backupPlaybookSHA = step.BackupRef, step.Backup.InstallRunID, step.Backup.PlaybookSHA256
			backupCapturedAt = step.Backup.CapturedAt.UTC().Format(time.RFC3339Nano)
		}
		digestSteps = append(digestSteps, digestStep{
			ComponentID: step.ComponentID, ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, ReleaseSpecDigest: step.ReleaseSpecDigest,
			ActionID: step.ActionID, Action: step.Action, FromReleaseID: step.FromReleaseID, ToReleaseID: step.ToReleaseID,
			Playbook: step.Playbook, PlaybookDigest: step.PlaybookDigest, Tags: step.Tags, Limit: step.Limit,
			Variables: variables, RequiredCredentials: step.RequiredCredentials,
			TimeoutSeconds: step.TimeoutSeconds, NeedsApproval: step.NeedsApproval,
			BackupRef: backupRef, BackupInstallRunID: backupInstallRunID,
			BackupPlaybookSHA: backupPlaybookSHA, BackupCapturedAt: backupCapturedAt,
		})
	}
	encoded, _ := json.Marshal(struct {
		EnvironmentRevisionID      string
		TreeDigest                 string
		Steps                      []digestStep
		ArtifactTransfers          []lockedArtifactTransfer
		ImageTransfers             []lockedImageTransfer
		InstallationBaselineDigest string
	}{EnvironmentRevisionID: environmentRevisionID, TreeDigest: plan.TreeDigest, Steps: digestSteps, ArtifactTransfers: plan.ArtifactTransfers, ImageTransfers: plan.ImageTransfers, InstallationBaselineDigest: plan.InstallationBaselineDigest})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func (p *Platform) componentTestPlanDTO(ctx context.Context, environment domain.Environment, plan lockedPlan, digest string, destructive bool) ComponentTestPlan {
	versions := map[string]string{}
	version := func(releaseID string) string {
		if releaseID == "" {
			return ""
		}
		if cached, ok := versions[releaseID]; ok {
			return cached
		}
		release, err := p.store.GetComponentRelease(ctx, releaseID)
		if err != nil {
			return ""
		}
		versions[releaseID] = release.Version
		return release.Version
	}
	steps := make([]ComponentTestPlanStep, 0, len(plan.Steps))
	for index, step := range plan.Steps {
		var backupInstallRunID, backupPlaybookSHA string
		var backupCapturedAt *time.Time
		if (step.Action == domain.ActionRollback || step.Action == domain.ActionUninstall) && step.Backup != nil {
			backupInstallRunID, backupPlaybookSHA = step.Backup.InstallRunID, step.Backup.PlaybookSHA256
			capturedAt := step.Backup.CapturedAt
			backupCapturedAt = &capturedAt
		}
		steps = append(steps, ComponentTestPlanStep{
			Order: index + 1, ComponentID: step.ComponentID, ComponentName: step.ComponentName,
			ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, Action: step.Action,
			Playbook: step.Playbook, Limit: step.Limit, NeedsApproval: step.NeedsApproval,
			FromReleaseID: step.FromReleaseID, FromReleaseVersion: version(step.FromReleaseID),
			ToReleaseID: step.ToReleaseID, ToReleaseVersion: version(step.ToReleaseID),
			BackupRef: step.BackupRef, BackupInstallRunID: backupInstallRunID,
			BackupCapturedAt: backupCapturedAt, BackupPlaybookSHA: backupPlaybookSHA,
		})
	}
	return ComponentTestPlan{
		EnvironmentID: environment.ID, EnvironmentRevisionID: environment.CurrentRevisionID,
		Destructive: destructive, RequiresApproval: destructive, PlanDigest: digest, Steps: steps,
	}
}

func (p *Platform) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, steps []lockedStep, resolvedParametersByNode map[string]map[string]resolvedParameter, expectedPlanDigest string) (domain.Run, error) {
	now := time.Now().UTC()
	runID := newID("run")
	plan, planDigest, destructive, err := p.prepareLockedPlan(ctx, environment, kind, runID, now, steps)
	if err != nil {
		return domain.Run{}, err
	}
	if expectedPlanDigest != "" && expectedPlanDigest != planDigest {
		base := fmt.Errorf("%w: execution plan changed after preview; refresh the plan before submitting", domain.ErrConflict)
		return domain.Run{}, actionableExistingError(base, "execution.plan_changed", "环境 Revision、输入、Release 定义或可执行内容在预览后发生变化", "重新预览执行计划", p.planRefreshHref(ctx, kind, releaseID, revisionID, environment.ID))
	}
	snapshot := structToMap(plan)
	if kind == domain.RunComponentTest {
		snapshot["componentTestEvidence"] = componentTestEvidence(action, plan.Steps)
	}
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
	if revisionID != "" {
		revision, err := p.store.GetScenarioRevision(ctx, revisionID)
		if err != nil {
			return domain.Run{}, err
		}
		snapshot["scenarioRevisionSpecDigest"] = scenarioRevisionSpecDigest(revision)
	}
	redactedRefs := make([]domain.CredentialRef, 0)
	if environment.Revision != nil {
		redactedRefs = domain.RedactCredentialRefs(environment.Revision.CredentialRefs, false)
	}
	snapshot["environmentRevisionId"] = environment.CurrentRevisionID
	snapshot["credentialRefs"] = redactedRefs
	status := domain.RunQueued
	var approval *domain.Approval
	run := domain.Run{
		ID: runID, Kind: kind, Status: status, RequestedBy: user.ID,
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

func componentTestEvidence(action domain.ActionKind, steps []lockedStep) string {
	if action == domain.ActionRollback {
		rollbackSeen := false
		for _, step := range steps {
			if step.Action == domain.ActionRollback {
				rollbackSeen = true
				continue
			}
			if rollbackSeen && step.Action == domain.ActionVerify {
				return "rollback_verify"
			}
		}
		return "rollback_only"
	}
	primarySeen := false
	for _, step := range steps {
		if step.Action == domain.ActionUpgrade || step.Action == domain.ActionInstall || step.Action == domain.ActionConfigure || step.Action == domain.ActionPreflight || step.Action == domain.ActionInspect {
			primarySeen = true
			continue
		}
		if primarySeen && step.Action == domain.ActionVerify {
			return "install_verify"
		}
	}
	return "incomplete"
}

func injectEnvironmentVariables(revision domain.EnvironmentRevision, steps []lockedStep) error {
	credentialNames := make(map[string]struct{}, len(revision.CredentialRefs))
	for _, ref := range revision.CredentialRefs {
		credentialNames[ref.Name] = struct{}{}
	}
	for index := range steps {
		if steps[index].Variables == nil {
			steps[index].Variables = map[string]any{}
		}
		for name, value := range revision.Variables {
			if _, exists := steps[index].Variables[name]; exists {
				return fmt.Errorf("%w: environment variable %q conflicts with a component parameter", domain.ErrInvalid, name)
			}
			if _, exists := credentialNames[name]; exists {
				return fmt.Errorf("%w: environment variable %q conflicts with a CredentialRef", domain.ErrInvalid, name)
			}
			steps[index].Variables[name] = value
		}
	}
	return nil
}

func (p *Platform) bindComponentArtifacts(ctx context.Context, revision domain.EnvironmentRevision, plan *lockedPlan) error {
	targetStation := strings.TrimSpace(revision.Variables[fileStationVariable])
	seenTransfers := map[string]bool{}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		release, err := p.store.GetComponentRelease(ctx, step.ReleaseID)
		if err != nil {
			return err
		}
		if len(release.Artifacts) == 0 {
			continue
		}
		if targetStation == "" {
			return fmt.Errorf("%w: environment must define %s before running release %s", domain.ErrConflict, fileStationVariable, release.Version)
		}
		targetStation, err = normalizeFileStation(targetStation)
		if err != nil {
			return err
		}
		for _, artifact := range release.Artifacts {
			for suffix, value := range map[string]any{
				"_path":   artifact.RelativePath,
				"_url":    artifactURL(targetStation, artifact.RelativePath),
				"_sha256": artifact.SHA256,
			} {
				name := artifact.Alias + suffix
				if _, exists := step.Variables[name]; exists {
					return fmt.Errorf("%w: generated artifact variable %q conflicts with a component parameter", domain.ErrInvalid, name)
				}
				step.Variables[name] = value
			}
			if artifact.FileStation == targetStation {
				continue
			}
			mirrored, err := p.store.HasComponentArtifactMirror(ctx, targetStation, artifact.RelativePath, artifact.SHA256)
			if err != nil {
				return err
			}
			key := targetStation + "\x00" + artifact.RelativePath + "\x00" + artifact.SHA256
			if mirrored || seenTransfers[key] {
				continue
			}
			seenTransfers[key] = true
			plan.ArtifactTransfers = append(plan.ArtifactTransfers, lockedArtifactTransfer{
				Alias: artifact.Alias, SourceStation: artifact.FileStation, TargetStation: targetStation,
				RelativePath: artifact.RelativePath, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
			})
		}
	}
	return nil
}

func artifactURL(station, relativePath string) string {
	return (&url.URL{Scheme: "http", Host: station, Path: "/" + strings.TrimPrefix(relativePath, "/")}).String()
}

func (p *Platform) bindComponentImages(ctx context.Context, revision domain.EnvironmentRevision, plan *lockedPlan) error {
	targetRegistry := strings.TrimSpace(revision.Variables[imageRegistryVariable])
	seenTransfers := map[string]bool{}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		build, err := p.store.LatestSucceededComponentImageBuild(ctx, step.ReleaseID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if targetRegistry == "" {
			return fmt.Errorf("%w: environment must define %s before running release %s", domain.ErrConflict, imageRegistryVariable, step.ReleaseVersion)
		}
		targetRegistry, err = normalizeImageRegistry(targetRegistry)
		if err != nil {
			return err
		}
		sourceRegistry, err := p.sourceRegistryForBuild(ctx, build)
		if err != nil {
			return err
		}
		suffix := strings.TrimPrefix(build.ImageRef, sourceRegistry+"/")
		if suffix == build.ImageRef || suffix == "" {
			return fmt.Errorf("%w: image %s is outside its source registry %s", domain.ErrConflict, build.ImageRef, sourceRegistry)
		}
		targetRef := targetRegistry + "/" + suffix
		digestIndex := strings.LastIndex(build.ImageDigest, "@sha256:")
		if digestIndex < 0 {
			return fmt.Errorf("%w: successful image build %s has no immutable digest", domain.ErrConflict, build.ID)
		}
		targetRepository := targetRef
		if tagIndex := strings.LastIndex(targetRepository, ":"); tagIndex > strings.LastIndex(targetRepository, "/") {
			targetRepository = targetRepository[:tagIndex]
		}
		targetDigest := targetRepository + build.ImageDigest[digestIndex:]
		if sourceRegistry != targetRegistry {
			mirroredDigest, mirrored, err := p.store.GetComponentImageMirror(ctx, targetRegistry, build.ImageDigest)
			if err != nil {
				return err
			}
			if mirrored {
				targetDigest = mirroredDigest
			} else {
				key := targetRegistry + "\x00" + build.ImageDigest
				if !seenTransfers[key] {
					seenTransfers[key] = true
					plan.ImageTransfers = append(plan.ImageTransfers, lockedImageTransfer{
						SourceRegistry: sourceRegistry, TargetRegistry: targetRegistry, SourceDigest: build.ImageDigest,
						TargetRef: targetRef, TargetDigest: targetDigest,
					})
				}
			}
		}
		for name, value := range map[string]any{"component_image_ref": targetRef, "component_image_digest": targetDigest} {
			if _, exists := step.Variables[name]; exists {
				return fmt.Errorf("%w: generated image variable %q conflicts with a component parameter", domain.ErrInvalid, name)
			}
			step.Variables[name] = value
		}
	}
	return nil
}

func (p *Platform) sourceRegistryForBuild(ctx context.Context, build domain.ComponentImageBuild) (string, error) {
	if build.EnvironmentRevisionID != "" {
		revision, err := p.store.GetEnvironmentRevision(ctx, build.EnvironmentRevisionID)
		if err == nil {
			if registry := strings.TrimSpace(revision.Variables[imageRegistryVariable]); registry != "" {
				return normalizeImageRegistry(registry)
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return "", err
		}
	}
	marker := "/components/"
	index := strings.Index(build.ImageRef, marker)
	if index <= 0 {
		return "", fmt.Errorf("%w: cannot determine source registry for image %s", domain.ErrConflict, build.ImageRef)
	}
	return normalizeImageRegistry(build.ImageRef[:index])
}

func (p *Platform) bindBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error {
	for index := range plan.Steps {
		step := &plan.Steps[index]
		switch step.Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			release, err := p.store.GetComponentRelease(ctx, step.ReleaseID)
			if err != nil {
				return err
			}
			metadata := domain.BackupMetadata{
				EnvironmentID: environmentID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID,
				ActionID: step.ActionID, InstallRunID: runID, CapturedAt: capturedAt,
				PlaybookSHA256: step.PlaybookDigest, DependencySnapshot: releaseDependencySnapshot(release),
			}
			step.BackupRef = path.Join("/var/lib/clusterforge/backups", safeBackupSegment(environmentID), safeBackupSegment(step.ComponentID), safeBackupSegment(step.ReleaseID), safeBackupSegment(runID))
			step.Backup = &metadata
			if current, currentErr := p.store.GetEnvironmentComponentInstallation(ctx, environmentID, step.ComponentID); currentErr == nil && current.BackupRef != step.BackupRef {
				step.Variables["clusterforge_replaced_backup_ref"] = current.BackupRef
				step.Variables["clusterforge_cleanup_replaced_backup_on_success"] = true
			} else if currentErr != nil && !errors.Is(currentErr, domain.ErrNotFound) {
				return currentErr
			}
			bindBackupVariables(step, "capture", kind != domain.RunScenario)
		case domain.ActionRollback:
			installation, err := p.store.GetEnvironmentComponentInstallation(ctx, environmentID, step.ComponentID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: rollback refused: environment %s has no installed-component backup_ref for component %s", domain.ErrConflict, environmentID, step.ComponentID)
				}
				return err
			}
			expectedReleaseID := step.ReleaseID
			if step.FromReleaseID != "" {
				expectedReleaseID = step.FromReleaseID
			}
			if err := validateInstallationBackup(installation, environmentID, step.ComponentID, expectedReleaseID); err != nil {
				return err
			}
			if err := p.validateCurrentInstallEvidence(ctx, installation); err != nil {
				return err
			}
			step.BackupRef = installation.BackupRef
			metadata := installation.Backup
			step.Backup = &metadata
			bindBackupVariables(step, "restore", kind != domain.RunScenario && isFinalCleanupStep(plan.Steps, index))
		case domain.ActionUninstall:
			installation, err := p.store.GetEnvironmentComponentInstallation(ctx, environmentID, step.ComponentID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: uninstall refused: environment %s has no installed-component record for component %s", domain.ErrConflict, environmentID, step.ComponentID)
				}
				return err
			}
			if err := validateInstallationBackup(installation, environmentID, step.ComponentID, step.ReleaseID); err != nil {
				return err
			}
			step.BackupRef = installation.BackupRef
			metadata := installation.Backup
			step.Backup = &metadata
			bindBackupVariables(step, "cleanup", isFinalCleanupStep(plan.Steps, index))
		}
	}
	return nil
}

func bindBackupVariables(step *lockedStep, operation string, cleanupOnSuccess bool) {
	metadata := step.Backup
	step.Variables["clusterforge_backup_ref"] = step.BackupRef
	step.Variables["clusterforge_backup_marker"] = path.Join(step.BackupRef, ".captured")
	step.Variables["clusterforge_backup_operation"] = operation
	step.Variables["clusterforge_backup_cleanup_on_success"] = cleanupOnSuccess && (operation == "restore" || operation == "cleanup")
	step.Variables["clusterforge_backup_metadata"] = map[string]any{
		"environment_id":      metadata.EnvironmentID,
		"component_id":        metadata.ComponentID,
		"release_id":          metadata.ReleaseID,
		"action_id":           metadata.ActionID,
		"install_run_id":      metadata.InstallRunID,
		"captured_at":         metadata.CapturedAt.UTC().Format(time.RFC3339Nano),
		"playbook_sha256":     metadata.PlaybookSHA256,
		"dependency_snapshot": metadata.DependencySnapshot,
	}
}

func releaseDependencySnapshot(release domain.ComponentRelease) map[string]any {
	dependencies := make([]map[string]any, 0, len(release.Dependencies))
	for _, dependency := range release.Dependencies {
		dependencies = append(dependencies, map[string]any{
			"component_id":       dependency.UpstreamComponentID,
			"release_id":         dependency.UpstreamReleaseID,
			"parameter_mappings": dependency.ParameterMappings,
		})
	}
	return map[string]any{"dependencies": dependencies}
}

func validateInstallationBackup(installation domain.EnvironmentComponentInstallation, environmentID, componentID, releaseID string) error {
	metadata := installation.Backup
	if installation.BackupRef == "" || installation.InstallRunID == "" || metadata.EnvironmentID == "" || metadata.ComponentID == "" || metadata.ReleaseID == "" || metadata.InstallRunID == "" || metadata.PlaybookSHA256 == "" || metadata.CapturedAt.IsZero() {
		return fmt.Errorf("%w: rollback refused: installed-component backup metadata is incomplete", domain.ErrConflict)
	}
	if installation.EnvironmentID != environmentID || metadata.EnvironmentID != environmentID {
		return fmt.Errorf("%w: rollback refused: backup environment does not match %s", domain.ErrConflict, environmentID)
	}
	if installation.ComponentID != componentID || metadata.ComponentID != componentID {
		return fmt.Errorf("%w: rollback refused: backup component does not match %s", domain.ErrConflict, componentID)
	}
	if installation.ReleaseID != releaseID || metadata.ReleaseID != releaseID {
		return fmt.Errorf("%w: rollback refused: backup release %s does not match %s", domain.ErrConflict, metadata.ReleaseID, releaseID)
	}
	if installation.InstallRunID != metadata.InstallRunID {
		return fmt.Errorf("%w: rollback refused: backup install Run metadata does not match the installed-component record", domain.ErrConflict)
	}
	return nil
}

func (p *Platform) validateCurrentInstallEvidence(ctx context.Context, installation domain.EnvironmentComponentInstallation) error {
	release, err := p.store.GetComponentRelease(ctx, installation.ReleaseID)
	if err != nil {
		return fmt.Errorf("%w: rollback refused: installed release no longer exists", domain.ErrConflict)
	}
	var capturedAction domain.ActionDefinition
	for _, action := range release.Actions {
		if action.ID == installation.Backup.ActionID {
			capturedAction = action
			break
		}
	}
	if capturedAction.ID == "" {
		return fmt.Errorf("%w: rollback refused: captured install action no longer matches release %s", domain.ErrConflict, release.ID)
	}
	digester, ok := p.runner.(digestRunner)
	if !ok {
		return fmt.Errorf("%w: rollback refused: runner cannot verify the captured Playbook hash", domain.ErrConflict)
	}
	currentDigest, _, err := digester.Digest(capturedAction.Playbook)
	if err != nil {
		return fmt.Errorf("%w: rollback refused: cannot verify captured Playbook: %v", domain.ErrConflict, err)
	}
	if currentDigest != installation.Backup.PlaybookSHA256 {
		return fmt.Errorf("%w: rollback refused: captured Playbook hash does not match the current release definition", domain.ErrConflict)
	}
	return nil
}

func safeBackupSegment(value string) string {
	if value != "" {
		valid := true
		for _, char := range value {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
				valid = false
				break
			}
		}
		if valid && value != "." && value != ".." {
			return value
		}
	}
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("id-%x", digest[:12])
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
	p.nextWorkerToken++
	token := p.nextWorkerToken
	p.workers[environmentID] = environmentWorkerState{token: token, heartbeat: time.Now()}
	p.mu.Unlock()
	go p.environmentWorker(environmentID, token)
}

const (
	queueWatchdogInterval = 2 * time.Second
	queueWorkerStaleAfter = 10 * time.Second
)

func (p *Platform) queueWatchdog() {
	ticker := time.NewTicker(queueWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.rootCtx.Done():
			return
		case now := <-ticker.C:
			if count, err := p.store.FailInvalidActiveRuns(p.rootCtx, now.UTC()); err != nil {
				p.hub.Publish("run.worker_error", map[string]any{"error": err.Error()})
			} else if count > 0 {
				p.hub.Publish("run.updated", map[string]any{"status": domain.RunFailed, "reconciled": count})
			}
			environments, err := p.store.ListQueuedEnvironmentIDs(p.rootCtx)
			if err != nil {
				p.hub.Publish("run.worker_error", map[string]any{"error": err.Error()})
				continue
			}
			for _, environmentID := range environments {
				running, runErr := p.store.HasRunningRun(p.rootCtx, environmentID)
				if runErr != nil || running {
					continue
				}
				p.recoverEnvironmentWorker(environmentID, now.Add(-queueWorkerStaleAfter))
			}
		}
	}
}

func (p *Platform) recoverEnvironmentWorker(environmentID string, staleBefore time.Time) {
	p.mu.Lock()
	state, exists := p.workers[environmentID]
	if exists && state.heartbeat.After(staleBefore) {
		p.mu.Unlock()
		return
	}
	p.nextWorkerToken++
	token := p.nextWorkerToken
	p.workers[environmentID] = environmentWorkerState{token: token, heartbeat: time.Now()}
	p.mu.Unlock()
	go p.environmentWorker(environmentID, token)
}

func (p *Platform) touchEnvironmentWorker(environmentID string, token uint64) {
	p.mu.Lock()
	if state, ok := p.workers[environmentID]; ok && state.token == token {
		state.heartbeat = time.Now()
		p.workers[environmentID] = state
	}
	p.mu.Unlock()
}

func (p *Platform) environmentWorker(environmentID string, token uint64) {
	defer func() {
		p.mu.Lock()
		if state, ok := p.workers[environmentID]; ok && state.token == token {
			delete(p.workers, environmentID)
		}
		p.mu.Unlock()
		// Close the narrow hand-off window where a run is queued after this
		// worker's final empty claim but before it removes itself. Any enqueue
		// after the removal schedules its own worker; an enqueue before removal
		// is discovered here.
		if p.rootCtx.Err() == nil {
			running, err := p.store.HasRunningRun(context.Background(), environmentID)
			if err != nil || running {
				return
			}
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
		p.touchEnvironmentWorker(environmentID, token)
		run, err := p.store.ClaimNextRun(p.rootCtx, environmentID, time.Now().UTC())
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			return
		}
		if err != nil {
			p.hub.Publish("run.worker_error", map[string]any{"environmentId": environmentID, "error": err.Error()})
			return
		}
		p.executeRun(run)
		p.touchEnvironmentWorker(environmentID, token)
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
	if err := p.validateLockedRollbackPlan(ctx, run, plan); err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := p.mirrorRunImages(ctx, run.ID, plan.ImageTransfers); err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := p.mirrorRunArtifacts(ctx, run.ID, plan.ArtifactTransfers); err != nil {
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
		if lifecycleErr := p.recordSuccessfulLifecycleStep(context.Background(), run, locked, finished); lifecycleErr != nil {
			step.Status = domain.RunFailed
			step.Summary = lifecycleErr.Error()
			if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
				log.Printf("run %s: persist failed lifecycle step %s: %v", run.ID, step.ID, stepErr)
			}
			p.finishRun(run, domain.RunFailed, lifecycleErr)
			return
		}
		p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "stepId": step.ID, "status": step.Status})
	}
	p.finishRun(run, domain.RunSucceeded, nil)
}

func (p *Platform) mirrorRunImages(ctx context.Context, runID string, transfers []lockedImageTransfer) error {
	docker := p.dockerBinary
	if docker == "" {
		docker = "docker"
	}
	for _, transfer := range transfers {
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: fmt.Sprintf("mirroring image from %s to %s", transfer.SourceRegistry, transfer.TargetRegistry), CreatedAt: time.Now().UTC()})
		for _, args := range [][]string{{"pull", transfer.SourceDigest}, {"tag", transfer.SourceDigest, transfer.TargetRef}, {"push", transfer.TargetRef}, {"pull", transfer.TargetDigest}} {
			output, err := exec.CommandContext(ctx, docker, args...).CombinedOutput()
			message := strings.TrimSpace(string(output))
			if len(message) > 16*1024 {
				message = message[len(message)-(16*1024):]
			}
			if message != "" {
				_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: Redact(message).(string), CreatedAt: time.Now().UTC()})
			}
			if err != nil {
				return fmt.Errorf("docker %s failed: %w", args[0], err)
			}
		}
		if err := p.store.RecordComponentImageMirror(ctx, transfer.TargetRegistry, transfer.SourceDigest, transfer.TargetRef, transfer.TargetDigest, time.Now().UTC()); err != nil {
			return err
		}
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: "image is ready at " + transfer.TargetDigest, CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (p *Platform) mirrorRunArtifacts(ctx context.Context, runID string, transfers []lockedArtifactTransfer) error {
	for _, transfer := range transfers {
		message := fmt.Sprintf("mirroring media %s from %s to %s", transfer.Alias, transfer.SourceStation, transfer.TargetStation)
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: message, CreatedAt: time.Now().UTC()})

		registered, err := verifyArtifactAtStation(ctx, transfer.TargetStation, transfer.RelativePath, transfer.SHA256)
		if err != nil {
			return fmt.Errorf("verify target media %s: %w", transfer.Alias, err)
		}
		if !registered {
			if err := copyArtifactBetweenStations(ctx, transfer); err != nil {
				return fmt.Errorf("mirror media %s: %w", transfer.Alias, err)
			}
		}
		if err := p.store.RecordComponentArtifactMirror(ctx, transfer.SourceStation, transfer.TargetStation, transfer.RelativePath, transfer.SHA256, time.Now().UTC()); err != nil {
			return fmt.Errorf("record mirrored media %s: %w", transfer.Alias, err)
		}
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: fmt.Sprintf("media %s is ready on %s (sha256:%s)", transfer.Alias, transfer.TargetStation, transfer.SHA256), CreatedAt: time.Now().UTC()})
	}
	return nil
}

func verifyArtifactAtStation(ctx context.Context, station, relativePath, sha256Value string) (bool, error) {
	payload, _ := json.Marshal(map[string]string{"path": relativePath, "sha256": sha256Value})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+station+"/api/v1/register", strings.NewReader(string(payload)))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return true, nil
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnprocessableEntity {
		return false, nil
	}
	message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	return false, fmt.Errorf("file station returned %s: %s", response.Status, strings.TrimSpace(string(message)))
}

func copyArtifactBetweenStations(ctx context.Context, transfer lockedArtifactTransfer) error {
	sourceRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL(transfer.SourceStation, transfer.RelativePath), nil)
	if err != nil {
		return err
	}
	sourceResponse, err := http.DefaultClient.Do(sourceRequest)
	if err != nil {
		return err
	}
	defer sourceResponse.Body.Close()
	if sourceResponse.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(sourceResponse.Body, 8<<10))
		return fmt.Errorf("source file station returned %s: %s", sourceResponse.Status, strings.TrimSpace(string(message)))
	}
	targetURL := "http://" + transfer.TargetStation + "/api/v1/files?path=" + url.QueryEscape(transfer.RelativePath) + "&sha256=" + transfer.SHA256
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, sourceResponse.Body)
	if err != nil {
		return err
	}
	request.ContentLength = sourceResponse.ContentLength
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return fmt.Errorf("target file station returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var metadata fssFileMetadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return err
	}
	if metadata.SHA256 != transfer.SHA256 || metadata.RelativePath != transfer.RelativePath {
		return fmt.Errorf("target file station returned mismatched metadata")
	}
	return nil
}

func (p *Platform) recordSuccessfulLifecycleStep(ctx context.Context, run domain.Run, step lockedStep, installedAt time.Time) error {
	switch step.Action {
	case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
		if step.Backup == nil || step.BackupRef == "" {
			return fmt.Errorf("%w: successful install action is missing its locked backup metadata", domain.ErrConflict)
		}
		return p.store.UpsertEnvironmentComponentInstallation(ctx, domain.EnvironmentComponentInstallation{
			EnvironmentID: run.EnvironmentID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID,
			InstallRunID: run.ID, BackupRef: step.BackupRef, Backup: *step.Backup,
			TestOnly: run.Kind != domain.RunScenario, InstalledAt: installedAt,
		})
	case domain.ActionRollback, domain.ActionUninstall:
		if step.Backup == nil {
			return fmt.Errorf("%w: successful rollback action is missing its locked backup metadata", domain.ErrConflict)
		}
		if hasLaterCleanupStep(run.InputSnapshot, step) {
			return nil
		}
		err := p.store.DeleteEnvironmentComponentInstallation(ctx, run.EnvironmentID, step.ComponentID, step.Backup.InstallRunID)
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: rollback backup_ref was replaced while the Run was active", domain.ErrConflict)
		}
		return err
	default:
		return nil
	}
}

func (p *Platform) finishRun(run domain.Run, status domain.RunStatus, cause error) {
	errText := ""
	if cause != nil {
		errText = Redact(cause.Error()).(string)
	}
	if err := p.store.UpdateRunStatus(context.Background(), run.ID, []domain.RunStatus{domain.RunRunning}, status, errText, time.Now().UTC()); err != nil {
		log.Printf("finishRun %s: update status to %s: %v", run.ID, status, err)
	}
	if run.Kind == domain.RunComponentTest && run.Action != domain.ActionRollback && status == domain.RunSucceeded {
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
		Idempotent          bool              `json:"idempotent"`
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
		Artifacts              []domain.ComponentArtifact   `json:"artifacts"`
	}{
		Version: release.Version, Type: release.Type, ReleaseNotes: release.ReleaseNotes,
		Breaking: release.Breaking, RiskLevel: release.RiskLevel,
		EnvironmentConstraints: release.EnvironmentConstraints, Parameters: release.Parameters, Artifacts: release.Artifacts,
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
			Idempotent:    action.Idempotent,
			FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		})
	}
	encoded, _ := json.Marshal(spec)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func scenarioRevisionSpecDigest(revision domain.ScenarioRevision) string {
	encoded, _ := json.Marshal(struct {
		Graph           domain.ScenarioGraph
		ExecutionPolicy map[string]any
	}{Graph: revision.Graph, ExecutionPolicy: revision.ExecutionPolicy})
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
		base := fmt.Errorf("%w: run is already terminal", domain.ErrConflict)
		return run, actionableExistingError(base, "run.already_terminal", "该 Run 已进入终态，不能再次取消", "刷新运行详情", "/runs?selected="+run.ID)
	}
	p.audit(ctx, user, "run.cancel_requested", "run", run.ID, nil)
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	return run, nil
}

func (p *Platform) DecideApproval(ctx context.Context, user domain.User, approvalID, decision, reason string) (domain.Run, error) {
	if user.Role != domain.RoleEnvironmentOwner {
		base := fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
		return domain.Run{}, actionableExistingError(base, "permission.environment_owner_required", "只有目标环境的 Environment Owner 可以审批危险作业", "返回我的工作", "/")
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
		base := fmt.Errorf("%w: approval belongs to another owner's environment", domain.ErrForbidden)
		return run, actionableExistingError(base, "permission.environment_owner_required", "该审批属于另一位 Environment Owner 管理的环境", "查看运行详情", "/runs?selected="+run.ID)
	}
	if approval.Status != "pending" {
		if approval.Status == decision && run.Status != domain.RunAwaitingApproval {
			// Treat an identical retry as success. This makes the endpoint safe for
			// double clicks and network retries without duplicating audit events or
			// attempting to enqueue the run a second time.
			return run, nil
		}
		base := fmt.Errorf("%w: approval was already %s", domain.ErrConflict, approval.Status)
		return run, actionableExistingError(base, "approval.already_decided", "审批状态已由另一请求消费，当前页面数据已过期", "刷新运行详情", "/runs?selected="+run.ID)
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

func (p *Platform) BatchDecideApprovals(ctx context.Context, user domain.User, approvalIDs []string, decision, reason string) ([]domain.Run, error) {
	if user.Role != domain.RoleEnvironmentOwner {
		return nil, fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: batch approval reason is required", domain.ErrInvalid)
	}
	runs, err := p.store.BatchDecideApprovals(ctx, approvalIDs, user.ID, decision, reason, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	environments := map[string]bool{}
	for _, run := range runs {
		if decision == "rejected" && run.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
		p.audit(ctx, user, "approval.batch_"+decision, "run", run.ID, map[string]any{"reason": reason, "batchSize": len(runs)})
		p.hub.Publish("approval.updated", map[string]any{"runId": run.ID, "decision": decision, "batch": true})
		environments[run.EnvironmentID] = true
	}
	if decision == "approved" {
		for environmentID := range environments {
			p.schedule(environmentID)
		}
	}
	return runs, nil
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
