package domain

import "time"

type RunPlanStep struct {
	RollbackSourceVariables map[string]any  `json:"rollbackSourceVariables,omitempty"`
	RollbackSourceFrozen    bool            `json:"rollbackSourceFrozen,omitempty"`
	RollbackSourceActionID  string          `json:"rollbackSourceActionId,omitempty"`
	Media                   []RunMedia      `json:"media,omitempty"`
	SourceParametersFrozen  bool            `json:"sourceParametersFrozen,omitempty"`
	SourceType              string          `json:"sourceType,omitempty"`
	ScenarioRevisionID      string          `json:"scenarioRevisionId,omitempty"`
	AcceptanceJobID         string          `json:"acceptanceJobId,omitempty"`
	Stage                   string          `json:"stage,omitempty"`
	MayMutate               bool            `json:"mayMutate,omitempty"`
	ParentActionID          string          `json:"parentActionId"`
	SourceNodeID            string          `json:"sourceNodeId"`
	Phase                   string          `json:"phase"`
	Become                  bool            `json:"become"`
	ID                      string          `json:"id"`
	NodeID                  string          `json:"nodeId"`
	Name                    string          `json:"name"`
	ComponentID             string          `json:"componentId"`
	ComponentName           string          `json:"componentName"`
	ReleaseID               string          `json:"releaseId"`
	ReleaseVersion          string          `json:"releaseVersion"`
	ReleaseSpecDigest       string          `json:"releaseSpecDigest"`
	ActionID                string          `json:"actionId"`
	Action                  ActionKind      `json:"action"`
	FromReleaseID           string          `json:"fromReleaseId,omitempty"`
	ToReleaseID             string          `json:"toReleaseId,omitempty"`
	Playbook                string          `json:"playbook"`
	PlaybookDigest          string          `json:"playbookDigest"`
	WorkspaceDigest         string          `json:"workspaceDigest"`
	Tags                    []string        `json:"tags"`
	Limit                   string          `json:"limit"`
	Variables               map[string]any  `json:"variables"`
	RequiredCredentials     []string        `json:"requiredCredentials"`
	TimeoutSeconds          int             `json:"timeoutSeconds"`
	NeedsApproval           bool            `json:"needsApproval"`
	RetrySafe               bool            `json:"retrySafe"`
	BackupRef               string          `json:"backupRef,omitempty"`
	Backup                  *BackupMetadata `json:"backup,omitempty"`
}

type RunExecutionPlan struct {
	ResetTargets               []RunInventoryHost        `json:"resetTargets,omitempty"`
	ResetBoundaryDigest        string                    `json:"resetBoundaryDigest,omitempty"`
	RecoveryEnvironmentDigest  string                    `json:"recoveryEnvironmentDigest,omitempty"`
	ParentSteps                []RunPlanStep             `json:"parentSteps"`
	Runtime                    RunRuntime                `json:"runtime"`
	Steps                      []RunPlanStep             `json:"steps"`
	DeliveryRequirements       []RunDeliveryRequirement  `json:"deliveryRequirements,omitempty"`
	DeliveryDecisions          []RunDeliveryDecision     `json:"deliveryDecisions,omitempty"`
	DeliveryResults            []RunDeliveryResult       `json:"deliveryResults,omitempty"`
	ArtifactTransfers          []RunArtifactTransfer     `json:"artifactTransfers,omitempty"`
	ImageTransfers             []RunImageTransfer        `json:"imageTransfers,omitempty"`
	TreeDigest                 string                    `json:"treeDigest"`
	InstallationBaseline       []RunInstallationBaseline `json:"installationBaseline,omitempty"`
	InstallationBaselineDigest string                    `json:"installationBaselineDigest,omitempty"`
}

type RunInstallationBaseline struct {
	NodeID         string `json:"nodeId"`
	ComponentID    string `json:"componentId"`
	ReleaseID      string `json:"releaseId"`
	InstallRunID   string `json:"installRunId"`
	BackupRef      string `json:"backupRef"`
	PlaybookSHA256 string `json:"playbookSha256"`
}

type RunDeliveryRequirement struct {
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

type RunDeliveryDecision struct {
	RequirementID string    `json:"requirementId"`
	Mode          string    `json:"mode"`
	DecidedBy     string    `json:"decidedBy"`
	DecidedAt     time.Time `json:"decidedAt"`
}

type RunDeliveryResult struct {
	RequirementID  string     `json:"requirementId"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	ActualLocation string     `json:"actualLocation,omitempty"`
	Message        string     `json:"message,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

type RunArtifactTransfer struct {
	RequirementID string `json:"requirementId"`
	Alias         string `json:"alias"`
	SourceURL     string `json:"sourceUrl"`
	TargetStation string `json:"targetStation"`
	RelativePath  string `json:"relativePath"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
}

type RunImageTransfer struct {
	RequirementID  string `json:"requirementId"`
	SourceRegistry string `json:"sourceRegistry"`
	TargetRegistry string `json:"targetRegistry"`
	SourceDigest   string `json:"sourceDigest"`
	TargetRef      string `json:"targetRef"`
	TargetDigest   string `json:"targetDigest"`
}

type ActionExecutionReceipt struct {
	RunID         string         `json:"runId"`
	StepID        string         `json:"stepId"`
	EnvironmentID string         `json:"environmentId"`
	ComponentID   string         `json:"componentId"`
	ReleaseID     string         `json:"releaseId"`
	ActionID      string         `json:"actionId"`
	SourceNodeID  string         `json:"sourceNodeId"`
	Status        string         `json:"status"`
	BackupRef     string         `json:"backupRef"`
	Backup        BackupMetadata `json:"backup"`
	StartedAt     time.Time      `json:"startedAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}
