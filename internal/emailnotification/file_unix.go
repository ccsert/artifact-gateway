//go:build unix

package emailnotification

import "syscall"

// Opening with O_NONBLOCK lets fstat reject a replaced FIFO/device without waiting.
func settingOpenFlags() int { return syscall.O_NONBLOCK }
