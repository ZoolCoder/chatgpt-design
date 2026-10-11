package browser

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

// detach starts Chrome without this process's console, so it survives the run.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}
