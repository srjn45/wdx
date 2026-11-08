package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/runtest"
)

// in runs a command with dir as the working directory.
func in(dir string) func(*Env) {
	return func(env *Env) { env.WorkDir = dir }
}

// newProjectDir creates a project named name in home and returns its
// checkout.
func newProjectDir(t *testing.T, home, name string) string {
	t.Helper()
	id := newProject(t, home, name)
	v, _ := wdxJSON(t, home, "project", "get", "--id", id)
	return v.Project.Worktrees[0].Path
}

// newTerminal creates a terminal named name in the project dir is in.
func newTerminal(t *testing.T, home, dir, name string) api.Terminal {
	t.Helper()
	v, exit := wdxJSONWith(t, home, in(dir), "terminal", "create", "--name", name)
	if exit != clikit.ExitOK || v.Result != "created" || v.Terminal == nil || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("create terminal %s: exit %d, %+v", name, exit, v)
	}
	return *v.Terminal
}

func terminalNames(list []api.Terminal) []string {
	var names []string
	for _, term := range list {
		names = append(names, term.ProjectName+"/"+term.Name)
	}
	return names
}

func TestTerminalCreate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newProject(t, home, "lib")

	v, exit := wdxJSONWith(t, home, in(app), "terminal", "create")
	if exit != clikit.ExitOK || v.Terminal.ProjectName != "app" || v.Terminal.Dir != app || v.Terminal.Pid == nil || v.Terminal.Name == "" {
		t.Fatalf("unexpected create: exit %d, %+v", exit, v.Terminal)
	}
	// A project flag wins over the working directory.
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-name", "lib", "--name", "build")
	if exit != clikit.ExitOK || v.Terminal.ProjectName != "lib" || v.Terminal.Dir == app {
		t.Fatalf("want build in lib's clone path, got exit %d, %+v", exit, v.Terminal)
	}

	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-name", "lib", "--name", "BUILD")
	wantError(t, v, exit, api.ErrorCodeNameTaken, clikit.ExitRejected)
	wantHint(t, v, "choose another --name")
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--name", "a:b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "create")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-id", "proj-00000000")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	if v.Hint != nil {
		t.Fatalf("want no hint when a project flag is given, got %q", *v.Hint)
	}
	v, exit = wdxJSONWith(t, home, in(filepath.Join(app, "missing")), "terminal", "create")
	wantError(t, v, exit, api.ErrorCodeStartFailed, clikit.ExitSystem)
	if failed, _ := v.Details["terminal"].(map[string]any); failed["state"] != "failed" {
		t.Fatalf("want the failed terminal in details, got %v", v.Details)
	}

	out := wdxWith(t, home, in(app), "terminal", "create", "--name", "server")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "created terminal server in app\n  dir    "+app+"\n  shell  ") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestTerminalCreateInClosedProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	v, exit := wdxJSONWith(t, home, in(app), "terminal", "create")
	wantError(t, v, exit, api.ErrorCodeProjectClosed, clikit.ExitRejected)
	wantHint(t, v, "open the project first with wdx project open")
}

func TestTerminalList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	lib := newProjectDir(t, home, "lib")
	newTerminal(t, home, app, "b")
	newTerminal(t, home, app, "a")
	newTerminal(t, home, lib, "c")

	for _, tc := range []struct {
		dir  string
		args []string
		want []string
	}{
		{app, nil, []string{"app/a", "app/b"}},
		{filepath.Join(lib, "sub"), nil, []string{"lib/c"}},
		{app, []string{"--project-name", "lib"}, []string{"lib/c"}},
		{t.TempDir(), []string{"--all"}, []string{"app/a", "app/b", "lib/c"}},
	} {
		v, exit := wdxJSONWith(t, home, in(tc.dir), append([]string{"terminal", "list"}, tc.args...)...)
		if got := terminalNames(v.Terminals); exit != clikit.ExitOK || !slices.Equal(got, tc.want) {
			t.Errorf("%v: want %v, got exit %d, %v", tc.args, tc.want, exit, got)
		}
	}
	v, exit := wdxJSONWith(t, home, in(t.TempDir()), "terminal", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id, or --all")

	out := wdxWith(t, home, in(app), "terminal", "list")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "NAME  STATE    DIR") || !strings.Contains(out.Stdout, "\na     running  "+app) {
		t.Fatalf("unexpected text: %q", out.Stdout)
	}
	if out := wdxWith(t, home, in(app), "terminal", "list", "--all"); !strings.HasPrefix(out.Stdout, "PROJECT  NAME") {
		t.Fatalf("want a project column with --all, got %q", out.Stdout)
	}
}

func TestTerminalGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newProject(t, home, "lib")
	term := newTerminal(t, home, app, "build")

	for _, args := range [][]string{{"--id", term.Id}, {"--name", "BUILD", "--project-name", "app"}} {
		v, exit := wdxJSON(t, home, append([]string{"terminal", "get"}, args...)...)
		if exit != clikit.ExitOK || v.Result != "" || v.Terminal == nil || v.Terminal.Id != term.Id || v.Terminal.State != api.TerminalStateRunning {
			t.Fatalf("get %v: unexpected exit %d, %+v", args, exit, v)
		}
	}
	v, exit := wdxJSONWith(t, home, in(filepath.Join(app, "sub")), "terminal", "get", "--name", "build")
	if exit != clikit.ExitOK || v.Terminal.Id != term.Id {
		t.Fatalf("want build from its project's dir, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "terminal", "get", "--name", "build", "--project-name", "lib")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "get", "--name", "build")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the terminal's project, or pass --project-name or --project-id")

	out := wdx(t, home, "terminal", "get", "--id", term.Id)
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "terminal build in app\n  state    running\n  dir      "+app+"\n") ||
		!strings.Contains(out.Stdout, "\n  id       "+term.Id+"\n") {
		t.Fatalf("unexpected text: %q", out.Stdout)
	}
}

func TestProjectGetTerminals(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "b")
	newTerminal(t, home, app, "a")

	v, exit := wdxJSON(t, home, "project", "get", "--name", "app")
	if exit != clikit.ExitOK || v.Project.Terminals == nil || !slices.Equal(terminalNames(*v.Project.Terminals), []string{"app/a", "app/b"}) {
		t.Fatalf("want terminals a and b, got exit %d, %+v", exit, v.Project)
	}
	out := wdx(t, home, "project", "get", "--name", "app")
	if !strings.Contains(out.Stdout, "\n  terminals: a  running  "+app+"  term-") || !strings.Contains(out.Stdout, "\n             b  running  "+app+"  term-") {
		t.Fatalf("unexpected text: %q", out.Stdout)
	}
}

func TestTerminalStart(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	missing := filepath.Join(app, "missing")
	wdxJSONWith(t, home, in(missing), "terminal", "create", "--name", "build")
	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}

	v, exit := wdxJSONWith(t, home, in(app), "terminal", "start", "--name", "build")
	if exit != clikit.ExitOK || v.Result != "started" || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want build started, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "terminal", "start", "--id", v.Terminal.Id)
	wantError(t, v, exit, api.ErrorCodeTerminalRunning, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "start", "--name", "build")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the terminal's project, or pass --project-name or --project-id")
	v, exit = wdxJSON(t, home, "terminal", "start", "--name", "nope", "--project-name", "app")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)

}

func TestTerminalRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")

	v, exit := wdxJSONWith(t, home, in(app), "terminal", "remove", "--name", "build")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "remove", "--name", "build", "--cancel")
	wantError(t, v, exit, api.ErrorCodeTerminalRunning, clikit.ExitRejected)
	wantHint(t, v, "pass --stop-running to stop it")
	// Without --json and a terminal to ask on, the answer must be given.
	out := wdxWith(t, home, in(app), "terminal", "remove", "--name", "build")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "hint: pass --stop-running or --cancel") {
		t.Fatalf("unexpected text error: %+v", out)
	}

	v, exit = wdxJSON(t, home, "terminal", "remove", "--id", term.Id, "--project-name", "app", "--stop-running")
	if exit != clikit.ExitOK || v.Result != "removed" || v.Terminal.Id != term.Id {
		t.Fatalf("want build removed, got exit %d, %+v", exit, v)
	}
	if runtest.Alive(*term.Pid) {
		t.Fatalf("want shell %d gone", *term.Pid)
	}
	v, exit = wdxJSON(t, home, "terminal", "remove", "--id", term.Id, "--cancel")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)

	// One that is not running needs no answer.
	newTerminal(t, home, app, "idle")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if out := wdxWith(t, home, in(app), "terminal", "remove", "--name", "idle"); out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "removed terminal idle from app\n  dir    "+app+"\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestProjectCloseAndRemoveWithTerminals(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")

	v, exit := wdxJSON(t, home, "project", "close", "--name", "app")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	v, exit = wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeProjectInUse, clikit.ExitRejected)
	wantHint(t, v, "pass --stop-running to stop them")

	v, exit = wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if exit != clikit.ExitOK || v.Result != "closed" || len(v.Stopped) != 1 || v.Stopped[0].Id != term.Id || v.Stopped[0].State != api.TerminalStateStopped {
		t.Fatalf("want app closed with build stopped, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "project", "open", "--local", app)
	if exit != clikit.ExitOK || v.Result != "opened" || len(v.Started) != 1 || v.Started[0].State != api.TerminalStateRunning {
		t.Fatalf("want app opened with build started, got exit %d, %+v", exit, v)
	}

	v, exit = wdxJSON(t, home, "project", "remove", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeProjectInUse, clikit.ExitRejected)
	out := wdx(t, home, "project", "remove", "--name", "app", "--stop-running")
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "\n  stopped terminal build ("+term.Id+")\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
	if v, _ := wdxJSON(t, home, "terminal", "list", "--all"); len(v.Terminals) != 0 {
		t.Fatalf("want the terminals removed with the project, got %v", terminalNames(v.Terminals))
	}
}

func TestCompleteTerminals(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	lib := newProjectDir(t, home, "lib")
	a := newTerminal(t, home, app, "a")
	newTerminal(t, home, lib, "c")

	for _, tc := range []struct {
		dir  string
		args []string
		want []string
	}{
		{app, []string{"terminal", "start", "--name", ""}, []string{"a\trunning, " + a.Id}},
		{app, []string{"terminal", "remove", "--project-name", "lib", "--name", ""}, []string{"c\trunning, " + "*"}},
		{app, []string{"terminal", "remove", "--id", ""}, []string{a.Id + "\ta in app"}},
		{t.TempDir(), []string{"terminal", "start", "--name", ""}, nil},
	} {
		out := wdxWith(t, home, in(tc.dir), append([]string{"__complete"}, tc.args...)...)
		lines := strings.Split(strings.TrimSuffix(out.Stdout, "\n"), "\n")
		got := lines[:len(lines)-1]
		if len(got) != len(tc.want) {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
			continue
		}
		for i := range got {
			if w := tc.want[i]; got[i] != w && (!strings.HasSuffix(w, "*") || !strings.HasPrefix(got[i], strings.TrimSuffix(w, "*"))) {
				t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
			}
		}
	}
}

func TestHolderHidden(t *testing.T) {
	t.Parallel()
	out := wdx(t, t.TempDir(), "--help")
	if out.Exit != clikit.ExitOK || strings.Contains(out.Stdout, "holder") || !strings.Contains(out.Stdout, "terminal") {
		t.Fatalf("want terminal listed and holder hidden: %s", out.Stdout)
	}
}

func TestTerminalSend(t *testing.T) {
	t.Parallel()
	// The shell ends with what it read in its dir, once it has read two lines.
	home := newHomeWithShell(t, "#!/bin/sh\nread first\nread second\necho \"$first, $second\" > got\n")
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")
	const secret = "the-secret-prompt"

	v, exit := wdxJSONWith(t, home, in(app), "terminal", "send", "--name", "build", "--prompt", secret)
	if exit != clikit.ExitOK || v.Result != "queued" || v.Terminal.Id != term.Id {
		t.Fatalf("want it queued, got exit %d, %+v", exit, v)
	}
	out := wdx(t, home, "terminal", "send", "--id", term.Id, "--prompt=ls -la")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "queued terminal build in app\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
	got := filepath.Join(app, "got")
	if !eventually(t, func() bool { b, err := os.ReadFile(got); return err == nil && len(b) > 0 }) {
		t.Fatal("the shell did not read the messages")
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != secret+", ls -la\n" {
		t.Fatalf("the shell read %q, %v", b, err)
	}

	// The shell has ended.
	if !eventually(t, func() bool {
		v, _ := wdxJSON(t, home, "terminal", "get", "--id", term.Id)
		return v.Terminal.State == api.TerminalStateExited
	}) {
		t.Fatal("the shell did not exit")
	}
	v, exit = wdxJSON(t, home, "terminal", "send", "--id", term.Id, "--prompt", secret)
	wantError(t, v, exit, api.ErrorCodeTerminalNotRunning, clikit.ExitRejected)
	wantHint(t, v, "start its shell with wdx terminal start, or open its project")

	v, exit = wdxJSONWith(t, home, in(app), "terminal", "send", "--name", "nope", "--prompt", secret)
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "terminal", "send", "--id", term.Id, "--prompt", "")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	v, exit = wdxJSON(t, home, "terminal", "send", "--id", term.Id)
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)

	files, err := filepath.Glob(filepath.Join(home, "logs", "*.log"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no logs: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if logged := string(b); strings.Contains(logged, secret) || strings.Contains(logged, "ls -la") {
			t.Fatalf("a prompt is logged in %s", f)
		}
	}
}

func TestTerminalSendTooLarge(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")
	v, exit := wdxJSON(t, home, "terminal", "send", "--id", term.Id, "--prompt", strings.Repeat("x", 1<<20))
	wantError(t, v, exit, api.ErrorCodeSendFailed, clikit.ExitSystem)
}

// TestTerminalSendDropped leaves messages queued when the shell ends: they are
// lost, which the holder logs. The last may be dropped from the inbox or fail
// to be written, whichever comes first once the shell has gone.
func TestTerminalSendDropped(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, "#!/bin/sh\nstty raw -echo\n: > ready\nexec sleep 60\n")
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")
	if !eventually(t, func() bool { _, err := os.Stat(filepath.Join(app, "ready")); return err == nil }) {
		t.Fatal("the shell did not get ready")
	}
	// Too much for a program that does not read, so the next stays queued.
	for _, prompt := range []string{strings.Repeat("x", 512<<10), "next"} {
		if v, exit := wdxJSON(t, home, "terminal", "send", "--id", term.Id, "--prompt", prompt); exit != clikit.ExitOK {
			t.Fatalf("want it queued, got exit %d, %+v", exit, v)
		}
	}
	if v, exit := wdxJSON(t, home, "terminal", "remove", "--id", term.Id, "--stop-running"); exit != clikit.ExitOK {
		t.Fatalf("want it removed, got exit %d, %+v", exit, v)
	}
	if !eventually(t, func() bool {
		files, _ := filepath.Glob(filepath.Join(home, "logs", "*.log"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for line := range strings.Lines(string(b)) {
				if strings.Contains(line, `"msg":"messages dropped: the shell has ended"`) ||
					(strings.Contains(line, `"msg":"message not delivered"`) && strings.Contains(line, `"prompt_bytes":4,`)) {
					return true
				}
			}
		}
		return false
	}) {
		t.Fatal("the dropped message is not logged")
	}
}
