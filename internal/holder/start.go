package holder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// readyTimeout is how long a holder has to start the shell.
const readyTimeout = 5 * time.Second

// StartOptions say what holder to start.
type StartOptions struct {
	// Executable is the wdx binary, run as "<Executable> holder".
	Executable string
	Home       string
	ID         string
	Project    string
	Dir        string
	Shell      string
	// HistoryLines bounds the terminal's history; 0 keeps none.
	HistoryLines int64
	// Env holds the variables the shell inherits, as KEY=value. They reach
	// the holder in its environment, never its arguments, which ps shows.
	Env []string
}

// Started is a holder that reported its shell running.
type Started struct {
	HolderPID int
	PID       int // the shell's
}

// Start starts a holder in a session of its own, so it outlives this
// process, and waits until its shell runs.
func Start(ctx context.Context, o StartOptions) (Started, error) {
	log := logging.FromContext(ctx)
	ready, readyW, err := os.Pipe()
	if err != nil {
		return Started{}, err
	}
	defer ready.Close()
	cmd := exec.Command(o.Executable, "holder", "--id", o.ID, "--project", o.Project, "--dir", o.Dir, "--shell", o.Shell,
		"--history-lines", strconv.FormatInt(o.HistoryLines, 10))
	cmd.Env = append(slices.Clip(o.Env), identity.Env("HOME")+"="+o.Home)
	cmd.ExtraFiles = []*os.File{readyW} // fd 3
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	readyW.Close()
	if err != nil {
		return Started{}, fmt.Errorf("start the holder: %w", err)
	}
	log.Info("holder started", "terminal_id", o.ID, "holder_pid", cmd.Process.Pid)
	// Reaps the holder should it exit while this process runs.
	go func() { _ = cmd.Wait() }()

	if err := ready.SetReadDeadline(time.Now().Add(readyTimeout)); err != nil {
		_ = cmd.Process.Kill()
		return Started{}, fmt.Errorf("wait for the holder: %w", err)
	}
	var m readyMessage
	err = json.NewDecoder(ready).Decode(&m)
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		_ = cmd.Process.Kill() // it may have just exited
		return Started{}, fmt.Errorf("the holder did not start the shell within %s", readyTimeout)
	case errors.Is(err, io.EOF):
		return Started{}, errors.New("the holder exited before starting the shell")
	case err != nil:
		_ = cmd.Process.Kill() // it may have just exited
		return Started{}, fmt.Errorf("read the holder's ready message: %w", err)
	case m.Error != "":
		return Started{}, errors.New(m.Error)
	}
	log.Info("holder ready", "terminal_id", o.ID, "holder_pid", cmd.Process.Pid, "pid", m.PID)
	return Started{HolderPID: cmd.Process.Pid, PID: m.PID}, nil
}
