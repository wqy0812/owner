package service

import (
	"context"
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

type playbookValidationRunner interface {
	ValidatePlaybooks([]string) map[string]error
}

type workspaceRunner interface {
	PrepareWorkspace(playbook, expectedTreeSHA256 string) (*ansiblerunner.Workspace, error)
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
	WorkspaceDigest     string                 `json:"workspaceDigest"`
	Tags                []string               `json:"tags"`
	Limit               string                 `json:"limit"`
	Variables           map[string]any         `json:"variables"`
	RequiredCredentials []string               `json:"requiredCredentials"`
	TimeoutSeconds      int                    `json:"timeoutSeconds"`
	NeedsApproval       bool                   `json:"needsApproval"`
	RetrySafe           bool                   `json:"retrySafe"`
	BackupRef           string                 `json:"backupRef,omitempty"`
	Backup              *domain.BackupMetadata `json:"backup,omitempty"`
}

type lockedPlan struct {
	Steps                      []lockedStep                 `json:"steps"`
	DeliveryRequirements       []DeliveryRequirement        `json:"deliveryRequirements,omitempty"`
	DeliveryDecisions          []DeliveryDecision           `json:"deliveryDecisions,omitempty"`
	DeliveryResults            []DeliveryResult             `json:"deliveryResults,omitempty"`
	ArtifactTransfers          []lockedArtifactTransfer     `json:"artifactTransfers,omitempty"`
	ImageTransfers             []lockedImageTransfer        `json:"imageTransfers,omitempty"`
	TreeDigest                 string                       `json:"treeDigest"`
	InstallationBaseline       []lockedInstallationBaseline `json:"installationBaseline,omitempty"`
	InstallationBaselineDigest string                       `json:"installationBaselineDigest,omitempty"`
}

type DeliveryRequirement struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	Name              string   `json:"name"`
	Identity          string   `json:"identity"`
	Source            string   `json:"source"`
	Target            string   `json:"target,omitempty"`
	SourceReadable    bool     `json:"sourceReadable"`
	TargetPresent     bool     `json:"targetPresent"`
	TransferAvailable bool     `json:"transferAvailable"`
	ReleaseID         string   `json:"releaseId"`
	ComponentID       string   `json:"componentId"`
	ComponentName     string   `json:"componentName"`
	ComponentOwnerID  string   `json:"componentOwnerId"`
	StepIDs           []string `json:"stepIds"`
	SizeBytes         int64    `json:"sizeBytes,omitempty"`
	TargetStation     string   `json:"targetStation,omitempty"`
	RelativePath      string   `json:"relativePath,omitempty"`
	SourceRegistry    string   `json:"sourceRegistry,omitempty"`
	TargetRegistry    string   `json:"targetRegistry,omitempty"`
	TargetRef         string   `json:"targetRef,omitempty"`
}

type DeliveryDecision struct {
	RequirementID string    `json:"requirementId"`
	Mode          string    `json:"mode"`
	DecidedBy     string    `json:"decidedBy"`
	DecidedAt     time.Time `json:"decidedAt"`
}

type DeliveryDecisionInput struct {
	RequirementID string `json:"requirementId"`
	Mode          string `json:"mode"`
}

type DeliveryResult struct {
	RequirementID  string     `json:"requirementId"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	ActualLocation string     `json:"actualLocation,omitempty"`
	Message        string     `json:"message,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

type lockedInstallationBaseline struct {
	ComponentID    string `json:"componentId"`
	ReleaseID      string `json:"releaseId"`
	InstallRunID   string `json:"installRunId"`
	BackupRef      string `json:"backupRef"`
	PlaybookSHA256 string `json:"playbookSha256"`
}

type lockedArtifactTransfer struct {
	RequirementID string `json:"requirementId"`
	Alias         string `json:"alias"`
	SourceURL     string `json:"sourceUrl"`
	TargetStation string `json:"targetStation"`
	RelativePath  string `json:"relativePath"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
}

type lockedImageTransfer struct {
	RequirementID  string `json:"requirementId"`
	SourceRegistry string `json:"sourceRegistry"`
	TargetRegistry string `json:"targetRegistry"`
	SourceDigest   string `json:"sourceDigest"`
	TargetRef      string `json:"targetRef"`
	TargetDigest   string `json:"targetDigest"`
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
	EnvironmentID        string               `json:"environmentId"`
	Mode                 ComponentTestMode    `json:"mode"`
	RollbackVerification RollbackVerification `json:"rollbackVerification"`
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
	DeliveryRequirements  []DeliveryRequirement   `json:"deliveryRequirements"`
}

type preparedComponentTest struct {
	release     domain.ComponentRelease
	environment domain.Environment
	action      domain.ActionKind
	steps       []lockedStep
	provenance  map[string]map[string]resolvedParameter
}
