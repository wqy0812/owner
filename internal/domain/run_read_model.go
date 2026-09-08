package domain

import "time"

// RunReadModel is a projection for evidence, workbench and diagnostics. It cannot be
// passed to execution and contains no executable inputs or credential material.
type RunReadModel struct {
	ID                    string        `json:"id"`
	Kind                  RunKind       `json:"kind"`
	Status                RunStatus     `json:"status"`
	RequestedBy           string        `json:"requestedBy"`
	EnvironmentID         string        `json:"environmentId"`
	EnvironmentRevisionID string        `json:"environmentRevisionId"`
	ComponentReleaseID    string        `json:"componentReleaseId,omitempty"`
	ScenarioRevisionID    string        `json:"scenarioRevisionId,omitempty"`
	Action                ActionKind    `json:"action,omitempty"`
	Destructive           bool          `json:"destructive"`
	ArtifactDigest        string        `json:"artifactDigest"`
	RetryOfRunID          string        `json:"retryOfRunId,omitempty"`
	RetryRootRunID        string        `json:"retryRootRunId,omitempty"`
	RetryAttempt          int           `json:"retryAttempt,omitempty"`
	RetryStartStep        int           `json:"retryStartStep,omitempty"`
	Error                 string        `json:"error,omitempty"`
	CreatedAt             time.Time     `json:"createdAt"`
	StartedAt             *time.Time    `json:"startedAt,omitempty"`
	FinishedAt            *time.Time    `json:"finishedAt,omitempty"`
	Subject               RunSubject    `json:"subject"`
	LockedSteps           []RunReadStep `json:"lockedSteps"`
	ParentSteps           []RunReadStep `json:"parentSteps"`
	Steps                 []RunStep     `json:"steps"`
	Approval              *Approval     `json:"approval,omitempty"`
	Cleaned               bool          `json:"cleaned"`
}
type RunReadStep struct {
	ID                 string     `json:"id"`
	ComponentName      string     `json:"componentName"`
	SourceType         string     `json:"sourceType,omitempty"`
	ScenarioRevisionID string     `json:"scenarioRevisionId,omitempty"`
	Stage              string     `json:"stage,omitempty"`
	ParentActionID     string     `json:"parentActionId"`
	SourceNodeID       string     `json:"sourceNodeId"`
	Phase              string     `json:"phase"`
	NodeID             string     `json:"nodeId"`
	Name               string     `json:"name"`
	ComponentID        string     `json:"componentId"`
	ReleaseID          string     `json:"releaseId"`
	ReleaseSpecDigest  string     `json:"releaseSpecDigest"`
	ActionID           string     `json:"actionId"`
	Action             ActionKind `json:"action"`
}

func ReadModelFromRun(r Run) RunReadModel {
	out := RunReadModel{ID: r.ID, Kind: r.Kind, Status: r.Status, RequestedBy: r.RequestedBy, EnvironmentID: r.EnvironmentID, EnvironmentRevisionID: r.EnvironmentRevisionID, ComponentReleaseID: r.ComponentReleaseID, ScenarioRevisionID: r.ScenarioRevisionID, Action: r.Action, Destructive: r.Destructive, ArtifactDigest: r.ArtifactDigest, RetryOfRunID: r.RetryOfRunID, RetryRootRunID: r.RetryRootRunID, RetryAttempt: r.RetryAttempt, RetryStartStep: r.RetryStartStep, Error: r.Error, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Subject: r.Snapshot.Subject, Steps: r.Steps, Approval: r.Approval}
	out.LockedSteps = ReadSteps(r.Snapshot.Plan.Steps)
	out.ParentSteps = ReadSteps(r.Snapshot.Plan.ParentSteps)
	return out
}
func ReadSteps(steps []RunPlanStep) []RunReadStep {
	out := make([]RunReadStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, RunReadStep{ID: s.ID, ComponentName: s.ComponentName, NodeID: s.NodeID, Name: s.Name, ComponentID: s.ComponentID, ReleaseID: s.ReleaseID, ReleaseSpecDigest: s.ReleaseSpecDigest, Phase: s.Phase, Action: s.Action, ActionID: s.ActionID, ParentActionID: s.ParentActionID, SourceNodeID: s.SourceNodeID, SourceType: s.SourceType, ScenarioRevisionID: s.ScenarioRevisionID, Stage: s.Stage})
	}
	return out
}
