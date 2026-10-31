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
	"cmp"
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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/screen"
)

// Options say what a holder runs.
type Options struct {
	ID      string // the terminal's
	Project string // the project's name, for WDX_PROJECT
	Dir     string
	Shell   string
	// AgentID is the agent's the terminal runs, if any, for WDX_AGENT_ID: what
	// it passes to create a child of itself.
	AgentID string
	// Command, when given, is run through the shell in place of a shell to
	// type in: the shell starts only to read the user's profile and rc, and
	// the command's end is the terminal's.
	Command []string
	// HistoryLines bounds the terminal's history; 0 keeps none.
	HistoryLines int64
	// ParseScreen, when given, reads what the program in the terminal is doing
	// from its screen; see watchScreen.
	ParseScreen ParseScreen
	// Deliver, when given, puts a message into the program in place of typing it
	// and pressing Enter, and Interrupt stops its turn. They come from the cli,
	// as ParseScreen does.
	Deliver   func(w io.Writer, message string) error
	Interrupt []byte
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
	// WriteTimeout bounds a write to a peer and the wait for its first frame;
	// writeTimeout when 0.
	WriteTimeout time.Duration
	// vtDelay holds the vt back at the start, for tests of it falling behind.
	vtDelay time.Duration
	// screenHold, for tests of what must not wait for the vt, holds the screen
	// from the start, as the vt does for each chunk, until this file exists;
	// the file with ".held" added says it is held.
	screenHold string
	// watchTimes are watchScreen's waits, when tests shorten them.
	watchTimes watchTimes
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
		logging.FromContext(ctx).Error("ready not reported", "err", werr)
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
	screenMu sync.Mutex
	// vtMoved is told when vtPos moves or vtStopped is set.
	vtMoved sync.Cond
	// vtStopped is set when the vt stops following the spool.
	vtStopped    bool
	vtDelay      time.Duration
	screenHold   string
	writeTimeout time.Duration
	screen       *screen.Screen
	screenClosed bool
	watch        *screenWatch // nil when nothing reads the screen
	size         Size
	vtPos        int64
	history      *historyWriter // nil when history is off
	lag          lag
	// nextLine is the number the next history line will take: written
	// with h.screenMu held, read without by hello, which must not wait
	// for the vt.
	nextLine   atomic.Int64
	historyDir string
	historyErr bool // a failed write is logged once
	// historyDone is set once the screen the shell left is written: later
	// output, from a child the shell left behind, does not go to history.
	historyDone bool

	// The vt's answers to programs' queries, for the PTY while no client
	// is attached; full drops them, logged once.
	answers        chan []byte
	answersDropped atomic.Bool

	// The clients' side: the PTY's reader queues output to them.
	clientsMu  sync.Mutex
	clients    map[*client]bool
	left       chan struct{} // closed and replaced when a client detaches
	clientExit []byte        // the exited frame's payload, once the shell ended
	spoolErr   bool          // a failed append is logged once

	inbox      *inbox
	deliverTo  func(w io.Writer, message string) error
	interrupt  []byte
	readerDone chan struct{} // closed once the inboxReader returns
}

// answerQueue bounds the vt's answers waiting for a program to read its
// input; a program that never reads them must not stop the vt.
const answerQueue = 64

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
	cmd := shellCommand(o.Shell, o.Command)
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
	if o.AgentID != "" {
		cmd.Env = append(cmd.Env, identity.Env("AGENT_ID")+"="+o.AgentID)
	}
	h := &holder{
		id:           o.ID,
		log:          log,
		listener:     listener,
		cmd:          cmd,
		ended:        make(chan struct{}),
		drained:      make(chan struct{}),
		followed:     make(chan struct{}),
		released:     make(chan struct{}),
		size:         startSize,
		lag:          lag{limit: lagLimit},
		answers:      make(chan []byte, answerQueue),
		clients:      map[*client]bool{},
		left:         make(chan struct{}),
		inbox:        newInbox(),
		deliverTo:    o.Deliver,
		interrupt:    o.Interrupt,
		readerDone:   make(chan struct{}),
		vtDelay:      o.vtDelay,
		screenHold:   o.screenHold,
		writeTimeout: cmp.Or(o.WriteTimeout, writeTimeout),
	}
	h.vtMoved.L = &h.screenMu
	if h.deliverTo == nil {
		h.deliverTo = typeMessage
	}
	if o.ParseScreen != nil {
		h.watch = newScreenWatch(log, o.ParseScreen, h.readScreen, o.watchTimes)
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
	log.Info("shell started", "pid", cmd.Process.Pid, "shell", o.Shell, "command", o.Command, "dir", o.Dir, "socket", o.Socket)
	return h, nil
}

