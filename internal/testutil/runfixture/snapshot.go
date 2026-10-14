package runfixture

import (
	"bytes"
	"encoding/json"

	"codex/platform-demo/internal/domain"
)

// RunSnapshotFixture preserves the authored contents of existing record fixtures
// during the storage transition. It is NOT a valid execution-plan factory: tests
// of execution must submit through the Service. The source representation is
// local to test fixtures; production accepts typed snapshots only.
func Snapshot(value any) domain.RunSnapshot {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var old fixtureSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&old); err != nil {
		panic(err)
	}
	return old.Convert()
}

func StepMaps(steps []domain.RunPlanStep) []any {
	raw, err := json.Marshal(steps)
	if err != nil {
		panic(err)
	}
	var out []any
	if err = json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

type fixtureSnapshot struct {
	domain.RunExecutionPlan
	ScenarioContractVersion    *int64                                          `json:"scenarioContractVersion,omitempty"`
	ExecutionMode              domain.ScenarioExecutionMode                    `json:"executionMode"`
	ScenarioID                 string                                          `json:"scenarioId"`
	ScenarioRevisionSpecDigest string                                          `json:"scenarioRevisionSpecDigest"`
	ComponentReleaseSpecDigest string                                          `json:"componentReleaseSpecDigest"`
	ComponentTestEvidence      string                                          `json:"componentTestEvidence"`
	TargetNodes                []domain.ScenarioTargetNode                     `json:"targetNodes"`
	SourceRevisionID           string                                          `json:"sourceRevisionId"`
	BaselineRunID              string                                          `json:"baselineRunId"`
	BaselineGeneration         *int64                                          `json:"baselineGeneration"`
	BaselineInstallationDigest string                                          `json:"baselineInstallationDigest"`
	BaselineTestOnly           *bool                                           `json:"baselineTestOnly"`
	RecoveredReceipts          []domain.ActionExecutionReceipt                 `json:"recoveredReceipts,omitempty"`
	EnvironmentRevisionID      string                                          `json:"environmentRevisionId"`
	PlanDigest                 string                                          `json:"planDigest"`
	SubmissionKey              string                                          `json:"submissionKey"`
	SubmissionDigest           string                                          `json:"submissionDigest"`
	AcceptanceJobIDs           []string                                        `json:"acceptanceJobIds"`
	ResolvedParametersByNode   map[string]map[string]domain.RunParameterSource `json:"resolvedParametersByNode"`
	CredentialRefs             []domain.CredentialRef                          `json:"credentialRefs"`
	RetryRecoveryStateDigest   string                                          `json:"retryRecoveryStateDigest"`
	RetryBaselineGeneration    *int64                                          `json:"retryBaselineGeneration"`
	RetryBaselineMutatingRunID string                                          `json:"retryBaselineMutatingRunId"`
}

func (old fixtureSnapshot) Convert() domain.RunSnapshot {
	s := domain.SnapshotFromExecutionPlan(old.RunExecutionPlan)
	s.Subject = domain.RunSubject{ComponentReleaseSpecDigest: old.ComponentReleaseSpecDigest, ComponentTestEvidence: old.ComponentTestEvidence, ScenarioID: old.ScenarioID, ScenarioRevisionSpecDigest: old.ScenarioRevisionSpecDigest}
	s.Inputs = domain.RunInputs{ResolvedParametersByNode: old.ResolvedParametersByNode, CredentialRefs: old.CredentialRefs}
	if old.ScenarioContractVersion != nil || old.ExecutionMode != "" {
		s.ScenarioExecution = domain.ScenarioRunExecution{Mode: old.ExecutionMode, SourceRevisionID: old.SourceRevisionID, TargetNodes: old.TargetNodes, AcceptanceJobIDs: old.AcceptanceJobIDs, RecoveredReceipts: old.RecoveredReceipts, Baseline: domain.ScenarioRunBaseline{RunID: old.BaselineRunID, InstallationDigest: old.BaselineInstallationDigest}}
		if old.BaselineGeneration != nil {
			s.ScenarioExecution.Baseline.Generation = *old.BaselineGeneration
		}
		if old.BaselineTestOnly != nil {
			s.ScenarioExecution.Baseline.TestOnly = *old.BaselineTestOnly
		}
	}
	s.Submission = domain.RunSubmission{Key: old.SubmissionKey, Digest: old.SubmissionDigest, PlanDigest: old.PlanDigest}
	s.Retry = domain.RunRetryContext{RecoveryStateDigest: old.RetryRecoveryStateDigest, BaselineMutatingRunID: old.RetryBaselineMutatingRunID}
	if old.RetryBaselineGeneration != nil {
		s.Retry.BaselineGeneration = *old.RetryBaselineGeneration
	}
	return s
}
