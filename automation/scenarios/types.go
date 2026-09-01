package scenarios

import "time"

type runDTO struct {
	ID                    string                `json:"id"`
	Kind                  string                `json:"kind"`
	Status                string                `json:"status"`
	EnvironmentID         string                `json:"environmentId"`
	EnvironmentRevisionID string                `json:"environmentRevisionId"`
	ComponentReleaseID    string                `json:"componentReleaseId"`
	ScenarioRevisionID    string                `json:"scenarioRevisionId"`
	Action                string                `json:"action"`
	RequestedBy           string                `json:"requestedBy"`
	RetryOfRunID          string                `json:"retryOfRunId"`
	RetryRootRunID        string                `json:"retryRootRunId"`
	RetryAttempt          int                   `json:"retryAttempt"`
	RetryStartStep        int                   `json:"retryStartStep"`
	Error                 string                `json:"error"`
	Approval              *approvalDTO          `json:"approval"`
	Steps                 []runStepDTO          `json:"steps"`
	DeliveryRequirements  []deliveryRequirement `json:"deliveryRequirements"`
	DeliveryResults       []deliveryResult      `json:"deliveryResults"`
}

type approvalDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type runStepDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	ExitCode *int   `json:"exitCode"`
}

type deliveryRequirement struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Identity      string `json:"identity"`
	Source        string `json:"source"`
	Target        string `json:"target"`
	TargetPresent bool   `json:"targetPresent"`
}

type deliveryResult struct {
	RequirementID  string     `json:"requirementId"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	ActualLocation string     `json:"actualLocation"`
	Message        string     `json:"message"`
	CompletedAt    *time.Time `json:"completedAt"`
}

type componentPlan struct {
	EnvironmentID         string                `json:"environmentId"`
	EnvironmentRevisionID string                `json:"environmentRevisionId"`
	RequiresApproval      bool                  `json:"requiresApproval"`
	PlanDigest            string                `json:"planDigest"`
	Steps                 []componentPlanStep   `json:"steps"`
	DeliveryRequirements  []deliveryRequirement `json:"deliveryRequirements"`
}

type componentPlanStep struct {
	Order         int    `json:"order"`
	ComponentID   string `json:"componentId"`
	ReleaseID     string `json:"releaseId"`
	Action        string `json:"action"`
	Playbook      string `json:"playbook"`
	Limit         string `json:"limit"`
	NeedsApproval bool   `json:"needsApproval"`
	BackupRef     string `json:"backupRef"`
}

type rollbackPlan struct {
	EnvironmentID         string                `json:"environmentId"`
	EnvironmentName       string                `json:"environmentName"`
	EnvironmentRevisionID string                `json:"environmentRevisionId"`
	Sources               []rollbackSource      `json:"sources"`
	NodeCount             int                   `json:"nodeCount"`
	ComponentCount        int                   `json:"componentCount"`
	PlanDigest            string                `json:"planDigest"`
	DeliveryRequirements  []deliveryRequirement `json:"deliveryRequirements"`
}

type rollbackSource struct {
	RunID string `json:"runId"`
}

type lifecycleDTO struct {
	ActiveRunCount        int  `json:"activeRunCount"`
	InstallationCount     int  `json:"installationCount"`
	ActiveImageBuildCount int  `json:"activeImageBuildCount"`
	Archived              bool `json:"archived"`
	CanArchive            bool `json:"canArchive"`
}

type environmentDTO struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	ArchivedAt        *time.Time          `json:"archivedAt"`
	CurrentRevisionID string              `json:"currentRevisionId"`
	CurrentRevision   environmentRevision `json:"currentRevision"`
}

type environmentRevision struct {
	ID        string            `json:"id"`
	Revision  int               `json:"revision"`
	Hosts     []environmentHost `json:"hosts"`
	Variables map[string]string `json:"variables"`
	Facts     map[string]any    `json:"facts"`
}

type environmentHost struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Groups  []string `json:"groups"`
}

type scenarioDTO struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	CurrentRevisionID string           `json:"currentRevisionId"`
	CurrentRevision   scenarioRevision `json:"currentRevision"`
}

type scenarioRevision struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	Nodes           []any  `json:"nodes"`
	Edges           []any  `json:"edges"`
	ExecutionPolicy any    `json:"executionPolicy"`
}

type componentDTO struct {
	ID       string       `json:"id"`
	Slug     string       `json:"slug"`
	Name     string       `json:"name"`
	Releases []releaseDTO `json:"releases"`
}

type releaseDTO struct {
	ID        string        `json:"id"`
	Version   string        `json:"version"`
	Status    string        `json:"status"`
	Actions   []actionDTO   `json:"actions"`
	Artifacts []artifactDTO `json:"artifacts"`
	Images    []imageDTO    `json:"images"`
}

type actionDTO struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Playbook       string   `json:"playbook"`
	Tags           []string `json:"tags"`
	Limit          string   `json:"limit"`
	HostGroup      string   `json:"hostGroup"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	Destructive    bool     `json:"destructive"`
}

type artifactDTO struct {
	Alias     string `json:"alias"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"sourceUrl"`
}

type imageDTO struct {
	LogicalName string `json:"logicalName"`
	Digest      string `json:"digest"`
	SourceRef   string `json:"sourceRef"`
}
