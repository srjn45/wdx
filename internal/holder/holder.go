// Package holder runs a terminal's shell in a PTY, in a process of its own
// that outlives the daemon, and talks to it over a Unix socket.
//
// The holder is the shell's parent: it learns of its exit from the kernel,
// keeps the exit code until the daemon has recorded it, and always drains
// the PTY, since a shell whose output nobody reads blocks. See
// docs/TERMINALS.md.
package holder

import (
	"bytes"
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

	"golang.org/x/sys/unix"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/screen"
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
	// HistoryLines bounds the terminal's history; 0 keeps none.
	HistoryLines int64
	// Home is wdx's, which the shell gets as WDX_HOME so that wdx run in it
	// reaches the daemon that owns it.
	Home   string
	Socket string
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
	spool    *spool
	ended    chan struct{} // closed once the shell is reaped
	drained  chan struct{} // closed once the PTY's output is read to its end
	followed chan struct{} // closed once the vt has read the spool to its end
	released chan struct{} // closed once the daemon releases the holder
	release  sync.Once

	mu   sync.Mutex
	exit *Exit
	conn net.Conn // the daemon's; nil when none is connected

	// The vt's side: it follows the spool to vtPos and writes history.
	screenMu   sync.Mutex
	screen     *screen.Screen
	size       Size
	vtPos      int64
	history    *historyWriter // nil when history is off
	historyDir string
	historyErr bool // a failed write is logged once

	// The clients' side: the PTY's reader queues output to them.
	clientsMu  sync.Mutex
	clients    map[*client]bool
	clientExit []byte // the exited frame's payload, once the shell ended
	spoolErr   bool   // a failed append is logged once
}

// startSize is a terminal's size until a client attaches.
var startSize = Size{Cols: 80, Rows: 24}

// Lines the holder writes into history. A pane writes MarkInterrupted
// too when its vt resets, so that it numbers history lines as the holder
// does.
const (
	markRestarted   = "── shell restarted %s ──"
	MarkInterrupted = "── history interrupted ──"
)

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
		identity.Env("HOME") + "=" + o.Home,
	}, o.Env...)
	h := &holder{
		id:       o.ID,
		log:      log,
		listener: listener,
		cmd:      cmd,
		ended:    make(chan struct{}),
		drained:  make(chan struct{}),
		followed: make(chan struct{}),
		released: make(chan struct{}),
		size:     startSize,
		clients:  map[*client]bool{},
	}
	if err := h.open(identity.TerminalDir(o.Home, o.ID), o.HistoryLines); err != nil {
		listener.Close()
		return nil, err
	}
	master, err := pty.Start(cmd, startSize.Cols, startSize.Rows)
	if err != nil {
		listener.Close()
		h.spool.close()
		if h.history != nil {
			h.history.close()
		}
		return nil, fmt.Errorf("start %s in %s: %w", o.Shell, o.Dir, err)
	}
	h.master = master
	log.Info("shell started", "pid", cmd.Process.Pid, "shell", o.Shell, "dir", o.Dir, "socket", o.Socket)
	return h, nil
}

// HistoryDir is where the history of terminal id is kept under home.
func HistoryDir(home, id string) string {
	return historyDir(identity.TerminalDir(home, id))
}

func historyDir(terminalDir string) string {
	return filepath.Join(terminalDir, "history")
}

// open starts the spool, the screen and, unless historyLines is 0, the
// history in dir, which only the user may enter.
func (h *holder) open(dir string, historyLines int64) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("the terminal's directory %q is not absolute: no home", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	var err error
	if h.spool, err = openSpool(filepath.Join(dir, "spool"), spoolSegment, spoolBacklog, spoolKeep); err != nil {
		return err
	}
	h.screen = screen.New(startSize.Cols, startSize.Rows, h.answer)
	h.historyDir = historyDir(dir)
	if historyLines == 0 {
		return nil
	}
	if h.history, err = openHistory(h.historyDir, historyLines); err != nil {
		h.spool.close()
		return err
	}
	if h.history.next > 0 {
		h.writeHistory(fmt.Sprintf(markRestarted, time.Now().Format(time.DateTime)))
	}
	h.log.Info("history opened", "dir", h.historyDir, "history_lines", historyLines, "next_line", h.history.next)
	return nil
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
	go h.read()
	go h.follow()
	go h.wait()
	go h.exitClients()
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
	h.closeClients()
	select {
	case <-h.followed:
	case <-time.After(exitDrain):
	}
	h.spool.close()
	h.screenMu.Lock()
	// What the shell left on the screen goes to history, as a terminal
	// keeps it scrolled back when the shell is started again.
	h.writeHistory(h.screen.Lines()...)
	h.screen.Close()
	if h.history != nil {
		h.history.close()
	}
	h.screenMu.Unlock()
	return nil
}

