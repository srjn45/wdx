package holder

import (
	"cmp"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"slices"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/srjn45/wdx/internal/screen"
)

// ParseScreen reads what a program is doing from the plain text of its screen's
// lines and the same lines with their dim cells blank. It comes from the cli,
// as the holder knows nothing of any ACA.
type ParseScreen func(lines, undimmed []string) screen.ScreenState

// watchTimes are the waits of watching a screen.
type watchTimes struct {
	// moment is how long after a change the screen is read.
	moment time.Duration
	rules  screen.MotionRules
}

func (t watchTimes) orDefaults() watchTimes {
	return watchTimes{
		moment: cmp.Or(t.moment, screen.ReadMoment),
		rules: screen.MotionRules{
			Still: cmp.Or(t.rules.Still, screen.StillTime),
			Echo:  cmp.Or(t.rules.Echo, screen.EchoTime),
		},
	}
}

// screenWatch keeps the latest state of a screen: what a parser reads, and what
// the motion fallback makes of what it cannot. It runs apart from the vt and the
// PTY, which it never holds up, and each change is logged with its values only:
// the screen's text never leaves the holder. docs/AGENT_STATE.md.
type screenWatch struct {
	log   *slog.Logger
	parse ParseScreen
	// read takes the screen's two views under one hold of its lock; false once
	// the screen is closed.
	read  func() (lines, undimmed []string, ok bool)
	times watchTimes
	// now is the time since the watch began, the clock its fallback goes by.
	now func() time.Duration

	// changed and inputs wake the goroutine, and never block who tells it: a
	// wake pending is enough, as it reads the latest when it runs.
	changed chan struct{}
	inputs  chan struct{}
	// lastInput is when the last input was sent, as time since begun; 0 is none.
	lastInput atomic.Int64
	latest    atomic.Pointer[screen.ScreenState]
	// published is told after latest changes, for the inboxReader.
	published chan struct{}
	panicked  atomic.Bool // a panic in the parser is logged once

	quit  chan struct{} // closed to stop the goroutine
	ended chan struct{} // closed when the goroutine has ended
}

func newScreenWatch(log *slog.Logger, parse ParseScreen, read func() (lines, undimmed []string, ok bool), times watchTimes) *screenWatch {
	w := &screenWatch{
		log: log, parse: parse, read: read, times: times.orDefaults(),
		changed: make(chan struct{}, 1), inputs: make(chan struct{}, 1), published: make(chan struct{}, 1),
		quit: make(chan struct{}), ended: make(chan struct{}),
	}
	begun := time.Now()
	w.now = func() time.Duration { return time.Since(begun) }
	w.latest.Store(&screen.ScreenState{Activity: screen.ActivityUnknown})
	return w
}

// readScreen takes the screen's plain text and its undimmed text, under one hold
// of the lock so they are of the same moment.
func (h *holder) readScreen() (lines, undimmed []string, ok bool) {
	h.screenMu.Lock()
	defer h.screenMu.Unlock()
	if h.screenClosed {
		return nil, nil, false
	}
	return h.screen.Text(), h.screen.Undimmed(), true
}

// start runs the goroutine, until done is closed or stop is called.
func (w *screenWatch) start(done <-chan struct{}) {
	if w == nil {
		return
	}
	go w.watchScreen(done)
}

// stop ends the goroutine. A parser that hangs keeps it from ending, but must
// not keep the holder from quitting.
func (w *screenWatch) stop() {
	if w == nil {
		return
	}
	close(w.quit)
	select {
	case <-w.ended:
	case <-time.After(exitDrain):
		w.log.Warn("screen watch still in its parser on quitting")
	}
}

// screenChanged says the screen changed.
func (w *screenWatch) screenChanged() {
	if w == nil {
		return
	}
	wake(w.changed)
}

// inputFrame says a frame of a client's input is about to go to the program.
func (w *screenWatch) inputFrame(frame []byte) {
	if w == nil || !isInput(frame) {
		return
	}
	w.inputSent()
}

// resized says the program is about to redraw for a new size, as for input, and
// the screen may have changed with it.
func (w *screenWatch) resized() {
	if w == nil {
		return
	}
	w.inputSent()
	wake(w.changed)
}

func (w *screenWatch) inputSent() {
	w.lastInput.Store(int64(max(w.now(), 1)))
	w.log.Debug("input holds the screen's answer")
	wake(w.inputs)
}

func wake(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// stateChanged tells when the state changed; nil, which never tells, when
// nothing reads the screen.
func (w *screenWatch) stateChanged() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.published
}

// state is the latest state read.
func (w *screenWatch) state() screen.ScreenState { return *w.latest.Load() }

