package holder

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/waittest"
)

// parserDirVar names a directory in the environment of a test holder, and gives
// it testParser. stillVar shortens its still time.
const (
	parserDirVar = "TEST_PARSER_DIR"
	stillVar     = "TEST_STILL"
)

// testParser is Claude Code's parser with a test's hold on it: it notes the first
// line of every screen, panics on one with BOOM and waits on one with HOLD until
// the file release exists, having created held.
func testParser(dir string) ParseScreen {
	return func(lines, undimmed []string) screen.ScreenState {
		first := ""
		if len(lines) > 0 {
			first = lines[0]
		}
		if f, err := os.OpenFile(filepath.Join(dir, "parsed"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, first)
			f.Close()
		}
		if strings.Contains(first, "BOOM") {
			panic("BOOM " + first)
		}
		if strings.Contains(first, "HOLD") {
			_ = os.WriteFile(filepath.Join(dir, "held"), nil, 0o600)
			for {
				if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
					break
				}
				<-time.After(10 * time.Millisecond)
			}
		}
		return aca.ClaudeCodeScreenParser{}.Parse(lines, undimmed)
	}
}

// claudeScreens draw real footers of Claude Code, whose parser reads them, under
// first as the screen's first line. wait waits for the file named.
const claudeScreens = `
rule=────────────────
draw() { printf '\033[2J\033[H%s\r\n%s\r\n❯ \r\n%s\r\n%s' "$1" "$rule" "$rule" "$2"; }
idle() { draw "$1" '  ⏸ manual mode on · ? for shortcuts · ← 1 agent'; }
working() { draw "$1" '  ⏸ manual mode on · esc to interrupt · ← 1 agent'; }
waiting() { printf '\033[2J\033[H%s\r\n Do you want to proceed?\r\n ❯ 1. Yes\r\n\r\n Esc to cancel · Tab to amend' "$1"; }
cutoff() { draw "$1" '  ⏸ manual mode on · /tasks to see su…'; }
wait_for() { until [ -f "$1" ]; do sleep 0.01; done; }
stty raw -echo
`

// watched starts a holder whose shell runs claudeScreens and body, its screen
// read by testParser. It returns the parser's directory.
func (f fixture) watched(t *testing.T, id, body string, env ...string) (string, Started) {
	t.Helper()
	dir := t.TempDir()
	o := f.options(id, f.script(t, "cd "+dir+"\n"+claudeScreens+body))
	o.Env = append(append(o.Env, parserDirVar+"="+dir), env...)
	s, err := Start(limited(t), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHolder(s) })
	return dir, s
}

type logLine map[string]any

func (l logLine) msg() string { s, _ := l["msg"].(string); return s }

