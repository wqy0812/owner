package service

import yaml "gopkg.in/yaml.v3"

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

// Acceptance definition and source edits use the same revision identity as the
// graph. Keeping one CAS boundary prevents a source editor overwriting a newer
// graph/parameter change and makes all test evidence content-bound.
type ScenarioAcceptanceInput struct {
	ExpectedRevisionDigest string                            `json:"expectedRevisionDigest"`
	Jobs                   []domain.ScenarioAcceptanceJob    `json:"jobs"`
	Parameters             []domain.ParameterDefinition      `json:"parameters"`
	Values                 map[string]any                    `json:"values"`
	Bindings               []domain.ScenarioParameterBinding `json:"bindings"`
}

type ScenarioAcceptanceDefinition struct {
	RevisionID     string                            `json:"revisionId"`
	RevisionDigest string                            `json:"revisionDigest"`
	Jobs           []domain.ScenarioAcceptanceJob    `json:"jobs"`
	Parameters     []domain.ParameterDefinition      `json:"parameters"`
	Values         map[string]any                    `json:"values"`
	Bindings       []domain.ScenarioParameterBinding `json:"bindings"`
	Workspace      PlaybookWorkspace                 `json:"workspace"`
	Editable       bool                              `json:"editable"`
}

var scenarioAcceptanceIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)
var scenarioAcceptanceVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (p *ScenarioService) authorizeScenarioAcceptance(ctx context.Context, user domain.User, revisionID string, write bool) (domain.ScenarioRevision, domain.Scenario, error) {
	revision, err := p.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return revision, domain.Scenario{}, err
	}
	scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
	if err != nil {
		return revision, scenario, err
	}
	owner := user.Role == domain.RoleScenarioOwner && user.ID == scenario.OwnerID
	if write {
		if !owner {
			return revision, scenario, domain.ErrForbidden
		}
		if scenario.CurrentRevisionID != revisionID || revision.AbandonedAt != nil || (revision.Status != domain.RevisionDraft && revision.Status != domain.RevisionTesting && revision.Status != domain.RevisionTestPassed) {
			return revision, scenario, fmt.Errorf("%w: only the current unpublished scenario revision can be edited", domain.ErrConflict)
		}
		active, err := p.store.HasActiveScenarioRevisionRun(ctx, revisionID)
		if err != nil {
			return revision, scenario, err
		}
		if active {
			return revision, scenario, fmt.Errorf("%w: scenario revision has an active run", domain.ErrConflict)
		}
	} else if !owner && user.Role != domain.RolePlatformAdmin && revision.Status != domain.RevisionReleased {
		return revision, scenario, domain.ErrForbidden
	}
	return revision, scenario, nil
}

func (p *ScenarioService) ReadAcceptance(ctx context.Context, user domain.User, revisionID string) (ScenarioAcceptanceDefinition, error) {
	revision, scenario, err := p.authorizeScenarioAcceptance(ctx, user, revisionID, false)
	if err != nil {
		return ScenarioAcceptanceDefinition{}, err
	}
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return ScenarioAcceptanceDefinition{}, err
	}
	jobs, parameters, bindings := revision.AcceptanceJobs, revision.AcceptanceParameters, revision.AcceptanceBindings
	if jobs == nil {
		jobs = []domain.ScenarioAcceptanceJob{}
	}
	if parameters == nil {
		parameters = []domain.ParameterDefinition{}
	}
	if bindings == nil {
		bindings = []domain.ScenarioParameterBinding{}
	}
	values := revision.AcceptanceValues
	if values == nil {
		values = map[string]any{}
	}
	editable := user.Role == domain.RoleScenarioOwner && user.ID == scenario.OwnerID && scenario.CurrentRevisionID == revision.ID && revision.AbandonedAt == nil && (revision.Status == domain.RevisionDraft || revision.Status == domain.RevisionTesting || revision.Status == domain.RevisionTestPassed)
	if editable {
		_, _, e := p.authorizeScenarioAcceptance(ctx, user, revisionID, true)
		editable = e == nil
	}
	return ScenarioAcceptanceDefinition{RevisionID: revisionID, RevisionDigest: domain.ScenarioRevisionSpecDigest(revision), Jobs: jobs, Parameters: parameters, Values: values, Bindings: bindings, Workspace: workspace, Editable: editable}, nil
}

