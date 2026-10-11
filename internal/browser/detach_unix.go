//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// detach puts Chrome in its own session, so it survives this process and its terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