// logged is every line in the home's logs so far, in order.
func (f fixture) logged(t *testing.T) []logLine {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(f.home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	var lines []logLine
	for _, name := range files {
		file, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		scan := bufio.NewScanner(file)
		scan.Buffer(nil, 1<<20)
		for scan.Scan() {
			var line logLine
			if json.Unmarshal(scan.Bytes(), &line) == nil {
				lines = append(lines, line)
			}
		}
		file.Close()
	}
	return lines
}

// states is the activities of the logged changes.
func (f fixture) states(t *testing.T) []string {
	t.Helper()
	var states []string
	for _, line := range f.logged(t) {
		if line.msg() == "screen state changed" {
			a, _ := line["activity"].(string)
			states = append(states, a)
		}
	}
	return states
}

func (f fixture) waitStates(t *testing.T, want ...string) {
	t.Helper()
	var got []string
	if !waittest.Until(t, func() bool {
		got = f.states(t)
		return len(got) >= len(want)
	}) || !slices.Equal(got, want) {
		t.Fatalf("logged states %v, want %v", got, want)
	}
}

func (f fixture) waitLogged(t *testing.T, msg string) logLine {
	t.Helper()
	var found logLine
	waittest.For(t, "the log line "+msg, func() bool {
		for _, line := range f.logged(t) {
			if line.msg() == msg {
				found = line
				return true
			}
		}
		return false
	})
	return found
}

func step(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitParsed waits until the parser has seen a screen whose first line is first.
func waitParsed(t *testing.T, dir, first string) {
	t.Helper()
	waittest.For(t, "a screen headed "+first+" parsed", func() bool {
		b, _ := os.ReadFile(filepath.Join(dir, "parsed"))
		return slices.Contains(strings.Split(string(b), "\n"), first)
	})
}

// endHolder ends the shell's holder once the shell has exited.
func (f fixture) endHolder(t *testing.T, id string, s Started) {
	t.Helper()
	c := f.dial(t, id)
	waitExit(t, c)
	if err := c.Release(); err != nil {
		t.Fatal(err)
	}
	waitGone(t, s.HolderPID)
}

// TestWatchScreenLogsChanges: the real parser on real footers: a change is logged
// and a repeat is not, with values only, and the watch ends with the terminal.
func TestWatchScreenLogsChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir, s := f.watched(t, "term-00000201", `
idle SECRET1
wait_for repeat
idle SECRET2
wait_for work
working SECRET3
wait_for prompt
waiting SECRET4
wait_for end
`)
	f.waitStates(t, "idle")
	step(t, dir, "repeat")
	waitParsed(t, dir, "SECRET2")
	step(t, dir, "work")
	f.waitStates(t, "idle", "working")
	step(t, dir, "prompt")
	f.waitStates(t, "idle", "working", "waiting")
	step(t, dir, "end")
	f.endHolder(t, "term-00000201", s)
	if got := f.states(t); !slices.Equal(got, []string{"idle", "working", "waiting"}) {
		t.Errorf("logged states %v", got)
	}
	f.waitLogged(t, "screen watch ended")
	for _, line := range f.logged(t) {
		if b, _ := json.Marshal(line); strings.Contains(string(b), "SECRET") {
			t.Errorf("the screen's text in a log line: %s", b)
		}
		if line.msg() == "screen state changed" && (line["input_box"] == nil || line["draft"] == nil) {
			t.Errorf("a change without its values: %v", line)
		}
	}
}

// TestWatchScreenStillTime: a footer that does not say, in a real holder: its
// timer makes a screen that stopped moving idle.
func TestWatchScreenStillTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.watched(t, "term-00000202", "cutoff one\nexec sleep 60\n", stillVar+"=100ms")
	f.waitStates(t, "working", "idle")
}

func TestWatchScreenNotOnAShell(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.start(t, "term-00000203", f.script(t, "printf 'output'; exit 0"))
	f.endHolder(t, "term-00000203", s)
	for _, line := range f.logged(t) {
		if strings.Contains(line.msg(), "screen") && line.msg() != "screen kept in history" {
			t.Errorf("a shell logged %v", line)
		}
	}
	f.waitLogged(t, "holder released")
}

// TestWatchScreenParserPanic: the panic reads unknown, says nothing of the screen,
// and the terminal lives on, as does reading it once the parser recovers.
func TestWatchScreenParserPanic(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir, _ := f.watched(t, "term-00000204", `
idle one
wait_for boom
printf '\033[2J\033[HBOOM SECRET\r\n'
wait_for again
idle two
wait_for echo
dd bs=1 count=5 2>/dev/null
exec sleep 60
`)
	f.waitStates(t, "idle")
	step(t, dir, "boom")
	f.waitStates(t, "idle", "unknown")
	line := f.waitLogged(t, "screen parser panicked")
	if b, _ := json.Marshal(line); strings.Contains(string(b), "SECRET") {
		t.Errorf("the screen's text in the log: %s", b)
	}
	step(t, dir, "again")
	f.waitStates(t, "idle", "unknown", "idle")
	c := f.attach(t, "term-00000204", startSize, 0)
	c.input("alive")
	step(t, dir, "echo")
	c.waitOutput("alive")
}

// TestWatchScreenHeldParser: with the parser held up the shell's output goes on,
// to clients and to the vt, whose lock an attach takes.
func TestWatchScreenHeldParser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir, _ := f.watched(t, "term-00000205", `
idle one
wait_for hold
printf '\033[2J\033[HHOLD\r\n'
wait_for more
printf 'AFTER\r\n'
wait_for release
idle two
exec sleep 60
`)
	f.waitStates(t, "idle")
	step(t, dir, "hold")
	waitFile(t, filepath.Join(dir, "held"))
	step(t, dir, "more")
	c := f.attach(t, "term-00000205", startSize, 0)
	c.waitOutput("AFTER")
	late := f.attach(t, "term-00000205", startSize, 0)
	late.waitOutput("AFTER")
	if got := f.states(t); !slices.Equal(got, []string{"idle"}) {
		t.Errorf("states %v while the parser is held", got)
	}
	step(t, dir, "release")
	waitParsed(t, dir, "two")
}

