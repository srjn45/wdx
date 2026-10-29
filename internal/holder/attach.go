package holder

import (
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/pty"
)

const (
	// clientQueue bounds the output queued to a client; one that falls
	// further behind is disconnected, as too slow.
	clientQueue = 4 << 20
	// historyFrame bounds the lines one history frame carries, in bytes.
	historyFrame = 256 << 10
	// outputFrame bounds the bytes one output frame carries.
	outputFrame = 32 << 10
	// exitDrain is how long the clients' exited waits for the shell's last
	// output: a child the shell left behind can keep the PTY open.
	exitDrain = time.Second
)

// client is an attached client: the PTY's reader queues output to it, and
// its writer sends what is queued after the screen it attached to.
type client struct {
	conn *net.UnixConn
	pid  int

	mu     sync.Mutex
	queue  []queued
	queued int // bytes
	wake   chan struct{}
	gone   string // why it was disconnected; "" while attached
}

type queued struct {
	t       frameType
	payload []byte
}

// send queues a frame without waiting; a client whose queue overflows is
// disconnected.
func (c *client) send(t frameType, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gone != "" {
		return
	}
	if c.queued+len(payload) > clientQueue {
		c.disconnect("too slow")
		return
	}
	c.queue = append(c.queue, queued{t, payload})
	c.queued += len(payload)
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// disconnect closes the connection, which ends the client's goroutines;
// c.mu is held.
func (c *client) disconnect(why string) {
	if c.gone == "" {
		c.gone = why
		c.conn.Close()
	}
}

// next takes what is queued, or nil once the client is gone.
func (c *client) next() []queued {
	for {
		c.mu.Lock()
		if c.gone != "" {
			c.mu.Unlock()
			return nil
		}
		if q := c.queue; len(q) > 0 {
			c.queue, c.queued = nil, 0
			c.mu.Unlock()
			return q
		}
		c.mu.Unlock()
		<-c.wake
	}
}

// serveClient attaches a client: the history and screen it asks for, then
// the output, while its input and size go to the shell.
func (h *holder) serveClient(conn *net.UnixConn) {
	c := &client{conn: conn, pid: peerPID(conn), wake: make(chan struct{}, 1)}
	log := h.log.With("client_pid", c.pid)
	h.mu.Lock()
	err := h.write(conn, frameHello, h.hello())
	h.mu.Unlock()
	if err != nil {
		log.Info("hello not sent to client", "err", err.Error())
		return
	}
	// The hello's deadline would fail every write after it: a client may
	// stay attached for days, and one that stops reading is dropped by
	// its queue instead.
	_ = conn.SetWriteDeadline(time.Time{})
	_ = conn.SetReadDeadline(time.Now().Add(writeTimeout))
	t, body, err := readFrame(conn)
	var req attachRequest
	if err == nil && t == frameAttach {
		err = json.Unmarshal(body, &req)
	}
	if err != nil || t != frameAttach || !validSize(req.Size) {
		log.Warn("client did not ask to attach", "frame", int(t), "err", err)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	start := h.attach(c, req.Size)
	log.Info("client attached", "cols", req.Cols, "rows", req.Rows, "history", req.History, "pos", start.Pos)
	written := make(chan error, 1)
	go func() {
		err := h.writeClient(c, start, req.History)
		if err != nil {
			// The client must not stay attached without its output.
			c.mu.Lock()
			c.disconnect("output not sent: " + err.Error())
			c.mu.Unlock()
		}
		written <- err
	}()

	reason := h.readClient(c)
	h.detach(c)
	c.mu.Lock()
	c.disconnect(reason)
	why := c.gone
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}: // the writer sees it is gone
	default:
	}
	if err := <-written; err != nil && why == reason {
		why = err.Error()
	}
	log.Info("client detached", "reason", why)
}

func validSize(s Size) bool {
	return s.Cols > 0 && s.Rows > 0 && s.Cols <= 10_000 && s.Rows <= 10_000
}

// attachStart is where an attaching client's stream starts: the screen,
// the history line it continues, and the spool's output up to live, where
// the queue takes over.
type attachStart struct {
	Snapshot
	paint []byte
	live  int64
	exit  []byte // the exited payload when the shell already ended
}

// attach resizes the terminal to the client's size, takes the screen and
// adds the client, which gets every output from then on queued.
func (h *holder) attach(c *client, size Size) attachStart {
	h.screenMu.Lock()
	defer h.screenMu.Unlock()
	h.resizeLocked(size)
	start := attachStart{
		Snapshot: Snapshot{Size: h.size, Pos: h.vtPos, HistoryLines: h.historyNext()},
		paint:    h.screen.Snapshot(),
	}
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	start.live = h.spool.position()
	start.exit = h.clientExit
	h.clients[c] = true
	return start
}

