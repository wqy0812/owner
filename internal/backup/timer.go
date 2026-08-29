package backup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type TimerController interface {
	SetEnabled(context.Context, bool) error
	Enabled(context.Context) (bool, error)
}

type SystemdTimerController struct {
	Binary string
	Unit   string
}

func NewSystemdTimerController(binary, unit string) *SystemdTimerController {
	if binary == "" {
		binary = "systemctl"
	}
	if unit == "" {
		unit = "clusterforge-backup.timer"
	}
	return &SystemdTimerController{Binary: binary, Unit: unit}
}

func (c *SystemdTimerController) SetEnabled(ctx context.Context, enabled bool) error {
	action := []string{"disable", "--now", c.Unit}
	if enabled {
		action = []string{"enable", "--now", c.Unit}
	}
	output, err := exec.CommandContext(ctx, c.Binary, action...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("set backup timer enabled=%t: %w: %s", enabled, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (c *SystemdTimerController) Enabled(ctx context.Context) (bool, error) {
	err := exec.CommandContext(ctx, c.Binary, "is-enabled", "--quiet", c.Unit).Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return false, nil
		}
		return false, err
	}
	// An enabled timer can still be stopped or failed. Treat it as healthy only
	// while the unit is active, otherwise the six-hour schedule will not fire.
	err = exec.CommandContext(ctx, c.Binary, "is-active", "--quiet", c.Unit).Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, err
}