// TestWatchScreenHungParserLetsTheHolderEnd: a parser that never returns does not
// keep the terminal's end from being told, or the holder from quitting.
func TestWatchScreenHungParserLetsTheHolderEnd(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir, s := f.watched(t, "term-00000206", `
printf '\033[2J\033[HHOLD\r\n'
wait_for end
`)
	waitFile(t, filepath.Join(dir, "held"))
	step(t, dir, "end")
	f.endHolder(t, "term-00000206", s)
	f.waitLogged(t, "screen watch still in its parser on quitting")
}

// TestWatchScreenInput: what a client sends counts as input in the order it came,
// but for mouse movement; a resize is told after all of it.
func TestWatchScreenInput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.watched(t, "term-00000207", "idle one\nexec sleep 60\n")
	f.waitStates(t, "idle")
	c := f.attach(t, "term-00000207", startSize, 0)
	const movement, wheel = "\x1b[<35;10;5M", "\x1b[<64;10;5M"
	for _, frame := range []string{movement, movement + movement, wheel, movement + "x", movement} {
		c.input(frame)
	}
	c.resize(Size{Cols: 90, Rows: 30})
	f.waitLogged(t, "terminal resized")
	inputs := 0
	for _, line := range f.logged(t) {
		if line.msg() == "terminal resized" {
			break
		}
		if line.msg() == "input holds the screen's answer" {
			inputs++
		}
	}
	if inputs != 2 {
		t.Errorf("%d frames counted as input, want the wheel and the frame with a key", inputs)
	}
}

// TestWatchScreenAnswersAreNotInput: the vt's answers go to the program without
// being input to the fallback.
func TestWatchScreenAnswersAreNotInput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir, _ := f.watched(t, "term-00000208", `
printf '\033[5;7H\033[6n'
dd bs=1 count=6 2>/dev/null > answer
idle one
exec sleep 60
`)
	var got []byte
	if !waittest.Until(t, func() bool {
		got, _ = os.ReadFile(filepath.Join(dir, "answer"))
		return string(got) == "\x1b[5;7R"
	}) {
		t.Fatalf("no answer: %q", got)
	}
	f.waitStates(t, "idle")
	for _, line := range f.logged(t) {
		if line.msg() == "input holds the screen's answer" {
			t.Errorf("an answer counted as input: %v", line)
		}
	}
}

func TestIsInput(t *testing.T) {
	t.Parallel()
	const (
		moved   = "\x1b[<35;10;5M"
		dragged = "\x1b[<32;10;5M"
		x10     = "\x1b[MC*%"
	)
	cases := []struct {
		name  string
		frame string
		want  bool
	}{
		{"a key", "a", true},
		{"Esc", "\x1b", true},
		{"an arrow", "\x1b[A", true},
		{"a paste", "\x1b[200~text\x1b[201~", true},
		{"a paste of what looks like movement", "\x1b[200~" + moved + "\x1b[201~", true},
		{"SGR movement", moved, false},
		{"SGR movement with a large place", "\x1b[<35;1000;500M", false},
		{"SGR drag", dragged, false},
		{"several moves", moved + moved + dragged, false},
		{"movement with shift held", "\x1b[<39;10;5M", false},
		{"SGR press", "\x1b[<0;10;5M", true},
		{"SGR release", "\x1b[<0;10;5m", true},
		{"SGR wheel up", "\x1b[<64;10;5M", true},
		{"SGR wheel down", "\x1b[<65;10;5M", true},
		{"movement then a key", moved + "a", true},
		{"a key then movement", "a" + moved, true},
		{"movement then a wheel", moved + "\x1b[<64;10;5M", true},
		{"a click among moves", moved + "\x1b[<0;10;5M" + moved, true},
		{"cut before its end", "\x1b[<35;10;5", true},
		{"cut after its start", "\x1b[<", true},
		{"no number", "\x1b[<;10;5M", true},
		{"too few numbers", "\x1b[<35;10M", true},
		{"X10 movement", x10, false},
		{"X10 movement, large place as UTF-8", "\x1b[MCà%", false},
		{"X10 movement, large place as a byte", "\x1b[MC\xe0%", false},
		{"X10 moves and a key", x10 + x10 + "a", true},
		{"X10 press", "\x1b[M *%", true},
		{"X10 wheel", "\x1b[M`*%", true},
		{"X10 cut", "\x1b[MC*", true},
		{"nothing", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := isInput([]byte(c.frame)); got != c.want {
				t.Errorf("isInput(%q) = %v", c.frame, got)
			}
		})
	}
}

