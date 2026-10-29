package style

import (
	"bytes"
	"testing"
)

func TestStyles(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		s := New(on)
		for name, f := range map[string]func(string) string{
			"heading": s.Heading, "command": s.Command, "flag": s.Flag, "placeholder": s.Placeholder,
			"success": s.Success, "warning": s.Warning, "failure": s.Failure, "hint": s.Hint,
			"name": s.Name, "muted": s.Muted, "bar name": s.BarName, "bar muted": s.BarMuted,
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
