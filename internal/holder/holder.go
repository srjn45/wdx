// Package holder runs a terminal's shell in a PTY, in a process of its own
// that outlives the daemon, and talks to it over a Unix socket.
//
// The holder is the shell's parent: it learns of its exit from the kernel,
// keeps the exit code until the daemon has recorded it, and always drains
// the PTY, since a shell whose output nobody reads blocks. See
// docs/TERMINALS.md.
package holder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// writeTimeout bounds a write to the daemon, so one that stopped reading
// cannot stall the holder.
const writeTimeout = 5 * time.Second

// Options say what a holder runs.
type Options struct {
	ID      string // the terminal's
	Project string // the project's name, for WDX_PROJECT
	Dir     string
	Shell   string
	Socket  string
	// Env holds the variables the shell inherits from the daemon, as
	// KEY=value; the holder adds the user's and its own.
	Env []string
	// Ready is told the shell's pid once it runs, or why it did not start,
	// and then closed.
	Ready io.WriteCloser
}

// readyMessage is the one line a holder writes on its ready pipe.
type readyMessage struct {
	PID   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
}

// Run starts the shell and serves the daemon until it releases the holder
// after the shell ended, or until ctx is done, which kills the shell.
func Run(ctx context.Context, o Options) error {
	ctx = logging.NewContext(ctx, logging.FromContext(ctx).With("terminal_id", o.ID))
	h, err := start(ctx, o)
	ready := readyMessage{}
	if err != nil {
		ready.Error = err.Error()
	} else {
		ready.PID = h.cmd.Process.Pid
	}
	if werr := json.NewEncoder(o.Ready).Encode(ready); werr != nil {
		logging.FromContext(ctx).Error("ready not reported", "err", werr.Error())
	}
	o.Ready.Close()
	if err != nil {
		return err
	}
	return h.serve(ctx)
}

type holder struct {
	id       string
	log      *slog.Logger
	listener *net.UnixListener
	cmd      *exec.Cmd
	master   *os.File
	output   *ring
	ended    chan struct{} // closed once the shell is reaped
	released chan struct{} // closed once the daemon releases the holder
	release  sync.Once

	mu   sync.Mutex
	exit *Exit
	conn net.Conn // the daemon's; nil when none is connected
}

// start listens on the socket and starts the shell in a new PTY.
func start(ctx context.Context, o Options) (*holder, error) {
	log := logging.FromContext(ctx)
	account, err := LookupAccount(ctx)
	if err != nil {
		return nil, err
	}
	listener, err := listen(o.Socket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(o.Shell)
	// A leading dash makes it a login shell, which reads the user's profile.
	cmd.Args = []string{"-" + filepath.Base(o.Shell)}
	cmd.Dir = o.Dir
	cmd.Env = append([]string{
		"HOME=" + account.Home,
		"USER=" + account.Name,
		"LOGNAME=" + account.Name,
		"SHELL=" + account.LoginShell(),
		"TERM=xterm-256color",
		identity.Env("TERMINAL_ID") + "=" + o.ID,
		identity.Env("PROJECT") + "=" + o.Project,
	}, o.Env...)
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("start %s in %s: %w", o.Shell, o.Dir, err)
	}
	log.Info("shell started", "pid", cmd.Process.Pid, "shell", o.Shell, "dir", o.Dir, "socket", o.Socket)
	return &holder{
		id:       o.ID,
		log:      log,
		listener: listener,
		cmd:      cmd,
		master:   master,
		output:   newRing(ringSize),
		ended:    make(chan struct{}),
		released: make(chan struct{}),
	}, nil
}

