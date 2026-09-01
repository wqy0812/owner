package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type runRecord struct {
	Group    string           `json:"group"`
	Purpose  string           `json:"purpose"`
	ID       string           `json:"id"`
	Status   string           `json:"status"`
	Steps    int              `json:"steps"`
	Delivery []deliveryResult `json:"deliveryResults,omitempty"`
}

type automationReport struct {
	StartedAt     time.Time   `json:"startedAt"`
	FinishedAt    time.Time   `json:"finishedAt"`
	BaseURL       string      `json:"baseUrl"`
	EnvironmentID string      `json:"environmentId"`
	Checks        []string    `json:"checks"`
	Runs          []runRecord `json:"runs"`
}

func (h *harness) writeReport() error {
	h.report.FinishedAt = time.Now().UTC()
	if err := os.MkdirAll(h.cfg.OutputDir, 0o750); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(h.report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(h.cfg.OutputDir, "summary.json"), append(payload, '\n'), 0o600)
}
