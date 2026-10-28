package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/identity"
)

func TestCompletionScripts(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out := wdx(t, t.TempDir(), "completion", shell, "--color", "always")
		if out.exit != api.ExitOK || !strings.Contains(out.stdout, "__complete") || strings.Contains(out.stdout, esc) {
			t.Fatalf("%s: want an uncolored script, got exit %d:\n%.300s", shell, out.exit, out.stdout)
		}
		var v completionScript
		o := wdx(t, t.TempDir(), "completion", shell, "--json")
		if err := json.Unmarshal([]byte(o.stdout), &v); err != nil || v.Shell != shell || v.Script != out.stdout {
			t.Fatalf("%s: unexpected JSON %.300s: %v", shell, o.stdout, err)
		}
	}
}

// complete runs a shell's request for completions of args in home and
// returns the candidates offered, without the directive.
func complete(t *testing.T, home string, args ...string) []string {
	t.Helper()
	out := wdx(t, home, append([]string{"__complete"}, args...)...)
	lines := strings.Split(strings.TrimSuffix(out.stdout, "\n"), "\n")
	if out.exit != api.ExitOK || len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], ":") {
		t.Fatalf("%v: unexpected completion %+v", args, out)
	}
	return lines[:len(lines)-1]
}

func TestCompleteProjects(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	appID := newProject(t, home, "app")
	betaID := newProject(t, home, "beta")
	if _, exit := wdxJSON(t, home, "project", "close", "--name", "beta"); exit != api.ExitOK {
		t.Fatal("close beta")
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"project", "get", "--name", ""}, []string{"app\topen, " + appID, "beta\tclosed, " + betaID}},
		{[]string{"project", "remove", "--id", ""}, []string{appID + "\tapp", betaID + "\tbeta"}},
		{[]string{"project", "close", "--name", ""}, []string{"app\topen, " + appID}},
		{[]string{"project", "open", "--name", ""}, nil},
	} {
		if got := complete(t, home, tc.args...); !slices.Equal(got, tc.want) {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestCompleteWithoutDaemon(t *testing.T) {
	t.Parallel()
	if got := complete(t, t.TempDir(), "project", "get", "--name", ""); len(got) != 0 {
		t.Fatalf("want nothing offered, got %q", got)
	}
}

func TestCompleteStatic(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"--color", ""}, []string{"auto", "always", "never"}},
		{[]string{"project", "c"}, []string{"close\tClose a project", "create\tCreate a project in a new git repository"}},
		{[]string{"help", "s"}, []string{"settings\tevery setting, its default and where it is read from"}},
		{[]string{"help", "project", "o"}, []string{"open\tOpen a git repository as a project"}},
		{[]string{"project", "list", ""}, nil},
	} {
		got := complete(t, home, tc.args...)
		// cobra filters by prefix only in the shell; filter here alike.
		got = slices.DeleteFunc(got, func(c string) bool { return !strings.HasPrefix(c, tc.args[len(tc.args)-1]) })
		if !slices.Equal(got, tc.want) {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
	out := wdx(t, home, "__complete", "project", "open", "--local", "")
	if !strings.HasSuffix(out.stdout, ":16\n") { // ShellCompDirectiveFilterDirs
		t.Fatalf("want directories completed, got %q", out.stdout)
	}
}

// TestCompletionLoggedAtDebug checks a completion is logged at debug level
// in both processes, so it does not crowd the info log.
func TestCompletionLoggedAtDebug(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	complete(t, home, "project", "get", "--name", "")
	files, _ := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	var id string
	levels := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(b)) {
			var l struct {
				Level, Msg, Process string
				DebugID             string `json:"debug_id"`
			}
			json.Unmarshal([]byte(line), &l)
			if l.Msg == "command received" {
				id = l.DebugID
			}
			if l.DebugID == id && id != "" {
				levels[l.Process+" "+l.Msg] = l.Level
			}
		}
	}
	for _, msg := range []string{"cli command received", "cli daemon called", "cli command returned", "daemon request received", "daemon request returned"} {
		if levels[msg] != "DEBUG" {
			t.Errorf("%s: want DEBUG, got %q (all: %v)", msg, levels[msg], levels)
		}
	}
}
