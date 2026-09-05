package domain

import "time"

// ComponentSummary is the directory read model, not a Release contract or a
// readiness result. Keep fields explicit so contracts cannot leak into lists.
type ComponentSummary struct {
	ID               string         `json:"id"`
	Slug             string         `json:"slug"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	Layer            ComponentLayer `json:"layer"`
	Tags             []string       `json:"tags"`
	OwnerID          string         `json:"ownerId"`
	OwnerName        string         `json:"ownerName"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
	ReleaseCount     int            `json:"releaseCount"`
	DefaultReleaseID string         `json:"defaultReleaseId,omitempty"`
	HasDraft         bool           `json:"hasDraft"`
	NeedsAttention   bool           `json:"needsAttention"`
}