// shellCommand is what a holder starts: the login shell, or the command
// through it. Interactive too, since many keep their PATH in the rc file
// only a shell asked to be interactive reads. The command is one string of
// quoted words, which every shell parses, fish too, as it does not "$@".
func shellCommand(shell string, command []string) *exec.Cmd {
	if len(command) > 0 {
		words := make([]string, len(command))
		for i, word := range command {
			words[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
		}
		return exec.Command(shell, "-l", "-i", "-c", "exec "+strings.Join(words, " "))
	}
	cmd := exec.Command(shell)
	// A leading dash makes it a login shell, which reads the user's profile.
	cmd.Args = []string{"-" + filepath.Base(shell)}
	return cmd
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
	h.nextLine.Store(h.history.next)
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
	go h.writeAnswers()
	go h.inboxReader()
	go h.follow()
	go h.wait()
	go h.exitClients()
	go h.accept(ctx)
	h.watch.start(h.followed)

	select {
	case <-h.released:
		h.log.Info("holder released")
		h.waitClients()
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
	// So that a message whose write failed gets its line in the log. A child
	// the shell left behind can keep that write waiting.
	select {
	case <-h.readerDone:
	case <-time.After(exitDrain):
	}
	select {
	case <-h.followed:
	case <-time.After(exitDrain):
	}
	h.watch.stop()
	h.spool.close()
	h.screenMu.Lock()
	h.screenClosed = true
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
		h.log.Error("output not spooled: the screen and history miss it", "err", err)
	}
	for c := range h.clients {
		c.send(frameOutput, chunk)
	}
}

// follow feeds the spool to the screen at the vt's own pace, and what
// scrolls off it to history.
func (h *holder) follow() {
	defer close(h.followed)
	defer func() {
		h.screenMu.Lock()
		defer h.screenMu.Unlock()
		h.vtStopped = true
		h.vtMoved.Broadcast()
	}()
	time.Sleep(h.vtDelay)
	h.holdScreen()
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

func (h *holder) holdScreen() {
	if h.screenHold == "" {
		return
	}
	h.screenMu.Lock()
	defer h.screenMu.Unlock()
	_ = os.WriteFile(h.screenHold+".held", nil, 0o600)
	for _, err := os.Stat(h.screenHold); err != nil; _, err = os.Stat(h.screenHold) {
		time.Sleep(10 * time.Millisecond)
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
	start := time.Now()
	lines, reset := h.screen.Write(p)
	took := time.Since(start)
	h.writeHistory(lines...)
	if reset {
		h.log.Warn("vt reset: it failed on the output")
		h.writeHistory(MarkInterrupted)
	}
	h.vtPos = from + int64(len(p))
	h.vtMoved.Broadcast()
	h.spool.consume(h.vtPos)
	h.lag.fed(h.log, h.spool.position()-h.vtPos, len(p), took)
	h.watch.screenChanged()
}

// lagLimit is how far behind the output the vt falls before the holder
// logs it: about 5 s of the vt's work at its 1.5 MB/s (docs/VT.md, Speed).
const lagLimit = 8 << 20

// lag watches how far the vt is behind the output, and logs when it falls
// behind limit and when it catches up: whether its speed needs work.
type lag struct {
	limit int64
	since time.Time     // zero while the vt keeps up
	max   int64         // the most it was behind since then
	bytes int64         // what it parsed since then
	busy  time.Duration // the time it took
}

// fed records a piece of n bytes the vt took to parse, leaving it behind
// bytes behind the output.
func (l *lag) fed(log *slog.Logger, behind int64, n int, took time.Duration) {
	if l.since.IsZero() {
		if behind < l.limit {
			return
		}
		l.since, l.max, l.bytes, l.busy = time.Now(), behind, 0, 0
		log.Info("vt fell behind", "lag_bytes", behind)
		return
	}
	l.max = max(l.max, behind)
	l.bytes += int64(n)
	l.busy += took
	if behind > 0 {
		return
	}
	log.Info("vt caught up",
		"duration_ms", time.Since(l.since).Milliseconds(),
		"max_lag_bytes", l.max,
		"bytes", l.bytes,
		"bytes_per_second", int64(float64(l.bytes)/max(l.busy.Seconds(), 1e-9)))
	l.since = time.Time{}
}

// writeHistory appends lines to history, when it is on; h.screenMu is
// held.
func (h *holder) writeHistory(lines ...string) {
	if h.history == nil || h.historyDone || len(lines) == 0 {
		return
	}
	first := h.history.starts[0]
	defer func() { h.nextLine.Store(h.history.next) }()
	if err := h.history.append(lines...); err != nil {
		if !h.historyErr {
			h.historyErr = true
			h.log.Error("history not written", "err", err)
		}
		return
	}
	if h.history.starts[0] != first {
		h.log.Info("history trimmed", "first_line", h.history.starts[0])
	}
}

// answer writes the vt's answers to programs' queries to the shell when no
// client is attached; an attached client's terminal answers them itself.
func (h *holder) answer(p []byte) {
	h.clientsMu.Lock()
	attached := len(h.clients) > 0
	h.clientsMu.Unlock()
	if attached {
		return
	}
	select {
	case h.answers <- p:
	default:
		if !h.answersDropped.Swap(true) {
			h.log.Warn("answers dropped: the program does not read its input")
		}
	}
}

// writeAnswers writes the vt's answers to the PTY, until its output ends.
func (h *holder) writeAnswers() {
	for {
		select {
		case p := <-h.answers:
			// A closed PTY wants no answers.
			_, _ = h.master.Write(p)
		case <-h.drained:
			return
		}
	}
}

// wait reaps the shell and tells the daemon how it ended, once what the
// shell left on the screen is in history.
func (h *holder) wait() {
	_ = h.cmd.Wait() // an exit status other than 0 is an error; ProcessState has it
	exit := &Exit{Code: exitCode(h.cmd.ProcessState), At: time.Now()}
	h.log.Info("shell exited", "exit_code", exit.Code)
	h.keepScreen()
	h.mu.Lock()
	h.exit = exit
	h.mu.Unlock()
	close(h.ended)
	h.dropMessages()
	h.sendExit()
}

// keepScreen writes what the shell left on the main screen to history, as
// a terminal keeps it scrolled back, once the vt has read the shell's output:
// whoever hears of the exit finds it there, and a shell started again
// continues below it. The vt may take long after a burst of output, and the
// exit waits for it; history would lose what it had not read.
func (h *holder) keepScreen() {
	if h.history == nil {
		return
	}
	start := time.Now()
	select {
	case <-h.drained:
	case <-time.After(exitDrain):
		// A child the shell left behind keeps the PTY open; what it writes
		// from here on is not the shell's.
		h.log.Info("screen kept before the output ended")
	}
	end := h.spool.position()
	h.screenMu.Lock()
	defer h.screenMu.Unlock()
	for h.vtPos < end && !h.vtStopped {
		h.vtMoved.Wait()
	}
	h.writeHistory(h.screen.Lines()...)
	h.historyDone = true
	h.log.Info("screen kept in history", "duration_ms", time.Since(start).Milliseconds())
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
		h.log.Info("exit not sent", "err", err)
	}
}

// write sends a frame to c; h.mu must be held.
func (h *holder) write(c net.Conn, t frameType, payload any) error {
	// Fails only on a closed connection, which the write reports.
	_ = c.SetWriteDeadline(time.Now().Add(h.writeTimeout))
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
	_ = c.SetReadDeadline(time.Now().Add(h.writeTimeout))
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
		h.log.Info("hello not sent", "err", err)
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
			h.log.Info("daemon disconnected", "err", err)
			return
		}
		switch t {
		case frameStop:
			var req stopRequest
			if err := json.Unmarshal(body, &req); err != nil {
				h.log.Warn("bad stop request", "err", err)
				continue
			}
			go h.stop(time.Duration(req.GraceMS) * time.Millisecond)
		case frameSend:
			var req sendRequest
			if err := json.Unmarshal(body, &req); err != nil {
				h.log.Warn("bad send request", "err", err)
				continue
			}
			h.queue(message{text: req.Prompt, instant: req.Instant})
		case frameRelease:
			select {
			case <-h.ended:
				h.release.Do(func() { close(h.released) })
				return
			default:
				h.log.Warn("release ignored: the shell is running")
			}
		default:
			h.log.Warn("unknown frame ignored", "frame", int(t))
		}
	}
}

// hello is what the holder says to whoever connects; h.mu is held.
func (h *holder) hello() Hello {
	return Hello{Version: Version, TerminalID: h.id, PID: h.cmd.Process.Pid, HolderPID: os.Getpid(), Exited: h.exit, HistoryLines: h.nextLine.Load()}
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
