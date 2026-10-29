package termview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/identity"
)

func TestMain(m *testing.M) {
	holdertest.RunIfHolder()
	os.Exit(m.Run())
}

// waitLimit bounds every wait for a holder or its shell.
const waitLimit = 10 * time.Second

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
	// Sockets live under home: a path in t.TempDir can be too long for one.
	home, err := os.MkdirTemp("/tmp", "wdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	shell := filepath.Join(home, "shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const id = "term-0000a001"
	started, err := holder.Start(context.Background(), holder.StartOptions{
		Executable: exe, Home: home, ID: id, Project: "wdx", Dir: t.TempDir(), Shell: shell,
		Env: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-started.PID, syscall.SIGKILL)
		_ = syscall.Kill(started.HolderPID, syscall.SIGKILL)
	})
	s, err := holder.Attach(context.Background(), identity.HolderSocket(home, id), id, holder.Size{Cols: 80, Rows: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	d := &driver{t: t, m: New(s), msgs: make(chan tea.Msg, 16)}
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

// waitFor handles messages until ok holds.
func (d *driver) waitFor(what string, ok func() bool) {
	d.t.Helper()
	deadline := time.After(waitLimit)
	for !ok() {
		select {
		case msg := <-d.msgs:
			if e, isEnd := msg.(EndedMsg); isEnd {
				d.ended = &e
				continue
			}
			d.send(msg)
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

// TestPaneReset feeds the vt output it panics on: the pane starts again
// empty and keeps showing what follows.
func TestPaneReset(t *testing.T) {
	t.Parallel()
	d := attach(t, `echo ready; read x; printf 'before\033[1;30r\033[30;1H\033[S'; sleep 0.2; printf '\033[r\033[Hafter'; exec sleep 60`)
	d.waitText("ready") // attached before the output it fails on
	d.typeText("\n")
	d.waitText("after")
	if d.m.Resets() != 1 || strings.Contains(d.m.Text(), "before") {
		t.Fatalf("want one reset and an empty screen before after, got %d resets:\n%s", d.m.Resets(), d.m.Text())
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
