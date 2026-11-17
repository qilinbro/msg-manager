//go:build !windows

package telegramuser

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
