//go:build !windows

package master

import "syscall"

// setBroadcast enables SO_BROADCAST on a unix socket fd. The fd is an int here
// (vs syscall.Handle on windows, see broadcast_windows.go).
func setBroadcast(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
