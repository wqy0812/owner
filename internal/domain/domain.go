package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Role string

const (
	RoleComponentOwner   Role = "component_owner"
	RoleScenarioOwner    Role = "scenario_owner"
	RoleEnvironmentOwner Role = "environment_owner"
	RolePlatformAdmin    Role = "platform_admin"
)

func (r Role) Valid() bool {
	return r == RoleComponentOwner || r == RoleScenarioOwner || r == RoleEnvironmentOwner || r == RolePlatformAdmin
}

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

type PlatformOptionCategoryKind string

const (
	PlatformOptionEnvironmentDimension PlatformOptionCategoryKind = "environment_dimension"
	PlatformOptionHostGroup            PlatformOptionCategoryKind = "host_group"
)

func (kind PlatformOptionCategoryKind) Valid() bool {
	return kind == PlatformOptionEnvironmentDimension || kind == PlatformOptionHostGroup
}

type PlatformOptionUsage struct {
	ComponentReleases    int `json:"componentReleases"`
	ScenarioRevisions    int `json:"scenarioRevisions"`
	EnvironmentRevisions int `json:"environmentRevisions"`
}

func (usage PlatformOptionUsage) InUse() bool {
	return usage.ComponentReleases > 0 || usage.ScenarioRevisions > 0 || usage.EnvironmentRevisions > 0
}

type PlatformOption struct {
	ID             string              `json:"id"`
	CategoryID     string              `json:"categoryId"`
	ParentOptionID string              `json:"parentOptionId,omitempty"`
	Value          string              `json:"value"`
	Label          string              `json:"label"`
	SortOrder      int                 `json:"sortOrder"`
	CreatedBy      string              `json:"createdBy"`
	CreatedAt      time.Time           `json:"createdAt"`
	RetiredAt      *time.Time          `json:"retiredAt,omitempty"`
	Usage          PlatformOptionUsage `json:"usage"`
}

type PlatformOptionCategory struct {
	ID                  string                     `json:"id"`
	ParentCategoryID    string                     `json:"parentCategoryId,omitempty"`
	Key                 string                     `json:"key"`
	Label               string                     `json:"label"`
	Kind                PlatformOptionCategoryKind `json:"kind"`
	EnvironmentRequired bool                       `json:"environmentRequired"`
	SortOrder           int                        `json:"sortOrder"`
	CreatedBy           string                     `json:"createdBy"`
	CreatedAt           time.Time                  `json:"createdAt"`
	RetiredAt           *time.Time                 `json:"retiredAt,omitempty"`
	Usage               PlatformOptionUsage        `json:"usage"`
	Options             []PlatformOption           `json:"options"`
}

type ReleaseStatus string

const (
	ReleaseDraft      ReleaseStatus = "draft"
	ReleaseReleased   ReleaseStatus = "released"
	ReleaseDeprecated ReleaseStatus = "deprecated"
)

type ReleaseReviewStatus string

const (
	ReleaseReviewNotSubmitted ReleaseReviewStatus = "not_submitted"
	ReleaseReviewPending      ReleaseReviewStatus = "pending"
	ReleaseReviewApproved     ReleaseReviewStatus = "approved"
	ReleaseReviewRejected     ReleaseReviewStatus = "rejected"
)

type ReleaseReview struct {
	Status         ReleaseReviewStatus `json:"status"`
	ContractDigest string              `json:"contractDigest,omitempty"`
	SubmittedAt    *time.Time          `json:"submittedAt,omitempty"`
	ReviewedBy     string              `json:"reviewedBy,omitempty"`
	ReviewedAt     *time.Time          `json:"reviewedAt,omitempty"`
	Comment        string              `json:"comment,omitempty"`
}

type ComponentLayer string

const (
	LayerHostFoundation          ComponentLayer = "host_foundation"
	LayerRuntimeState            ComponentLayer = "runtime_state"
	LayerOrchestrationCore       ComponentLayer = "orchestration_core"
	LayerClusterService          ComponentLayer = "cluster_service"
	LayerObservabilityManagement ComponentLayer = "observability_management"
	LayerPlatformExtension       ComponentLayer = "platform_extension"
)

