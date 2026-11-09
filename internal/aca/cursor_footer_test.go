package aca_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// cursorCheckout makes a directory as git leaves one, its HEAD alone, which is all the
// status line reads: a repository's root, whose .git is a folder, or a worktree's,
// whose .git is a file naming its gitdir, relative as git writes it.
func cursorCheckout(t *testing.T, dir, kind, head string) {
	t.Helper()
	gitdir := filepath.Join(dir, ".git")
	if kind == "worktree" {
		gitdir = filepath.Join(filepath.Dir(dir), "main", ".git", "worktrees", filepath.Base(dir))
		rel, err := filepath.Rel(dir, gitdir)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+rel+"\n")
	}
	writeFile(t, filepath.Join(gitdir, "HEAD"), head)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cursorStatusWith is the status fixture name with its fields changed, as Cursor
// would give them: Go's JSON escapes a quote or a backslash as JavaScript's does. A
// compact one is on one line; Cursor's own spacing is kept otherwise.
func cursorStatusWith(t *testing.T, name string, compact bool, change func(status map[string]any)) []byte {
	t.Helper()
	var status map[string]any
	if err := json.Unmarshal(cursorStatusInput(t, name), &status); err != nil {
		t.Fatal(err)
	}
	change(status)
	var data []byte
	var err error
	if compact {
		data, err = json.Marshal(status)
	} else {
		data, err = json.MarshalIndent(status, "", "  ")
	}
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(data, []byte(`\u0026`), []byte("&"))
}

// TestCursorStatusLineFooter: the status line prints Cursor's own footer from each
// status of 2026.11.05, pretty or on one line: the model with the share used, or alone
// while the share is null, then the directory, home as ~, and the branch HEAD names in
// a repository's root or a worktree's; a detached HEAD or no repository gives the
// directory alone. The user's own line follows (the wdx doc CURSOR_FOOTER).
func TestCursorStatusLineFooter(t *testing.T) {
	t.Parallel()
	models := map[string]string{
		"before-first-message.json": "Grok 4.5 High",
		"mid-session.json":          "Grok 4.5 High · 4.7%",
		"after-clear.json":          "Grok 4.5 High",
	}
	checkouts := []struct{ kind, head, after string }{
		{"repository", "ref: refs/heads/main\n", " · main"},
		{"worktree", "ref: refs/heads/task/a&b\n", " · task/a&b"},
		{"repository", "0123456789abcdef0123456789abcdef01234567\n", ""},
		{"none", "", ""},
	}
	for file, model := range models {
		for _, checkout := range checkouts {
			for _, compact := range []bool{false, true} {
				t.Run(file+"/"+checkout.kind+"/"+checkout.after, func(t *testing.T) {
					t.Parallel()
					l := cursorLaunch(t)
					dir := filepath.Join(l.UserHome, "w", "repo")
					if checkout.kind == "none" {
						writeFile(t, filepath.Join(dir, "file"), "")
					} else {
						cursorCheckout(t, dir, checkout.kind, checkout.head)
					}
					writeCursorUserSettings(t, l.UserHome, `{"statusLine": {"type": "command", "command": "echo their line"}}`)
					input := cursorStatusWith(t, file, compact, func(status map[string]any) { status["cwd"] = dir })
					want := model + "\n~/w/repo" + checkout.after + "\ntheir line\n"
					if out := cursorStatusLineIn(t, l, dir, input); out != want {
						t.Errorf("status line printed %q, want %q", out, want)
					}
				})
			}
		}
	}
}

