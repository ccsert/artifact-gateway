//go:build darwin || linux

package agctl

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryConfigLock(file *os.File) (bool, error) {
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(unix.SEEK_SET), Len: 1}
	err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &lock)
	if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return err == nil, err
}
