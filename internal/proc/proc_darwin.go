package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// userPIDs asks the kernel for the processes of the user this one runs as.
func userPIDs() ([]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(procs))
	for _, p := range procs {
		pids = append(pids, int(p.Proc.P_pid))
	}
	return pids, nil
}

// environ reads the process's arguments and environment through kern.procargs2:
// the count of arguments, the executable's path, NULs to align, the arguments, then
// the environment, each ended by a NUL. It returns the environment.
func environ(pid int) ([]byte, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	if len(buf) < 4 {
		return nil, errors.New("procargs2 too short")
	}
	argc := int(binary.LittleEndian.Uint32(buf))
	rest := buf[4:]
	path := bytes.IndexByte(rest, 0)
	if path < 0 {
		return nil, errors.New("procargs2 has no executable path")
	}
	rest = bytes.TrimLeft(rest[path:], "\x00")
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, errors.New("procargs2 ends in its arguments")
		}
		rest = rest[end+1:]
	}
	// The environment ends at an empty entry.
	if end := bytes.Index(rest, []byte{0, 0}); end >= 0 {
		rest = rest[:end+1]
	}
	return rest, nil
}

func name(pid int) string {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	n := p.Proc.P_comm[:]
	if end := bytes.IndexByte(n, 0); end >= 0 {
		n = n[:end]
	}
	return string(n)
}
