package domain

import "time"

type RunArchive struct {
	RunID         string     `json:"runId"`
	Status        string     `json:"status"`
	Source        string     `json:"source"`
	ActorID       string     `json:"actorId"`
	Error         string     `json:"error,omitempty"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
	SizeBytes     int64      `json:"sizeBytes"`
	SHA256        string     `json:"sha256,omitempty"`
	FormatVersion string     `json:"formatVersion,omitempty"`
	RelativePath  string     `json:"-"`
	LeaseToken    string     `json:"-"`
}
type RunRetentionPolicy struct {
	AutoArchive bool `json:"autoArchive"`
	AutoCleanup bool `json:"autoCleanup"`
	ArchiveDays int  `json:"archiveDays"`
	CleanupDays int  `json:"cleanupDays"`
}
type RunCleanupPreview struct {
	RunID    string   `json:"runId"`
	Eligible bool     `json:"eligible"`
	Reasons  []string `json:"reasons"`
}
type RunRetentionResult struct {
	RunID  string    `json:"runId"`
	Status string    `json:"status"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}
type RunArchiveHealth struct {
	CleanupResults []RunRetentionResult `json:"cleanupResults"`
	StorageError   string               `json:"storageError,omitempty"`
	Policy         RunRetentionPolicy   `json:"policy"`
	Configured     bool                 `json:"configured"`
	Pending        int                  `json:"pending"`
	SizeBytes      int64                `json:"sizeBytes"`
	Tasks          []RunArchive         `json:"tasks"`
	LastScanAt     *time.Time           `json:"lastScanAt,omitempty"`
	LastScanResult string               `json:"lastScanResult,omitempty"`
}
