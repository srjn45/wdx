package aca_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
)

// motionRules are the two waits of the motion fallback.
type motionRules struct {
	// still is how long the screen must not move before it counts as still.
	still time.Duration
	// echo is how long after input a change of the screen is the input's own.
	echo time.Duration
}

// screenMoment is what a watcher of the screen reads at one moment: the text,
// and what the parser makes of it.
type screenMoment struct {
	at      time.Duration
	text    string
	reading aca.ScreenState
}

type activityChange struct {
	at       time.Duration
	activity aca.Activity
}

// followMotion is what watchScreen would do for screens the parser cannot
// read, as a pure function of timed moments and the times input was sent. It
// knows nothing of any ACA. A reading the parser is sure of is final. Otherwise
// a screen that moved within the still time is working; one that has been still
// for it is idle if it has an input box and unknown if not.
//
// Input holds the last answer for the still time: its own changes are not
// motion, and hide what the program does meanwhile, so they are not stillness
// either.
func followMotion(moments []screenMoment, inputs []time.Duration, rules motionRules) []activityChange {
	const never = -time.Hour
	lastMotion, lastInput := never, never
	text, reading := "", aca.ScreenState{Activity: aca.ActivityUnknown}
	answer := aca.ActivityUnknown
	var changes []activityChange

	decide := func(at time.Duration) {
		next := answer
		switch {
		case reading.Activity != aca.ActivityUnknown:
			next = reading.Activity
		case at < lastMotion+rules.still:
			next = aca.ActivityWorking
		case at < lastInput+rules.still:
			// keeps the last answer
		case reading.InputBox:
			next = aca.ActivityIdle
		default:
			next = aca.ActivityUnknown
		}
		if next != answer || len(changes) == 0 {
			answer = next
			changes = append(changes, activityChange{at, answer})
		}
	}
	// expire answers for the times the still time ran out since the last event.
	var clock time.Duration
	expire := func(now time.Duration) {
		timers := []time.Duration{lastMotion + rules.still, lastInput + rules.still}
		slices.Sort(timers)
		for _, at := range timers {
			if at > clock && at <= now {
				decide(at)
			}
		}
		clock = now
	}

	nextInput := 0
	sent := func(limit time.Duration) {
		for nextInput < len(inputs) && inputs[nextInput] <= limit {
			at := inputs[nextInput]
			expire(at)
			lastInput = at
			decide(at)
			nextInput++
		}
	}
	for _, m := range moments {
		sent(m.at)
		expire(m.at)
		if m.text != text && m.at > lastInput+rules.echo {
			lastMotion = m.at
		}
		text, reading = m.text, m.reading
		decide(m.at)
	}
	sent(time.Hour)
	expire(time.Hour)
	return changes
}

