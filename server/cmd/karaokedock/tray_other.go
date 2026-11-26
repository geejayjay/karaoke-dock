//go:build !windows

package main

import (
	"context"
	"os/exec"
	"runtime"
)

func prepareTrayDefaults(_ *options) {}

func startTray(_ context.Context, _ func(), _ trayLinks) {}

func bootstrapDesktopDeps(_ context.Context, _, _ string) {}

func openBrowser(url string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_ = exec.Command(cmd, url).Start()
}
