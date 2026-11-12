package holder

import (
	"cmp"
	"errors"
	"io"
	"time"
)

// The waits of typing a message into a program whose screen is read, the wdx doc
// SEND_CONFIRMED: a key is pressed when the screen shows its time has come, and
// these bound how long the holder looks.
const (
	// pasteFloor is the least a paste is given to settle before the submit key,
	// and the wait between two keys a program would take as one.
	pasteFloor = 150 * time.Millisecond
	// pasteCeiling is the most a paste is given to show as a draft.
	pasteCeiling = 2 * time.Second
	// submitSettle is how long after the screen last changed a draft still there
	// is taken as the submit lost.
	submitSettle = time.Second
	// submitRetries is how many times the submit key is pressed again.
	submitRetries = 2
)

// SendTimes are those waits, when not the defaults: a test shortens them.
type SendTimes struct {
	PasteFloor, PasteCeiling, SubmitSettle time.Duration
	SubmitRetries                          int
}

func (t SendTimes) orDefaults() SendTimes {
	return SendTimes{
		PasteFloor:    cmp.Or(t.PasteFloor, pasteFloor),
		PasteCeiling:  cmp.Or(t.PasteCeiling, pasteCeiling),
		SubmitSettle:  cmp.Or(t.SubmitSettle, submitSettle),
		SubmitRetries: cmp.Or(t.SubmitRetries, submitRetries),
	}
}

// errEnded is a message cut short by the shell's end.
var errEnded = errors.New("the shell has ended")

// deliver puts one message into the shell or the ACA, or steers it into the ACA's
// running turn: paste, submit once the screen shows the paste, see that the draft
// went, and press the submit key again while the box holds what the paste put
// there and nothing else. The message counts as typed once submitted.
func (h *holder) deliver(m message, steer bool) {
	in := ptyInput{h}
	pastedAt := time.Now()
	if err := h.paste(in, m.text); err != nil {
		h.notDelivered(m, steer, err)
		return
	}
	shown, pasted, ok := h.pasteShown(m, pastedAt)
	if !ok {
		h.notDelivered(m, steer, errEnded)
		return
	}
	if err := h.submit(in, h.settle); err != nil {
		h.notDelivered(m, steer, err)
		return
	}
	done := "message delivered"
	if steer {
		done = "message steered"
	}
	attrs := []any{"message_id", m.id, "prompt_bytes", len(m.text), "instant", m.instant}
	if shown >= 0 {
		attrs = append(attrs, "paste_shown_ms", shown.Milliseconds())
	}
	h.log.Info(done, append(attrs, "submitted_ms", time.Since(pastedAt).Milliseconds())...)
	if m.id != "" {
		h.inbox.typed(m.id)
		h.reportDelivered(m.id)
	}
	// A paste the screen did not show is delivered as before the screen was read:
	// nothing tells what the box would hold.
	if shown >= 0 && !h.submitted(m, pasted) {
		return
	}
	if steer {
		if !h.pause(h.sendTimes.PasteFloor) {
			return
		}
		if err := h.sendNow(in); err != nil {
			h.log.Info("message not steered", "message_id", m.id, "err", err)
		}
	}
}

func (h *holder) notDelivered(m message, steer bool, err error) {
	h.log.Info("message not delivered", "message_id", m.id, "prompt_bytes", len(m.text), "instant", m.instant, "steered", steer, "err", err)
	if m.id != "" {
		h.inbox.notTyped(m.id)
	}
}

// pasteShown waits until the screen reads a draft, at least the paste floor and at
// most its ceiling after the paste, and returns when it first read one and its text,
// what the paste put in the box. shown is negative when no draft showed, as on a
// screen nothing reads; ok is false once the shell has ended.
func (h *holder) pasteShown(m message, pastedAt time.Time) (shown time.Duration, pasted string, ok bool) {
	if h.watch == nil {
		return -1, "", true
	}
	floor := time.NewTimer(h.sendTimes.PasteFloor)
	defer floor.Stop()
	ceiling := time.NewTimer(h.sendTimes.PasteCeiling)
	defer ceiling.Stop()
	shown, floorPassed := -1, false
	for {
		if state := h.watch.state(); state.Draft {
			if shown < 0 {
				shown = time.Since(pastedAt)
			}
			if floorPassed {
				return shown, state.DraftText, true
			}
		}
		select {
		case <-h.watch.stateChanged():
		case <-floor.C:
			floorPassed = true
		case <-ceiling.C:
			h.log.Info("paste not shown", "message_id", m.id, "prompt_bytes", len(m.text), "waited_ms", time.Since(pastedAt).Milliseconds())
			return -1, "", true
		case <-h.ended:
			return -1, "", false
		}
	}
}

// submitted waits until the screen reads no draft, up to the submit settle after
// the draft last changed (a change of any other part of the state, as a Codex
// turn makes several times a second, does not restart it), and presses the
// submit key again while the draft is what the paste put there: a person's text
// is never submitted. It says whether the draft went.
func (h *holder) submitted(m message, pasted string) bool {
	settle := time.NewTimer(h.sendTimes.SubmitSettle)
	defer settle.Stop()
	seen := h.watch.state()
	for attempt := 1; ; {
		if !seen.Draft {
			return true
		}
		select {
		case <-h.watch.stateChanged():
			state := h.watch.state()
			if state.Draft != seen.Draft || state.DraftText != seen.DraftText {
				settle.Reset(h.sendTimes.SubmitSettle)
			}
			seen = state
			continue
		case <-settle.C:
		case <-h.ended:
			return false
		}
		state := h.watch.state()
		switch {
		case !state.Draft:
			return true
		case state.DraftText != pasted:
			h.log.Info("message left in the box", "message_id", m.id, "attempts", attempt)
			return false
		case attempt > h.sendTimes.SubmitRetries:
			h.log.Info("message not submitted", "message_id", m.id, "attempts", attempt)
			return false
		}
		h.log.Info("message submitted again", "message_id", m.id, "attempt", attempt)
		if err := h.submit(ptyInput{h}, h.settle); err != nil {
			h.log.Info("message not submitted", "message_id", m.id, "attempts", attempt, "err", err)
			return false
		}
		attempt++
		settle.Reset(h.sendTimes.SubmitSettle)
	}
}

// settle is the wait an ACA's Submit makes between two keys of its own.
func (h *holder) settle() { h.pause(h.sendTimes.PasteFloor) }

// pause waits d; false once the shell has ended.
func (h *holder) pause(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-h.ended:
		return false
	}
}

// typeText and pressEnter deliver to a shell: the text, then the carriage return
// the Enter key sends.
func typeText(w io.Writer, text string) error {
	_, err := io.WriteString(w, text)
	return err
}

func pressEnter(w io.Writer, _ func()) error {
	_, err := io.WriteString(w, "\r")
	return err
}
