package holder

import (
	"slices"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/screen"
)

// How many ids of messages a holder remembers. Typed, to tell a daemon that was
// away; seen, to ignore a message sent again. A daemon that was away for more than
// this may have one typed twice, which is the least of what that would mean.
const (
	rememberedDelivered = 1024
	rememberedSeen      = 4096
)

// message is one message in the inbox.
type message struct {
	text    string
	instant bool
	id      string // "" from a sender too old to give one
}

// inbox holds the messages on their way into the shell or the ACA, in the order
// they came, instant ones first. It lives in memory: the shell it serves ends with
// the holder, and the daemon keeps what must outlive it, so this is no record of
// them. See docs/SEND.md.
type inbox struct {
	mu           sync.Mutex
	instantQueue []message
	generalQueue []message
	closed       bool
	arrived      chan struct{} // told when a message comes, for the inboxReader
	seen         map[string]bool
	seenOrder    []string
	delivered    []string // the last ids typed, oldest first
}

func newInbox() *inbox {
	return &inbox{arrived: make(chan struct{}, 1), seen: map[string]bool{}}
}

// addResult is what came of adding a message.
type addResult int

const (
	addQueued addResult = iota
	addRepeated
	addClosed
)

// add queues m, unless the inbox is closed or m has come before: a daemon sends again
// what it was not told of, and goes by a holder dropping what it has.
func (i *inbox) add(m message) addResult {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return addClosed
	}
	if m.id != "" {
		if i.seen[m.id] {
			return addRepeated
		}
		i.seen[m.id] = true
		i.seenOrder = append(i.seenOrder, m.id)
		if len(i.seenOrder) > rememberedSeen {
			delete(i.seen, i.seenOrder[0])
			i.seenOrder = i.seenOrder[1:]
		}
	}
	if m.instant {
		i.instantQueue = append(i.instantQueue, m)
	} else {
		i.generalQueue = append(i.generalQueue, m)
	}
	wake(i.arrived)
	return addQueued
}

// head is the message next in line, left in the inbox; false when there is none.
func (i *inbox) head() (message, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.instantQueue) > 0 {
		return i.instantQueue[0], true
	}
	if len(i.generalQueue) > 0 {
		return i.generalQueue[0], true
	}
	return message{}, false
}

// remove takes out the first message of m's queue, which is m when only one
// goroutine removes: a message arriving since head goes behind it, or is
// instant and stays in line for the next head.
func (i *inbox) remove(m message) {
	i.mu.Lock()
	defer i.mu.Unlock()
	queue := &i.generalQueue
	if m.instant {
		queue = &i.instantQueue
	}
	if len(*queue) > 0 {
		*queue = (*queue)[1:]
	}
}

// typed remembers that the message with id went in.
func (i *inbox) typed(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.delivered = append(i.delivered, id)
	if len(i.delivered) > rememberedDelivered {
		i.delivered = i.delivered[1:]
	}
}

// notTyped forgets a message that could not go in, so that it can be sent again.
func (i *inbox) notTyped(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.seen, id)
}

// typedIDs are the last ids typed.
func (i *inbox) typedIDs() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.delivered)
}

// length is how many messages wait.
func (i *inbox) length() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.instantQueue) + len(i.generalQueue)
}

// close refuses later messages and returns how many were left undelivered.
func (i *inbox) close() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed = true
	left := len(i.instantQueue) + len(i.generalQueue)
	i.instantQueue, i.generalQueue = nil, nil
	return left
}

// queue puts a message in the inbox. It never writes to the PTY: a program
// that stops reading its input must not hold up the daemon's frames.
func (h *holder) queue(m message) {
	switch h.inbox.add(m) {
	case addClosed:
		h.log.Info("message dropped: the shell has ended", "message_id", m.id, "prompt_bytes", len(m.text), "instant", m.instant)
		return
	case addRepeated:
		h.log.Info("message already here", "message_id", m.id)
		return
	}
	h.log.Info("message queued", "message_id", m.id, "prompt_bytes", len(m.text), "instant", m.instant)
	h.queueChanged()
}

// queueChanged says the inbox's length is not what the daemon was last told.
func (h *holder) queueChanged() {
	if h.watch != nil {
		wake(h.watch.statusChanged)
	}
}

// dropMessages closes the inbox once the shell has ended. It is not left to
// the inboxReader, which may still be writing to the PTY when the holder
// quits: the messages would be lost with nothing logged.
func (h *holder) dropMessages() {
	if left := h.inbox.close(); left > 0 {
		h.log.Info("messages dropped: the shell has ended", "messages", left)
	}
}

// action is what the inboxReader does with the message next in line.
type action int

const (
	actionHold action = iota
	actionDeliver
	actionInterrupt
	actionSteer
)

