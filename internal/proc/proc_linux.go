package proc

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// userPIDs lists /proc for the processes of the user this one runs as.
func userPIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid()) //nolint:gosec // a user id fits a uint32
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid == uid {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// environ is the process's environment, entries each ended by a NUL; empty for a
// zombie.
func environ(pid int) ([]byte, error) {
	return os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
}

func name(pid int) string {
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}
