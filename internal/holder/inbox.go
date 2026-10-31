package holder

import (
	"io"
	"sync"

	"github.com/srjn45/wdx/internal/screen"
)

// message is one message in the inbox.
type message struct {
	text    string
	instant bool
}

// inbox holds the messages waiting to go into the shell or the ACA, in the
// order they came, instant ones first. It lives in memory: the shell it serves
// ends with the holder. See docs/SEND.md.
type inbox struct {
	mu           sync.Mutex
	instantQueue []string
	generalQueue []string
	closed       bool
	arrived      chan struct{} // told when a message comes, for the inboxReader
}

func newInbox() *inbox {
	return &inbox{arrived: make(chan struct{}, 1)}
}

// add queues m; false once the inbox is closed.
func (i *inbox) add(m message) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return false
	}
	if m.instant {
		i.instantQueue = append(i.instantQueue, m.text)
	} else {
		i.generalQueue = append(i.generalQueue, m.text)
	}
	wake(i.arrived)
	return true
}

// head is the message next in line, left in the inbox; false when there is none.
func (i *inbox) head() (message, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.instantQueue) > 0 {
		return message{text: i.instantQueue[0], instant: true}, true
	}
	if len(i.generalQueue) > 0 {
		return message{text: i.generalQueue[0]}, true
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
	if !h.inbox.add(m) {
		h.log.Info("message dropped: the shell has ended", "prompt_bytes", len(m.text), "instant", m.instant)
		return
	}
	h.log.Info("message queued", "prompt_bytes", len(m.text), "instant", m.instant)
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
)

// nextAction is docs/SEND.md's table for a screen that is read. reason says why
// a message is held.
func nextAction(state screen.ScreenState, instant bool) (next action, reason string) {
	switch {
	case state.Draft:
		return actionHold, "draft"
	case state.Activity == screen.ActivityWaiting:
		return actionHold, "waiting"
	case !state.InputBox:
		// The motion fallback can call this working, as during startup, but a
		// message has nowhere to go.
		return actionHold, "no input box"
	case state.Activity == screen.ActivityUnknown:
		return actionHold, "unknown"
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
			next, reason = nextAction(h.watch.state(), m.instant)
		}
		if next == actionInterrupt && interrupted {
			next, reason = actionHold, "interrupted"
		}
		switch next {
		case actionDeliver:
			h.inbox.remove(m)
			h.deliver(m)
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
			if !h.waitForInbox() {
				return
			}
		}
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

// deliver puts one message into the shell or the ACA.
func (h *holder) deliver(m message) {
	if err := h.deliverTo(ptyInput{h}, m.text); err != nil {
		h.log.Info("message not delivered", "prompt_bytes", len(m.text), "instant", m.instant, "err", err)
		return
	}
	h.log.Info("message delivered", "prompt_bytes", len(m.text), "instant", m.instant)
}

// typeMessage delivers to a shell: the text, then the carriage return the
// Enter key sends.
func typeMessage(w io.Writer, text string) error {
	_, err := io.WriteString(w, text+"\r")
	return err
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
