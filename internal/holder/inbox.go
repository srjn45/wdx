package holder

import "sync"

// inbox holds the messages waiting to go into the shell, in the order they
// came. It lives in memory: the shell it serves ends with the holder. See
// docs/SEND.md.
type inbox struct {
	mu           sync.Mutex
	generalQueue []string
	closed       bool
	arrived      chan struct{} // told when a message comes, for the inboxReader
}

func newInbox() *inbox {
	return &inbox{arrived: make(chan struct{}, 1)}
}

// add queues message; false once the inbox is closed.
func (i *inbox) add(message string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return false
	}
	i.generalQueue = append(i.generalQueue, message)
	select {
	case i.arrived <- struct{}{}:
	default: // already told
	}
	return true
}

// next takes the oldest message; false when there is none.
func (i *inbox) next() (string, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.generalQueue) == 0 {
		return "", false
	}
	message := i.generalQueue[0]
	i.generalQueue = i.generalQueue[1:]
	return message, true
}

// close refuses later messages and returns how many were left undelivered.
func (i *inbox) close() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed = true
	left := len(i.generalQueue)
	i.generalQueue = nil
	return left
}

// queue puts a message in the inbox. It never writes to the PTY: a program
// that stops reading its input must not hold up the daemon's frames.
func (h *holder) queue(message string) {
	if !h.inbox.add(message) {
		h.log.Info("message dropped: the shell has ended", "prompt_bytes", len(message))
		return
	}
	h.log.Info("message queued", "prompt_bytes", len(message))
}

// dropMessages closes the inbox once the shell has ended. It is not left to
// the inboxReader, which may still be writing to the PTY when the holder
// quits: the messages would be lost with nothing logged.
func (h *holder) dropMessages() {
	if left := h.inbox.close(); left > 0 {
		h.log.Info("messages dropped: the shell has ended", "messages", left)
	}
}

// inboxReader delivers the inbox's messages one at a time, until the shell
// ends and dropMessages has emptied the inbox.
func (h *holder) inboxReader() {
	defer close(h.readerDone)
	for {
		message, ok := h.inbox.next()
		if !ok {
			select {
			case <-h.inbox.arrived:
				continue
			case <-h.ended:
				return
			}
		}
		h.deliver(message)
	}
}

// deliver puts one message into the shell, followed by the carriage return
// the Enter key sends.
func (h *holder) deliver(message string) {
	if _, err := h.master.Write([]byte(message + "\r")); err != nil {
		h.log.Info("message not delivered", "prompt_bytes", len(message), "err", err)
		return
	}
	h.log.Info("message delivered", "prompt_bytes", len(message))
}
