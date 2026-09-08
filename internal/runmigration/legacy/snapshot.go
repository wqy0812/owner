// Package legacy describes only the exact source contract of the offline Run
// migration. Server and business packages must never import this package.
package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"codex/platform-demo/internal/domain"
)

const SchemaContract = "clusterforge-v1-20260907-no-resource-contract"

type Snapshot struct {
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

func (old Snapshot) Convert() domain.RunSnapshot {
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

func Decode(raw []byte, run domain.Run) (domain.RunSnapshot, []domain.RunDeliveryResult, error) {
	var old Snapshot
	if err := checkSourceShape(raw, reflect.TypeFor[Snapshot](), "snapshot"); err != nil {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: %w", run.ID, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&old); err != nil {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s source snapshot: %w", run.ID, err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: trailing source snapshot data", run.ID)
	}
	if old.EnvironmentRevisionID != "" && old.EnvironmentRevisionID != run.EnvironmentRevisionID {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s environmentRevisionId differs from Run identity", run.ID)
	}
	scenario := run.Kind == domain.RunScenario || run.Kind == domain.RunScenarioTest
	if scenario && (old.ScenarioContractVersion == nil || *old.ScenarioContractVersion != 2 || old.BaselineGeneration == nil || old.BaselineTestOnly == nil) {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: scenarioContractVersion, baselineGeneration and baselineTestOnly are required", run.ID)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return domain.RunSnapshot{}, nil, err
	}
	if !scenario {
		// These fields were written only by the scenario execution constructor.
		// An associated ScenarioRevision on an environment rollback is identity,
		// not permission to reinterpret an orphan execution field.
		for _, key := range []string{"scenarioContractVersion", "executionMode", "targetNodes", "sourceRevisionId", "baselineRunId", "baselineGeneration", "baselineInstallationDigest", "baselineTestOnly", "recoveredReceipts", "acceptanceJobIds"} {
			if _, present := members[key]; present {
				return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: snapshot.%s is not valid for %s", run.ID, key, run.Kind)
			}
		}
	}
	if run.RetryOfRunID == "" {
		for _, key := range []string{"retryRecoveryStateDigest", "retryBaselineGeneration", "retryBaselineMutatingRunId"} {
			if _, present := members[key]; present {
				return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: snapshot.%s requires a retry identity", run.ID, key)
			}
		}
	}
	if scenario && run.RetryOfRunID != "" && old.RetryBaselineGeneration == nil {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: retryBaselineGeneration is required", run.ID)
	}
	run.Snapshot = old.Convert()
	run.DeliveryResults = old.DeliveryResults
	if err := domain.ValidateRunSnapshot(run); err != nil {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: %w", run.ID, err)
	}
	if _, err := domain.EncodeRunSnapshot(run.Snapshot); err != nil {
		return domain.RunSnapshot{}, nil, fmt.Errorf("Run %s: %w", run.ID, err)
	}
	return run.Snapshot, run.DeliveryResults, nil
}
