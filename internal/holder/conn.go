package holder

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// dialTimeout bounds a dial and the hello that follows, unless ctx ends
// sooner.
const dialTimeout = 5 * time.Second

// Conn is the daemon's connection to a holder. Nothing polls: the holder
// pushes the shell's exit, and the connection ending means the holder is
// gone or replaced it with a newer one.
type Conn struct {
	c      net.Conn
	hello  Hello
	exited chan Exit
	done   chan struct{}
	wmu    sync.Mutex
}

// Dial connects to the holder of terminal id listening on socket.
func Dial(ctx context.Context, socket, id string) (*Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	// Fails only on a closed connection, which greeting reports.
	_ = c.SetDeadline(deadline)
	hello, err := greet(c)
	if err == nil && hello.TerminalID != id {
		err = fmt.Errorf("the holder on %s runs terminal %s, not %s", socket, hello.TerminalID, id)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	conn := &Conn{c: c, hello: hello, exited: make(chan Exit, 1), done: make(chan struct{})}
	if hello.Exited != nil {
		conn.exited <- *hello.Exited
	}
	go conn.read()
	return conn, nil
}

func greet(c net.Conn) (Hello, error) {
	if err := writeFrame(c, frameHello, Hello{Version: Version}); err != nil {
		return Hello{}, err
	}
	t, body, err := readFrame(c)
	if err != nil {
		return Hello{}, err
	}
	var hello Hello
	if t != frameHello {
		return Hello{}, fmt.Errorf("the holder answered with frame %d, not hello", t)
	}
	if err := json.Unmarshal(body, &hello); err != nil {
		return Hello{}, err
	}
	if hello.Version != Version {
		return Hello{}, fmt.Errorf("the holder speaks protocol version %d, this daemon %d", hello.Version, Version)
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
	_ = c.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return writeFrame(c.c, t, payload)
}