func (p *ScenarioService) SaveAcceptance(ctx context.Context, user domain.User, revisionID string, input ScenarioAcceptanceInput) (ScenarioAcceptanceDefinition, error) {
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	revision, _, err := p.authorizeScenarioAcceptance(ctx, user, revisionID, true)
	if err != nil {
		return ScenarioAcceptanceDefinition{}, err
	}
	if input.ExpectedRevisionDigest == "" || input.ExpectedRevisionDigest != domain.ScenarioRevisionSpecDigest(revision) {
		return ScenarioAcceptanceDefinition{}, fmt.Errorf("%w: scenario revision changed; reload before saving", domain.ErrConflict)
	}
	for i := range input.Jobs {
		job := &input.Jobs[i]
		if job.NeedsYAMLMigration() {
			return ScenarioAcceptanceDefinition{}, fmt.Errorf("%w: 旧 facts 与工具探测不可写入，请直接编写 YAML", domain.ErrInvalid)
		}
		for _, previous := range revision.AcceptanceJobs {
			if previous.ID == job.ID {
				job.GatherFacts, job.RuntimeChecks = previous.GatherFacts, previous.RuntimeChecks
			}
		}
	}
	revision.AcceptanceJobs, revision.AcceptanceParameters, revision.AcceptanceValues, revision.AcceptanceBindings = input.Jobs, input.Parameters, input.Values, input.Bindings
	if err := p.normalizeScenarioAcceptance(ctx, &revision); err != nil {
		return ScenarioAcceptanceDefinition{}, err
	}
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return ScenarioAcceptanceDefinition{}, err
	}
	if workspace.TreeSHA256 != revision.AcceptanceTreeSHA256 {
		return ScenarioAcceptanceDefinition{}, fmt.Errorf("%w: acceptance files changed outside the revision; reload and save the affected files first", domain.ErrConflict)
	}
	revision.AcceptanceWorkspaceRoot, revision.AcceptanceTreeSHA256 = workspace.Root, workspace.TreeSHA256
	for index := range revision.AcceptanceJobs {
		job := &revision.AcceptanceJobs[index]
		job.PlaybookSHA256 = ""
		for _, file := range workspace.Files {
			if workspace.Root+file.Path == job.Playbook {
				job.PlaybookSHA256 = file.SHA256
			}
		}
	}
	if err := p.store.SaveScenarioRevisionDefinition(ctx, revision, input.ExpectedRevisionDigest); err != nil {
		return ScenarioAcceptanceDefinition{}, err
	}
	p.audit.Record(ctx, user, "scenario_revision.acceptance_updated", "scenario_revision", revisionID, map[string]any{"jobs": len(revision.AcceptanceJobs)})
	return p.ReadAcceptance(ctx, user, revisionID)
}

func (p *ScenarioService) normalizeScenarioAcceptance(ctx context.Context, revision *domain.ScenarioRevision) error {
	seen := map[string]bool{}
	for index := range revision.AcceptanceJobs {
		job := &revision.AcceptanceJobs[index]
		if job.ID == "" {
			job.ID = newID("acceptance")
		}
		if !scenarioAcceptanceIdentifier.MatchString(job.ID) || seen[strings.ToLower(job.ID)] {
			return fmt.Errorf("%w: acceptance job IDs must be unique safe identifiers", domain.ErrInvalid)
		}
		seen[strings.ToLower(job.ID)] = true
		job.Name, job.Purpose, job.HostGroup = strings.TrimSpace(job.Name), strings.TrimSpace(job.Purpose), strings.TrimSpace(job.HostGroup)
		if job.Name == "" || job.Purpose == "" || job.HostGroup == "" {
			return fmt.Errorf("%w: acceptance jobs require name, purpose and hostGroup", domain.ErrInvalid)
		}
		if err := p.catalogRules.validateHostGroupCatalog(ctx, job.HostGroup); err != nil {
			return err
		}
		if job.TimeoutSeconds == 0 {
			job.TimeoutSeconds = 300
		}
		if job.TimeoutSeconds < 1 || job.TimeoutSeconds > 86400 {
			return fmt.Errorf("%w: acceptance timeout must be between 1 and 86400 seconds", domain.ErrInvalid)
		}
		if job.RiskLevel == "" {
			job.RiskLevel = domain.RiskLow
		}
		switch job.RiskLevel {
		case domain.RiskLow, domain.RiskMedium, domain.RiskHigh, domain.RiskDestructive:
		default:
			return fmt.Errorf("%w: invalid acceptance risk level", domain.ErrInvalid)
		}
		credentials := map[string]bool{}
		for _, name := range job.RequiredCredentials {
			if strings.TrimSpace(name) != name || name == "" || credentials[name] {
				return fmt.Errorf("%w: required credential names must be non-empty and unique", domain.ErrInvalid)
			}
			credentials[name] = true
		}
		job.Playbook = scenarioAcceptancePrefix(*revision) + "tasks/acceptance/" + job.ID + ".yml"
	}
	return p.validateScenarioAcceptanceParameters(ctx, *revision)
}