func TestFollowMotion(t *testing.T) {
	t.Parallel()
	const ms = time.Millisecond
	box := aca.ScreenState{Activity: unknown, InputBox: true}
	none := aca.ScreenState{Activity: unknown}
	sure := func(a aca.Activity) aca.ScreenState { return aca.ScreenState{Activity: a} }
	rules := motionRules{still: 1000 * ms, echo: 100 * ms}
	at := func(n int) time.Duration { return time.Duration(n) * ms }
	cases := []struct {
		name    string
		moments []screenMoment
		inputs  []time.Duration
		want    []activityChange
	}{
		{
			name:    "still with a box is idle once the still time has passed",
			moments: []screenMoment{{at(0), "a", box}},
			want:    []activityChange{{at(0), working}, {at(1000), idle}},
		},
		{
			name:    "still with no box is unknown",
			moments: []screenMoment{{at(0), "a", none}},
			want:    []activityChange{{at(0), working}, {at(1000), unknown}},
		},
		{
			name:    "moving is working, and the last change starts the still time",
			moments: []screenMoment{{at(0), "a", box}, {at(800), "b", box}, {at(1600), "c", box}},
			want:    []activityChange{{at(0), working}, {at(2600), idle}},
		},
		{
			name:    "the same text arriving again is not motion",
			moments: []screenMoment{{at(0), "a", box}, {at(800), "a", box}, {at(1600), "a", box}},
			want:    []activityChange{{at(0), working}, {at(1000), idle}},
		},
		{
			name:    "a reading the parser is sure of is final while the screen moves",
			moments: []screenMoment{{at(0), "a", sure(idle)}, {at(100), "b", sure(idle)}, {at(200), "c", sure(waiting)}, {at(300), "d", sure(waiting)}},
			want:    []activityChange{{at(0), idle}, {at(200), waiting}},
		},
		{
			name:    "a reading the parser is sure of is final while the screen is still",
			moments: []screenMoment{{at(0), "a", sure(working)}, {at(9000), "a", sure(working)}},
			want:    []activityChange{{at(0), working}},
		},
		{
			name:    "keystrokes at an idle screen do not move it",
			moments: []screenMoment{{at(0), "a", sure(idle)}, {at(5050), "ab", box}, {at(5250), "abc", box}},
			inputs:  []time.Duration{at(5000), at(5200)},
			want:    []activityChange{{at(0), idle}},
		},
		{
			name:    "typing during work keeps working, and the work moves on",
			moments: []screenMoment{{at(0), "1", sure(working)}, {at(500), "2", sure(working)}, {at(1050), "2x", box}, {at(1500), "3x", box}, {at(2000), "4x", box}},
			inputs:  []time.Duration{at(1000)},
			want:    []activityChange{{at(0), working}, {at(3000), idle}},
		},
		{
			name:    "typing with no pause holds the answer as long as it lasts",
			moments: []screenMoment{{at(0), "1", sure(working)}, {at(550), "1a", box}, {at(1450), "1ab", box}, {at(2350), "1abc", box}},
			inputs:  []time.Duration{at(500), at(1400), at(2300)},
			want:    []activityChange{{at(0), working}, {at(3300), idle}},
		},
		{
			name:    "a late change after input is motion",
			moments: []screenMoment{{at(0), "a", sure(idle)}, {at(5500), "a!", box}},
			inputs:  []time.Duration{at(5000)},
			want:    []activityChange{{at(0), idle}, {at(5500), working}, {at(6500), idle}},
		},
		{
			name:    "a stall longer than the still time reads idle: the fallback's limit",
			moments: []screenMoment{{at(0), "1", box}, {at(500), "2", box}, {at(4000), "3", box}},
			want:    []activityChange{{at(0), working}, {at(1500), idle}, {at(4000), working}, {at(5000), idle}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := followMotion(c.moments, c.inputs, rules); !slices.Equal(got, c.want) {
				t.Errorf("\n got %v\nwant %v", got, c.want)
			}
		})
	}
}

// The fallback's two waits, from the sweep over these recordings, reported in
// docs/AGENT_STATE.md.
var fallbackRules = motionRules{still: 3 * time.Second, echo: 150 * time.Millisecond}

// motionRecording is a real session of Claude Code with its input times in
// raw.timing. turns are its real turns in seconds of the recording, read from
// the session's own transcript, which knows nothing of the screen: each from
// the first request to the API, or the Enter that sent it, to its
// turn_duration entry.
type motionRecording struct {
	cols, rows int
	turns      [][2]float64
}

var motionRecordings = map[string]motionRecording{
	"motion-120x40":        {cols: 120, rows: 40, turns: [][2]float64{{10.084, 16.388}, {29.316, 35.843}, {40.771, 46.514}, {59.513, 67.465}, {78.052, 82.21}, {87.501, 107.267}, {117.319, 123.195}, {135.539, 137.003}, {146.026, 158.131}}},
	"motion-80x24":         {cols: 80, rows: 24, turns: [][2]float64{{5.037, 10.612}, {22.154, 24.07}, {32.609, 36.533}, {47.193, 51.482}, {62.925, 64.98}, {71.185, 90.872}, {102.447, 106.855}, {119.673, 121.58}, {126.521, 139.174}}},
	"motion-60x24":         {cols: 60, rows: 24, turns: [][2]float64{{5.036, 9.124}, {20.345, 21.84}, {26.472, 31.227}, {47.957, 51.594}, {61.547, 63.123}, {67.819, 86.585}, {100.381, 104.661}, {115.96, 117.472}, {122.263, 135.132}}},
	"motion-40x24":         {cols: 40, rows: 24, turns: [][2]float64{{8.047, 14.794}, {25.531, 27.278}, {32.128, 36.906}, {51.846, 56.773}, {66.525, 68.441}, {73.255, 92.01}, {106.061, 109.868}, {119.891, 121.071}, {126.187, 139.307}}},
	"classic-motion-80x24": {cols: 80, rows: 24, turns: [][2]float64{{5.038, 12.719}, {23.643, 25.993}, {30.675, 36.204}, {46.875, 51.864}, {61.937, 64.34}, {70.637, 89.769}, {100.971, 105.621}, {116.646, 119.478}, {124.248, 136.558}}},
	"classic-motion-40x24": {cols: 40, rows: 24, turns: [][2]float64{{7.055, 21.438}, {44.903, 50.416}, {61.436, 73.427}, {99.386, 118.699}, {130.895, 142.519}, {168.674, 180.498}}},
	"motion-reduced-80x24": {cols: 80, rows: 24, turns: [][2]float64{{9.702, 15.049}, {28.038, 29.973}, {37.307, 42.338}, {53.16, 57.26}, {69.938, 71.516}, {76.609, 96.551}, {108.927, 113.204}, {123.997, 126.023}, {132.528, 145.086}}},
	"idle-typing-40x24":    {cols: 40, rows: 24, turns: [][2]float64{{5.935, 12.384}, {16.747, 18.447}, {30.038, 31.459}, {51.533, 56.075}, {72.082, 74.768}, {93.184, 107.816}}},
	// Only its stretch before the first turn is used, so its turns are not needed.
	"draft-120x40":              {cols: 120, rows: 40},
	"classic-idle-typing-40x24": {cols: 40, rows: 24, turns: [][2]float64{{5.932, 12.161}, {29.956, 31.514}, {62.006, 66.255}, {82.538, 85.443}, {103.637, 118.341}}},
}