// fakeScreen is a screen and a parser for a watch in a synctest bubble, whose
// clock is fake: there a sleep takes no time, and a time is exact.
type fakeScreen struct {
	lines atomic.Pointer[[]string]
	state atomic.Pointer[screen.ScreenState]
	reads atomic.Int64
	wakes atomic.Int64 // how often the goroutine looked at the clock
	parse func(lines, undimmed []string) screen.ScreenState
	watch *screenWatch
}

const (
	fakeMoment = 10 * time.Millisecond
	fakeStill  = 1000 * time.Millisecond
	fakeEcho   = 100 * time.Millisecond
)

func newFakeScreen(t *testing.T) *fakeScreen {
	t.Helper()
	s := &fakeScreen{}
	s.lines.Store(&[]string{})
	s.state.Store(&screen.ScreenState{Activity: screen.ActivityUnknown})
	s.parse = func(_, _ []string) screen.ScreenState { return *s.state.Load() }
	read := func() ([]string, []string, bool) {
		s.reads.Add(1)
		return *s.lines.Load(), nil, true
	}
	times := watchTimes{moment: fakeMoment, rules: screen.MotionRules{Still: fakeStill, Echo: fakeEcho}}
	s.watch = newScreenWatch(slog.New(slog.DiscardHandler), func(l, u []string) screen.ScreenState { return s.parse(l, u) }, read, times)
	now := s.watch.now
	s.watch.now = func() time.Duration {
		s.wakes.Add(1)
		return now()
	}
	done := make(chan struct{})
	s.watch.start(done)
	t.Cleanup(func() {
		close(done)
		<-s.watch.ended
	})
	return s
}

// draws changes the screen, and waits the moment for it to be read.
func (s *fakeScreen) draws(text string, state screen.ScreenState) {
	s.lines.Store(&[]string{text})
	s.state.Store(&state)
	s.watch.screenChanged()
	synctest.Wait()
	pass(fakeMoment)
}

// pass lets the bubble's time go by, and everything in it finish what that wakes.
func pass(d time.Duration) {
	time.Sleep(d) //nolint:forbidigo // the bubble's clock is fake: this takes no time
	synctest.Wait()
}

func wantActivity(t *testing.T, s *fakeScreen, want screen.Activity) {
	t.Helper()
	if got := s.watch.state().Activity; got != want {
		t.Errorf("activity %s, want %s", got, want)
	}
}

var (
	unreadable   = screen.ScreenState{Activity: screen.ActivityUnknown, InputBox: true}
	readableIdle = screen.ScreenState{Activity: screen.ActivityIdle, InputBox: true}
)

func TestWatchReadsAMomentAfterAChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		for range 3 {
			s.watch.screenChanged()
		}
		pass(fakeMoment - time.Millisecond)
		if got := s.reads.Load(); got != 0 {
			t.Errorf("%d reads before the moment", got)
		}
		pass(time.Millisecond)
		if got := s.reads.Load(); got != 1 {
			t.Errorf("%d reads of three changes in a moment, want 1", got)
		}
		// A screen that never stops is read all the same: the moment limits the
		// rate, and does not wait for quiet.
		for range 100 {
			s.watch.screenChanged()
			pass(time.Millisecond)
		}
		if got := s.reads.Load(); got < 9 || got > 11 {
			t.Errorf("%d reads in 100 moments' tenth, want 10 or so", got)
		}
	})
}

// TestWatchNothingPolls: a sure reading leaves no timer: in an hour of nothing the
// goroutine does not wake.
func TestWatchNothingPolls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		s.draws("a", readableIdle)
		wakes := s.wakes.Load()
		pass(time.Hour)
		if got := s.wakes.Load(); got != wakes {
			t.Errorf("woke %d times with nothing changing", got-wakes)
		}
		wantActivity(t, s, screen.ActivityIdle)
	})
}

func TestWatchStillTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		s.draws("a", unreadable)
		wantActivity(t, s, screen.ActivityWorking)
		pass(fakeStill - time.Millisecond)
		wantActivity(t, s, screen.ActivityWorking)
		pass(time.Millisecond)
		wantActivity(t, s, screen.ActivityIdle)
		wakes := s.wakes.Load()
		pass(time.Hour)
		if got := s.wakes.Load(); got != wakes {
			t.Errorf("woke %d times after the answer stopped depending on time", got-wakes)
		}
	})
}

// TestWatchInputHoldsTheAnswer: a wheel holds the answer, and movement does not.
func TestWatchInputHoldsTheAnswer(t *testing.T) {
	t.Parallel()
	const moved, wheel = "\x1b[<35;10;5M", "\x1b[<64;10;5M"
	cases := []struct {
		name  string
		frame string
		held  bool
	}{
		{"movement", moved, false},
		{"a wheel", wheel, true},
		{"a key", "a", true},
		{"movement with a key", moved + "a", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s := newFakeScreen(t)
				s.draws("a", unreadable) // working, until the still time after it
				pass(fakeStill / 2)
				s.watch.inputFrame([]byte(c.frame))
				pass(fakeStill / 2) // the still time since the screen moved has passed
				want := screen.ActivityIdle
				if c.held {
					want = screen.ActivityWorking
				}
				wantActivity(t, s, want)
				pass(fakeStill / 2)
				wantActivity(t, s, screen.ActivityIdle)
			})
		})
	}
}

// TestWatchEcho: a change just after input is the input's own, and a later one is
// motion.
func TestWatchEcho(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		s.draws("a", readableIdle)
		pass(fakeStill)
		s.watch.inputFrame([]byte("x"))
		s.draws("ax", unreadable)
		wantActivity(t, s, screen.ActivityIdle)
		pass(fakeStill)
		s.watch.inputFrame([]byte("y"))
		pass(fakeEcho + time.Millisecond)
		s.draws("late", unreadable)
		wantActivity(t, s, screen.ActivityWorking)
	})
}

func TestWatchResizeIsInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		s.draws("a", readableIdle)
		pass(fakeStill)
		s.state.Store(&unreadable)
		s.lines.Store(&[]string{"redrawn"})
		reads := s.reads.Load()
		s.watch.resized()
		pass(fakeMoment)
		if got := s.reads.Load(); got != reads+1 {
			t.Errorf("%d reads after a resize, want 1", got-reads)
		}
		wantActivity(t, s, screen.ActivityIdle)
	})
}

func TestWatchParserPanic(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		sure := s.parse
		s.draws("a", readableIdle)
		wantActivity(t, s, screen.ActivityIdle)
		s.parse = func(_, _ []string) screen.ScreenState { panic("no") }
		s.draws("moves, but no one can say what", unreadable)
		wantActivity(t, s, screen.ActivityUnknown)
		pass(time.Hour)
		wantActivity(t, s, screen.ActivityUnknown)
		s.parse = sure
		s.draws("b", readableIdle)
		wantActivity(t, s, screen.ActivityIdle)
	})
}

// TestWatchHeldParserBlocksNobody: telling the watch never blocks, with its
// goroutine stuck in the parser, and it ends once the parser returns.
func TestWatchHeldParserBlocksNobody(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeScreen(t)
		release := make(chan struct{})
		s.parse = func(_, _ []string) screen.ScreenState {
			<-release
			return readableIdle
		}
		s.draws("a", unreadable)
		for range 10 {
			s.watch.screenChanged()
			s.watch.inputFrame([]byte("x"))
			s.watch.resized()
		}
		wantActivity(t, s, screen.ActivityUnknown)
		close(release)
		synctest.Wait()
		wantActivity(t, s, screen.ActivityIdle)
	})
}

func TestWatchOfNothing(t *testing.T) {
	t.Parallel()
	var w *screenWatch
	w.start(nil)
	w.screenChanged()
	w.inputFrame([]byte("x"))
	w.resized()
	w.stop()
}
