//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// ApplyProcAttrs is a no-op before starting on Unix.
func ApplyProcAttrs(cmd *exec.Cmd, low bool) { applyProcAttrs(cmd, low) }

func applyProcAttrs(_ *exec.Cmd, _ bool) {}

func startAndLower(cmd *exec.Cmd, low bool) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if low {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
	}
	return nil
}

// StartLowered starts a command and lowers its priority when asked.
func StartLowered(cmd *exec.Cmd, low bool) error { return startAndLower(cmd, low) }