// followRecording replays a recording through the screen, the parser and the
// fallback, as the holder would.
func followRecording(t *testing.T, dir string, rules motionRules) []activityChange {
	t.Helper()
	moments, inputs := recordedMoments(t, dir)
	return followMotion(moments, inputs, rules)
}

func recordedMoments(t *testing.T, dir string) ([]screenMoment, []time.Duration) {
	t.Helper()
	rec := motionRecordings[dir]
	_, inputs := readRecording(t, dir)
	var moments []screenMoment
	replay(t, claudeCodeRecording{dir: dir, cols: rec.cols, rows: rec.rows}, func(at time.Duration, lines []string) {
		moments = append(moments, screenMoment{at, strings.Join(lines, "\n"), aca.ClaudeCodeScreenParser{}.Parse(lines, nil)})
	})
	return moments, inputs
}

// between is the activities from one time to another, the first being the
// one current at from.
func between(changes []activityChange, from, to time.Duration) []aca.Activity {
	var activities []aca.Activity
	for i, c := range changes {
		switch {
		case c.at > to:
			return activities
		case c.at > from:
			activities = append(activities, c.activity)
		case i+1 == len(changes) || changes[i+1].at > from:
			activities = append(activities, c.activity)
		}
	}
	return activities
}

// motionCases are what really happened in stretches of the recordings. real is
// the truth, from the transcript; flashes is what the fallback says instead,
// where it differs. Those are its known wrong answers, kept as they are.
var motionCases = func() []motionCase {
	const s = time.Second
	return []motionCase{
		// A running turn with a cut-off footer, ...
		{name: "turn, cut off, 80 auto", dir: "motion-80x24", from: 32 * s, to: 40 * s, real: []aca.Activity{idle, working, idle}},
		{name: "turn, cut off, 60", dir: "motion-60x24", from: 26 * s, to: 33 * s, real: []aca.Activity{idle, working, idle}},
		{name: "turn, cut off, 40", dir: "motion-40x24", from: 31 * s, to: 45 * s, real: []aca.Activity{idle, working, idle}},
		{name: "turn, cut off, classic 80", dir: "classic-motion-80x24", from: 30 * s, to: 38 * s, real: []aca.Activity{idle, working, idle}},
		{name: "turn, cut off, classic 40", dir: "classic-motion-40x24", from: 44 * s, to: 52 * s, real: []aca.Activity{idle, working, idle}},
		// ... a subagent running with one: the panel's row decides it, not motion, ...
		{name: "subagent, cut off, 60", dir: "motion-60x24", from: 12 * s, to: 20 * s, real: []aca.Activity{working}},
		{name: "subagent, cut off, 40", dir: "motion-40x24", from: 16 * s, to: 25 * s, real: []aca.Activity{working}},
		// ... typing during a turn, ...
		{name: "typing in a turn, 80", dir: "motion-80x24", from: 81 * s, to: 90 * s, real: []aca.Activity{working}},
		{name: "typing in a turn, 40", dir: "motion-40x24", from: 80 * s, to: 91 * s, real: []aca.Activity{working}},
		{name: "typing in a turn, classic 80", dir: "classic-motion-80x24", from: 75 * s, to: 89 * s, real: []aca.Activity{working}},
		// ... typing at an idle agent and leaving a draft, ...
		{name: "keystrokes at idle, multi-line, 120", dir: "draft-120x40", from: 34 * s, to: 44 * s, real: []aca.Activity{idle}},
		{name: "Ctrl-C's notice goes, then a long draft, 120", dir: "draft-120x40", from: 43900 * time.Millisecond, to: 49900 * time.Millisecond, real: []aca.Activity{idle}, flashes: []aca.Activity{idle, working, idle}},
		{name: "draft at idle, classic 40", dir: "classic-idle-typing-40x24", from: 35 * s, to: 56 * s, real: []aca.Activity{idle}},
		{name: "draft at idle, 40, with tmux's notice going", dir: "idle-typing-40x24", from: 38 * s, to: 48 * s, real: []aca.Activity{idle}, flashes: []aca.Activity{idle, working, idle}},
		// ... the turn ending while the footer cannot be read, ...
		{name: "turn ends, cut off, 40", dir: "motion-40x24", from: 138 * s, to: 143 * s, real: []aca.Activity{working, idle}},
		{name: "turn ends, cut off, classic 40", dir: "classic-motion-40x24", from: 179 * s, to: 184 * s, real: []aca.Activity{working, idle}},
		// ... with text in the box, ...
		{name: "turn ends, draft, 80", dir: "motion-80x24", from: 138 * s, to: 149 * s, real: []aca.Activity{working, idle}},
		{name: "turn ends, draft, 60", dir: "motion-60x24", from: 134 * s, to: 145 * s, real: []aca.Activity{working, idle}},
		{name: "turn ends, draft, classic 80", dir: "classic-motion-80x24", from: 135 * s, to: 146 * s, real: []aca.Activity{working, idle}},
		// ... and what moves on an idle screen that cannot be read.
		{name: "the /tasks hint goes, under a draft, 80", dir: "motion-80x24", from: 145 * s, to: 152 * s, real: []aca.Activity{idle}, flashes: []aca.Activity{idle, working, idle}},
		{name: "the /tasks hint goes, under a draft, classic 80", dir: "classic-motion-80x24", from: 143 * s, to: 150 * s, real: []aca.Activity{idle}, flashes: []aca.Activity{idle, working, idle}},
		{name: "a suggestion fills the box, 40", dir: "motion-40x24", from: 37 * s, to: 45 * s, real: []aca.Activity{idle}, flashes: []aca.Activity{working, idle}},
		{name: "Ctrl+Y's hint goes, classic 40", dir: "classic-idle-typing-40x24", from: 57 * s, to: 61 * s, real: []aca.Activity{idle}, flashes: []aca.Activity{idle, working, idle}},
		{name: "the effort hint goes, 40", dir: "motion-40x24", from: 95500 * time.Millisecond, to: 105 * s, real: []aca.Activity{idle}, flashes: []aca.Activity{idle, working, idle}},
	}
}()