// listen listens on socket, in a directory only the user may enter.
func listen(socket string) (*net.UnixListener, error) {
	dir := filepath.Dir(socket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	// Left behind by a holder that was killed; the daemon starts one holder
	// per terminal.
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
}

func (h *holder) serve(ctx context.Context) error {
	defer h.master.Close()
	go io.Copy(h.output, h.master) // ends with an error once the PTY closes
	go h.wait()
	go h.accept(ctx)

	select {
	case <-h.released:
		h.log.Info("holder released")
	case <-ctx.Done():
		h.signal(syscall.SIGKILL)
		<-h.ended
		h.log.Info("holder interrupted")
	}
	h.listener.Close() // removes the socket
	h.mu.Lock()
	if h.conn != nil {
		h.conn.Close()
	}
	h.mu.Unlock()
	return nil
}

// wait reaps the shell and tells the daemon how it ended.
func (h *holder) wait() {
	h.cmd.Wait()
	exit := &Exit{Code: exitCode(h.cmd.ProcessState), At: time.Now()}
	h.log.Info("shell exited", "exit_code", exit.Code)
	h.mu.Lock()
	h.exit = exit
	h.mu.Unlock()
	close(h.ended)
	h.sendExit()
}

// exitCode is the shell's exit status, or 128 + the signal that killed it.
func exitCode(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return state.ExitCode()
}

func (h *holder) sendExit() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil || h.exit == nil {
		return
	}
	if err := h.write(h.conn, frameExited, h.exit); err != nil {
		h.log.Info("exit not sent", "err", err.Error())
	}
}

// write sends a frame to c; h.mu must be held.
func (h *holder) write(c net.Conn, t frameType, payload any) error {
	c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return writeFrame(c, t, payload)
}

func (h *holder) accept(ctx context.Context) {
	for {
		c, err := h.listener.AcceptUnix()
		if err != nil {
			return // the listener is closed
		}
		go h.handle(ctx, c)
	}
}

// handle serves a daemon's connection. A new one replaces the last: a
// daemon that restarted dials again.
func (h *holder) handle(ctx context.Context, c *net.UnixConn) {
	defer c.Close()
	if uid, err := peerUID(c); err != nil || uid != os.Getuid() {
		h.log.Warn("connection refused", "peer_uid", uid, "err", err)
		return
	}
	c.SetReadDeadline(time.Now().Add(writeTimeout))
	if t, _, err := readFrame(c); err != nil || t != frameHello {
		h.log.Warn("connection without hello", "err", err)
		return
	}
	c.SetReadDeadline(time.Time{})

	h.mu.Lock()
	if h.conn != nil {
		h.conn.Close()
	}
	h.conn = c
	err := h.write(c, frameHello, Hello{Version: Version, TerminalID: h.id, PID: h.cmd.Process.Pid, HolderPID: os.Getpid(), Exited: h.exit})
	h.mu.Unlock()
	if err != nil {
		h.log.Info("hello not sent", "err", err.Error())
		return
	}
	h.log.Info("daemon connected")
	defer func() {
		h.mu.Lock()
		if h.conn == c {
			h.conn = nil
		}
		h.mu.Unlock()
	}()

	for {
		t, body, err := readFrame(c)
		if err != nil {
			h.log.Info("daemon disconnected", "err", err.Error())
			return
		}
		switch t {
		case frameStop:
			var req stopRequest
			if err := json.Unmarshal(body, &req); err != nil {
				h.log.Warn("bad stop request", "err", err.Error())
				continue
			}
			go h.stop(time.Duration(req.GraceMS) * time.Millisecond)
		case frameRelease:
			select {
			case <-h.ended:
				h.release.Do(func() { close(h.released) })
				return
			default:
				h.log.Warn("release ignored: the shell is running")
			}
		default:
			h.log.Warn("unknown frame ignored", "type", int(t))
		}
	}
}

// stop hangs up on the shell and the program in the foreground, and kills
// both once grace has passed; the shell's exit answers the daemon.
func (h *holder) stop(grace time.Duration) {
	select {
	case <-h.ended:
		h.sendExit()
		return
	default:
	}
	h.log.Info("stopping shell", "grace_ms", grace.Milliseconds())
	h.signal(syscall.SIGHUP)
	select {
	case <-h.ended:
	case <-time.After(grace):
		h.log.Info("shell still running after the grace period, killing it")
		h.signal(syscall.SIGKILL)
	}
}

// signal sends sig to the shell's process group and to the PTY's
// foreground one, when that is another.
func (h *holder) signal(sig syscall.Signal) {
	pid := h.cmd.Process.Pid
	syscall.Kill(-pid, sig)
	raw, err := h.master.SyscallConn()
	if err != nil {
		return
	}
	foreground := 0
	raw.Control(func(fd uintptr) {
		foreground, _ = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	})
	if foreground > 0 && foreground != pid {
		syscall.Kill(-foreground, sig)
	}
}
