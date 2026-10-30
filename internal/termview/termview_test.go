package termview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	holdertest.RunIfHolder()
	os.Exit(m.Run())
}

// driver runs a pane as a Bubble Tea program does: commands in goroutines,
// every Update on the test's goroutine.
type driver struct {
	t     *testing.T
	m     *Model
	msgs  chan tea.Msg
	ended *EndedMsg
}

// attach starts a holder whose shell runs script in sh, attaches to it at
// 80×24 and returns the pane on it, driven.
func attach(t *testing.T, script string) *driver {
	t.Helper()
	return startShell(t, script).attach()
}

// shell is a terminal's holder, history on, for a test to attach to.
type shell struct {
	t        *testing.T
	home, id string
}

func startShell(t *testing.T, script string) shell {
	t.Helper()
	// Sockets live under home: a path in t.TempDir can be too long for one.
	home, err := os.MkdirTemp("/tmp", "wdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	path := filepath.Join(home, "shell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sh := shell{t: t, home: home, id: "term-0000a001"}
	started, err := holder.Start(context.Background(), holder.StartOptions{
		Executable: exe, Home: home, ID: sh.id, Project: "wdx", Dir: t.TempDir(), Shell: path,
		Env: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}, HistoryLines: 100_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-started.PID, syscall.SIGKILL)
		_ = syscall.Kill(started.HolderPID, syscall.SIGKILL)
	})
	return sh
}

// fetch reads the terminal's history from its files, as the daemon does.
func (sh shell) fetch(from int64, count int) (History, error) {
	h, err := holder.ReadHistory(holder.HistoryDir(sh.home, sh.id), from, count)
	return History{From: h.From, First: h.First, Lines: h.Lines}, err
}

