package holder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/waittest"
)

// Variables a test sets on a holder for its SendTimes.
const (
	pasteFloorVar   = "TEST_PASTE_FLOOR"
	pasteCeilingVar = "TEST_PASTE_CEILING"
	submitSettleVar = "TEST_SUBMIT_SETTLE"
)

// testSendTimes are a test holder's: short, as most test programs draw no draft,
// unless the test set them.
func testSendTimes() SendTimes {
	times := SendTimes{PasteFloor: 10 * time.Millisecond, PasteCeiling: 100 * time.Millisecond, SubmitSettle: 100 * time.Millisecond}
	for name, d := range map[string]*time.Duration{pasteFloorVar: &times.PasteFloor, pasteCeilingVar: &times.PasteCeiling, submitSettleVar: &times.SubmitSettle} {
		if v, err := time.ParseDuration(os.Getenv(name)); err == nil { //nolint:forbidigo // the test holder's main reads its settings as runHolder does
			*d = v
		}
	}
	return times
}

const testFloor = 10 * time.Millisecond

// sendTimesEnv sets a test holder's times; a zero one is left as testSendTimes has it.
func sendTimesEnv(ceiling, settle time.Duration) []string {
	env := []string{pasteFloorVar + "=" + testFloor.String()}
	if ceiling > 0 {
		env = append(env, pasteCeilingVar+"="+ceiling.String())
	}
	if settle > 0 {
		env = append(env, submitSettleVar+"="+settle.String())
	}
	return env
}

// pasted is what Claude Code's Paste writes, without the Enter that paste adds.
func pasted(message string) string { return strings.TrimSuffix(paste(message), "\r") }

// program runs a program that draws what script says, in the background, and
// records all its input in got, as acaProgram.
func program(script string) string {
	return "( " + script + "\nsleep 1000 ) &\nexec cat > got\n"
}

const (
	atRest = "  ⏸ manual mode on"
	inTurn = "  ⏸ manual mode on · esc to interrupt"
)

func intAttr(t *testing.T, line logLine, key string) int64 {
	t.Helper()
	v, ok := line[key].(float64)
	if !ok {
		t.Fatalf("%s logged without %s: %v", line.msg(), key, line)
	}
	return int64(v)
}

func (f fixture) count(t *testing.T, msg string) int {
	t.Helper()
	n := 0
	for _, line := range f.logged(t) {
		if line.msg() == msg {
			n++
		}
	}
	return n
}

// TestDeliverSubmitsOnceThePasteShows: with a ceiling no test reaches, Enter comes
// because the draft showed, after the floor.
func TestDeliverSubmitsOnceThePasteShows(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000901"
	p := len(pasted("hello"))
	dir, _ := f.watched(t, id, program(fmt.Sprintf(`idle one
got_bytes %d
boxed two hello '%s'
got_bytes %d
idle three`, p, atRest, p+1)), sendTimesEnv(waittest.Limit(t), waittest.Limit(t))...)
	conn := f.dial(t, id)
	waitParsed(t, dir, "one")
	if err := conn.SendAgent("hello", false); err != nil {
		t.Fatal(err)
	}
	waitGot(t, dir, paste("hello"))
	line := f.waitLogged(t, "message delivered")
	shown, submitted := intAttr(t, line, "paste_shown_ms"), intAttr(t, line, "submitted_ms")
	if shown > submitted || submitted < testFloor.Milliseconds() {
		t.Errorf("the paste shown at %d ms and submitted at %d ms, the floor %v", shown, submitted, testFloor)
	}
	if n := f.count(t, "paste not shown"); n != 0 {
		t.Errorf("paste not shown logged %d times", n)
	}
}

// TestDeliverAtTheCeiling: a draft that never shows has Enter pressed at the ceiling,
// logged.
func TestDeliverAtTheCeiling(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000902"
	dir, _ := f.watched(t, id, program("idle one"), sendTimesEnv(50*time.Millisecond, 0)...)
	conn := f.dial(t, id)
	waitParsed(t, dir, "one")
	if err := conn.SendAgent("hello", false); err != nil {
		t.Fatal(err)
	}
	waitGot(t, dir, paste("hello"))
	f.waitLogged(t, "paste not shown")
	if line := f.waitLogged(t, "message delivered"); line["paste_shown_ms"] != nil {
		t.Errorf("a paste not shown logged as shown: %v", line)
	}
}