// read drains the PTY into the spool and the clients' queues, never
// waiting for either: a shell whose output nobody reads blocks.
func (h *holder) read() {
	defer close(h.drained)
	defer h.spool.finish()
	buf := make([]byte, 32<<10)
	for {
		n, err := h.master.Read(buf)
		if n > 0 {
			h.output(buf[:n])
		}
		if err != nil {
			return // the PTY closed: the shell and its children are gone
		}
	}
}

func (h *holder) output(p []byte) {
	chunk := bytes.Clone(p)
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	if err := h.spool.append(chunk); err != nil && !h.spoolErr {
		h.spoolErr = true
		h.log.Error("output not spooled: the screen and history miss it", "err", err.Error())
	}
	for c := range h.clients {
		c.send(frameOutput, chunk)
	}
}

// follow feeds the spool to the screen at the vt's own pace, and what
// scrolls off it to history.
func (h *holder) follow() {
	defer close(h.followed)
	buf := make([]byte, 32<<10)
	for {
		wait := h.spool.wait()
		from, n, err := h.spool.read(h.vtPos, buf)
		if n > 0 || from > h.vtPos {
			h.feed(from, buf[:n])
			continue
		}
		if err != nil {
			return // the spool is finished and read
		}
		<-wait
	}
}

func (h *holder) feed(from int64, p []byte) {
	h.screenMu.Lock()
	defer h.screenMu.Unlock()
	if from > h.vtPos {
		h.log.Warn("history interrupted: the screen fell too far behind", "skipped_bytes", from-h.vtPos)
		h.screen.Reset(h.size.Cols, h.size.Rows)
		h.writeHistory(MarkInterrupted)
	}
	lines, reset := h.screen.Write(p)
	h.writeHistory(lines...)
	if reset {
		h.log.Warn("vt reset: it failed on the output")
		h.writeHistory(MarkInterrupted)
	}
	h.vtPos = from + int64(len(p))
	h.spool.consume(h.vtPos)
}

// writeHistory appends lines to history, when it is on; h.screenMu is
// held.
func (h *holder) writeHistory(lines ...string) {
	if h.history == nil || len(lines) == 0 {
		return
	}
	first := h.history.starts[0]
	if err := h.history.append(lines...); err != nil {
		if !h.historyErr {
			h.historyErr = true
			h.log.Error("history not written", "err", err.Error())
		}
		return
	}
	if h.history.starts[0] != first {
		h.log.Info("history trimmed", "first_line", h.history.starts[0])
	}
}

// historyNext is the number the next history line will take; h.screenMu
// is held.
func (h *holder) historyNext() int64 {
	if h.history == nil {
		return 0
	}
	return h.history.next
}

// answer writes the vt's answers to programs' queries to the shell when no
// client is attached; an attached client's terminal answers them itself.
func (h *holder) answer(p []byte) {
	h.clientsMu.Lock()
	attached := len(h.clients) > 0
	h.clientsMu.Unlock()
	if !attached {
		// A closed PTY wants no answers.
		_, _ = h.master.Write(p)
	}
}

// wait reaps the shell and tells the daemon how it ended.
func (h *holder) wait() {
	_ = h.cmd.Wait() // an exit status other than 0 is an error; ProcessState has it
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
	// Fails only on a closed connection, which the write reports.
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
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

// handle serves a connection: a client's, or the daemon's. A daemon's
// replaces the last: a daemon that restarted dials again.
func (h *holder) handle(ctx context.Context, c *net.UnixConn) {
	defer c.Close()
	if uid, err := peerUID(c); err != nil || uid != os.Getuid() {
		h.log.Warn("connection refused", "peer_uid", uid, "err", err)
		return
	}
	// Fails only on a closed connection, which the read reports.
	_ = c.SetReadDeadline(time.Now().Add(writeTimeout))
	t, body, err := readFrame(c)
	var hello Hello
	if err == nil && t == frameHello {
		err = json.Unmarshal(body, &hello)
	}
	if err != nil || t != frameHello {
		h.log.Warn("connection without hello", "err", err)
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	if hello.Role == roleClient {
		h.serveClient(c)
		return
	}

	h.mu.Lock()
	if h.conn != nil {
		h.conn.Close()
	}
	h.conn = c
	err = h.write(c, frameHello, h.hello())
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

// hello is what the holder says to whoever connects; h.mu is held.
func (h *holder) hello() Hello {
	h.screenMu.Lock()
	next := h.historyNext()
	h.screenMu.Unlock()
	return Hello{Version: Version, TerminalID: h.id, PID: h.cmd.Process.Pid, HolderPID: os.Getpid(), Exited: h.exit, HistoryLines: next}
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
	// A group that is gone needs no signal.
	_ = syscall.Kill(-pid, sig)
	raw, err := h.master.SyscallConn()
	if err != nil {
		return
	}
	foreground := 0
	// When the PTY is closed there is no foreground group to signal.
	_ = raw.Control(func(fd uintptr) {
		foreground, _ = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	})
	if foreground > 0 && foreground != pid {
		_ = syscall.Kill(-foreground, sig)
	}
}
