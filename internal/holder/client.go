package holder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// ErrAttachUnsupported is a holder that speaks a protocol before attach:
// its terminal was started by an older wdx.
var ErrAttachUnsupported = errors.New("the terminal's holder is too old to attach to; restart the terminal")

// ErrConnectionLost is a connection to a holder that ended without the
// shell's exit: the holder is gone, or dropped the client as too slow.
var ErrConnectionLost = errors.New("the connection to the terminal was lost")

// Session is a client attached to a terminal's holder: Read gives what to
// show, Write what was typed.
type Session struct {
	conn net.Conn
	// Hello is what the holder said on connecting.
	Hello Hello
	// History is the history the session asked for, the lines right before
	// the screen.
	History HistoryLines
	// Snapshot is the screen's size and place in the output.
	Snapshot Snapshot

	payload []byte // the rest of the output frame being read
	exit    *Exit

	wmu sync.Mutex
}

// Attach attaches to the holder of terminal id listening on socket, at
// size, after history lines of history: all of it when negative. The
// terminal takes size, as the latest client to attach.
func Attach(ctx context.Context, socket, id string, size Size, history int) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	// Fails only on a closed connection, which the attach reports.
	_ = c.SetDeadline(deadline)
	s := &Session{conn: c}
	if err := s.attach(id, size, history); err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	return s, nil
}

func (s *Session) attach(id string, size Size, history int) error {
	if err := writeFrame(s.conn, frameHello, Hello{Version: Version, Role: roleClient}); err != nil {
		return err
	}
	t, body, err := readFrame(s.conn)
	if err != nil {
		return err
	}
	if t != frameHello || json.Unmarshal(body, &s.Hello) != nil {
		return fmt.Errorf("the holder answered with frame %d, not hello", t)
	}
	if s.Hello.Version < 2 {
		// It took this connection for the daemon's, which dials it again.
		return ErrAttachUnsupported
	}
	if s.Hello.TerminalID != id {
		return fmt.Errorf("the holder runs terminal %s, not %s", s.Hello.TerminalID, id)
	}
	if err := writeFrame(s.conn, frameAttach, attachRequest{Size: size, History: history}); err != nil {
		return err
	}
	for {
		t, body, err := readFrame(s.conn)
		if err != nil {
			return err
		}
		switch t {
		case frameHistory:
			var h HistoryLines
			if err := json.Unmarshal(body, &h); err != nil {
				return err
			}
			if len(s.History.Lines) == 0 {
				s.History.From = h.From
			}
			s.History.Lines = append(s.History.Lines, h.Lines...)
		case frameSnapshot:
			return json.Unmarshal(body, &s.Snapshot)
		default:
			return fmt.Errorf("the holder sent frame %d before the screen", t)
		}
	}
}

// Read reads what to show: the bytes that paint the screen, then the
// output. It returns io.EOF once the shell has exited, which Exit then
// tells, and ErrConnectionLost when the connection ends before.
func (s *Session) Read(p []byte) (int, error) {
	for len(s.payload) == 0 {
		if s.exit != nil {
			return 0, io.EOF
		}
		t, body, err := readFrame(s.conn)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrConnectionLost, err)
		}
		switch t {
		case frameOutput:
			s.payload = body
		case frameExited:
			var exit Exit
			if err := json.Unmarshal(body, &exit); err != nil {
				return 0, err
			}
			s.exit = &exit
		}
	}
	n := copy(p, s.payload)
	s.payload = s.payload[n:]
	return n, nil
}

// Exit is how the shell ended, once Read returned io.EOF.
func (s *Session) Exit() *Exit {
	return s.exit
}

// Write sends what was typed to the shell.
func (s *Session) Write(p []byte) (int, error) {
	for rest := p; len(rest) > 0; {
		n := min(len(rest), outputFrame)
		if err := s.write(frameInput, rest[:n]); err != nil {
			return len(p) - len(rest), err
		}
		rest = rest[n:]
	}
	return len(p), nil
}

// Resize makes size the terminal's.
func (s *Session) Resize(size Size) error {
	body, err := json.Marshal(size)
	if err != nil {
		return err
	}
	return s.write(frameResize, body)
}

func (s *Session) write(t frameType, body []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	// Fails only on a closed connection, which the write reports.
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return writeRaw(s.conn, t, body)
}

// Close detaches; the shell runs on.
func (s *Session) Close() error {
	return s.conn.Close()
}
