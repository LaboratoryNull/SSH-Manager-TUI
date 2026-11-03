//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Windows keeps a running executable locked against writes and deletion,
// but it can be renamed. The update therefore moves the running sshm.exe
// aside to sshm.exe.old and puts the verified binary in its place, right
// away and in-process. The previous version (v1.1.1 and older) started a
// hidden PowerShell helper and quit; when antivirus software or a policy
// blocked that helper the update silently never happened.
func installExecutable(source, target string) (bool, error) {
	return false, replaceByRename(source, target, retryWindowsSharing)
}

// retryWindowsSharing retries a file operation for a few seconds while the
// file is briefly locked, typically by an antivirus scan of the download.
func retryWindowsSharing(operation func() error) error {
	const (
		errorSharingViolation syscall.Errno = 32
		errorLockViolation    syscall.Errno = 33
	)
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		if err = operation(); err == nil {
			return nil
		}
		if !errors.Is(err, errorSharingViolation) && !errors.Is(err, errorLockViolation) && !errors.Is(err, os.ErrPermission) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("%w (файл занят: закройте другие окна sshm и проверьте антивирус)", err)
}
