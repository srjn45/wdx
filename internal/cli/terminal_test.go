package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/api"
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
	if exit != api.ExitOK || v.Result != "created" || v.Terminal == nil || v.Terminal.State != api.TerminalStateRunning {
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
	if exit != api.ExitOK || v.Terminal.ProjectName != "app" || v.Terminal.Dir != app || v.Terminal.Pid == nil || v.Terminal.Name == "" {
		t.Fatalf("unexpected create: exit %d, %+v", exit, v.Terminal)
	}
	// A project flag wins over the working directory.
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-name", "lib", "--name", "build")
	if exit != api.ExitOK || v.Terminal.ProjectName != "lib" || v.Terminal.Dir == app {
		t.Fatalf("want build in lib's clone path, got exit %d, %+v", exit, v.Terminal)
	}

	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-name", "lib", "--name", "BUILD")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "choose another --name")
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--name", "a:b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "create")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-id", "proj-00000000")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	if v.Hint != nil {
		t.Fatalf("want no hint when a project flag is given, got %q", *v.Hint)
	}
	v, exit = wdxJSONWith(t, home, in(filepath.Join(app, "missing")), "terminal", "create")
	wantError(t, v, exit, api.ErrorCodeStartFailed, api.ExitSystem)
	if failed, _ := v.Details["terminal"].(map[string]any); failed["state"] != "failed" {
		t.Fatalf("want the failed terminal in details, got %v", v.Details)
	}

	out := wdxWith(t, home, in(app), "terminal", "create", "--name", "server")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "created terminal server in app\n  dir    "+app+"\n  shell  ") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestTerminalCreateInClosedProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	v, exit := wdxJSONWith(t, home, in(app), "terminal", "create")
	wantError(t, v, exit, api.ErrorCodeProjectClosed, api.ExitRejected)
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
		if got := terminalNames(v.Terminals); exit != api.ExitOK || !slices.Equal(got, tc.want) {
			t.Errorf("%v: want %v, got exit %d, %v", tc.args, tc.want, exit, got)
		}
	}
	v, exit := wdxJSONWith(t, home, in(t.TempDir()), "terminal", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id, or --all")

	out := wdxWith(t, home, in(app), "terminal", "list")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "NAME  STATE    DIR") || !strings.Contains(out.stdout, "\na     running  "+app) {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
	if out := wdxWith(t, home, in(app), "terminal", "list", "--all"); !strings.HasPrefix(out.stdout, "PROJECT  NAME") {
		t.Fatalf("want a project column with --all, got %q", out.stdout)
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
		if exit != api.ExitOK || v.Result != "" || v.Terminal == nil || v.Terminal.Id != term.Id || v.Terminal.State != api.TerminalStateRunning {
			t.Fatalf("get %v: unexpected exit %d, %+v", args, exit, v)
		}
	}
	v, exit := wdxJSONWith(t, home, in(filepath.Join(app, "sub")), "terminal", "get", "--name", "build")
	if exit != api.ExitOK || v.Terminal.Id != term.Id {
		t.Fatalf("want build from its project's dir, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "terminal", "get", "--name", "build", "--project-name", "lib")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "get", "--name", "build")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the terminal's project, or pass --project-name or --project-id")

	out := wdx(t, home, "terminal", "get", "--id", term.Id)
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "terminal build in app\n  state    running\n  dir      "+app+"\n") ||
		!strings.Contains(out.stdout, "\n  id       "+term.Id+"\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestProjectGetTerminals(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "b")
	newTerminal(t, home, app, "a")

	v, exit := wdxJSON(t, home, "project", "get", "--name", "app")
	if exit != api.ExitOK || v.Project.Terminals == nil || !slices.Equal(terminalNames(*v.Project.Terminals), []string{"app/a", "app/b"}) {
		t.Fatalf("want terminals a and b, got exit %d, %+v", exit, v.Project)
	}
	out := wdx(t, home, "project", "get", "--name", "app")
	if !strings.Contains(out.stdout, "\n  terminals: a  running  "+app+"  term-") || !strings.Contains(out.stdout, "\n             b  running  "+app+"  term-") {
		t.Fatalf("unexpected text: %q", out.stdout)
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
	if exit != api.ExitOK || v.Result != "started" || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want build started, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "terminal", "start", "--id", v.Terminal.Id)
	wantError(t, v, exit, api.ErrorCodeTerminalRunning, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "start", "--name", "build")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the terminal's project, or pass --project-name or --project-id")
	v, exit = wdxJSON(t, home, "terminal", "start", "--name", "nope", "--project-name", "app")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

}

func TestTerminalRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")

	v, exit := wdxJSONWith(t, home, in(app), "terminal", "remove", "--name", "build")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "remove", "--name", "build", "--cancel")
	wantError(t, v, exit, api.ErrorCodeTerminalRunning, api.ExitRejected)
	wantHint(t, v, "pass --stop-running to stop it")
	// Without --json and a terminal to ask on, the answer must be given.
	out := wdxWith(t, home, in(app), "terminal", "remove", "--name", "build")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "hint: pass --stop-running or --cancel") {
		t.Fatalf("unexpected text error: %+v", out)
	}

	v, exit = wdxJSON(t, home, "terminal", "remove", "--id", term.Id, "--project-name", "app", "--stop-running")
	if exit != api.ExitOK || v.Result != "removed" || v.Terminal.Id != term.Id {
		t.Fatalf("want build removed, got exit %d, %+v", exit, v)
	}
	if syscall.Kill(*term.Pid, 0) == nil {
		t.Fatalf("want shell %d gone", *term.Pid)
	}
	v, exit = wdxJSON(t, home, "terminal", "remove", "--id", term.Id, "--cancel")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

	// One that is not running needs no answer.
	newTerminal(t, home, app, "idle")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if out := wdxWith(t, home, in(app), "terminal", "remove", "--name", "idle"); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "removed terminal idle from app\n  dir    "+app+"\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestProjectCloseAndRemoveWithTerminals(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")

	v, exit := wdxJSON(t, home, "project", "close", "--name", "app")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeProjectInUse, api.ExitRejected)
	wantHint(t, v, "pass --stop-running to stop them")

	v, exit = wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if exit != api.ExitOK || v.Result != "closed" || len(v.Stopped) != 1 || v.Stopped[0].Id != term.Id || v.Stopped[0].State != api.TerminalStateStopped {
		t.Fatalf("want app closed with build stopped, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "project", "open", "--local", app)
	if exit != api.ExitOK || v.Result != "opened" || len(v.Started) != 1 || v.Started[0].State != api.TerminalStateRunning {
		t.Fatalf("want app opened with build started, got exit %d, %+v", exit, v)
	}

	v, exit = wdxJSON(t, home, "project", "remove", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeProjectInUse, api.ExitRejected)
	out := wdx(t, home, "project", "remove", "--name", "app", "--stop-running")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "\n  stopped terminal build ("+term.Id+")\n") {
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
		lines := strings.Split(strings.TrimSuffix(out.stdout, "\n"), "\n")
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
	if out.exit != api.ExitOK || strings.Contains(out.stdout, "holder") || !strings.Contains(out.stdout, "terminal") {
		t.Fatalf("want terminal listed and holder hidden: %s", out.stdout)
	}
}
