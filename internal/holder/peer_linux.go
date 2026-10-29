package holder

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the uid of the process at the other end of c.
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid, cerr := -1, error(nil)
	if err := raw.Control(func(fd uintptr) {
		var cred *unix.Ucred
		if cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); cerr == nil {
			uid = int(cred.Uid)
		}
	}); err != nil {
		return -1, err
	}
	return uid, cerr
}

// peerPID is the pid of the process at the other end of c, or 0.
func peerPID(c *net.UnixConn) int {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = raw.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}
