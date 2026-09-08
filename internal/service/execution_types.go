package service

import (
	"time"

	"codex/platform-demo/internal/domain"
)

type digestRunner interface {
	Digest(string) (playbookSHA256, treeSHA256 string, err error)
}

type DeliveryDecisionInput struct {
	RequirementID string `json:"requirementId"`
	Mode          string `json:"mode"`
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
	EnvironmentID         string                          `json:"environmentId"`
	EnvironmentRevisionID string                          `json:"environmentRevisionId"`
	Destructive           bool                            `json:"destructive"`
	RequiresApproval      bool                            `json:"requiresApproval"`
	PlanDigest            string                          `json:"planDigest"`
	Steps                 []ComponentTestPlanStep         `json:"steps"`
	DeliveryRequirements  []domain.RunDeliveryRequirement `json:"deliveryRequirements"`
}

type preparedComponentTest struct {
	release     domain.ComponentRelease
	environment domain.Environment
	action      domain.ActionKind
	steps       []domain.RunPlanStep
	provenance  map[string]map[string]resolvedParameter
}