func (p *ScenarioService) validateScenarioAcceptanceParameters(ctx context.Context, revision domain.ScenarioRevision) error {
	if err := rejectSensitiveMap(revision.AcceptanceValues, "scenario acceptance value"); err != nil {
		return err
	}
	bindings := map[string]domain.ScenarioParameterBinding{}
	nodes := map[string]domain.ScenarioNode{}
	for _, node := range revision.Graph.Nodes {
		nodes[node.ID] = node
	}
	for _, binding := range revision.AcceptanceBindings {
		if _, found := bindings[binding.Parameter]; found {
			return fmt.Errorf("%w: acceptance parameter %s has multiple bindings", domain.ErrInvalid, binding.Parameter)
		}
		parameter, declared := domain.ParameterByName(revision.AcceptanceParameters, binding.Parameter)
		if !declared || binding.SourceParameter == "" {
			return fmt.Errorf("%w: acceptance binding must name a declared parameter and source", domain.ErrInvalid)
		}
		if _, exists := revision.AcceptanceValues[binding.Parameter]; exists {
			return fmt.Errorf("%w: bound acceptance parameter %s cannot have a manual value", domain.ErrInvalid, binding.Parameter)
		}
		switch binding.Source {
		case "node":
			node, exists := nodes[binding.NodeID]
			if !exists {
				return fmt.Errorf("%w: acceptance binding source node does not exist", domain.ErrInvalid)
			}
			release, err := p.store.GetComponentRelease(ctx, node.ReleaseID)
			if err != nil {
				return err
			}
			source, exists := domain.ParameterByName(release.Parameters, binding.SourceParameter)
			if !exists || source.Visibility != domain.ParameterPublic || source.Type != parameter.Type {
				return fmt.Errorf("%w: acceptance node binding must use a public parameter of the same type", domain.ErrInvalid)
			}
		case "environment":
			if binding.NodeID != "" || isSensitiveKey(binding.SourceParameter) {
				return fmt.Errorf("%w: invalid environment acceptance binding; credentials use CredentialRefs", domain.ErrInvalid)
			}
		default:
			return fmt.Errorf("%w: acceptance parameter binding source must be node or environment", domain.ErrInvalid)
		}
		bindings[binding.Parameter] = binding
	}
	definitions := append([]domain.ParameterDefinition(nil), revision.AcceptanceParameters...)
	for index := range definitions {
		parameter := &definitions[index]
		if !scenarioAcceptanceVariable.MatchString(parameter.Name) || strings.HasPrefix(parameter.Name, "cf_") || parameter.Name == "cf" || strings.HasPrefix(parameter.Name, "ansible_") {
			return fmt.Errorf("%w: invalid or reserved acceptance parameter name", domain.ErrInvalid)
		}
		// Binding ownership is represented by the explicit source; use existing
		// scalar/enum/default validation without fabricating component contracts.
		parameter.ValueProvider, parameter.Modifiable, parameter.EnvironmentBinding = domain.ParameterProviderScenarioOwner, true, nil
		if parameter.HasFixedValue() {
			return fmt.Errorf("%w: acceptance parameters use typed values or bindings, not fixed component values", domain.ErrInvalid)
		}
	}
	if err := validateReleaseParameters(domain.ComponentRelease{Parameters: definitions}); err != nil {
		return err
	}
	for name, value := range revision.AcceptanceValues {
		parameter, exists := domain.ParameterByName(definitions, name)
		if !exists {
			return fmt.Errorf("%w: undeclared acceptance parameter %s", domain.ErrInvalid, name)
		}
		if err := validateResolvedParameters([]domain.ParameterDefinition{parameter}, map[string]any{name: value}); err != nil {
			return err
		}
	}
	return nil
}

