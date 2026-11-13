package holder

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/srjn45/wdx/internal/screen"
)

// ErrSendUnsupported is a holder that speaks a protocol before the inbox: its
// terminal was started by an older wdx.
var ErrSendUnsupported = errors.New("the terminal's holder is too old to take messages; restart the terminal")

// ErrAgentSendUnsupported is an agent's holder that speaks a protocol before it
// delivers to an ACA: it was started by an older wdx.
var ErrAgentSendUnsupported = errors.New("the agent's holder is too old to take messages; close and open its project to restart it")

// ErrMessageTooLarge is a message that does not fit in a frame.
var ErrMessageTooLarge = fmt.Errorf("the message does not fit in the %d bytes the holder takes", maxPayload)

// ErrAnswerUnsupported is an agent's holder too old to answer its prompts: it
// was started by an older wdx.
var ErrAnswerUnsupported = errors.New("the agent's holder is too old to answer its prompts; restart the agent")

// ErrAnswerTooLarge is an answer that does not fit in a frame.
var ErrAnswerTooLarge = fmt.Errorf("the answer does not fit in the %d bytes the holder takes", maxPayload)

// Conn is the daemon's connection to a holder. Nothing polls: the holder
// pushes the shell's exit and its status, and the connection ending means the holder is
// gone or replaced it with a newer one.
type Conn struct {
	deliveredMu      sync.Mutex
	delivered        []string // ids told and not taken yet
	deliveredChanged chan struct{}
	c                net.Conn
	writeTimeout     time.Duration
	hello            Hello
	exited           chan Exit
	done             chan struct{}
	wmu              sync.Mutex
	// status is the latest told, kept here so that the reader never waits for
	// who records it.
	status        atomic.Pointer[Status]
	statusChanged chan struct{}
	// answers are the answers sent and not reported yet, by id.
	answersMu sync.Mutex
	answers   map[string]chan AnswerReport
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
	conn := &Conn{c: c, writeTimeout: limits.Write, hello: hello, exited: make(chan Exit, 1), done: make(chan struct{}), statusChanged: make(chan struct{}, 1), answers: map[string]chan AnswerReport{}}
	if hello.Status != nil {
		conn.status.Store(hello.Status)
	} else {
		conn.status.Store(&Status{Activity: string(screen.ActivityUnknown)})
	}
	if hello.Exited != nil {
		conn.exited <- *hello.Exited
	}
	conn.delivered = hello.Delivered
	conn.deliveredChanged = make(chan struct{}, 1)
	if len(hello.Delivered) > 0 {
		wake(conn.deliveredChanged)
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

// Status is the holder's latest status. A holder with none to tell, as one too
// old to, is unknown.
func (c *Conn) Status() Status {
	return *c.status.Load()
}

// StatusChanged tells when Status changed; a wake pending is enough, as Status
// gives the latest.
func (c *Conn) StatusChanged() <-chan struct{} {
	return c.statusChanged
}

// Delivered tells when TakeDelivered has something; a wake pending is enough, as it
// gives all there is.
func (c *Conn) Delivered() <-chan struct{} {
	return c.deliveredChanged
}

// TakeDelivered returns the ids of the messages the holder typed since it was last
// asked, those of its hello first.
func (c *Conn) TakeDelivered() []string {
	c.deliveredMu.Lock()
	defer c.deliveredMu.Unlock()
	ids := c.delivered
	c.delivered = nil
	return ids
}

// Confirms reports whether the holder tells which messages it typed, and ignores one
// sent again.
func (c *Conn) Confirms() bool {
	return c.hello.Confirms
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
		switch t {
		case frameExited:
			var exit Exit
			if json.Unmarshal(body, &exit) != nil {
				continue
			}
			select {
			case c.exited <- exit:
			default: // already told
			}
		case frameStatus:
			var status Status
			if json.Unmarshal(body, &status) != nil {
				continue
			}
			c.status.Store(&status)
			wake(c.statusChanged)
		case frameDelivered:
			var report deliveredReport
			if json.Unmarshal(body, &report) != nil {
				continue
			}
			c.deliveredMu.Lock()
			c.delivered = append(c.delivered, report.IDs...)
			c.deliveredMu.Unlock()
			wake(c.deliveredChanged)
		case frameAnswered:
			var report AnswerReport
			if json.Unmarshal(body, &report) != nil {
				continue
			}
			c.answersMu.Lock()
			if waiting, ok := c.answers[report.ID]; ok {
				waiting <- report // buffered, and reported once
				delete(c.answers, report.ID)
			}
			c.answersMu.Unlock()
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
	return c.send(sendRequest{Prompt: prompt})
}

// SendMessage is Send with an id, which a holder that Confirms reports it by, and
// ignores it sent again by.
func (c *Conn) SendMessage(id, prompt string) error {
	if c.hello.Version < inboxVersion {
		return ErrSendUnsupported
	}
	return c.send(sendRequest{Prompt: prompt, ID: id})
}

// SendAgentMessage is SendAgent with an id.
func (c *Conn) SendAgentMessage(id, prompt string, instant bool) error {
	if c.hello.Version < agentInboxVersion {
		return ErrAgentSendUnsupported
	}
	return c.send(sendRequest{Prompt: prompt, Instant: instant, ID: id})
}

// SendAgent is Send for a terminal running an ACA, for whom instant says the
// message may interrupt its turn.
func (c *Conn) SendAgent(prompt string, instant bool) error {
	if c.hello.Version < agentInboxVersion {
		return ErrAgentSendUnsupported
	}
	return c.send(sendRequest{Prompt: prompt, Instant: instant})
}

func (c *Conn) send(req sendRequest) error {
	body, err := json.Marshal(req)
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

// Answers reports whether the holder answers its program's prompts.
func (c *Conn) Answers() bool {
	return c.hello.Answers
}

// Answer has the holder answer the prompt on its screen with answers, one per
// question, and returns what came of it, once the holder has pressed what it
// would and seen the prompt close, or stopped. It waits until ctx is done or
// the connection ends: the holder bounds each key's wait itself.
func (c *Conn) Answer(ctx context.Context, answers []Answer) (AnswerReport, error) {
	if !c.hello.Answers {
		return AnswerReport{}, ErrAnswerUnsupported
	}
	req := answerRequest{ID: rand.Text(), Answers: answers}
	body, err := json.Marshal(req)
	if err != nil {
		return AnswerReport{}, err
	}
	if len(body) > maxPayload {
		return AnswerReport{}, ErrAnswerTooLarge
	}
	reported := make(chan AnswerReport, 1)
	c.answersMu.Lock()
	c.answers[req.ID] = reported
	c.answersMu.Unlock()
	defer func() {
		c.answersMu.Lock()
		delete(c.answers, req.ID)
		c.answersMu.Unlock()
	}()
	c.wmu.Lock()
	// Fails only on a closed connection, which the write reports.
	_ = c.c.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	err = writeRaw(c.c, frameAnswer, body)
	c.wmu.Unlock()
	if err != nil {
		return AnswerReport{}, err
	}
	select {
	case report := <-reported:
		return report, nil
	case <-c.done:
		return AnswerReport{}, ErrConnectionLost
	case <-ctx.Done():
		return AnswerReport{}, ctx.Err()
	}
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
