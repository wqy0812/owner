package service

import (
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	mediadelivery "codex/platform-demo/internal/delivery"
	"codex/platform-demo/internal/domain"
)

type digestRunner interface {
	Digest(string) (playbookSHA256, treeSHA256 string, err error)
}

type lockedStep struct {
	RollbackSourceVariables map[string]any            `json:"rollbackSourceVariables,omitempty"`
	RollbackSourceFrozen    bool                      `json:"rollbackSourceFrozen,omitempty"`
	RollbackSourceActionID  string                    `json:"rollbackSourceActionId,omitempty"`
	PreCheckRequired        bool                      `json:"preCheckRequired,omitempty"`
	RuntimeChecks           []domain.RuntimeCheck     `json:"runtimeChecks,omitempty"`
	ResourceContract        *domain.ResourceContract  `json:"resourceContract,omitempty"`
	Resources               []domain.ResourceInstance `json:"resources,omitempty"`
	Media                   []ansiblerunner.JobMedia  `json:"media,omitempty"`
	SourceParametersFrozen  bool                      `json:"sourceParametersFrozen,omitempty"`
	SourceType              string                    `json:"sourceType,omitempty"`
	ScenarioRevisionID      string                    `json:"scenarioRevisionId,omitempty"`
	AcceptanceJobID         string                    `json:"acceptanceJobId,omitempty"`
	Stage                   string                    `json:"stage,omitempty"`
	MayMutate               bool                      `json:"mayMutate,omitempty"`
	ParentActionID          string                    `json:"parentActionId"`
	SourceNodeID            string                    `json:"sourceNodeId"`
	Phase                   string                    `json:"phase"`
	Become                  bool                      `json:"become"`
	GatherFacts             bool                      `json:"gatherFacts"`
	ID                      string                    `json:"id"`
	NodeID                  string                    `json:"nodeId"`
	Name                    string                    `json:"name"`
	ComponentID             string                    `json:"componentId"`
	ComponentName           string                    `json:"componentName"`
	ReleaseID               string                    `json:"releaseId"`
	ReleaseVersion          string                    `json:"releaseVersion"`
	ReleaseSpecDigest       string                    `json:"releaseSpecDigest"`
	ActionID                string                    `json:"actionId"`
	Action                  domain.ActionKind         `json:"action"`
	FromReleaseID           string                    `json:"fromReleaseId,omitempty"`
	ToReleaseID             string                    `json:"toReleaseId,omitempty"`
	Playbook                string                    `json:"playbook"`
	PlaybookDigest          string                    `json:"playbookDigest"`
	WorkspaceDigest         string                    `json:"workspaceDigest"`
	Tags                    []string                  `json:"tags"`
	Limit                   string                    `json:"limit"`
	Variables               map[string]any            `json:"variables"`
	RequiredCredentials     []string                  `json:"requiredCredentials"`
	TimeoutSeconds          int                       `json:"timeoutSeconds"`
	NeedsApproval           bool                      `json:"needsApproval"`
	RetrySafe               bool                      `json:"retrySafe"`
	BackupRef               string                    `json:"backupRef,omitempty"`
	Backup                  *domain.BackupMetadata    `json:"backup,omitempty"`
}

type lockedPlan struct {
	ExistingResources          []domain.ResourceInstance               `json:"existingResources,omitempty"`
	ResourcePolicyVersion      int                                     `json:"resourcePolicyVersion,omitempty"`
	RecoveryEnvironmentDigest  string                                  `json:"recoveryEnvironmentDigest,omitempty"`
	ParentSteps                []lockedStep                            `json:"parentSteps"`
	Runtime                    ansiblerunner.JobRuntime                `json:"runtime"`
	Steps                      []lockedStep                            `json:"steps"`
	DeliveryRequirements       []mediadelivery.Requirement             `json:"deliveryRequirements,omitempty"`
	DeliveryDecisions          []mediadelivery.Decision                `json:"deliveryDecisions,omitempty"`
	DeliveryResults            []mediadelivery.Result                  `json:"deliveryResults,omitempty"`
	ArtifactTransfers          []mediadelivery.PlannedArtifactTransfer `json:"artifactTransfers,omitempty"`
	ImageTransfers             []mediadelivery.PlannedImageTransfer    `json:"imageTransfers,omitempty"`
	TreeDigest                 string                                  `json:"treeDigest"`
	InstallationBaseline       []lockedInstallationBaseline            `json:"installationBaseline,omitempty"`
	InstallationBaselineDigest string                                  `json:"installationBaselineDigest,omitempty"`
}

