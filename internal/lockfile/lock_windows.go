package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION, which package syscall does not name.
const errorSharingViolation syscall.Errno = 32

// tryLock opens path for writing and lets others only read it, so a second writer fails until
// the handle closes, which it does when the process ends.
func tryLock(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, errorSharingViolation) {
		return nil, errBusy
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
