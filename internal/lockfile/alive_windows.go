package lockfile

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// Alive reports whether a process with pid is still running.
func Alive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// Access denied still means the process exists.
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