// waitHistory waits until the terminal's history holds lines lines.
func (sh shell) waitHistory(lines int64) {
	sh.t.Helper()
	deadline := waittest.Deadline(sh.t)
	for {
		h, err := holder.TailHistory(holder.HistoryDir(sh.home, sh.id), 1)
		if err == nil && h.Next >= lines {
			return
		}
		if time.Now().After(deadline) {
			sh.t.Fatalf("history stays at %d lines", h.Next)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (sh shell) attach() *driver {
	sh.t.Helper()
	return sh.attachFetching(sh.fetch)
}

// attachFetching attaches a pane that fetches history with fetch.
func (sh shell) attachFetching(fetch FetchFunc) *driver {
	t := sh.t
	t.Helper()
	s, err := holder.Attach(context.Background(), identity.HolderSocket(sh.home, sh.id), sh.id, holder.Size{Cols: 80, Rows: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	d := &driver{t: t, m: New(s, fetch), msgs: make(chan tea.Msg, 16)}
	t.Cleanup(d.m.Close)
	d.run(d.m.Init())
	return d
}

func (d *driver) run(cmd tea.Cmd) {
	if cmd != nil {
		go func() { d.msgs <- cmd() }()
	}
}

func (d *driver) send(msg tea.Msg) {
	d.run(d.m.Update(msg))
}

// waitFor handles messages until ok holds, checking it after each and
// every 20 ms, for conditions outside the pane.
func (d *driver) waitFor(what string, ok func() bool) {
	d.t.Helper()
	deadline := waittest.After(d.t)
	for !ok() {
		select {
		case msg := <-d.msgs:
			if e, isEnd := msg.(EndedMsg); isEnd {
				d.ended = &e
				continue
			}
			d.send(msg)
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			d.t.Fatalf("no %s on the pane:\n%s", what, d.m.Text())
		}
	}
}

func (d *driver) waitText(s string) {
	d.t.Helper()
	d.waitFor(strings.ReplaceAll(s, "\x1b", "ESC"), func() bool { return strings.Contains(d.m.Text(), s) })
}

// typeText types s, a key per character.
func (d *driver) typeText(s string) {
	for _, r := range s {
		if r == '\n' {
			d.send(tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

const interactive = "PS1='$ ' exec /bin/sh"

func TestPaneShowsAndTypes(t *testing.T) {
	t.Parallel()
	d := attach(t, interactive)
	d.waitText("$")
	d.typeText("echo typed''here\n")
	d.waitText("typedhere")
	if c := d.m.Cursor(); c == nil || c.Y < 1 {
		t.Fatalf("want the cursor below the output, got %+v", c)
	}
}

// TestPaneEncodesForTheProgram types into cat -v after the program asked
// for application cursor keys, bracketed paste and the mouse: each reaches
// it encoded as it asked.
func TestPaneEncodesForTheProgram(t *testing.T) {
	t.Parallel()
	d := attach(t, `printf '\033[?1h\033[?2004h\033[?1000h\033[?1006hready\n'; stty raw -echo; exec cat -v`)
	d.waitText("ready")
	d.send(tea.KeyPressMsg{Code: tea.KeyUp})
	d.waitText("^[OA")
	d.send(tea.PasteMsg{Content: "pasted"})
	d.waitText("^[[200~pasted^[[201~")
	d.send(tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft})
	d.waitText("^[[<0;3;4M")
	d.send(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown})
	d.waitText("^[[<65;1;1M")
}

// TestPaneWheelOnAlternateScreen turns the wheel over a program on the
// alternate screen that did not ask for the mouse: it gets arrow keys.
func TestPaneWheelOnAlternateScreen(t *testing.T) {
	t.Parallel()
	d := attach(t, `printf '\033[?1049hready\n'; stty raw -echo; exec cat -v`)
	d.waitText("ready")
	d.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	d.waitText("^[[B^[[B^[[B")
}

func TestPaneResize(t *testing.T) {
	t.Parallel()
	d := attach(t, interactive)
	d.waitText("$")
	if err := d.m.SetSize(100, 30); err != nil {
		t.Fatal(err)
	}
	if cols, rows := d.m.Size(); cols != 100 || rows != 30 {
		t.Fatalf("got %d×%d", cols, rows)
	}
	d.typeText("stty size\n")
	d.waitText("30 100")
}

// TestPaneFollowsTerminalSize attaches a second client at another size:
// the pane draws the terminal at it, within its area, until a key takes
// the size back.
func TestPaneFollowsTerminalSize(t *testing.T) {
	t.Parallel()
	sh := startShell(t, interactive)
	d := sh.attach()
	d.waitText("$")
	other, err := holder.Attach(context.Background(), identity.HolderSocket(sh.home, sh.id), sh.id, holder.Size{Cols: 100, Rows: 30}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	d.waitText("30 100")
	if cols, rows := d.m.TerminalSize(); cols != 100 || rows != 30 {
		t.Fatalf("the pane draws the terminal at %d×%d", cols, rows)
	}
	if lines := strings.Split(d.m.Content(), "\n"); len(lines) != 24 {
		t.Fatalf("got %d lines in a pane of 24 rows", len(lines))
	}
	d.typeText("stty size\n")
	d.waitText("24 80")
	if cols, rows := d.m.TerminalSize(); cols != 80 || rows != 24 {
		t.Fatalf("a key left the terminal at %d×%d", cols, rows)
	}
}

// TestPaneAnswersQueries: an attached client answers the program's queries,
// which the holder leaves to it; the pane's vt does.
func TestPaneAnswersQueries(t *testing.T) {
	t.Parallel()
	d := attach(t, `stty raw -echo; printf '\033[5;7H\033[6n'; answer=$(dd bs=1 count=6 2>/dev/null | od -An -c | tr -d ' '); printf '\033[10;1Hgot %s\n' "$answer"; exec sleep 60`)
	d.waitText(`got 033[5;7R`)
}

func TestPaneCursorAndTitle(t *testing.T) {
	t.Parallel()
	d := attach(t, `printf '\033]2;my title\a\033[6 qready'; exec sleep 60`)
	d.waitText("ready")
	if d.m.Title() != "my title" {
		t.Fatalf("got title %q", d.m.Title())
	}
	if c := d.m.Cursor(); c == nil || c.Shape != tea.CursorBar || c.Blink {
		t.Fatalf("want a steady bar, got %+v", c)
	}
}

// TestPaneReset feeds the vt output it fails on: the pane starts again
// empty, keeps showing what follows, and marks the gap in what it keeps.
func TestPaneReset(t *testing.T) {
	t.Parallel()
	d := attach(t, `echo ready; read x; printf 'before\033[z\033[2K\rafter'; exec sleep 60`)
	d.waitText("ready") // attached before the output it fails on
	d.m.screen.FailOn('z')
	d.typeText("\n")
	d.waitText("after")
	if d.m.Resets() != 1 || strings.Contains(d.m.Text(), "before") || !slices.Contains(d.m.back.kept, holder.MarkInterrupted) {
		t.Fatalf("want one reset, its gap marked and an empty screen before after, got %d resets, %q kept:\n%s", d.m.Resets(), d.m.back.kept, d.m.Text())
	}
}

func TestPaneEnded(t *testing.T) {
	t.Parallel()
	d := attach(t, "echo ready; read x; exit 5")
	d.waitText("ready")
	d.typeText("\n")
	d.waitFor("end", func() bool { return d.ended != nil })
	if d.ended.Pane != d.m || d.ended.Exit == nil || d.ended.Exit.Code != 5 || d.ended.Err != nil {
		t.Fatalf("got %+v, want the exit code 5", d.ended)
	}
}

// firstLine is the first line the pane shows.
func (d *driver) firstLine() string {
	line, _, _ := strings.Cut(d.m.Text(), "\n")
	return line
}

// TestPaneScrollsWhatItSaw scrolls through lines printed while attached,
// which the pane keeps, and back to the live screen with a key.
func TestPaneScrollsWhatItSaw(t *testing.T) {
	t.Parallel()
	d := attach(t, "echo ready; read x; seq 1 100; "+interactive)
	d.waitText("ready")
	d.typeText("\n")
	d.waitText("100")
	d.send(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if !d.m.Scrolled() || d.m.Cursor() != nil {
		t.Fatal("want PageUp at the shell to scroll back, without the cursor")
	}
	d.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	d.run(d.m.ScrollUp(1000))
	if top, _ := d.m.Position(); top != 0 || d.firstLine() != "ready" {
		t.Fatalf("want the top at line 0, ready, got %d %q", top, d.firstLine())
	}
	d.send(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !d.m.Scrolled() {
		t.Fatal("want PageDown to scroll down a page, still back")
	}
	d.typeText("echo li''ve\n")
	if d.m.Scrolled() {
		t.Fatal("want a key to return to the live screen")
	}
	d.waitText("live")
}

// TestPaneScrollsFetchedHistory attaches after the output scrolled into
// history: scrolling back fetches it a page at a time.
func TestPaneScrollsFetchedHistory(t *testing.T) {
	t.Parallel()
	sh := startShell(t, "seq 1 3000; exec sleep 60")
	sh.waitHistory(2977) // 3,000 lines and the cursor's, on 24 rows
	d := sh.attach()
	d.waitText("3000")
	d.run(d.m.ScrollUp(500))
	d.waitFor("line 2478 at the top", func() bool { return d.firstLine() == "2478" }) // line 2477, from 0
	d.run(d.m.ScrollUp(5000))
	d.waitFor("line 1 at the top", func() bool { return d.firstLine() == "1" })
	if len(d.m.back.pages) > cachedPages {
		t.Fatalf("%d pages cached", len(d.m.back.pages))
	}
}

// TestPaneFetchFails scrolls back into history it cannot fetch: the pane
// says so and tries again on the next scroll, not at once.
func TestPaneFetchFails(t *testing.T) {
	t.Parallel()
	sh := startShell(t, "seq 1 3000; exec sleep 60")
	sh.waitHistory(2977)
	var fetches atomic.Int64
	d := sh.attachFetching(func(int64, int) (History, error) {
		fetches.Add(1)
		return History{}, errors.New("no history")
	})
	d.waitText("3000")
	d.run(d.m.ScrollUp(500))
	d.waitFor("the fetch failing", func() bool { return d.m.FetchErr() != nil })
	// A retry would be a fetch started handling the failure: none is.
	if len(d.m.back.fetching) != 0 || fetches.Load() != 1 {
		t.Fatalf("fetching %v after one scroll fetched %d times", d.m.back.fetching, fetches.Load())
	}
	d.run(d.m.ScrollUp(1))
	d.waitFor("a second fetch", func() bool { return fetches.Load() == 2 })
}

// TestPaneScrollStays prints while the view is scrolled back: it stays on
// the lines it shows.
func TestPaneScrollStays(t *testing.T) {
	t.Parallel()
	d := attach(t, "echo ready; read x; seq 1 100; read x; seq 101 150; "+interactive)
	d.waitText("ready")
	d.typeText("\n")
	d.waitText("100")
	d.run(d.m.ScrollUp(30))
	before := d.m.Text()
	_, lines := d.m.Position()
	if _, err := d.m.session.Write([]byte("\n")); err != nil { // typed elsewhere
		t.Fatal(err)
	}
	d.waitFor("the output", func() bool { _, now := d.m.Position(); return now >= lines+50 })
	if d.m.Text() != before {
		t.Fatalf("the view moved:\n%s\nthen\n%s", before, d.m.Text())
	}
}

// TestPanePageKeysOnAlternateScreen: PageUp goes to a program on the
// alternate screen, as the wheel does as arrows.
func TestPanePageKeysOnAlternateScreen(t *testing.T) {
	t.Parallel()
	d := attach(t, `printf '\033[?1049hready\n'; stty raw -echo; exec cat -v`)
	d.waitText("ready")
	d.send(tea.KeyPressMsg{Code: tea.KeyPgUp})
	d.waitText("^[[5~")
	if d.m.Scrolled() {
		t.Fatal("want no scrolling on the alternate screen")
	}
}

// history shows the history of sh's terminal alone, its last rows lines
// at first, in a pane of 80 × rows.
func (sh shell) history(rows int) *driver {
	sh.t.Helper()
	h, err := holder.TailHistory(holder.HistoryDir(sh.home, sh.id), rows)
	if err != nil {
		sh.t.Fatal(err)
	}
	d := &driver{t: sh.t, m: NewHistory(History{From: h.From, First: h.First, Lines: h.Lines}, 80, rows, sh.fetch), msgs: make(chan tea.Msg, 16)}
	d.run(d.m.Init())
	return d
}

// TestHistoryPane shows a terminal that exited from its history: its last
// screen at the bottom, read-only, scrolling up into fetched pages.
func TestHistoryPane(t *testing.T) {
	t.Parallel()
	sh := startShell(t, "seq 1 3000; echo last; exit 0")
	sh.waitHistory(3001) // what scrolled off and the last screen
	d := sh.history(10)
	if d.m.Scrolled() || d.m.Cursor() != nil || d.firstLine() != "2992" || !strings.HasSuffix(d.m.Text(), "\nlast") {
		t.Fatalf("want 2992 to last at the bottom, not scrolled, got:\n%s", d.m.Text())
	}
	before := d.m.Text()
	d.typeText("x\n")
	d.send(tea.PasteMsg{Content: "y"})
	if d.m.Text() != before {
		t.Fatalf("want keys and pastes to do nothing, got:\n%s", d.m.Text())
	}
	d.send(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if !d.m.Scrolled() {
		t.Fatal("want PageUp to scroll back")
	}
	d.waitFor("a page up, fetched", func() bool { return d.firstLine() == "2983" })
	d.run(d.m.ScrollUp(5000))
	d.waitFor("line 1 at the top", func() bool { return d.firstLine() == "1" })
	d.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	d.run(d.m.ScrollUp(-5000))
	if d.m.Scrolled() || d.m.Text() != before {
		t.Fatalf("want scrolling down to stop at the last screen, got:\n%s", d.m.Text())
	}
	if err := d.m.SetSize(80, 12); err != nil || d.firstLine() != "2990" {
		t.Fatalf("want a taller pane to stay at the bottom, got %q, %v", d.firstLine(), err)
	}
}

// TestHistoryPaneShort: history shorter than the pane shows from the top.
func TestHistoryPaneShort(t *testing.T) {
	t.Parallel()
	sh := startShell(t, "echo one; echo two; exit 0")
	sh.waitHistory(2)
	d := sh.history(10)
	if d.m.Scrolled() || d.firstLine() != "one" || !strings.HasPrefix(d.m.Text(), "one\ntwo\n") {
		t.Fatalf("want one and two at the top, got:\n%s", d.m.Text())
	}
	d.run(d.m.ScrollUp(3))
	if d.m.Scrolled() || d.firstLine() != "one" {
		t.Fatalf("want nothing above to scroll to, got %q", d.firstLine())
	}
}