// TestCursorStatusLineFooterHome: home is ~ only as the whole of cwd or a folder of
// its path, matched as it is, whatever it holds; an agent anywhere else shows its
// path whole.
func TestCursorStatusLineFooterHome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ home, cwd, want string }{
		{"/h/some.one", "/h/some.one", "~"},
		{"/h/some.one", "/h/some.one/w", "~/w"},
		{"/h/some.one", "/h/someXone/w", "/h/someXone/w"},
		{"/h/some.one", "/h/some.one2", "/h/some.one2"},
		{"/h/it's [a]*$", "/h/it's [a]*$/w", "~/w"},
		{"/h/a", "/work", "/work"},
		{"/", "/work", "/work"},
	} {
		l := cursorLaunch(t)
		l.UserHome = tc.home
		input := cursorStatusWith(t, "mid-session.json", true, func(status map[string]any) { status["cwd"] = tc.cwd })
		if out := cursorStatusLine(t, l, input); out != "Grok 4.5 High · 4.7%\n"+tc.want+"\n" {
			t.Errorf("home %q, cwd %q: status line printed %q, want the directory %q", tc.home, tc.cwd, out, tc.want)
		}
	}
}

// TestCursorStatusLineFooterShare: the share used is rounded to a tenth, as Cursor's
// own footer shows it, carrying through nines; one that is not a plain number is left
// out, the model alone.
func TestCursorStatusLineFooterShare(t *testing.T) {
	t.Parallel()
	if !strings.Contains(string(cursorStatusInput(t, "mid-session.json")), `"used_percentage": 4.7,`) {
		t.Fatal("the fixture's share is not where the test changes it")
	}
	for share, want := range map[string]string{
		"4.7": "4.7%", "4.75": "4.8%", "4.749": "4.7%", "12": "12.0%", "0": "0.0%",
		"0.04": "0.0%", "0.849999": "0.8%", "9.95": "10.0%", "99.96": "100.0%",
		"1e-5": "", "-1": "", `"4.7"`: "",
	} {
		l := cursorLaunch(t)
		input := bytes.Replace(cursorStatusInput(t, "mid-session.json"), []byte(`"used_percentage": 4.7`), []byte(`"used_percentage": `+share), 1)
		line := "Grok 4.5 High"
		if want != "" {
			line += " · " + want
		}
		if out := cursorStatusLine(t, l, input); out != line+"\n/work\n" {
			t.Errorf("share %s: status line printed %q, want %q first", share, out, line)
		}
	}
}

// TestCursorStatusLineFooterUnreadable: a display_name or cwd with a quote or a
// backslash, which sed cannot read, leaves that part out, the rest printed and the
// user's line under it; a line left with nothing is not printed. A status that is no
// JSON prints nothing of wdx's.
func TestCursorStatusLineFooterUnreadable(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`Grok "4.5"`, `C:\work`} {
		for name, tc := range map[string]struct {
			change func(map[string]any)
			want   string
		}{
			"display_name": {func(s map[string]any) { s["model"].(map[string]any)["display_name"] = value }, "4.7%\n/work\n"},
			"cwd":          {func(s map[string]any) { s["cwd"] = value }, "Grok 4.5 High · 4.7%\n"},
			"both":         {func(s map[string]any) { s["model"].(map[string]any)["display_name"] = value; s["cwd"] = value }, "4.7%\n"},
		} {
			for _, compact := range []bool{false, true} {
				l := cursorLaunch(t)
				writeCursorUserSettings(t, l.UserHome, `{"statusLine": {"type": "command", "command": "echo their line"}}`)
				input := cursorStatusWith(t, "mid-session.json", compact, tc.change)
				if out := cursorStatusLine(t, l, input); out != tc.want+"their line\n" {
					t.Errorf("%s %q: status line printed %q, want %q", name, value, out, tc.want+"their line\n")
				}
			}
		}
	}
	l := cursorLaunch(t)
	if out := cursorStatusLine(t, l, []byte("not json\n")); out != "" {
		t.Errorf("not JSON: status line printed %q", out)
	}
	if out := cursorStatusLine(t, l, nil); out != "" {
		t.Errorf("no input: status line printed %q", out)
	}
	if size, _, err := (aca.Cursor{}).ContextSize(l); err == nil {
		t.Errorf("no input read as %+v", size)
	}
}
