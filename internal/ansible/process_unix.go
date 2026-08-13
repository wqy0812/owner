//go:build unix

package ansible

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessCancellation(cmd *exec.Cmd, grace time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = grace
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func forceKillProcessGroup(process *os.Process) {
	if process != nil {
		_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	}
}