func ValidateComponentClassification(component Component) error {
	if component.Layer != LayerHostFoundation && component.Layer != LayerRuntimeState && component.Layer != LayerOrchestrationCore && component.Layer != LayerClusterService && component.Layer != LayerObservabilityManagement && component.Layer != LayerPlatformExtension {
		return fmt.Errorf("%w: invalid component layer %q", ErrInvalid, component.Layer)
	}
	if len(component.Tags) > 8 {
		return fmt.Errorf("%w: components may define at most 8 tags", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, tag := range component.Tags {
		if tag == "" || len(tag) > 32 || tag != strings.ToLower(tag) || strings.TrimSpace(tag) != tag || strings.ContainsAny(tag, " ,") || seen[tag] {
			return fmt.Errorf("%w: component tags must be unique lowercase values of at most 32 characters", ErrInvalid)
		}
		seen[tag] = true
	}
	return nil
}

type RiskLevel string

const (
	RiskLow         RiskLevel = "low"
	RiskMedium      RiskLevel = "medium"
	RiskHigh        RiskLevel = "high"
	RiskDestructive RiskLevel = "destructive"
)

type Component struct {
	ID          string             `json:"id"`
	Slug        string             `json:"slug"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Layer       ComponentLayer     `json:"layer"`
	Tags        []string           `json:"tags"`
	OwnerID     string             `json:"ownerId"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	Releases    []ComponentRelease `json:"releases,omitempty"`
}

type ComponentRelease struct {
	ID                      string               `json:"id"`
	ComponentID             string               `json:"componentId"`
	LineID                  string               `json:"lineId"`
	LineName                string               `json:"lineName"`
	ParentReleaseID         string               `json:"parentReleaseId,omitempty"`
	TemplateSourceReleaseID string               `json:"templateSourceReleaseId,omitempty"`
	Version                 string               `json:"version"`
	Status                  ReleaseStatus        `json:"status"`
	ReleaseNotes            string               `json:"releaseNotes"`
	Compatibility           ReleaseCompatibility `json:"compatibility"`
	// Candidate is an explicit component-owner handoff. A ready Draft marked as
	// a candidate may be composed and tested by a scenario owner, then released
	// atomically with that scenario revision.
	Candidate              bool                  `json:"candidate"`
	Review                 ReleaseReview         `json:"review"`
	PublicationGeneration  int64                 `json:"-"`
	Readiness              ReleaseReadiness      `json:"readiness"`
	RiskLevel              RiskLevel             `json:"riskLevel"`
	EnvironmentConstraints map[string]any        `json:"environmentConstraints"`
	Parameters             []ParameterDefinition `json:"parameters"`
	Dependencies           []ComponentDependency `json:"dependencies"`
	Actions                []ActionDefinition    `json:"actions"`
	Artifacts              []ComponentArtifact   `json:"artifacts"`
	Images                 []ComponentImage      `json:"images"`
	CreatedAt              time.Time             `json:"createdAt"`
	ReleasedAt             *time.Time            `json:"releasedAt,omitempty"`
	DeprecatedAt           *time.Time            `json:"deprecatedAt,omitempty"`
}

type ReleaseCompatibility string

const (
	CompatibilityNotApplicable ReleaseCompatibility = "not_applicable"
	CompatibilityCompatible    ReleaseCompatibility = "compatible"
	CompatibilityBreaking      ReleaseCompatibility = "breaking"
)

func (value ReleaseCompatibility) Valid() bool {
	return value == CompatibilityNotApplicable || value == CompatibilityCompatible || value == CompatibilityBreaking
}

type ComponentReleaseLine struct {
	ID                     string             `json:"id"`
	ComponentID            string             `json:"componentId"`
	Name                   string             `json:"name"`
	LatestReleasedID       string             `json:"latestReleasedId,omitempty"`
	CurrentDraftID         string             `json:"currentDraftId,omitempty"`
	EvolutionEligible      bool               `json:"evolutionEligible"`
	EvolutionParentID      string             `json:"evolutionParentId,omitempty"`
	EvolutionBlockedReason string             `json:"evolutionBlockedReason,omitempty"`
	Releases               []ComponentRelease `json:"releases"`
	CreatedAt              time.Time          `json:"createdAt"`
}

type ReadinessStatus string

const (
	ReadinessReady   ReadinessStatus = "ready"
	ReadinessBlocked ReadinessStatus = "blocked"
	ReadinessRisky   ReadinessStatus = "risky"
)

type ReadinessBlocker struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	ActionURL string `json:"actionUrl"`
}

type ReleaseReadiness struct {
	Status                  ReadinessStatus    `json:"status"`
	Blockers                []ReadinessBlocker `json:"blockers"`
	InstallEvidenceRunID    string             `json:"installEvidenceRunId,omitempty"`
	RollbackEvidenceRunID   string             `json:"rollbackEvidenceRunId,omitempty"`
	TransitionEvidenceRunID string             `json:"transitionEvidenceRunId,omitempty"`
	RuntimeEvidence         []RuntimeEvidence  `json:"runtimeEvidence,omitempty"`
}

type RuntimeCompatibility struct {
	Runtime string `json:"runtime"`
	Version string `json:"version"`
}

type RuntimeEvidence struct {
	Runtime                 string `json:"runtime"`
	Version                 string `json:"version"`
	InstallEvidenceRunID    string `json:"installEvidenceRunId,omitempty"`
	RollbackEvidenceRunID   string `json:"rollbackEvidenceRunId,omitempty"`
	TransitionEvidenceRunID string `json:"transitionEvidenceRunId,omitempty"`
	Complete                bool   `json:"complete"`
}

type ComponentArtifact struct {
	ID              string    `json:"id"`
	ReleaseID       string    `json:"releaseId"`
	Alias           string    `json:"alias"`
	Filename        string    `json:"filename"`
	SHA256          string    `json:"sha256"`
	SizeBytes       int64     `json:"sizeBytes"`
	SourceURL       string    `json:"sourceUrl"`
	SourceUpdatedBy string    `json:"sourceUpdatedBy"`
	SourceUpdatedAt time.Time `json:"sourceUpdatedAt"`
	CreatedBy       string    `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`
}

type ComponentImage struct {
	ID              string    `json:"id"`
	ReleaseID       string    `json:"releaseId"`
	LogicalName     string    `json:"logicalName"`
	Digest          string    `json:"digest"`
	SourceRef       string    `json:"sourceRef"`
	SourceUpdatedBy string    `json:"sourceUpdatedBy"`
	SourceUpdatedAt time.Time `json:"sourceUpdatedAt"`
	CreatedBy       string    `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`
}

type ImageBuildStatus string

const (
	ImageBuildQueued      ImageBuildStatus = "queued"
	ImageBuildRunning     ImageBuildStatus = "running"
	ImageBuildSucceeded   ImageBuildStatus = "succeeded"
	ImageBuildFailed      ImageBuildStatus = "failed"
	ImageBuildCancelled   ImageBuildStatus = "cancelled"
	ImageBuildInterrupted ImageBuildStatus = "interrupted"
)

type ComponentImageBuild struct {
	ID                    string           `json:"id"`
	ReleaseID             string           `json:"releaseId"`
	EnvironmentID         string           `json:"environmentId,omitempty"`
	EnvironmentRevisionID string           `json:"environmentRevisionId,omitempty"`
	RequestedBy           string           `json:"requestedBy"`
	Status                ImageBuildStatus `json:"status"`
	DockerfileSHA256      string           `json:"dockerfileSha256"`
	ImageTag              string           `json:"imageTag"`
	ImageRef              string           `json:"imageRef"`
	ImageDigest           string           `json:"imageDigest,omitempty"`
	Error                 string           `json:"error,omitempty"`
	CreatedAt             time.Time        `json:"createdAt"`
	StartedAt             *time.Time       `json:"startedAt,omitempty"`
	FinishedAt            *time.Time       `json:"finishedAt,omitempty"`
	Logs                  []ImageBuildLog  `json:"logs,omitempty"`
}

type ImageBuildLog struct {
	ID        int64     `json:"id"`
	BuildID   string    `json:"buildId"`
	Stream    string    `json:"stream"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type ParameterVisibility string

const (
	ParameterInternal ParameterVisibility = "internal"
	ParameterPublic   ParameterVisibility = "public"
)

type ParameterType string

const (
	ParameterTypeString  ParameterType = "string"
	ParameterTypeBoolean ParameterType = "boolean"
	ParameterTypeInteger ParameterType = "integer"
	ParameterTypeNumber  ParameterType = "number"
	ParameterTypeObject  ParameterType = "object"
	ParameterTypeArray   ParameterType = "array"
)

type ParameterValueProvider string

const (
	ParameterProviderComponentOwner   ParameterValueProvider = "component_owner"
	ParameterProviderScenarioOwner    ParameterValueProvider = "scenario_owner"
	ParameterProviderEnvironmentOwner ParameterValueProvider = "environment_owner"
	ParameterProviderUpstreamMapping  ParameterValueProvider = "upstream_mapping"
)

func (p ParameterValueProvider) Valid() bool {
	return p == ParameterProviderComponentOwner || p == ParameterProviderScenarioOwner || p == ParameterProviderEnvironmentOwner || p == ParameterProviderUpstreamMapping
}

type EnvironmentBindingKind string

const (
	EnvironmentBindingPrivate EnvironmentBindingKind = "private"
	EnvironmentBindingGlobal  EnvironmentBindingKind = "global"
)

type EnvironmentParameterBinding struct {
	Kind         EnvironmentBindingKind `json:"kind"`
	DefinitionID string                 `json:"definitionId,omitempty"`
}

type EnvironmentParameterDefinition struct {
	ID           string        `json:"id"`
	Key          string        `json:"key"`
	Label        string        `json:"label"`
	Description  string        `json:"description"`
	Type         ParameterType `json:"type"`
	Enum         []any         `json:"enum,omitempty"`
	MinLength    int           `json:"minLength,omitempty"`
	DefaultValue any           `json:"defaultValue,omitempty"`
	CreatedBy    string        `json:"createdBy"`
	CreatedAt    time.Time     `json:"createdAt"`
	Usage        int           `json:"usage"`
}

type EnvironmentVariableDefinition struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	Usage       int       `json:"usage"`
}

func EnvironmentParameterValueKey(releaseID string, parameter ParameterDefinition) string {
	if parameter.EnvironmentBinding != nil && parameter.EnvironmentBinding.Kind == EnvironmentBindingGlobal {
		return "global:" + parameter.EnvironmentBinding.DefinitionID
	}
	return "release:" + releaseID + ":" + parameter.Name
}

func (b EnvironmentParameterBinding) Valid() bool {
	return b.Kind == EnvironmentBindingPrivate || (b.Kind == EnvironmentBindingGlobal && strings.TrimSpace(b.DefinitionID) != "")
}

type ParameterDefinition struct {
	Name               string                       `json:"name"`
	Description        string                       `json:"description"`
	Type               ParameterType                `json:"type"`
	Required           bool                         `json:"required"`
	Visibility         ParameterVisibility          `json:"visibility"`
	Modifiable         bool                         `json:"modifiable"`
	ValueProvider      ParameterValueProvider       `json:"valueProvider"`
	FixedValue         any                          `json:"fixedValue,omitempty"`
	SuggestedValue     any                          `json:"suggestedValue,omitempty"`
	TestValue          any                          `json:"testValue,omitempty"`
	EnvironmentBinding *EnvironmentParameterBinding `json:"environmentBinding,omitempty"`
	Enum               []any                        `json:"enum,omitempty"`
	MinLength          int                          `json:"minLength,omitempty"`
}

func (p ParameterDefinition) HasFixedValue() bool     { return p.FixedValue != nil }
func (p ParameterDefinition) HasSuggestedValue() bool { return p.SuggestedValue != nil }
func (p ParameterDefinition) HasTestValue() bool      { return p.TestValue != nil }

func (p ParameterType) Valid() bool {
	switch p {
	case ParameterTypeString, ParameterTypeBoolean, ParameterTypeInteger, ParameterTypeNumber, ParameterTypeObject, ParameterTypeArray:
		return true
	default:
		return false
	}
}

func (p ParameterVisibility) Valid() bool {
	return p == ParameterInternal || p == ParameterPublic
}

func ParameterByName(parameters []ParameterDefinition, name string) (ParameterDefinition, bool) {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return ParameterDefinition{}, false
}

func MappedTargets(dependencies []ComponentDependency) map[string]ParameterMapping {
	targets := make(map[string]ParameterMapping)
	for _, dependency := range dependencies {
		for _, mapping := range dependency.ParameterMappings {
			targets[mapping.TargetParameter] = mapping
		}
	}
	return targets
}

func MappingContract(dependencies []ComponentDependency) []string {
	keys := make([]string, 0)
	for _, dependency := range dependencies {
		for _, mapping := range dependency.ParameterMappings {
			keys = append(keys, dependency.UpstreamComponentID+"\x00"+mapping.UpstreamParameter+"\x00"+mapping.TargetParameter)
		}
	}
	slices.Sort(keys)
	return keys
}

type ParameterMapping struct {
	UpstreamParameter string `json:"upstreamParameter"`
	TargetParameter   string `json:"targetParameter"`
}

type ComponentDependency struct {
	ID                    string             `json:"id"`
	ReleaseID             string             `json:"releaseId"`
	UpstreamComponentID   string             `json:"upstreamComponentId"`
	UpstreamReleaseID     string             `json:"upstreamReleaseId"`
	UpstreamComponentName string             `json:"upstreamComponentName,omitempty"`
	UpstreamVersion       string             `json:"upstreamVersion,omitempty"`
	Purpose               string             `json:"purpose"`
	ParameterMappings     []ParameterMapping `json:"parameterMappings"`
}

type ActionKind string

const (
	ActionInspect   ActionKind = "inspect"
	ActionPreflight ActionKind = "preflight"
	ActionInstall   ActionKind = "install"
	ActionConfigure ActionKind = "configure"
	ActionVerify    ActionKind = "verify"
	ActionUpgrade   ActionKind = "upgrade"
	ActionRollback  ActionKind = "rollback"
	ActionUninstall ActionKind = "uninstall"
)

type ActionDefinition struct {
	ID                  string     `json:"id"`
	ReleaseID           string     `json:"releaseId"`
	Name                string     `json:"name"`
	Kind                ActionKind `json:"kind"`
	Playbook            string     `json:"playbook"`
	PlaybookSHA256      string     `json:"-"`
	Tags                []string   `json:"tags"`
	HostGroup           string     `json:"hostGroup"`
	RequiredCredentials []string   `json:"requiredCredentials"`
	TimeoutSeconds      int        `json:"timeoutSeconds"`
	RiskLevel           RiskLevel  `json:"riskLevel"`
	Destructive         bool       `json:"destructive"`
	Idempotent          bool       `json:"idempotent"`
	FromReleaseID       string     `json:"fromReleaseId,omitempty"`
	ToReleaseID         string     `json:"toReleaseId,omitempty"`
}

func (a ActionDefinition) NeedsApproval() bool {
	if a.Destructive || a.RiskLevel == RiskDestructive || a.Kind == ActionRollback {
		return true
	}
	s := strings.ToLower(string(a.Kind) + " " + a.Name + " " + a.Playbook)
	for _, marker := range []string{"recovery", "clean", "destroy", "uninstall", " rcv", "rcv_"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

type RevisionStatus string

const (
	RevisionDraft      RevisionStatus = "draft"
	RevisionTesting    RevisionStatus = "testing"
	RevisionTestPassed RevisionStatus = "test_passed"
	RevisionReleased   RevisionStatus = "released"
	RevisionDeprecated RevisionStatus = "deprecated"
)

type Scenario struct {
	ID                string             `json:"id"`
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	OwnerID           string             `json:"ownerId"`
	CurrentRevisionID string             `json:"currentRevisionId,omitempty"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
	Revisions         []ScenarioRevision `json:"revisions,omitempty"`
}

type ScenarioRevision struct {
	ID                    string         `json:"id"`
	ScenarioID            string         `json:"scenarioId"`
	Revision              int            `json:"revision"`
	Status                RevisionStatus `json:"status"`
	PublicationGeneration int64          `json:"-"`
	Graph                 ScenarioGraph  `json:"graph"`
	CreatedAt             time.Time      `json:"createdAt"`
	TestPassedAt          *time.Time     `json:"testPassedAt,omitempty"`
	ReleasedAt            *time.Time     `json:"releasedAt,omitempty"`
	DeprecatedAt          *time.Time     `json:"deprecatedAt,omitempty"`
	AbandonedAt           *time.Time     `json:"abandonedAt,omitempty"`
}

type ScenarioGraph struct {
	Nodes []ScenarioNode `json:"nodes"`
	Edges []ScenarioEdge `json:"edges"`
}

type ScenarioNode struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	ReleaseID         string            `json:"releaseId"`
	Action            ActionKind        `json:"action"`
	HostGroup         string            `json:"hostGroup"`
	ParameterValues   map[string]any    `json:"parameterValues"`
	DependencySources map[string]string `json:"dependencySources,omitempty"`
	Position          GraphPosition     `json:"position"`
	Destructive       bool              `json:"destructive,omitempty"`
}

type GraphPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type ScenarioEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}

type ValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
}

func ValidateGraph(g ScenarioGraph) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if len(g.Nodes) == 0 {
		return append(issues, ValidationIssue{Code: "empty_graph", Message: "scenario graph must contain at least one node"})
	}
	nodes := map[string]ScenarioNode{}
	for _, n := range g.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			issues = append(issues, ValidationIssue{Code: "missing_node_id", Message: "node id is required"})
			continue
		}
		if _, ok := nodes[n.ID]; ok {
			issues = append(issues, ValidationIssue{Code: "duplicate_node", Message: "duplicate node id", NodeID: n.ID})
		}
		nodes[n.ID] = n
		if n.ReleaseID == "" {
			issues = append(issues, ValidationIssue{Code: "missing_release", Message: "node must lock a component release", NodeID: n.ID})
		}
		if n.Action == "" {
			issues = append(issues, ValidationIssue{Code: "missing_action", Message: "node action is required", NodeID: n.ID})
		}
		if n.HostGroup == "" {
			issues = append(issues, ValidationIssue{Code: "missing_host_group", Message: "node hostGroup is required", NodeID: n.ID})
		}
	}
	adj := map[string][]string{}
	indegree := map[string]int{}
	for id := range nodes {
		indegree[id] = 0
	}
	edgeIDs := map[string]bool{}
	for _, e := range g.Edges {
		if e.ID != "" && edgeIDs[e.ID] {
			issues = append(issues, ValidationIssue{Code: "duplicate_edge", Message: "duplicate edge id"})
		}
		edgeIDs[e.ID] = true
		if _, ok := nodes[e.Source]; !ok {
			issues = append(issues, ValidationIssue{Code: "missing_edge_source", Message: "edge source does not exist", NodeID: e.Source})
			continue
		}
		if _, ok := nodes[e.Target]; !ok {
			issues = append(issues, ValidationIssue{Code: "missing_edge_target", Message: "edge target does not exist", NodeID: e.Target})
			continue
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
		indegree[e.Target]++
	}
	queue := make([]string, 0)
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adj[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodes) {
		issues = append(issues, ValidationIssue{Code: "cycle", Message: "scenario graph contains a cycle"})
	}
	return issues
}

