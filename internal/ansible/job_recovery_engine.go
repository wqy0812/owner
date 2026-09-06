package ansible

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Recovery calculation is read-only and runs the same Python 3.6-compatible
// module shipped with native jobs. Runtime/credential checks still precede any
// execution; a missing helper fails preview rather than selecting a fallback.
func recoveryQuery(request map[string]any) (json.RawMessage, error) {
	source, err := jobPlugins.ReadFile("job_plugins/cf_recovery.py")
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-B", "-c", string(source))
	command.Stdin = bytes.NewReader(input)
	output, runErr := command.Output()
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("recovery engine unavailable: %v", runErr)
	}
	if response.Error != "" {
		return nil, fmt.Errorf("%s", response.Error)
	}
	if runErr != nil {
		return nil, fmt.Errorf("recovery engine failed: %w", runErr)
	}
	return response.Result, nil
}

func recoveryStages(request map[string]any) ([]JobStep, error) {
	data, err := recoveryQuery(request)
	if err != nil {
		return nil, err
	}
	var result []JobStep
	err = json.Unmarshal(data, &result)
	return result, err
}

func ActionBoundaryStatus(phase, boundary string) (string, error) {
	data, err := recoveryQuery(map[string]any{"operation": "boundary-status", "phase": phase, "boundary": boundary})
	if err != nil {
		return "", err
	}
	var result string
	err = json.Unmarshal(data, &result)
	return result, err
}
