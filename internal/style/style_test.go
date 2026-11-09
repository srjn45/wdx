package style

import (
	"bytes"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
)

func TestRowTintsKeptAcrossResets(t *testing.T) {
	t.Parallel()
	s := New(true)
	for name, tc := range map[string]struct {
		paint func(string) string
		tint  string
	}{
		"active": {s.ActiveRow, "\x1b[48;2;54;61;78m"},
		"open":   {s.OpenRow, "\x1b[48;2;43;49;63m"},
		"cursor": {s.CursorRow, "\x1b[48;2;38;79;120m"},
	} {
		got := tc.paint("a" + s.Muted("b") + "c")
		want := tc.tint + "a\x1b[2mb\x1b[0m" + tc.tint + "c\x1b[0m"
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestSection(t *testing.T) {
	t.Parallel()
	if got, want := New(true).Section("Agents", "3 "), "\x1b[48;2;54;61;78;1mAgents\x1b[22;2m3 \x1b[0m"; got != want {
		t.Errorf("on: got %q, want %q", got, want)
	}
	if got, want := New(false).Section("Agents", "3 "), "\x1b[7mAgents3 \x1b[0m"; got != want {
		t.Errorf("off: got %q, want %q", got, want)
	}
}

// A style paints tide in the theme named, the default when no theme has the
// name.
func TestWithTheme(t *testing.T) {
	t.Parallel()
	if names := ThemeNames(); len(names) == 0 || names[0] != "slate" {
		t.Fatalf("themes %q, want slate first, the default", names)
	}
	def := New(true)
	if def.theme.name != "slate" {
		t.Errorf("New's theme %q, want the default", def.theme.name)
	}
	if got := def.WithTheme("slate"); got.theme.name != "slate" || got.Base() != themes[0].base {
		t.Errorf("slate: theme %q, base %v", got.theme.name, got.Base())
	}
	if got := def.WithTheme("nope"); got != def {
		t.Errorf("an unknown theme changed the style: %+v", got)
	}
}

// Selected takes the cursor's tint, and reverse video when off.
func TestSelected(t *testing.T) {
	t.Parallel()
	s := New(true)
	if got, want := s.Selected("row"), s.CursorRow("row"); got != want {
		t.Errorf("on: got %q, want %q", got, want)
	}
	off := New(false)
	if got, want := off.Selected("a"+off.Muted("b")+"c"), "\x1b[7mabc\x1b[0m"; got != want {
		t.Errorf("off: got %q, want %q", got, want)
	}
}

func TestStyles(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		s := New(on)
		for name, f := range map[string]func(string) string{
			"heading": s.Heading, "command": s.Command, "flag": s.Flag, "placeholder": s.Placeholder,
			"success": s.Success, "warning": s.Warning, "failure": s.Failure, "hint": s.Hint, "remote": s.Remote,
			"name": s.Name, "muted": s.Muted, "focused": s.Focused, "active row": s.ActiveRow, "open row": s.OpenRow, "cursor row": s.CursorRow, "bar name": s.BarName, "bar muted": s.BarMuted,
			"code keyword": s.CodeKeyword, "code string": s.CodeString, "code comment": s.CodeComment, "code number": s.CodeNumber, "code type": s.CodeType, "code function": s.CodeFunction,
			"context fine": s.ContextFine, "context warning": s.ContextWarning, "context critical": s.ContextCritical,
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

// TestRemoteBar: every theme paints a remote TIDE's bar in a cyan of its own,
// and reverse video stands for it without color.
func TestRemoteBar(t *testing.T) {
	t.Parallel()
	for _, name := range ThemeNames() {
		on := New(true).WithTheme(name)
		if on.theme.remote == "" {
			t.Errorf("theme %s has no remote bar", name)
		}
		if got := on.Bar(" desktop ", StatusRemote); got != "\x1b["+on.theme.remote+"m desktop \x1b[0m" {
			t.Errorf("theme %s: got %q", name, got)
		}
	}
	if got := New(false).Bar(" desktop ", StatusRemote); got != "\x1b[7m desktop \x1b[0m" {
		t.Errorf("without color: got %q", got)
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

// TestContext: a context size takes the color of the threshold its tokens reached,
// from each one up.
func TestContext(t *testing.T) {
	t.Parallel()
	s := New(true)
	for _, tc := range []struct {
		tokens int64
		want   func(string) string
	}{
		{999, s.ContextFine}, {1000, s.ContextWarning}, {1001, s.ContextWarning},
		{1999, s.ContextWarning}, {2000, s.ContextCritical}, {2001, s.ContextCritical},
	} {
		if got, want := s.Context(tc.tokens, 1000, 2000, "1k"), tc.want("1k"); got != want {
			t.Errorf("%d: %q, want %q", tc.tokens, got, want)
		}
	}
	if got := New(false).Context(2001, 1000, 2000, "2k"); got != "2k" {
		t.Errorf("off: %q", got)
	}
}