// TestDeliverSubmitsAgain: a draft left as the paste made it gets Enter once more,
// and the next message goes only once it has gone.
func TestDeliverSubmitsAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000903"
	first, second := len(paste("hello")), len(pasted("next"))
	dir, _ := f.watched(t, id, program(fmt.Sprintf(`idle one
got_bytes %d
boxed two hello '%s'
got_bytes %d
idle three
got_bytes %d
boxed four next '%s'
got_bytes %d
idle five`, first-1, atRest, first+1, first+1+second, atRest, first+second+2)), sendTimesEnv(waittest.Limit(t), 500*time.Millisecond)...)
	conn := f.dial(t, id)
	waitParsed(t, dir, "one")
	for _, message := range []string{"hello", "next"} {
		if err := conn.SendAgent(message, false); err != nil {
			t.Fatal(err)
		}
	}
	waitGot(t, dir, paste("hello")+"\r"+paste("next"))
	if attempt := intAttr(t, f.waitLogged(t, "message submitted again"), "attempt"); attempt != 1 {
		t.Errorf("submitted again at attempt %d", attempt)
	}
	if n := f.count(t, "message submitted again"); n != 1 {
		t.Errorf("submitted again %d times, want once", n)
	}
}

// TestDeliverNotSubmitted: a draft that stays gets Enter again as many times as
// the retries allow, and no more.
func TestDeliverNotSubmitted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000904"
	dir, _ := f.watched(t, id, program(fmt.Sprintf(`idle one
got_bytes %d
boxed two hello '%s'`, len(pasted("hello")), atRest)), sendTimesEnv(waittest.Limit(t), 50*time.Millisecond)...)
	conn := f.dial(t, id)
	waitParsed(t, dir, "one")
	if err := conn.SendAgent("hello", false); err != nil {
		t.Fatal(err)
	}
	if attempts := intAttr(t, f.waitLogged(t, "message not submitted"), "attempts"); attempts != submitRetries+1 {
		t.Errorf("not submitted after %d attempts", attempts)
	}
	waitGot(t, dir, paste("hello")+strings.Repeat("\r", submitRetries))
	if n := f.count(t, "message submitted again"); n != submitRetries {
		t.Errorf("submitted again %d times, want %d", n, submitRetries)
	}
}

// TestDeliverLeavesAnotherDraft: a draft that is not what the paste made, as when
// a person typed, gets nothing more.
func TestDeliverLeavesAnotherDraft(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000905"
	p := len(pasted("hello"))
	dir, _ := f.watched(t, id, program(fmt.Sprintf(`idle one
got_bytes %d
boxed two hello '%s'
got_bytes %d
boxed three 'hello world' '%s'`, p, atRest, p+1, atRest)), sendTimesEnv(waittest.Limit(t), 500*time.Millisecond)...)
	conn := f.dial(t, id)
	waitParsed(t, dir, "one")
	if err := conn.SendAgent("hello", false); err != nil {
		t.Fatal(err)
	}
	f.waitLogged(t, "message left in the box")
	waitGot(t, dir, paste("hello"))
	if n := f.count(t, "message submitted again"); n != 0 {
		t.Errorf("submitted again %d times", n)
	}
}

// TestDeliverSteersOnceSubmitted: SendNow comes once the draft has gone, never with
// the Enter. The program notes how much it had read as it cleared the draft.
func TestDeliverSteersOnceSubmitted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "term-00000906"
	dir, _ := f.watched(t, id, program(fmt.Sprintf(`working one
got_bytes %d
boxed two instant '%s'
wait_for clear
wc -c < got > cleared
working three`, len(pasted("instant")), inTurn)), append(sendTimesEnv(waittest.Limit(t), waittest.Limit(t)), steerVar+"=1")...)
	conn := f.dial(t, id)
	f.waitStates(t, "working")
	if err := conn.SendAgent("instant", true); err != nil {
		t.Fatal(err)
	}
	f.waitLogged(t, "message steered")
	step(t, dir, "clear")
	waitGot(t, dir, paste("instant")+sendNow)
	cleared, err := os.ReadFile(filepath.Join(dir, "cleared"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(cleared)); got != fmt.Sprint(len(paste("instant"))) {
		t.Errorf("the draft cleared after %s bytes, want the paste and Enter, %d", got, len(paste("instant")))
	}
}
