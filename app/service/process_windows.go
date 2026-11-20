//go:build windows

package service

import (
	"os/exec"
	"time"
)

// setProcessGroup does nothing: windows has no process group to signal, so a stop ends the shell only
func (s *Scheduler) setProcessGroup(*exec.Cmd) {}

// terminate kills the command's own process; processes it started keep running
func (s *Scheduler) terminate(cmd *exec.Cmd, _ time.Duration) {
	_ = cmd.Process.Kill()
}
