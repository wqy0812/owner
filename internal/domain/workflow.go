package domain

import (
	"encoding/json"
	"time"
)

// WorkflowSession is durable authoring/preparation state, never Run evidence.
type WorkflowSession struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	OwnerID       string          `json:"ownerId"`
	RequestKey    string          `json:"-"`
	RequestDigest string          `json:"-"`
	Status        string          `json:"status"`
	Version       int64           `json:"version"`
	Input         json.RawMessage `json:"input"`
	Output        json.RawMessage `json:"output"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}
