//go:build !windows

package plansession

import "syscall"

// mkfifo creates a named pipe (FIFO) at the given path with 0600 permissions.
func mkfifo(path string) error {
	return syscall.Mkfifo(path, 0600)
}
