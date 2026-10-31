// Holdercheck drives the real claude under a real wdx holder, with no daemon, and
// writes what the holder logged to $REAL_BASE/<name>.log. It uses a little quota.
// See ../README.md.
//
//	holdercheck states|fullscreen|wheeldraft|send <name> <wdx binary> <claude binary> <cols>x<rows> [claude flag]...
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/screen"
)

type run struct {
	home, work, name, id string
	size                 holder.Size
	started              holder.Started
	conn                 *holder.Conn
	ctx                  context.Context
	out                  *os.File
	seen                 int // states already handled by expect
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func (r *run) mark(format string, a ...any) {
	fmt.Fprintf(r.out, "%s MARK %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, a...))
}

type state struct {
	time, activity string
	box, draft     bool
}

func (r *run) logs(msg string) []map[string]any {
	files, _ := filepath.Glob(filepath.Join(identity.LogsDir(r.home), "*.log"))
	sort.Strings(files)
	var out []map[string]any
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(nil, 1<<20)
		for scan.Scan() {
			var line map[string]any
			if json.Unmarshal(scan.Bytes(), &line) == nil && (msg == "" || line["msg"] == msg) {
				out = append(out, line)
			}
		}
		f.Close()
	}
	return out
}

func (r *run) states() []state {
	var out []state
	for _, l := range r.logs("screen state changed") {
		a, _ := l["activity"].(string)
		b, _ := l["input_box"].(bool)
		d, _ := l["draft"].(bool)
		t, _ := l["time"].(string)
		out = append(out, state{t, a, b, d})
	}
	return out
}

// expect waits until a new logged state satisfies ok, or the limit passes.
func (r *run) expect(what string, limit time.Duration, ok func(state) bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		s := r.states()
		for i := r.seen; i < len(s); i++ {
			if ok(s[i]) {
				r.seen = i + 1
				r.mark("reached %s", what)
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.mark("TIMEOUT waiting for %s", what)
	return false
}

func activity(a string) func(state) bool {
	return func(s state) bool { return s.activity == a }
}

// keys attaches at the terminal's size, types and leaves.
func (r *run) keys(text string) {
	s, err := holder.Attach(r.ctx, identity.HolderSocket(r.home, r.id), r.id, r.size, 0)
	check(err)
	defer s.Close()
	_, err = s.Write([]byte(text))
	check(err)
	r.mark("sent %q", text)
}

func (r *run) slow(text string, gap time.Duration) {
	for _, c := range text {
		r.keys(string(c))
		time.Sleep(gap)
	}
}

// snap prints the bottom of the screen a client would see.
func (r *run) snap(what string, n int) {
	lines := r.text()
	from := max(0, len(lines)-n)
	fmt.Fprintf(r.out, "%s SCREEN %s\n", time.Now().Format("15:04:05.000"), what)
	for _, l := range lines[from:] {
		fmt.Fprintf(r.out, "    |%s\n", l)
	}
	r.mark("parser reads %+v", aca.ClaudeCodeScreenParser{}.Parse(lines, nil))
}

func (r *run) text() []string {
	s, err := holder.Attach(r.ctx, identity.HolderSocket(r.home, r.id), r.id, r.size, 0)
	check(err)
	defer s.Close()
	m := screen.New(r.size.Cols, r.size.Rows, nil)
	defer m.Close()
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := s.Read(buf)
			if err != nil {
				return
			}
			mu.Lock()
			m.Write(buf[:n])
			mu.Unlock()
		}
	}()
	time.Sleep(250 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return m.Text()
}

func (r *run) finish() {
	r.keys("\x03")
	time.Sleep(500 * time.Millisecond)
	r.keys("\x03")
	select {
	case <-r.conn.Exited():
	case <-time.After(5 * time.Second):
		check(r.conn.Stop(2 * time.Second))
		select {
		case <-r.conn.Exited():
		case <-time.After(5 * time.Second):
			r.mark("shell did not exit")
		}
	}
	_ = r.conn.Release()
	r.dump()
}

func (r *run) dump() {
	spool := filepath.Join(identity.TerminalDir(r.home, r.id), "spool")
	segs, _ := filepath.Glob(spool + "/*")
	sort.Strings(segs)
	var raw []byte
	for _, s := range segs {
		b, _ := os.ReadFile(s)
		raw = append(raw, b...)
	}
	_ = os.WriteFile(filepath.Join(filepath.Dir(r.work), r.name+".raw"), raw, 0o600)
	fmt.Fprintf(r.out, "RAW %d bytes\n", len(raw))
	for _, q := range []string{"\x1b[?2026h", "\x1b[?2026l", "\x1b[?2026$p", "\x1b[?1049h", "\x1b[>0q", "\x1b[c", "\x1b[>c", "\x1b[6n", "\x1b[?1003h", "\x1b[?1002h", "\x1b[?1000h", "\x1b[?1006h", "\x1b[?1004h", "\x1b]11;?", "\x1b]10;?", "\x1b[>1u", "\x1b[?u", "\x1b[?2004h"} {
		fmt.Fprintf(r.out, "  %q: %d\n", q, strings.Count(string(raw), q))
	}
	for _, s := range r.states() {
		fmt.Fprintf(r.out, "STATE %s activity=%s box=%v draft=%v\n", s.time, s.activity, s.box, s.draft)
	}
	for _, l := range r.logs("screen read") {
		fmt.Fprintf(r.out, "READ %v activity=%v box=%v draft=%v\n", l["time"], l["activity"], l["input_box"], l["draft"])
	}
	for _, msg := range []string{"screen watch started", "screen watch ended", "screen parser panicked", "answers dropped: the program does not read its input"} {
		fmt.Fprintf(r.out, "LOG %q: %d\n", msg, len(r.logs(msg)))
	}
	// Exact session folder of this run's directory, and nothing else.
	home, _ := os.UserHomeDir()
	slug := regexp.MustCompile(`[^A-Za-z0-9-]`).ReplaceAllString(r.work, "-")
	dir := filepath.Join(home, ".claude", "projects", slug)
	if _, err := os.Stat(dir); err == nil {
		fmt.Fprintf(r.out, "REMOVED %s\n", dir)
		check(os.RemoveAll(dir))
	}
}

func start(name, wdx, claude string, cols, rows int, extra ...string) *run {
	base := os.Getenv("REAL_BASE")
	realBase, err0 := filepath.EvalSymlinks(base)
	check(err0)
	r := &run{home: filepath.Join(base, "h-"+name), work: filepath.Join(realBase, "w-"+name), name: name, id: "term-00000001",
		size: holder.Size{Cols: cols, Rows: rows}, ctx: context.Background()}
	check(os.MkdirAll(r.home, 0o700))
	check(os.MkdirAll(r.work, 0o700))
	check(os.WriteFile(identity.ConfigPath(r.home), []byte("log:\n  level: debug\notel:\n  endpoint: \"\"\n"), 0o600))
	check(exec.Command("git", "init", "-q", r.work).Run())
	var err error
	r.out, err = os.Create(filepath.Join(base, name+".log"))
	check(err)
	command := append([]string{"/usr/bin/env", "DISABLE_AUTOUPDATER=1", claude, "--safe-mode", "--model", "haiku", "--permission-mode", "manual"}, extra...)
	r.started, err = holder.Start(r.ctx, holder.StartOptions{Executable: wdx, Home: r.home, ID: r.id, Project: "real", Dir: r.work,
		Shell: "/bin/sh", ACA: "claude_code", Command: command, HistoryLines: 100_000,
		Env: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "DISABLE_AUTOUPDATER=1"}})
	check(err)
	r.mark("holder %d shell %d: %q", r.started.HolderPID, r.started.PID, command)
	check(os.WriteFile(filepath.Join(base, name+".pids"), fmt.Appendf(nil, "%d %d\n", r.started.HolderPID, r.started.PID), 0o600))
	r.conn, err = holder.Dial(r.ctx, identity.HolderSocket(r.home, r.id), r.id)
	check(err)
	go func() { <-r.conn.Done() }()
	return r
}

func (r *run) trust() {
	r.expect("trust prompt", 30*time.Second, activity("waiting"))
	r.snap("trust", 8)
	time.Sleep(time.Second)
	r.keys("\x1b[B\r")
	r.expect("idle at start", 30*time.Second, activity("idle"))
	time.Sleep(2 * time.Second)
	r.snap("idle", 6)
}

func states(r *run) {
	r.trust()
	r.mark("typing a draft")
	r.slow("hello", 200*time.Millisecond)
	r.expect("draft", 5*time.Second, func(s state) bool { return s.draft })
	time.Sleep(4 * time.Second) // past the still time
	r.snap("draft", 6)
	r.keys("\x15")
	r.expect("draft gone", 5*time.Second, func(s state) bool { return !s.draft })
	time.Sleep(2 * time.Second)
	r.snap("cleared", 6)

	r.keys("Run this with Bash, exactly: ping -c 1 127.0.0.1")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("working", 15*time.Second, activity("working"))
	r.expect("permission prompt", 60*time.Second, activity("waiting"))
	time.Sleep(2 * time.Second)
	r.snap("permission", 10)
	r.keys("\x1b")
	r.expect("idle after deny", 15*time.Second, activity("idle"))
	time.Sleep(2 * time.Second)
	r.snap("after esc at permission", 6)

	r.keys("Create the file a.txt containing the word x, with the Write tool.")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("working", 15*time.Second, activity("working"))
	r.expect("file permission prompt", 60*time.Second, activity("waiting"))
	time.Sleep(2 * time.Second)
	r.snap("file permission", 10)
	r.keys("\x1b")
	r.expect("idle after file deny", 15*time.Second, activity("idle"))
	time.Sleep(2 * time.Second)
	r.snap("after esc at file permission", 6)

	r.keys("Use the AskUserQuestion tool to ask me: red or blue? Options Red and Blue.")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("working", 15*time.Second, activity("working"))
	r.expect("decision prompt", 60*time.Second, activity("waiting"))
	time.Sleep(2 * time.Second)
	r.snap("decision", 10)
	r.keys("\x1b")
	r.expect("idle after decision esc", 15*time.Second, activity("idle"))
	time.Sleep(2 * time.Second)
	r.snap("after esc at decision", 6)

	r.keys("Write a 200 word story about a lighthouse. No tools.")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("working", 15*time.Second, activity("working"))
	time.Sleep(1500 * time.Millisecond)
	r.snap("working", 6)
	r.mark("typing during the turn")
	r.slow("abc", 200*time.Millisecond)
	r.expect("draft in a turn", 5*time.Second, func(s state) bool { return s.draft })
	r.snap("draft in a turn", 6)
	r.keys("\x15")
	r.keys("\x1b")
	r.mark("Esc in the turn")
	r.expect("idle after esc in a turn", 15*time.Second, activity("idle"))
	time.Sleep(2 * time.Second)
	r.snap("after esc", 6)
	r.finish()
}

func fullscreen(r *run) {
	r.trust()
	r.keys("Write the numbers 1 to 150, one per line, nothing else. No tools.")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("working", 15*time.Second, activity("working"))
	r.expect("idle after the numbers", 60*time.Second, activity("idle"))
	time.Sleep(3 * time.Second)
	r.snap("after the numbers", 12)

	s, err := holder.Attach(r.ctx, identity.HolderSocket(r.home, r.id), r.id, r.size, 0)
	check(err)
	defer s.Close()
	m := screen.New(r.size.Cols, r.size.Rows, nil)
	defer m.Close()
	var mu sync.Mutex
	var last time.Time
	chunks, size := 0, 0
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := s.Read(buf)
			if err != nil {
				return
			}
			mu.Lock()
			m.Write(buf[:n])
			last = time.Now()
			chunks++
			size += n
			mu.Unlock()
		}
	}()
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	fmt.Fprintf(r.out, "ALT=%v any=%v button=%v normal=%v sgr=%v focus=%v\n", m.AltScreen(), m.Mode(ansi.ModeMouseAnyEvent), m.Mode(ansi.ModeMouseButtonEvent), m.Mode(ansi.ModeMouseNormal), m.Mode(ansi.ModeMouseExtSgr), m.Mode(ansi.ModeFocusEvent))
	mu.Unlock()
	measure := func(what, frame string) {
		mu.Lock()
		chunks, size = 0, 0
		mu.Unlock()
		t0 := time.Now()
		_, err := s.Write([]byte(frame))
		check(err)
		time.Sleep(600 * time.Millisecond)
		mu.Lock()
		text := m.Text()
		parsed := aca.ClaudeCodeScreenParser{}.Parse(text, m.Undimmed())
		took := time.Duration(0)
		if chunks > 0 {
			took = last.Sub(t0)
		}
		fmt.Fprintf(r.out, "%s %s: redraw ended %.1f ms after, %d chunks, %d bytes, parser %+v, bottom %q\n", t0.Format("15:04:05.000"), what, float64(took.Microseconds())/1000, chunks, size, parsed, text[max(0, len(text)-2):])
		mu.Unlock()
	}
	col := r.size.Cols / 2
	row := r.size.Rows / 2
	move := fmt.Sprintf("\x1b[<35;%d;%dM", col, row)
	up := fmt.Sprintf("\x1b[<64;%d;%dM", col, row)
	down := fmt.Sprintf("\x1b[<65;%d;%dM", col, row)
	for i := range 3 {
		measure(fmt.Sprintf("move %d", i), fmt.Sprintf("\x1b[<35;%d;%dM", col+i, row))
	}
	_ = move
	for i := range 12 {
		measure(fmt.Sprintf("wheel up %d", i), up)
	}
	for i := range 4 {
		measure(fmt.Sprintf("wheel down %d", i), down)
	}
	measure("5 wheels up in a frame", strings.Repeat(up, 5))
	mu.Lock()
	text := m.Text()
	fmt.Fprintf(r.out, "SCREEN scrolled\n")
	for _, l := range text[max(0, len(text)-12):] {
		fmt.Fprintf(r.out, "    |%s\n", l)
	}
	mu.Unlock()
	// A busy run of wheels, then the stream of states.
	r.mark("wheel burst")
	for range 20 {
		_, _ = s.Write([]byte(up))
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(4 * time.Second)
	s.Close()
	r.mark("detached")
	r.finish()
}

