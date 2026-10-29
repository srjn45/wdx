// Package pty opens pseudo-terminals, starts commands in them and sizes
// them. github.com/creack/pty v1.1.24 passes the ioctls' pointers through
// a function call, which the unsafe rules forbid: when the stack moves
// meanwhile, the kernel writes the PTY's number elsewhere and it opens
// /dev/pts/0, someone else's terminal. Here every pointer goes straight to
// the system call.
package pty

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// Start starts cmd in a new session with a new PTY of cols×rows as its
// controlling terminal and its stdin, stdout and stderr, and returns the
// PTY's master.
func Start(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	master, slave, err := Open()
	if err != nil {
		return nil, err
	}
	defer slave.Close() // the command has its own
	if err := Setsize(master, cols, rows); err != nil {
		master.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return master, nil
}

// Setsize sets the size of the terminal f is a side of.
func Setsize(f *os.File, cols, rows int) error {
	return control(f, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
}

// control runs fn with f's descriptor, which Fd would make blocking.
func control(f *os.File, fn func(fd int) error) error {
	raw, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := raw.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}