// detach removes the client: no more output is queued to it, and with
// none left the vt answers programs' queries again.
func (h *holder) detach(c *client) {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	delete(h.clients, c)
}

// writeClient sends the history and the screen, the spool from the
// screen's position to live, then the queue, until the client is gone. The
// exited frame ends it.
func (h *holder) writeClient(c *client, start attachStart, history int) error {
	if err := h.sendHistory(c.conn, start.HistoryLines, history); err != nil {
		return err
	}
	if err := writeFrame(c.conn, frameSnapshot, start.Snapshot); err != nil {
		return err
	}
	for p := start.paint; len(p) > 0; {
		n := min(len(p), outputFrame)
		if err := writeRaw(c.conn, frameOutput, p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	buf := make([]byte, outputFrame)
	for pos := start.Pos; pos < start.live; {
		from, n, err := h.spool.read(pos, buf)
		if n == 0 {
			return cmp.Or(err, io.ErrUnexpectedEOF)
		}
		n = int(min(int64(n), start.live-from))
		if err := writeRaw(c.conn, frameOutput, buf[:n]); err != nil {
			return err
		}
		pos = from + int64(n)
	}
	frames := []queued{{frameExited, start.exit}}
	if start.exit == nil {
		frames = c.next()
	}
	for ; frames != nil; frames = c.next() {
		for _, f := range frames {
			if err := writeRaw(c.conn, f.t, f.payload); err != nil {
				return err
			}
			if f.t == frameExited {
				c.mu.Lock()
				c.disconnect("shell exited")
				c.mu.Unlock()
				return nil
			}
		}
	}
	return nil
}

// sendHistory sends the count history lines before line next: all of them
// when count is negative.
func (h *holder) sendHistory(w io.Writer, next int64, count int) error {
	if count == 0 || next == 0 {
		return nil
	}
	from := int64(0)
	if count > 0 {
		from = max(0, next-int64(count))
	}
	var frame HistoryLines
	size := 0
	flush := func() error {
		if len(frame.Lines) == 0 {
			return nil
		}
		err := writeFrame(w, frameHistory, frame)
		frame, size = HistoryLines{}, 0
		return err
	}
	for from < next {
		got, err := ReadHistory(h.historyDir, from, int(min(next-from, 1000)))
		if err != nil || len(got.Lines) == 0 {
			break // trimmed or gone: the client gets what there was
		}
		for i, line := range got.Lines {
			if size+len(line) > historyFrame {
				if err := flush(); err != nil {
					return err
				}
			}
			if len(frame.Lines) == 0 {
				frame.From = got.From + int64(i)
			}
			frame.Lines = append(frame.Lines, line)
			size += len(line)
		}
		from = got.From + int64(len(got.Lines))
	}
	return flush()
}

// readClient passes the client's input and sizes to the shell until it
// leaves, and says why it did.
func (h *holder) readClient(c *client) string {
	for {
		t, body, err := readFrame(c.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "detached"
			}
			return "connection closed"
		}
		switch t {
		case frameInput:
			// A closed PTY takes no input; the exited frame tells the client.
			_, _ = h.master.Write(body)
		case frameResize:
			var size Size
			if json.Unmarshal(body, &size) != nil || !validSize(size) {
				h.log.Warn("bad resize from client", "client_pid", c.pid)
				continue
			}
			h.screenMu.Lock()
			h.resizeLocked(size)
			h.screenMu.Unlock()
		default:
			h.log.Warn("unknown frame from client ignored", "client_pid", c.pid, "type", int(t))
		}
	}
}

// resizeLocked makes size the terminal's: the PTY's, which tells the
// program in it, and the screen's. The latest client to attach or resize
// sets it. h.screenMu is held.
func (h *holder) resizeLocked(size Size) {
	if size == h.size {
		return
	}
	if err := pty.Setsize(h.master, size.Cols, size.Rows); err != nil {
		h.log.Warn("terminal not resized", "err", err.Error())
		return
	}
	if !h.screen.Resize(size.Cols, size.Rows) {
		h.log.Warn("vt reset: it failed on the resize")
		h.writeHistory(markInterrupted)
	}
	h.size = size
	h.log.Info("terminal resized", "cols", size.Cols, "rows", size.Rows)
}

// exitClients tells the clients how the shell ended, once its last output
// is queued to them.
func (h *holder) exitClients() {
	<-h.ended
	select {
	case <-h.drained:
	case <-time.After(exitDrain):
	}
	h.mu.Lock()
	exit, _ := json.Marshal(h.exit)
	h.mu.Unlock()
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	h.clientExit = exit
	for c := range h.clients {
		c.send(frameExited, exit)
	}
}

// closeClients disconnects every client: the holder is quitting.
func (h *holder) closeClients() {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		c.disconnect("holder quit")
		c.mu.Unlock()
	}
}
