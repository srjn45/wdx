package holder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ErrHolderVersionUnsupported is a holder that predates clients relayed from
// another node: its terminal was started by an older wdx.
var ErrHolderVersionUnsupported = errors.New("the terminal's holder is too old to attach to from another node; restart the terminal")

// ErrClientVersionUnsupported is a holder that no longer serves the relayed
// client's version: the client's wdx is older than the holder's oldest.
var ErrClientVersionUnsupported = errors.New("the terminal's holder no longer serves the client's version")

// Relay is a connection to a holder made for a client the daemon relays,
// greeted already: the client's own hello is answered with the holder's, and
// the bytes after it are the protocol's, copied as they are.
type Relay struct {
	// Conn is the connection to the holder.
	Conn net.Conn
	// Hello is what the holder said.
	Hello Hello
	hello []byte // its payload, as the holder sent it
}

// DialRelay connects to the holder of terminal id listening on socket for a
// client on node, another node of the account, or on this one when node is
// "", which speaks version. A holder too old to know a relayed client is
// ErrHolderVersionUnsupported, one that no longer serves version
// ErrClientVersionUnsupported.
//
// It greets the holder in the client's place, before the daemon switches the
// client's connection, so that either is a problem the API answers rather
// than a stream that ends; the client's own hello comes only after the
// switch, so the client gives its version in the attach request.
func DialRelay(ctx context.Context, socket, id, node string, version int) (*Relay, error) {
	limits := limitsOf(ctx)
	ctx, cancel := context.WithTimeout(ctx, limits.Dial)
	defer cancel()
	c, err := dial(ctx, socket)
	if err != nil {
		return nil, err
	}
	r, err := greetRelay(c, id, node, version)
	if err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	return r, nil
}

func greetRelay(c net.Conn, id, node string, version int) (*Relay, error) {
	if err := writeFrame(c, frameHello, Hello{Version: version, Role: roleClient, Remote: node != "", Node: node}); err != nil {
		return nil, err
	}
	t, body, err := readFrame(c)
	if err != nil {
		return nil, err
	}
	var hello Hello
	if t != frameHello || json.Unmarshal(body, &hello) != nil {
		return nil, fmt.Errorf("the holder answered with frame %d, not hello", t)
	}
	if hello.Version < remoteVersion {
		// One before version 2 took this connection for the daemon's, which
		// dials it again; a later one waits for an attach, and logs none came.
		return nil, ErrHolderVersionUnsupported
	}
	if hello.OldestClient > version {
		return nil, fmt.Errorf("%w: it serves %d on, the client speaks %d", ErrClientVersionUnsupported, hello.OldestClient, version)
	}
	if hello.TerminalID != id {
		return nil, fmt.Errorf("the holder runs terminal %s, not %s", hello.TerminalID, id)
	}
	return &Relay{Conn: c, Hello: hello, hello: body}, nil
}

// Greet reads the client's hello from in, the stream out reads through, and
// answers it on out with the holder's, within the dial limit of ctx. What
// follows is the client's attach.
func (r *Relay) Greet(ctx context.Context, in io.Reader, out net.Conn) error {
	_ = out.SetDeadline(time.Now().Add(limitsOf(ctx).Dial))
	defer func() { _ = out.SetDeadline(time.Time{}) }()
	t, _, err := readFrame(in)
	if err != nil {
		return err
	}
	if t != frameHello {
		return fmt.Errorf("the client sent frame %d, not hello", t)
	}
	return writeRaw(out, frameHello, r.hello)
}