// watchScreen waits the moment after a change, reads the screen and parses it
// outside the lock. Its one timer is armed only for a read due or while the
// answer depends on the still time, so nothing polls.
func (w *screenWatch) watchScreen(done <-chan struct{}) {
	defer close(w.ended)
	defer w.log.Info("screen watch ended")
	w.log.Info("screen watch started")
	motion := screen.NewMotion(w.times.rules)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var (
		reading  bool          // a read is due
		readAt   time.Duration // when
		previous []string      // the text last read
		parsed   screen.ScreenState
		seen     int64 // the last input told to motion
		failed   bool  // the parser panicked on the last screen
	)
	for {
		wakeAt, armed := motion.Wake()
		if failed {
			armed = false
		}
		switch {
		case reading && (!armed || readAt < wakeAt):
			timer.Reset(readAt - w.now())
		case armed:
			timer.Reset(wakeAt - w.now())
		default:
			timer.Stop()
		}
		select {
		case <-done:
			return
		case <-w.quit:
			return
		case <-w.changed:
			if !reading {
				reading, readAt = true, w.now()+w.times.moment
			}
		case <-w.inputs:
		case <-timer.C:
		}
		if at := w.lastInput.Load(); at != seen {
			seen = at
			motion.Input(time.Duration(at))
		}
		now := w.now()
		if reading && now >= readAt {
			reading = false
			lines, undimmed, ok := w.read()
			if !ok {
				return
			}
			state, ok := w.parsed(lines, undimmed)
			failed = !ok
			if !failed {
				if state != parsed {
					parsed = state
					w.log.Debug("screen read", "activity", state.Activity, "input_box", state.InputBox, "draft", state.Draft, "in_turn", state.InTurn)
				}
				motion.Screen(now, !slices.Equal(lines, previous), state)
				previous = lines
			}
		} else {
			motion.Advance(now)
		}
		state := screen.ScreenState{Activity: screen.ActivityUnknown}
		if !failed {
			state = motion.State()
		}
		w.publish(state)
	}
}

// parsed runs the parser; a panic in it must not take down the holder, which
// owns the terminal.
func (w *screenWatch) parsed(lines, undimmed []string) (state screen.ScreenState, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
			if !w.panicked.Swap(true) {
				w.log.Error("screen parser panicked", "err", panicError(r), "stack", string(debug.Stack()))
			}
		}
	}()
	return w.parse(lines, undimmed), true
}

// panicError says what a panic was without a value it holds, which may be the
// screen's text; a runtime error's message has none.
func panicError(r any) string {
	if err, ok := r.(runtime.Error); ok {
		return err.Error()
	}
	return fmt.Sprintf("%T", r)
}

func (w *screenWatch) publish(state screen.ScreenState) {
	if state == w.state() {
		return
	}
	w.latest.Store(&state)
	wake(w.published)
	w.log.Info("screen state changed", "activity", state.Activity, "input_box", state.InputBox, "draft", state.Draft, "in_turn", state.InTurn)
}

// isInput reports whether a frame of a client's input is what the fallback
// counts as input: anything but mouse movement. A program that asks for every
// move, as Claude Code's fullscreen mode does, would otherwise have its answer
// held for as long as a mouse crosses its pane.
func isInput(frame []byte) bool {
	for len(frame) > 0 {
		n := mouseMovement(frame)
		if n == 0 {
			return true
		}
		frame = frame[n:]
	}
	return false
}

// mouseMovement is the length of the report of mouse movement p starts with, in
// the SGR or the X10 encoding, the two a vt sends; 0 if it starts with none. A
// report cut at the end of a frame counts as none, which holds the answer for
// a moment rather than reading the keys after it as movement.
func mouseMovement(p []byte) int {
	const esc = "\x1b["
	if len(p) < 3 || string(p[:2]) != esc {
		return 0
	}
	switch p[2] {
	case '<':
		button, n := 0, 3
		for part := range 3 {
			start := n
			value := 0
			for n < len(p) && p[n] >= '0' && p[n] <= '9' && n-start < 6 {
				value = value*10 + int(p[n]-'0')
				n++
			}
			if n == start || n == len(p) {
				return 0
			}
			if part == 0 {
				button = value
			}
			final := byte(';')
			if part == 2 {
				final = 'M'
			}
			if p[n] != final {
				return 0
			}
			n++
		}
		if !isMovement(button) {
			return 0
		}
		return n
	case 'M':
		const x10Offset = 32
		if len(p) < 6 || p[3] < x10Offset || !isMovement(int(p[3]-x10Offset)) {
			return 0
		}
		// The vt sends a large coordinate as UTF-8, a terminal as one byte.
		n := 4
		for range 2 {
			_, size := utf8.DecodeRune(p[n:])
			if size == 0 {
				return 0
			}
			n += size
		}
		return n
	}
	return 0
}

// isMovement reads a report's button code: bit 32 is motion, with or without a
// button held; bit 64 a wheel.
func isMovement(button int) bool {
	return button&32 != 0 && button&64 == 0
}
