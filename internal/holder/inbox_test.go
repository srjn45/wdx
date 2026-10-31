package holder

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestSendTypesInOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got := filepath.Join(f.dir, "got")
	f.start(t, "term-00000301", f.script(t, `read first; read second; echo "$first, $second" > `+got))
	c := f.dial(t, "term-00000301")
	for _, message := range []string{"one two", "three"} {
		if err := c.Send(message); err != nil {
			t.Fatal(err)
		}
	}
	if e := waitExit(t, c); e.Code != 0 {
		t.Fatalf("exit code %d", e.Code)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "one two, three\n" {
		t.Fatalf("the shell read %q, %v", b, err)
	}
}

// TestSendDoesNotHoldUpStop fills the input of a program that does not read
// it: the message cannot be written, and the holder must still take the next
// message and the stop.
func TestSendDoesNotHoldUpStop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ready := filepath.Join(f.dir, "ready")
	// Raw, or the terminal would throw away what does not fit in a line
	// rather than make the writer wait.
	f.start(t, "term-00000302", f.script(t, `stty raw -echo; : > `+ready+`; exec sleep 60`))
	c := f.dial(t, "term-00000302")
	if !waittest.Until(t, func() bool { _, err := os.Stat(ready); return err == nil }) {
		t.Fatal("the shell did not get ready")
	}
	if err := c.Send(strings.Repeat("x", 512<<10)); err != nil {
		t.Fatal(err)
	}
	if err := c.Send("next"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
	waitExit(t, c)
}

func TestSendTooLarge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got := filepath.Join(f.dir, "got")
	f.start(t, "term-00000303", f.script(t, `read line; echo "$line" > `+got))
	c := f.dial(t, "term-00000303")
	if err := c.Send(strings.Repeat("x", maxPayload)); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("want it refused as too large, got %v", err)
	}
	// Refused before anything was written: the connection still works.
	if err := c.Send("fits"); err != nil {
		t.Fatal(err)
	}
	waitExit(t, c)
	if b, err := os.ReadFile(got); err != nil || string(b) != "fits\n" {
		t.Fatalf("the shell read %q, %v", b, err)
	}
}

// TestSendToOlderHolder answers as a holder before the inbox would, which
// would ignore the frame: the message must be refused, not lost.
func TestSendToOlderHolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	socket := filepath.Join(f.home, "holder.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := readFrame(conn); err != nil {
			return
		}
		_ = writeFrame(conn, frameHello, Hello{Version: inboxVersion - 1, TerminalID: "term-00000304"})
		_, _, _ = readFrame(conn) // until the daemon closes
	}()
	c, err := Dial(limited(t), socket, "term-00000304")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Send("hello"); !errors.Is(err, ErrSendUnsupported) {
		t.Fatalf("want it refused as unsupported, got %v", err)
	}
}

// TestSendAgentToOlderHolder: a version 3 holder takes messages, but would type
// one into an ACA with no look at its screen.
func TestSendAgentToOlderHolder(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(newFixture(t).dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := readFrame(conn); err != nil {
			return
		}
		_ = writeFrame(conn, frameHello, Hello{Version: agentInboxVersion - 1, TerminalID: "term-00000305"})
		_, _, _ = readFrame(conn) // until the daemon closes
	}()
	c, err := Dial(limited(t), socket, "term-00000305")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SendAgent("hello", false); !errors.Is(err, ErrAgentSendUnsupported) {
		t.Fatalf("want it refused as unsupported, got %v", err)
	}
}

