package domain

import "time"

const ScenarioDigestVersion = 2

type ScenarioExecutionMode string

const (
	ScenarioExecutionInstall        ScenarioExecutionMode = "install"
	ScenarioExecutionUpgrade        ScenarioExecutionMode = "upgrade"
	ScenarioExecutionBaselineVerify ScenarioExecutionMode = "baseline_verify"
)

type ScenarioAcceptanceJob struct {
	RuntimeChecks       []RuntimeCheck `json:"runtimeChecks,omitempty"`
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Purpose             string         `json:"purpose"`
	HostGroup           string         `json:"hostGroup"`
	TimeoutSeconds      int            `json:"timeoutSeconds"`
	RiskLevel           RiskLevel      `json:"riskLevel"`
	RequiredCredentials []string       `json:"requiredCredentials,omitempty"`
	Become              bool           `json:"become"`
	GatherFacts         bool           `json:"gatherFacts"`
	Playbook            string         `json:"playbook"`
	PlaybookSHA256      string         `json:"playbookSha256"`
	MayMutate           bool           `json:"mayMutate"`
}

type ScenarioParameterBinding struct {
	Parameter       string `json:"parameter"`
	Source          string `json:"source"`
	NodeID          string `json:"nodeId,omitempty"`
	SourceParameter string `json:"sourceParameter"`
}

type ScenarioInstallation struct {
	EnvironmentID      string    `json:"environmentId"`
	ScenarioID         string    `json:"scenarioId"`
	RevisionID         string    `json:"revisionId,omitempty"`
	RunID              string    `json:"runId,omitempty"`
	MutatingRunID      string    `json:"mutatingRunId,omitempty"`
	State              string    `json:"state"`
	TestOnly           bool      `json:"testOnly"`
	InstallationDigest string    `json:"installationDigest"`
	Generation         int64     `json:"generation"`
	UpdatedAt          time.Time `json:"updatedAt"`
}
