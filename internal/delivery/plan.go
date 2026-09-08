package delivery

import "codex/platform-demo/internal/domain"

// Plan projects only media fields from immutable job metadata. Never use it to
// rewrite the full metadata or to calculate the full JobPlan digest.
type Plan struct {
	DeliveryRequirements []domain.RunDeliveryRequirement `json:"deliveryRequirements,omitempty"`
	DeliveryDecisions    []domain.RunDeliveryDecision    `json:"deliveryDecisions,omitempty"`
	DeliveryResults      []domain.RunDeliveryResult      `json:"deliveryResults,omitempty"`
	ArtifactTransfers    []domain.RunArtifactTransfer    `json:"artifactTransfers,omitempty"`
	ImageTransfers       []domain.RunImageTransfer       `json:"imageTransfers,omitempty"`
}