func TestNextAction(t *testing.T) {
	t.Parallel()
	const (
		idle    = screen.ActivityIdle
		working = screen.ActivityWorking
		waiting = screen.ActivityWaiting
		unknown = screen.ActivityUnknown
	)
	for _, c := range []struct {
		state   screen.ScreenState
		instant bool
		want    action
		reason  string
	}{
		{state: screen.ScreenState{Activity: idle, InputBox: true}, want: actionDeliver},
		{state: screen.ScreenState{Activity: idle, InputBox: true}, instant: true, want: actionDeliver},
		{state: screen.ScreenState{Activity: working, InputBox: true, InTurn: true}, want: actionDeliver},
		{state: screen.ScreenState{Activity: working, InputBox: true, InTurn: true}, instant: true, want: actionInterrupt},
		// Background work, or a footer too cut off to say.
		{state: screen.ScreenState{Activity: working, InputBox: true}, want: actionDeliver},
		{state: screen.ScreenState{Activity: working, InputBox: true}, instant: true, want: actionDeliver},
		{state: screen.ScreenState{Activity: waiting}, want: actionHold, reason: "waiting"},
		{state: screen.ScreenState{Activity: waiting}, instant: true, want: actionHold, reason: "waiting"},
		{state: screen.ScreenState{Activity: unknown, InputBox: true}, want: actionHold, reason: "unknown"},
		{state: screen.ScreenState{Activity: unknown, InputBox: true}, instant: true, want: actionHold, reason: "unknown"},
		// Motion with nothing to type into, as during startup.
		{state: screen.ScreenState{Activity: working}, want: actionHold, reason: "no input box"},
		{state: screen.ScreenState{Activity: working}, instant: true, want: actionHold, reason: "no input box"},
		{state: screen.ScreenState{Activity: unknown}, want: actionHold, reason: "no input box"},
		{state: screen.ScreenState{Activity: idle, InputBox: true, Draft: true}, want: actionHold, reason: "draft"},
		{state: screen.ScreenState{Activity: idle, InputBox: true, Draft: true}, instant: true, want: actionHold, reason: "draft"},
		{state: screen.ScreenState{Activity: working, InputBox: true, InTurn: true, Draft: true}, instant: true, want: actionHold, reason: "draft"},
	} {
		if got, reason := nextAction(c.state, c.instant); got != c.want || reason != c.reason {
			t.Errorf("%+v instant %v: got %v %q, want %v %q", c.state, c.instant, got, reason, c.want, c.reason)
		}
	}
}

func TestInboxOrder(t *testing.T) {
	t.Parallel()
	i := newInbox()
	for _, m := range []message{{text: "general 1"}, {text: "instant 1", instant: true}, {text: "general 2"}, {text: "instant 2", instant: true}} {
		if !i.add(m) {
			t.Fatal("refused")
		}
	}
	var got []string
	for {
		m, ok := i.head()
		if !ok {
			break
		}
		// One arriving between head and remove must not be what is removed.
		if m.text == "instant 1" {
			i.add(message{text: "instant 3", instant: true})
		}
		i.remove(m)
		got = append(got, m.text)
	}
	want := "instant 1,instant 2,instant 3,general 1,general 2"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
	i.add(message{text: "left"})
	if left := i.close(); left != 1 {
		t.Fatalf("%d left", left)
	}
	if i.add(message{text: "late"}) {
		t.Fatal("a closed inbox took a message")
	}
}

// paste is what a message is on Claude Code's input.
func paste(message string) string { return "\x1b[200~" + message + "\x1b[201~\r" }

// acaProgram runs a program that draws first, then what follows each named step,
// and records all its input in the file got of the directory watched returns.
// A background job of a script reads no terminal, so the program that reads is
// the one in front.
func acaProgram(first string, steps ...string) string {
	script := "( " + first + "\n"
	for i := 0; i+1 < len(steps); i += 2 {
		script += "wait_for " + steps[i] + "\n" + steps[i+1] + "\n"
	}
	return script + "sleep 1000 ) &\nexec cat > got\n"
}

func waitGot(t *testing.T, dir, want string) {
	t.Helper()
	var got []byte
	if !waittest.Until(t, func() bool {
		got, _ = os.ReadFile(filepath.Join(dir, "got"))
		return string(got) == want
	}) {
		t.Fatalf("the program read %q, want %q", got, want)
	}
}

// heldFor waits until the holder logged holding a message for reason, and checks
// nothing reached the program.
func (f fixture) heldFor(t *testing.T, dir, reason string) {
	t.Helper()
	f.waitHeld(t, reason)
	if got, _ := os.ReadFile(filepath.Join(dir, "got")); len(got) != 0 {
		t.Fatalf("held, yet the program read %q", got)
	}
}

func TestSendAgentHolds(t *testing.T) {
	t.Parallel()
	for i, c := range []struct {
		reason, screen string
		instant        bool
	}{
		{reason: "waiting", screen: "waiting one"},
		{reason: "waiting", screen: "waiting one", instant: true},
		{reason: "draft", screen: "drafted one"},
		{reason: "draft", screen: "drafted one", instant: true},
		{reason: "no input box", screen: "printf one"},
	} {
		t.Run(fmt.Sprintf("%s instant %v", c.reason, c.instant), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			id := fmt.Sprintf("term-0000031%d", i)
			dir, _ := f.watched(t, id, acaProgram(c.screen, "go", "idle two"))
			conn := f.dial(t, id)
			waitParsed(t, dir, "one")
			if err := conn.SendAgent("hello", c.instant); err != nil {
				t.Fatal(err)
			}
			f.heldFor(t, dir, c.reason)
			step(t, dir, "go")
			waitGot(t, dir, paste("hello"))
		})
	}
}

