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
