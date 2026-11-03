package update

import (
	"fmt"
	"os"
)

// previousSuffix marks the executable that an update moved aside.
const previousSuffix = ".old"

// replaceByRename moves target aside and source into its place, rolling
// back if the second step fails. retry wraps each rename (see the Windows
// implementation); it is kept OS-independent so the logic can be tested
// everywhere.
func replaceByRename(source, target string, retry func(func() error) error) error {
	previous := target + previousSuffix
	// A leftover from an earlier update; it is not running any more unless
	// another old window is still open, in which case the rename below
	// reports the problem.
	_ = os.Remove(previous)
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

// CleanupPreviousUpdate removes the executable an earlier update moved
// aside. Errors are ignored: another running copy may still hold it.
func CleanupPreviousUpdate() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(executable + previousSuffix)
}
