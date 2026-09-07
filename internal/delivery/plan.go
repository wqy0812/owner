package delivery

import "time"

type Requirement struct {
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

type Decision struct {
	RequirementID string    `json:"requirementId"`
	Mode          string    `json:"mode"`
	DecidedBy     string    `json:"decidedBy"`
	DecidedAt     time.Time `json:"decidedAt"`
}

type Result struct {
	RequirementID  string     `json:"requirementId"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	ActualLocation string     `json:"actualLocation,omitempty"`
	Message        string     `json:"message,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

type PlannedArtifactTransfer struct {
	RequirementID string `json:"requirementId"`
	Alias         string `json:"alias"`
	SourceURL     string `json:"sourceUrl"`
	TargetStation string `json:"targetStation"`
	RelativePath  string `json:"relativePath"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
}

type PlannedImageTransfer struct {
	RequirementID  string `json:"requirementId"`
	SourceRegistry string `json:"sourceRegistry"`
	TargetRegistry string `json:"targetRegistry"`
	SourceDigest   string `json:"sourceDigest"`
	TargetRef      string `json:"targetRef"`
	TargetDigest   string `json:"targetDigest"`
}

// Plan projects only media fields from immutable job metadata. Never use it to
// rewrite the full metadata or to calculate the full JobPlan digest.
type Plan struct {
	DeliveryRequirements []Requirement             `json:"deliveryRequirements,omitempty"`
	DeliveryDecisions    []Decision                `json:"deliveryDecisions,omitempty"`
	DeliveryResults      []Result                  `json:"deliveryResults,omitempty"`
	ArtifactTransfers    []PlannedArtifactTransfer `json:"artifactTransfers,omitempty"`
	ImageTransfers       []PlannedImageTransfer    `json:"imageTransfers,omitempty"`
}
