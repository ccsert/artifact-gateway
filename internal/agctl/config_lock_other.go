//go:build !darwin && !linux && !windows

package agctl

import (
	"errors"
	"os"
)

func tryConfigLock(*os.File) (bool, error) {
	return false, errors.New("profile writes are supported on Linux, macOS, and Windows")
}