// nextAction is docs/SEND.md's table for a screen that is read. reason says why
// a message is held. steers says the ACA can take an instant message into its turn.
func nextAction(state screen.ScreenState, instant, steers bool) (next action, reason string) {
	switch {
	case state.Draft:
		return actionHold, "draft"
	case state.SteerPending:
		return actionHold, "steer pending"
	case state.Activity == screen.ActivityWaiting:
		return actionHold, "waiting"
	case !state.InputBox:
		// The motion fallback can call this working, as during startup, but a
		// message has nowhere to go.
		return actionHold, "no input box"
	case state.Activity == screen.ActivityUnknown:
		return actionHold, "unknown"
	case instant && steers:
		return actionSteer, ""
	case instant && state.InTurn:
		return actionInterrupt, ""
	}
	return actionDeliver, ""
}

// inboxReader delivers the inbox's messages one at a time, each when the
// screen allows it, until the shell ends and dropMessages has emptied the
// inbox. It is woken by a message or a change of the state, so nothing polls.
func (h *holder) inboxReader() {
	defer close(h.readerDone)
	// Pressed once for each message: a second press could do something else
	// once the turn has stopped.
	interrupted := false
	held := ""
	for {
		m, ok := h.inbox.head()
		if !ok {
			if !h.waitForInbox() {
				return
			}
			continue
		}
		next, reason := actionDeliver, ""
		if h.watch != nil {
			next, reason = nextAction(h.watch.state(), m.instant, h.sendNow != nil)
			if next != actionHold && h.readsLines() {
				next, reason = actionHold, notReadingKeys
			}
		}
		if held == notReadingKeys && reason != notReadingKeys {
			h.log.Info("message no longer held", "reason", held)
			held = ""
		}
		if next == actionInterrupt && interrupted {
			next, reason = actionHold, "interrupted"
		}
		switch next {
		case actionDeliver, actionSteer:
			h.inbox.remove(m)
			h.queueChanged()
			h.deliver(m, next == actionSteer)
			interrupted, held = false, ""
		case actionInterrupt:
			h.interruptTurn()
			interrupted = true
			if !h.waitForInbox() {
				return
			}
		case actionHold:
			if reason != held {
				held = reason
				h.log.Info("message held", "reason", reason, "prompt_bytes", len(m.text), "instant", m.instant)
			}
			wait := h.waitForInbox
			if reason == notReadingKeys {
				wait = h.waitForKeys
			}
			if !wait() {
				return
			}
		}
	}
}

// notReadingKeys holds a message while the program has not yet switched its
// terminal from reading lines to reading keys, though its screen is ready:
// Cursor 2026.11.05 draws its box a little before that, and a message pasted
// then stays in the box, its Enters lost even once it reads keys.
const notReadingKeys = "not reading keys"

// keysCheckTime is how often a message held for notReadingKeys looks again, as
// nothing tells the holder when the terminal's mode changes.
const keysCheckTime = 50 * time.Millisecond

// readsLines says whether the PTY reads input a line at a time. A PTY that
// cannot say is taken to read keys, so the message goes and its write fails.
func (h *holder) readsLines() bool {
	canonical, err := pty.Canonical(h.master)
	return err == nil && canonical
}

// waitForKeys is waitForInbox that also gives up after keysCheckTime.
func (h *holder) waitForKeys() bool {
	timer := time.NewTimer(keysCheckTime)
	defer timer.Stop()
	select {
	case <-h.inbox.arrived:
		return true
	case <-h.watch.stateChanged():
		return true
	case <-timer.C:
		return true
	case <-h.ended:
		return false
	}
}

// waitForInbox waits for a message or a change of the state; false once the
// shell has ended.
func (h *holder) waitForInbox() bool {
	select {
	case <-h.inbox.arrived:
		return true
	case <-h.watch.stateChanged():
		return true
	case <-h.ended:
		return false
	}
}

// reportDelivered tells the daemon the message went in. With none there, or a write
// that fails, the next one's hello says it: typed is remembered first.
func (h *holder) reportDelivered(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return
	}
	if err := h.write(h.conn, frameDelivered, deliveredReport{IDs: []string{id}}); err != nil {
		h.log.Info("delivery not reported", "message_id", id, "err", err)
		// A write cut short leaves the frames out of step. Dialling again, the
		// daemon gets it in the hello.
		h.conn.Close()
	}
}

func (h *holder) interruptTurn() {
	if _, err := (ptyInput{h}).Write(h.interrupt); err != nil {
		h.log.Info("turn not interrupted", "err", err)
		return
	}
	h.log.Info("turn interrupted")
}

// ptyInput writes to the program as a client's input does. The screen watch is
// told, or it would read the echo of a message as the program moving by itself.
type ptyInput struct{ h *holder }

func (i ptyInput) Write(p []byte) (int, error) {
	if i.h.watch != nil {
		i.h.watch.inputSent()
	}
	return i.h.master.Write(p)
}
