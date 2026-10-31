package style

import (
	"bytes"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
)

func TestActiveRowKeepsItsTint(t *testing.T) {
	t.Parallel()
	s := New(true)
	got := s.ActiveRow("a" + s.Muted("b") + "c")
	want := "\x1b[100ma\x1b[2mb\x1b[0m\x1b[100mc\x1b[0m"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSection(t *testing.T) {
	t.Parallel()
	if got, want := New(true).Section("Agents"), "\x1b[1;30;47mAgents\x1b[0m"; got != want {
		t.Errorf("on: got %q, want %q", got, want)
	}
	if got, want := New(false).Section("Agents"), "\x1b[7mAgents\x1b[0m"; got != want {
		t.Errorf("off: got %q, want %q", got, want)
	}
}

func TestStyles(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		s := New(on)
		for name, f := range map[string]func(string) string{
			"heading": s.Heading, "command": s.Command, "flag": s.Flag, "placeholder": s.Placeholder,
			"success": s.Success, "warning": s.Warning, "failure": s.Failure, "hint": s.Hint,
			"name": s.Name, "muted": s.Muted, "focused": s.Focused, "active row": s.ActiveRow, "bar name": s.BarName, "bar muted": s.BarMuted,
		} {
			got := f("text")
			if Width(got) != len("text") || (got != "text") != on {
				t.Errorf("%s with on=%v: got %q", name, on, got)
			}
			if f("") != "" {
				t.Errorf("%s with on=%v: want empty text left empty", name, on)
			}
		}
	}
	if got := New(true).Success("created"); got != "\x1b[1;32mcreated\x1b[0m" {
		t.Fatalf("got %q", got)
	}
}

func TestBar(t *testing.T) {
	t.Parallel()
	for state, want := range map[StatusBar]string{StatusLive: "30;42", StatusPrefix: "30;43", StatusScrolled: "30;46", StatusFailed: "97;41"} {
		on := New(true)
		if got := on.Bar(" "+on.BarName("api")+" · app ", state); got != "\x1b["+want+"m \x1b[1mapi\x1b[22m · app \x1b[0m" {
			t.Errorf("state %d: got %q", state, got)
		}
		if got := New(false).Bar(" api ", state); got != "\x1b[7m api \x1b[0m" {
			t.Errorf("state %d without color: got %q", state, got)
		}
	}
}

func TestTable(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		s := New(on)
		var table Table
		table.Row(s.Heading("NAME"), s.Heading("STATE"), s.Heading("ORIGIN"))
		table.Row(s.Name("app"), s.Success("open"), "-")
		table.Row(s.Name("wérld"), s.Muted("closed"), "git@x")
		var b bytes.Buffer
		table.Write(&b)
		got := escape.ReplaceAllString(b.String(), "")
		want := "NAME   STATE   ORIGIN\napp    open    -\nwérld  closed  git@x\n"
		if got != want {
			t.Errorf("on=%v: got\n%s\nwant\n%s", on, got, want)
		}
	}
}

func TestBarLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                 string
		width                int
		left, right, version string
		want                 string
	}{
		{"all fit", 30, "left", "keys", "[v]", "left                keys  [v] "},
		{"version goes first", 12, "left", "keys", "[version]", "left   keys "},
		{"no keys: version alone at the end", 20, "left", "", "[v]", "left            [v] "},
		{"cut at the end when nothing fits", 6, "left", "keys", "[v]", "left k"},
	} {
		if got := BarLine(c.width, c.left, c.right, c.version); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBarVersion(t *testing.T) {
	t.Parallel()
	running := identity.Version
	plain := New(false)
	if got, want := plain.BarVersion(""), "[wdx "+running+"]"; got != want {
		t.Errorf("no answer: %q, want %q", got, want)
	}
	if got, want := plain.BarVersion(running), "[wdx "+running+"]"; got != want {
		t.Errorf("the same version: %q, want %q", got, want)
	}
	if got, want := plain.BarVersion("9.9.9"), "[wdx "+running+" · daemon 9.9.9]"; got != want {
		t.Errorf("another version: %q, want %q", got, want)
	}
	if got, want := New(true).BarVersion(""), "\x1b[1m[wdx "+running+"]\x1b[22m"; got != want {
		t.Errorf("colored: %q, want bold %q", got, want)
	}
}