type motionCase struct {
	name     string
	dir      string
	from, to time.Duration
	real     []aca.Activity
	flashes  []aca.Activity
}

// TestMotionFallbackOnRecordings replays each recording once, through the
// screen, the parser and the fallback, and checks its cases and the danger the
// fallback must not bring: idle while a turn really runs, past the moment a
// turn takes to show itself. With reduced motion the spinner does not animate,
// and the one stretch that stops for longer than the still time is the
// fallback's known failure.
func TestMotionFallbackOnRecordings(t *testing.T) {
	t.Parallel()
	const showsItself = 600 * time.Millisecond
	knownIdleInATurn := map[string]int{"motion-reduced-80x24": 1}
	for _, c := range motionCases {
		if _, ok := motionRecordings[c.dir]; !ok {
			t.Errorf("%s: no recording %s", c.name, c.dir)
		}
	}
	for dir, rec := range motionRecordings {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			changes := followRecording(t, dir, fallbackRules)
			for _, c := range motionCases {
				if c.dir != dir {
					continue
				}
				want := c.real
				if c.flashes != nil {
					want = c.flashes
				}
				if got := between(changes, c.from, c.to); !slices.Equal(got, want) {
					t.Errorf("%s, from %s to %s: got %v, want %v", c.name, c.from, c.to, got, want)
				}
			}
			idleInATurn := 0
			for i, c := range changes {
				if c.activity != idle {
					continue
				}
				until := time.Hour
				if i+1 < len(changes) {
					until = changes[i+1].at
				}
				for _, turn := range rec.turns {
					from, to := seconds(turn[0]), seconds(turn[1])
					if start := max(c.at, from+showsItself); start < min(until, to) {
						idleInATurn++
						t.Logf("idle from %s to %s in the turn %s to %s", start, min(until, to), from, to)
					}
				}
			}
			if idleInATurn != knownIdleInATurn[dir] {
				t.Errorf("idle in a turn %d times, want %d", idleInATurn, knownIdleInATurn[dir])
			}
		})
	}
}

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}