// TestSendAgentDeliversWithNoInterrupt: nothing but a running turn and --instant
// presses the interrupt key, which got would show.
func TestSendAgentDeliversWithNoInterrupt(t *testing.T) {
	t.Parallel()
	for i, c := range []struct {
		name, screen string
		instant      bool
		env          []string
	}{
		{name: "idle", screen: "idle one"},
		{name: "idle instant", screen: "idle one", instant: true},
		{name: "in a turn", screen: "working one"},
		{name: "background work", screen: "background one"},
		{name: "background work instant", screen: "background one", instant: true},
		{name: "cut-off footer instant", screen: "cutoff one", instant: true, env: []string{stillVar + "=100ms"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			id := fmt.Sprintf("term-0000032%d", i)
			dir, _ := f.watched(t, id, acaProgram(c.screen), c.env...)
			conn := f.dial(t, id)
			if err := conn.SendAgent("line one\nline two", c.instant); err != nil {
				t.Fatal(err)
			}
			waitGot(t, dir, paste("line one\nline two"))
		})
	}
}

// TestSendAgentInstantInterrupts presses the key once however the screen changes
// and whatever arrives meanwhile, delivers once the turn has stopped, and has the
// general message wait behind.
func TestSendAgentInstantInterrupts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000330"
	dir, _ := f.watched(t, id, acaProgram("working one", "still", "working two", "stopped", "idle three"))
	conn := f.dial(t, id)
	f.waitStates(t, "working")
	if err := conn.SendAgent("instant", true); err != nil {
		t.Fatal(err)
	}
	waitGot(t, dir, "\x1b")
	if err := conn.SendAgent("general", false); err != nil {
		t.Fatal(err)
	}
	f.waitHeld(t, "interrupted")
	step(t, dir, "still")
	waitParsed(t, dir, "two")
	step(t, dir, "stopped")
	waitGot(t, dir, "\x1b"+paste("instant")+paste("general"))
}

// TestSendAgentInstantGoesFirst queues both while a prompt is open.
func TestSendAgentInstantGoesFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000331"
	dir, _ := f.watched(t, id, acaProgram("waiting one", "go", "idle two"))
	conn := f.dial(t, id)
	f.waitStates(t, "waiting")
	if err := conn.SendAgent("general", false); err != nil {
		t.Fatal(err)
	}
	f.heldFor(t, dir, "waiting")
	if err := conn.SendAgent("instant", true); err != nil {
		t.Fatal(err)
	}
	f.waitLoggedCount(t, "message queued", 2)
	step(t, dir, "go")
	waitGot(t, dir, paste("instant")+paste("general"))
}

// TestSendAgentCountsAsInput: otherwise the fallback reads a message's echo as
// the program moving.
func TestSendAgentCountsAsInput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000332"
	dir, _ := f.watched(t, id, acaProgram("idle one"))
	conn := f.dial(t, id)
	f.waitStates(t, "idle")
	if err := conn.SendAgent("hello", false); err != nil {
		t.Fatal(err)
	}
	waitGot(t, dir, paste("hello"))
	f.waitLogged(t, "message delivered")
	inputs := 0
	for _, line := range f.logged(t) {
		if line.msg() == "input holds the screen's answer" {
			inputs++
		}
	}
	if inputs < 2 {
		t.Fatalf("%d inputs told to the watch, want the paste and the Enter", inputs)
	}
}

// waitHeld waits until the holder logged holding a message for reason.
func (f fixture) waitHeld(t *testing.T, reason string) {
	t.Helper()
	waittest.For(t, "a message held for "+reason, func() bool {
		for _, line := range f.logged(t) {
			if line.msg() == "message held" && line["reason"] == reason {
				return true
			}
		}
		return false
	})
}

func (f fixture) waitLoggedCount(t *testing.T, msg string, want int) {
	t.Helper()
	waittest.For(t, fmt.Sprintf("%d log lines %s", want, msg), func() bool {
		n := 0
		for _, line := range f.logged(t) {
			if line.msg() == msg {
				n++
			}
		}
		return n >= want
	})
}
