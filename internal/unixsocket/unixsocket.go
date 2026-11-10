// Package unixsocket checks a Unix socket's path before a program listens on
// it: the system takes a path only up to a length, and refuses a longer one
// with no more than "invalid argument".
package unixsocket

import (
	"fmt"
	"syscall"
)

// MaxPath is the longest path a socket may have on this system, in bytes:
// the size of sun_path less its terminating NUL, 107 on Linux and 103 on
// macOS.
const MaxPath = len(syscall.RawSockaddrUnix{}.Path) - 1

// TooLongError is a socket's path longer than the system allows.
type TooLongError struct {
	Path  string
	Limit int
}

func (e *TooLongError) Error() string {
	return fmt.Sprintf("the socket path %s is %d bytes, more than the %d this system allows", e.Path, len(e.Path), e.Limit)
}

// Check returns a *TooLongError when path is too long to listen on.
func Check(path string) error {
	if len(path) > MaxPath {
		return &TooLongError{Path: path, Limit: MaxPath}
	}
	return nil
}
