//go:build !windows

package service

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup runs the command in its own process group, so a stop reaches every process it starts
func (s *Scheduler) setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate sends SIGTERM to the command's process group and SIGKILL to whatever is left of it after grace
func (s *Scheduler) terminate(cmd *exec.Cmd, grace time.Duration) {
	pgid := -cmd.Process.Pid
	if errors.Is(syscall.Kill(pgid, syscall.SIGTERM), syscall.ESRCH) {
		return
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pgid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL)
}