func resolveAcceptanceParameters(revision domain.ScenarioRevision, environment domain.EnvironmentRevision, resolvedByNode map[string]map[string]any) (map[string]any, error) {
	values := map[string]any{}
	for name, value := range revision.AcceptanceValues {
		values[name] = deepCopy(value)
	}
	for _, binding := range revision.AcceptanceBindings {
		var value any
		var found bool
		switch binding.Source {
		case "node":
			value, found = resolvedByNode[binding.NodeID][binding.SourceParameter]
		case "environment":
			value, found = environment.Parameters[binding.SourceParameter]
			if !found {
				value, found = environment.Variables[binding.SourceParameter]
			}
		default:
			return nil, fmt.Errorf("%w: unknown acceptance parameter binding source", domain.ErrInvalid)
		}
		if found {
			values[binding.Parameter] = deepCopy(value)
		}
	}
	if err := validateResolvedParameters(revision.AcceptanceParameters, values); err != nil {
		return nil, err
	}
	if err := rejectSensitiveMap(values, "scenario acceptance parameter"); err != nil {
		return nil, err
	}
	return values, nil
}

func validateScenarioAcceptanceTasks(contents []byte, mayMutate bool) error {
	if len(contents) > MaxPlaybookBytes {
		return fmt.Errorf("%w: acceptance source exceeds 1 MiB", domain.ErrInvalid)
	}
	if err := ansiblerunner.ValidateRoleTasks(contents, !mayMutate); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return fmt.Errorf("%w: invalid acceptance source", domain.ErrInvalid)
	}
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index < len(node.Content); index += 2 {
				key, value := node.Content[index].Value, node.Content[index+1]
				if key == "always" {
					return fmt.Errorf("%w: acceptance cleanup belongs to the successful path; always blocks cannot preserve failed state", domain.ErrInvalid)
				}
				if key == "failed_when" && acceptanceConditionAlwaysFalse(value) {
					return fmt.Errorf("%w: acceptance tasks cannot suppress failures", domain.ErrInvalid)
				}
				if key == "block" {
					if err := visit(value); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return visit(root.Content[0])
}

func acceptanceConditionAlwaysFalse(value *yaml.Node) bool {
	if value.Kind == yaml.SequenceNode {
		if len(value.Content) == 0 {
			return true
		}
		// Ansible joins failed_when list conditions with AND.
		for _, condition := range value.Content {
			if acceptanceConditionAlwaysFalse(condition) {
				return true
			}
		}
		return false
	}
	if value.Kind != yaml.ScalarNode {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(value.Value))
	return text == "false" || text == "0" || text == "" || text == "{{ false }}" || text == "{{false}}"
}

func (p *PlanBuilder) lockScenarioAcceptanceSteps(ctx context.Context, revision domain.ScenarioRevision, environment domain.EnvironmentRevision, resolvedByNode map[string]map[string]any) ([]lockedStep, error) {
	if len(revision.AcceptanceJobs) == 0 {
		return nil, fmt.Errorf("%w: scenario requires at least one business acceptance job", domain.ErrInvalid)
	}
	checked := revision
	checked.AcceptanceJobs = append([]domain.ScenarioAcceptanceJob(nil), revision.AcceptanceJobs...)
	if err := p.scenarios.normalizeScenarioAcceptance(ctx, &checked); err != nil {
		return nil, err
	}
	if err := p.scenarios.validateScenarioAcceptanceWorkspace(revision); err != nil {
		return nil, err
	}
	variables, err := resolveAcceptanceParameters(revision, environment, resolvedByNode)
	if err != nil {
		return nil, err
	}
	steps := make([]lockedStep, 0, len(revision.AcceptanceJobs))
	for _, job := range revision.AcceptanceJobs {
		for _, name := range job.RequiredCredentials {
			found := false
			for _, credential := range environment.CredentialRefs {
				if credential.Name == name && credential.Valid() && credential.Reference != "" {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("%w: acceptance job %s requires CredentialRef %s", domain.ErrInvalid, job.Name, name)
			}
		}
		if job.NeedsYAMLMigration() {
			return nil, domain.YAMLMigrationRequired(job.Name)
		}
		steps = append(steps, lockedStep{ID: "acceptance-" + job.ID, NodeID: "acceptance:" + job.ID, Name: job.Name, SourceType: "scenario_acceptance", ScenarioRevisionID: revision.ID, AcceptanceJobID: job.ID, Stage: "acceptance", Phase: "acceptance", Action: domain.ActionKind("acceptance"), ActionID: job.ID, Playbook: job.Playbook, PlaybookDigest: job.PlaybookSHA256, WorkspaceDigest: revision.AcceptanceTreeSHA256, Variables: variables, RequiredCredentials: job.RequiredCredentials, Limit: job.HostGroup, TimeoutSeconds: job.TimeoutSeconds, Become: job.Become, MayMutate: job.MayMutate, NeedsApproval: job.RiskLevel == domain.RiskHigh || job.RiskLevel == domain.RiskDestructive, RetrySafe: !job.MayMutate})
	}
	return steps, nil
}
