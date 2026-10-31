package holder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ErrSendUnsupported is a holder that speaks a protocol before the inbox: its
// terminal was started by an older wdx.
var ErrSendUnsupported = errors.New("the terminal's holder is too old to take messages; restart the terminal")

// ErrMessageTooLarge is a message that does not fit in a frame.
var ErrMessageTooLarge = fmt.Errorf("the message does not fit in the %d bytes the holder takes", maxPayload)

// Conn is the daemon's connection to a holder. Nothing polls: the holder
// pushes the shell's exit, and the connection ending means the holder is
// gone or replaced it with a newer one.
type Conn struct {
	c            net.Conn
	writeTimeout time.Duration
	hello        Hello
	exited       chan Exit
	done         chan struct{}
	wmu          sync.Mutex
}

// Dial connects to the holder of terminal id listening on socket.
func Dial(ctx context.Context, socket, id string) (*Conn, error) {
	limits := limitsOf(ctx)
	ctx, cancel := context.WithTimeout(ctx, limits.Dial)
	defer cancel()
	c, err := dial(ctx, socket)
	if err != nil {
		return nil, err
	}
	hello, err := greet(c, roleDaemon)
	if err == nil && !drives(hello.Version) {
		err = fmt.Errorf("the holder speaks protocol version %d, which this daemon (version %d) cannot drive", hello.Version, Version)
	}
	if err == nil && hello.TerminalID != id {
		err = fmt.Errorf("the holder on %s runs terminal %s, not %s", socket, hello.TerminalID, id)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	conn := &Conn{c: c, writeTimeout: limits.Write, hello: hello, exited: make(chan Exit, 1), done: make(chan struct{})}
	if hello.Exited != nil {
		conn.exited <- *hello.Exited
	}
	go conn.read()
	return conn, nil
}

// dial connects to the holder on socket, with ctx's deadline on the
// connection for the greeting that follows.
func dial(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	// Fails only on a closed connection, which the greeting reports.
	_ = c.SetDeadline(deadline)
	return c, nil
}

// greet says hello as role and returns the holder's hello.
func greet(c net.Conn, role string) (Hello, error) {
	if err := writeFrame(c, frameHello, Hello{Version: Version, Role: role}); err != nil {
		return Hello{}, err
	}
	t, body, err := readFrame(c)
	if err != nil {
		return Hello{}, err
	}
	var hello Hello
	if t != frameHello || json.Unmarshal(body, &hello) != nil {
		return Hello{}, fmt.Errorf("the holder answered with frame %d, not hello", t)
	}
	return hello, nil
}

// Hello is what the holder told on connecting.
func (c *Conn) Hello() Hello {
	return c.hello
}

// Exited receives how the shell ended, also when it ended before the
// connection was made.
func (c *Conn) Exited() <-chan Exit {
	return c.exited
}

// Done is closed when the connection ends.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

func (c *Conn) read() {
	defer close(c.done)
	for {
		t, body, err := readFrame(c.c)
		if err != nil {
			return
		}
		if t != frameExited {
			continue
		}
		var exit Exit
		if json.Unmarshal(body, &exit) != nil {
			continue
		}
		select {
		case c.exited <- exit:
		default: // already told
		}
	}
}

// Stop asks the holder to hang up on the shell and to kill it after grace;
// Exited receives the result.
func (c *Conn) Stop(grace time.Duration) error {
	return c.write(frameStop, stopRequest{GraceMS: grace.Milliseconds()})
}

// Send puts prompt in the holder's inbox. The holder delivers it when it can,
// so nil does not mean the shell has it.
func (c *Conn) Send(prompt string) error {
	if c.hello.Version < inboxVersion {
		return ErrSendUnsupported
	}
	body, err := json.Marshal(sendRequest{Prompt: prompt})
	if err != nil {
		return err
	}
	if len(body) > maxPayload {
		return ErrMessageTooLarge
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// Fails only on a closed connection, which the write reports.
	_ = c.c.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	return writeRaw(c.c, frameSend, body)
}

// Release tells the holder the shell's exit is recorded: it quits.
func (c *Conn) Release() error {
	err := c.write(frameRelease, nil)
	c.Close()
	return err
}

// Close closes the connection; the holder keeps running.
func (c *Conn) Close() error {
	return c.c.Close()
}

func (c *Conn) write(t frameType, payload any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// Fails only on a closed connection, which the write reports.
	_ = c.c.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	return writeFrame(c.c, t, payload)
}