type Environment struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	OwnerID           string                  `json:"ownerId"`
	CurrentRevisionID string                  `json:"currentRevisionId,omitempty"`
	ArchivedAt        *time.Time              `json:"archivedAt,omitempty"`
	CreatedAt         time.Time               `json:"createdAt"`
	UpdatedAt         time.Time               `json:"updatedAt"`
	Revision          *EnvironmentRevision    `json:"revision,omitempty"`
	Revisions         []EnvironmentRevision   `json:"revisions,omitempty"`
	HealthCheck       *EnvironmentHealthCheck `json:"healthCheck,omitempty"`
	SSHCheck          *EnvironmentSSHCheck    `json:"sshCheck,omitempty"`
}

type EnvironmentRevision struct {
	ID             string            `json:"id"`
	EnvironmentID  string            `json:"environmentId"`
	Revision       int               `json:"revision"`
	Facts          map[string]any    `json:"facts"`
	Inventory      json.RawMessage   `json:"inventory"`
	Variables      map[string]string `json:"variables"`
	Parameters     map[string]any    `json:"parameters"`
	CredentialRefs []CredentialRef   `json:"credentialRefs"`
	CreatedBy      string            `json:"createdBy,omitempty"`
	ChangeReason   string            `json:"changeReason,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
}

type EnvironmentEndpointCheck struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Reachable bool   `json:"reachable"`
	LatencyMS int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

type EnvironmentHealthCheck struct {
	ID                    string                     `json:"id"`
	EnvironmentID         string                     `json:"environmentId"`
	EnvironmentRevisionID string                     `json:"environmentRevisionId"`
	Status                string                     `json:"status"`
	Results               []EnvironmentEndpointCheck `json:"results"`
	CheckedAt             time.Time                  `json:"checkedAt"`
}

type EnvironmentSSHHostCheck struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	User      string `json:"user,omitempty"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

type EnvironmentSSHCheck struct {
	ID                    string                    `json:"id"`
	EnvironmentID         string                    `json:"environmentId"`
	EnvironmentRevisionID string                    `json:"environmentRevisionId"`
	Status                string                    `json:"status"`
	DurationMS            int64                     `json:"durationMs"`
	Results               []EnvironmentSSHHostCheck `json:"results"`
	CheckedAt             time.Time                 `json:"checkedAt"`
}

type EnvironmentConnectivityCheck struct {
	TCP EnvironmentHealthCheck `json:"tcpCheck"`
	SSH EnvironmentSSHCheck    `json:"sshCheck"`
}

// BackupMetadata binds a remote backup to the exact installation that
// captured it. Playbooks persist this value in their .captured marker while
// the platform keeps the authoritative reference on the environment.
type BackupMetadata struct {
	EnvironmentID      string         `json:"environmentId"`
	ComponentID        string         `json:"componentId"`
	ReleaseID          string         `json:"releaseId"`
	ActionID           string         `json:"actionId"`
	InstallRunID       string         `json:"installRunId"`
	CapturedAt         time.Time      `json:"capturedAt"`
	PlaybookSHA256     string         `json:"playbookSha256"`
	DependencySnapshot map[string]any `json:"dependencySnapshot"`
}

type EnvironmentComponentInstallation struct {
	EnvironmentID string         `json:"environmentId"`
	ComponentID   string         `json:"componentId"`
	ReleaseID     string         `json:"releaseId"`
	InstallRunID  string         `json:"installRunId"`
	BackupRef     string         `json:"backupRef"`
	Backup        BackupMetadata `json:"backup"`
	TestOnly      bool           `json:"testOnly"`
	InstalledAt   time.Time      `json:"installedAt"`
}

type CredentialRef struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"` // sshKeyPath | envVarRef
	Reference  string `json:"reference,omitempty"`
	Configured bool   `json:"configured"`
}

