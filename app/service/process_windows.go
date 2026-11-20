//go:build windows

package service

import (
	"os/exec"
	"time"
)

// setProcessGroup does nothing: windows has no process group to signal, so a stop ends the shell only
func (s *Scheduler) setProcessGroup(*exec.Cmd) {}

// terminate kills the command's own process. Processes it started keep running, and one holding the
// command's output keeps the run, and the shutdown waiting for it, until it exits
func (s *Scheduler) terminate(cmd *exec.Cmd, _ time.Duration) {
	_ = cmd.Process.Kill()
}
