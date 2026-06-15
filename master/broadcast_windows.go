//go:build windows

package master

import "syscall"

// setBroadcast enables SO_BROADCAST on a windows socket fd. The fd is a
// syscall.Handle here (vs int on unix, see broadcast_unix.go).
func setBroadcast(fd uintptr) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