func (c CredentialRef) Valid() bool { return c.Kind == "sshKeyPath" || c.Kind == "envVarRef" }

func RedactCredentialRefs(refs []CredentialRef, privileged bool) []CredentialRef {
	out := slices.Clone(refs)
	for i := range out {
		out[i].Configured = out[i].Reference != ""
		if !privileged {
			out[i].Reference = ""
		}
	}
	return out
}

type RunStatus string

const (
	RunAwaitingApproval RunStatus = "awaiting_approval"
	RunQueued           RunStatus = "queued"
	RunRunning          RunStatus = "running"
	RunSucceeded        RunStatus = "succeeded"
	RunFailed           RunStatus = "failed"
	RunCancelled        RunStatus = "cancelled"
	RunRejected         RunStatus = "rejected"
	RunInterrupted      RunStatus = "interrupted"
)

type RunKind string

const (
	RunComponentTest       RunKind = "component_test"
	RunScenarioTest        RunKind = "scenario_test"
	RunScenario            RunKind = "scenario_run"
	RunEnvironmentRollback RunKind = "environment_rollback"
)

type Run struct {
	ID                    string         `json:"id"`
	Kind                  RunKind        `json:"kind"`
	Status                RunStatus      `json:"status"`
	RequestedBy           string         `json:"requestedBy"`
	EnvironmentID         string         `json:"environmentId"`
	EnvironmentRevisionID string         `json:"environmentRevisionId"`
	ComponentReleaseID    string         `json:"componentReleaseId,omitempty"`
	ScenarioRevisionID    string         `json:"scenarioRevisionId,omitempty"`
	Action                ActionKind     `json:"action,omitempty"`
	Destructive           bool           `json:"destructive"`
	InputSnapshot         map[string]any `json:"inputSnapshot"`
	ArtifactDigest        string         `json:"artifactDigest"`
	RetryOfRunID          string         `json:"retryOfRunId,omitempty"`
	RetryRootRunID        string         `json:"retryRootRunId,omitempty"`
	RetryAttempt          int            `json:"retryAttempt,omitempty"`
	RetryStartStep        int            `json:"retryStartStep,omitempty"`
	Error                 string         `json:"error,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	StartedAt             *time.Time     `json:"startedAt,omitempty"`
	FinishedAt            *time.Time     `json:"finishedAt,omitempty"`
	Steps                 []RunStep      `json:"steps,omitempty"`
	Approval              *Approval      `json:"approval,omitempty"`
}

type RunStep struct {
	ID         string     `json:"id"`
	RunID      string     `json:"runId"`
	NodeID     string     `json:"nodeId"`
	Name       string     `json:"name"`
	Status     RunStatus  `json:"status"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type RunLog struct {
	ID        int64     `json:"id"`
	RunID     string    `json:"runId"`
	StepID    string    `json:"stepId,omitempty"`
	Stream    string    `json:"stream"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type Approval struct {
	ID          string     `json:"id"`
	RunID       string     `json:"runId"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requestedAt"`
	DecidedBy   string     `json:"decidedBy,omitempty"`
	Decision    string     `json:"decision,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	DecidedAt   *time.Time `json:"decidedAt,omitempty"`
}

type Notification struct {
	ID          string         `json:"id"`
	UserID      string         `json:"userId"`
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	ResourceURL string         `json:"resourceUrl,omitempty"`
	Payload     map[string]any `json:"payload"`
	ReadAt      *time.Time     `json:"readAt,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

type AuditEvent struct {
	ID           string         `json:"id"`
	ActorID      string         `json:"actorId"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    time.Time      `json:"createdAt"`
}

type WorkPriority string

const (
	WorkPriorityCritical WorkPriority = "critical"
	WorkPriorityHigh     WorkPriority = "high"
	WorkPriorityNormal   WorkPriority = "normal"
	WorkPriorityInfo     WorkPriority = "info"
)

type WorkStatus string

const (
	WorkStatusBlocked        WorkStatus = "blocked"
	WorkStatusActionRequired WorkStatus = "action_required"
	WorkStatusInProgress     WorkStatus = "in_progress"
	WorkStatusAttention      WorkStatus = "attention"
)

type WorkSubject struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	ParentID    string `json:"parentId,omitempty"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type WorkReason struct {
	Code          string      `json:"code"`
	Message       string      `json:"message"`
	EvidenceRunID string      `json:"evidenceRunId,omitempty"`
	Cause         *WorkCause  `json:"cause,omitempty"`
	NextAction    *WorkAction `json:"nextAction,omitempty"`
}

type WorkAction struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

type WorkCause struct {
	Kind      string     `json:"kind"`
	Summary   string     `json:"summary"`
	ActorID   string     `json:"actorId,omitempty"`
	ActorName string     `json:"actorName,omitempty"`
	Action    string     `json:"action,omitempty"`
	At        *time.Time `json:"at,omitempty"`
}

type WorkExplanation struct {
	Reasons          []WorkReason `json:"reasons"`
	PrimaryAction    *WorkAction  `json:"primaryAction,omitempty"`
	SecondaryActions []WorkAction `json:"secondaryActions"`
}

// ActionableError preserves the existing sentinel error contract while adding
// a structured explanation that clients can render without parsing prose.
type ActionableError struct {
	Base        error
	Explanation WorkExplanation
}

func (e *ActionableError) Error() string {
	if e == nil || e.Base == nil {
		return "actionable error"
	}
	return e.Base.Error()
}

func (e *ActionableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Base
}

type WorkItem struct {
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	Priority         WorkPriority `json:"priority"`
	Status           WorkStatus   `json:"status"`
	Title            string       `json:"title"`
	Subject          WorkSubject  `json:"subject"`
	Reasons          []WorkReason `json:"reasons"`
	PrimaryAction    WorkAction   `json:"primaryAction"`
	SecondaryActions []WorkAction `json:"secondaryActions"`
	UpdatedAt        time.Time    `json:"updatedAt"`
}

type WorkbenchSummary struct {
	Critical       int `json:"critical"`
	ActionRequired int `json:"actionRequired"`
	InProgress     int `json:"inProgress"`
	Informational  int `json:"informational"`
}

type WorkbenchAssets struct {
	Components   int `json:"components"`
	Scenarios    int `json:"scenarios"`
	Environments int `json:"environments"`
}

type Workbench struct {
	GeneratedAt time.Time        `json:"generatedAt"`
	Role        Role             `json:"role"`
	Summary     WorkbenchSummary `json:"summary"`
	Assets      WorkbenchAssets  `json:"assets"`
	Items       []WorkItem       `json:"items"`
}

// CatalogBackupHealth is the role-facing health projection for publication
// recovery points. It contains no repository credentials or secret material.
type CatalogBackupHealth struct {
	Configured         bool       `json:"configured"`
	CurrentGeneration  int64      `json:"currentGeneration"`
	BackedUpGeneration int64      `json:"backedUpGeneration"`
	Behind             bool       `json:"behind"`
	LastSuccessfulAt   *time.Time `json:"lastSuccessfulAt,omitempty"`
	LastError          string     `json:"lastError,omitempty"`
	LastErrorAt        *time.Time `json:"lastErrorAt,omitempty"`
	RepositoryPath     string     `json:"repositoryPath,omitempty"`
}

type ImpactPath struct {
	ComponentIDs   []string `json:"componentIds"`
	ComponentNames []string `json:"componentNames"`
}

type ImpactRecipient struct {
	UserID      string       `json:"userId"`
	Role        Role         `json:"role"`
	Paths       []ImpactPath `json:"paths"`
	ScenarioIDs []string     `json:"scenarioIds,omitempty"`
}

type ImpactReport struct {
	ComponentID      string            `json:"componentId"`
	ChangeKind       string            `json:"changeKind"`
	LineID           string            `json:"lineId,omitempty"`
	LineName         string            `json:"lineName,omitempty"`
	FromReleaseID    string            `json:"fromReleaseId,omitempty"`
	ToReleaseID      string            `json:"toReleaseId,omitempty"`
	Recipients       []ImpactRecipient `json:"recipients"`
	ScenarioRunCount int               `json:"scenarioRunCount"`
}

var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid")
	ErrUnauthorized = errors.New("unauthorized")
)

type ValidationError struct {
	Message string
	Details any
}

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

// CodedError preserves a stable API error code and structured details while
// still participating in the existing sentinel-error status mapping.
type CodedError struct {
	Code    string
	Message string
	Details any
	Cause   error
}

func (e *CodedError) Error() string { return e.Message }
func (e *CodedError) Unwrap() error { return e.Cause }

func ValidateRole(user User, role Role) error {
	if user.Role != role {
		return fmt.Errorf("%w: requires %s role", ErrForbidden, role)
	}
	return nil
}
