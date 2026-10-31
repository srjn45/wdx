package screen

import (
	"slices"
	"time"
)

// How a screen is read over time; docs/AGENT_STATE.md has what chose them.
const (
	// ReadMoment is how long to wait after a change before reading the screen,
	// so that it reads a finished redraw.
	ReadMoment = 50 * time.Millisecond
	// StillTime is how long a screen must not move to count as still.
	StillTime = 3 * time.Second
	// EchoTime is how long after input a change of the screen is the input's own.
	EchoTime = 150 * time.Millisecond
)

// MotionRules are the two waits of the motion fallback.
type MotionRules struct {
	Still, Echo time.Duration
}

// Motion decides what a screen is doing when its parser cannot tell, knowing
// nothing of any ACA. A reading the parser is sure of is final. Otherwise a
// screen that moved within the still time is working; one that has been still
// for it is idle if it has an input box and unknown if not.
//
// Input holds the last answer for the still time: its own changes are not
// motion, and hide what the program does meanwhile, so they are not stillness
// either.
//
// It has no clock of its own: the caller gives every time, on one clock that
// does not go back and starts at about 0, and asks Wake when to call Advance.
type Motion struct {
	rules                 MotionRules
	clock                 time.Duration
	lastMotion, lastInput time.Duration
	reading               ScreenState
	answer                Activity
}

// NewMotion returns a Motion that has seen nothing.
func NewMotion(rules MotionRules) *Motion {
	const never = -time.Hour
	return &Motion{rules: rules, lastMotion: never, lastInput: never, reading: ScreenState{Activity: ActivityUnknown}, answer: ActivityUnknown}
}

// Input says input was sent at the given time.
func (m *Motion) Input(at time.Duration) {
	m.Advance(at)
	m.lastInput = max(m.lastInput, at)
	m.decide(at)
}

// Screen gives what the parser read at the given time; moved says the text
// differs from the last read.
func (m *Motion) Screen(at time.Duration, moved bool, reading ScreenState) {
	m.Advance(at)
	if moved && at > m.lastInput+m.rules.Echo {
		m.lastMotion = at
	}
	m.reading = reading
	m.decide(at)
}

// Advance says the time is now at, deciding again at each still time that ran
// out since the last call.
func (m *Motion) Advance(at time.Duration) {
	timers := []time.Duration{m.lastMotion + m.rules.Still, m.lastInput + m.rules.Still}
	slices.Sort(timers)
	for _, timer := range timers {
		if timer > m.clock && timer <= at {
			m.decide(timer)
		}
	}
	m.clock = max(m.clock, at)
}

// Wake is the next time the answer may change with nothing else happening; ok
// is false while the answer does not depend on the time.
func (m *Motion) Wake() (at time.Duration, ok bool) {
	if m.reading.Activity != ActivityUnknown {
		return 0, false
	}
	for _, timer := range []time.Duration{m.lastMotion + m.rules.Still, m.lastInput + m.rules.Still} {
		if timer > m.clock && (!ok || timer < at) {
			at, ok = timer, true
		}
	}
	return at, ok
}

// State is the last reading, with the activity decided from it.
func (m *Motion) State() ScreenState {
	state := m.reading
	state.Activity = m.answer
	return state
}

func (m *Motion) decide(at time.Duration) {
	switch {
	case m.reading.Activity != ActivityUnknown:
		m.answer = m.reading.Activity
	case at < m.lastMotion+m.rules.Still:
		m.answer = ActivityWorking
	case at < m.lastInput+m.rules.Still:
		// keeps the last answer
	case m.reading.InputBox:
		m.answer = ActivityIdle
	default:
		m.answer = ActivityUnknown
	}
}
