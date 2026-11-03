package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// previousSuffix marks the executable that an update moved aside.
const previousSuffix = ".old"

// replaceByRename moves target aside and source into its place, rolling
// back if the second step fails. retry wraps each rename (see the Windows
// implementation); it is kept OS-independent so the logic can be tested
// everywhere.
func replaceByRename(source, target string, retry func(func() error) error) error {
	previous := target + previousSuffix
	// A leftover from an earlier update. If a window of that version is
	// still open, Windows keeps it locked; use a unique name instead.
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		previous += "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if err := retry(func() error { return os.Rename(target, previous) }); err != nil {
		return fmt.Errorf("move current executable aside: %w", err)
	}
	if err := retry(func() error { return os.Rename(source, target) }); err != nil {
		if rollbackErr := os.Rename(previous, target); rollbackErr != nil {
			return fmt.Errorf("install update: %w; restoring %s failed: %v", err, target, rollbackErr)
		}
		return fmt.Errorf("install update: %w", err)
	}
	// Succeeds on Unix; on Windows the running image keeps the file until
	// exit and CleanupPreviousUpdate removes it on the next start.
	_ = os.Remove(previous)
	return nil
}

// CleanupPreviousUpdate removes executables earlier updates moved aside
// (sshm.exe.old, sshm.exe.old-…). Errors are ignored: a copy that is still
// running keeps its file until it exits.
func CleanupPreviousUpdate() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	leftovers, _ := filepath.Glob(executable + previousSuffix + "*")
	for _, leftover := range leftovers {
		_ = os.Remove(leftover)
	}
}
