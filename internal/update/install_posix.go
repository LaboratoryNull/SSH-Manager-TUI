//go:build !windows

package update

import "os"

func installExecutable(source, target string) (bool, error) {
	return false, os.Rename(source, target)
}