type DeliveryDecisionInput struct {
	RequirementID string `json:"requirementId"`
	Mode          string `json:"mode"`
}

type lockedInstallationBaseline struct {
	NodeID         string `json:"nodeId"`
	ComponentID    string `json:"componentId"`
	ReleaseID      string `json:"releaseId"`
	InstallRunID   string `json:"installRunId"`
	BackupRef      string `json:"backupRef"`
	PlaybookSHA256 string `json:"playbookSha256"`
}

type ComponentTestMode string

type RollbackVerificationKind string

const (
	ComponentTestInstallVerify      ComponentTestMode = "install_verify"
	ComponentTestRollback           ComponentTestMode = "rollback"
	ComponentTestEvolutionRoundTrip ComponentTestMode = "evolution_round_trip"

	RollbackVerificationTargetRelease RollbackVerificationKind = "target_release"
	RollbackVerificationOnly          RollbackVerificationKind = "rollback_only"
)

type RollbackVerification struct {
	Kind      RollbackVerificationKind `json:"kind"`
	ReleaseID string                   `json:"releaseId,omitempty"`
}

type ComponentTestRequest struct {
	ActionID             string               `json:"actionId"`
	EnvironmentID        string               `json:"environmentId"`
	Mode                 ComponentTestMode    `json:"mode"`
	RollbackVerification RollbackVerification `json:"rollbackVerification"`
	ExpectedPlanDigest   string               `json:"expectedPlanDigest,omitempty"`
}

type ComponentTestPlanStep struct {
	RollbackSourceActionID string            `json:"rollbackSourceActionId,omitempty"`
	Name                   string            `json:"name"`
	Stage                  string            `json:"stage"`
	SourceType             string            `json:"sourceType"`
	Phase                  string            `json:"phase"`
	ParentActionID         string            `json:"parentActionId"`
	ActionID               string            `json:"actionId"`
	NodeID                 string            `json:"nodeId"`
	Order                  int               `json:"order"`
	ComponentID            string            `json:"componentId"`
	ComponentName          string            `json:"componentName"`
	ReleaseID              string            `json:"releaseId"`
	ReleaseVersion         string            `json:"releaseVersion"`
	Action                 domain.ActionKind `json:"action"`
	Playbook               string            `json:"playbook"`
	Limit                  string            `json:"limit,omitempty"`
	NeedsApproval          bool              `json:"needsApproval"`
	FromReleaseID          string            `json:"fromReleaseId,omitempty"`
	FromReleaseVersion     string            `json:"fromReleaseVersion,omitempty"`
	ToReleaseID            string            `json:"toReleaseId,omitempty"`
	ToReleaseVersion       string            `json:"toReleaseVersion,omitempty"`
	BackupRef              string            `json:"backupRef,omitempty"`
	BackupInstallRunID     string            `json:"backupInstallRunId,omitempty"`
	BackupCapturedAt       *time.Time        `json:"backupCapturedAt,omitempty"`
	BackupPlaybookSHA      string            `json:"backupPlaybookSha256,omitempty"`
}

type ComponentTestPlan struct {
	EnvironmentID         string                      `json:"environmentId"`
	EnvironmentRevisionID string                      `json:"environmentRevisionId"`
	Destructive           bool                        `json:"destructive"`
	RequiresApproval      bool                        `json:"requiresApproval"`
	PlanDigest            string                      `json:"planDigest"`
	Steps                 []ComponentTestPlanStep     `json:"steps"`
	DeliveryRequirements  []mediadelivery.Requirement `json:"deliveryRequirements"`
}

type preparedComponentTest struct {
	release     domain.ComponentRelease
	environment domain.Environment
	action      domain.ActionKind
	steps       []lockedStep
	provenance  map[string]map[string]resolvedParameter
}