// wheelDraft: a footer that does not say, a still draft, and a wheel: the answer stays.
func wheelDraft(r *run) {
	r.trust()
	r.keys("Write the numbers 1 to 150, one per line, nothing else. No tools.")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("working", 15*time.Second, activity("working"))
	r.expect("idle after the numbers", 60*time.Second, activity("idle"))
	time.Sleep(4 * time.Second)
	r.keys("draft")
	r.expect("draft", 5*time.Second, func(s state) bool { return s.draft })
	time.Sleep(5 * time.Second)
	r.snap("still draft", 6)
	s, err := holder.Attach(r.ctx, identity.HolderSocket(r.home, r.id), r.id, r.size, 0)
	check(err)
	go func() {
		buf := make([]byte, 65536)
		for {
			if _, err := s.Read(buf); err != nil {
				return
			}
		}
	}()
	col, row := r.size.Cols/2, r.size.Rows/2
	r.mark("mouse moves for 2 s")
	for i := range 40 {
		_, _ = s.Write(fmt.Appendf(nil, "\x1b[<35;%d;%dM", col+i%5, row))
		time.Sleep(50 * time.Millisecond)
	}
	r.mark("wheel up 20 times, 100 ms apart")
	for range 20 {
		_, _ = s.Write(fmt.Appendf(nil, "\x1b[<64;%d;%dM", col, row))
		time.Sleep(100 * time.Millisecond)
	}
	r.mark("wheel over; still time")
	time.Sleep(5 * time.Second)
	r.mark("late redraw check over")
	s.Close()
	r.snap("after wheel", 6)
	r.finish()
}

