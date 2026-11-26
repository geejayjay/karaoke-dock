//go:build windows

package tools

import (
	"os/exec"
	"syscall"
)

const (
	belowNormalPriority = 0x00004000
	createNoWindow      = 0x08000000
)

// ApplyProcAttrs hides the console window and lowers priority when asked.
func ApplyProcAttrs(cmd *exec.Cmd, low bool) { applyProcAttrs(cmd, low) }

func applyProcAttrs(cmd *exec.Cmd, low bool) {
	flags := uint32(createNoWindow)
	if low {
		flags |= belowNormalPriority
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
}

func startAndLower(cmd *exec.Cmd, _ bool) error { return cmd.Start() }

// StartLowered starts a command, lowering priority where that needs a step
// after starting.
func StartLowered(cmd *exec.Cmd, low bool) error { return startAndLower(cmd, low) }