// send gives Claude messages in each state docs/SEND.md says something about, and
// marks what the holder did with them. The screens say whether Claude took them.
func send(r *run) {
	message := func(text string, instant bool) {
		check(r.conn.SendAgent(text, instant))
		r.mark("queued %q instant %v", text, instant)
	}
	waitDelivered := func(n int, limit time.Duration) {
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			if len(r.logs("message delivered")) >= n {
				r.mark("%d delivered", n)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		r.mark("TIMEOUT waiting for %d delivered, %d so far", n, len(r.logs("message delivered")))
	}
	settle := func(what string) {
		r.expect("idle after "+what, 90*time.Second, activity("idle"))
		time.Sleep(2 * time.Second)
		r.snap(what, 14)
	}
	longTurn := func() {
		r.keys("Write a 300 word story about a lighthouse. No tools.")
		time.Sleep(500 * time.Millisecond)
		r.keys("\r")
		r.expect("working", 15*time.Second, activity("working"))
		time.Sleep(2 * time.Second)
		r.snap("in the turn", 6)
	}

	r.mark("startup: held through the trust prompt")
	message("Reply with only the word STARTUP. No tools.", false)
	r.trust()
	waitDelivered(1, 30*time.Second)
	settle("startup message")

	r.mark("idle, several lines")
	message("Reply with only the word MULTI.\nNo tools.\nThat is all.", false)
	waitDelivered(2, 30*time.Second)
	settle("multi-line message")

	r.mark("general during a turn: the assumption that Claude queues it")
	longTurn()
	message("Reply with only the word BRAVO. No tools.", false)
	waitDelivered(3, 30*time.Second)
	time.Sleep(time.Second)
	r.snap("general just delivered in the turn", 10)
	settle("general during a turn")

	r.mark("instant during a turn")
	longTurn()
	message("Reply with only the word CHARLIE. No tools.", true)
	waitDelivered(4, 30*time.Second)
	time.Sleep(time.Second)
	r.snap("instant just delivered", 10)
	settle("instant during a turn")

	r.mark("general, then instant, in one turn: what Esc does to the queued one")
	longTurn()
	message("Reply with only the word DELTA. No tools.", false)
	waitDelivered(5, 30*time.Second)
	time.Sleep(time.Second)
	message("Reply with only the word ECHO. No tools.", true)
	time.Sleep(3 * time.Second)
	r.snap("after the interrupt with one queued", 12)
	waitDelivered(6, 30*time.Second)
	settle("general then instant")
	if last := r.states(); len(last) > 0 && last[len(last)-1].draft {
		r.mark("a draft is left in the box: clearing it")
		r.keys("\x15")
		settle("draft cleared")
	}

	r.mark("draft: held until it is cleared")
	r.slow("hello", 200*time.Millisecond)
	r.expect("draft", 5*time.Second, func(s state) bool { return s.draft })
	n := len(r.logs("message delivered"))
	message("Reply with only the word FOXTROT. No tools.", false)
	time.Sleep(3 * time.Second)
	r.snap("held behind the draft", 6)
	if got := len(r.logs("message delivered")); got != n {
		r.mark("WRONG: delivered behind a draft")
	}
	r.keys("\x15")
	waitDelivered(n+1, 30*time.Second)
	settle("after the draft")

	r.mark("permission prompt: held, instant too")
	r.keys("Run this with Bash, exactly: ping -c 1 127.0.0.1")
	time.Sleep(500 * time.Millisecond)
	r.keys("\r")
	r.expect("permission prompt", 60*time.Second, activity("waiting"))
	n = len(r.logs("message delivered"))
	message("Reply with only the word GOLF. No tools.", true)
	time.Sleep(3 * time.Second)
	r.snap("held at the prompt", 10)
	if got := len(r.logs("message delivered")); got != n {
		r.mark("WRONG: delivered at a prompt")
	}
	r.keys("\x1b")
	waitDelivered(n+1, 30*time.Second)
	settle("after the prompt")

	for _, l := range r.logs("") {
		if msg, _ := l["msg"].(string); strings.HasPrefix(msg, "message ") || msg == "turn interrupted" || msg == "turn not interrupted" {
			r.mark("log %v %s reason=%v instant=%v", l["time"], msg, l["reason"], l["instant"])
		}
	}
	r.finish()
}

func main() {
	scenario, name, wdx, claude := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	var cols, rows int
	fmt.Sscanf(os.Args[5], "%dx%d", &cols, &rows)
	r := start(name, wdx, claude, cols, rows, os.Args[6:]...)
	defer r.out.Close()
	switch scenario {
	case "states":
		states(r)
	case "fullscreen":
		fullscreen(r)
	case "wheeldraft":
		wheelDraft(r)
	case "send":
		send(r)
	}
}
