package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/runtest"
)

// typed returns a terminal for a command's stdin, with answers already
// typed into it, one per line.
func typed(t *testing.T, answers ...string) *os.File {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slave.Close()
		master.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, master) }() // the echo
	for _, a := range answers {
		if _, err := master.WriteString(a + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return slave
}

// answering runs a command with answers typed on its terminal.
func answering(t *testing.T, answers ...string) func(*Env) {
	stdin := typed(t, answers...)
	return func(env *Env) { env.Stdin = stdin }
}

func TestCreatePromptOpen(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")

	out := wdxWith(t, home, answering(t, "o"), "project", "create", "--name", "app")
	if out.exit != api.ExitOK || !strings.Contains(out.stderr, "choose [o/n/c]: ") || !strings.HasPrefix(out.stdout, "opened existing project app ("+id+")") {
		t.Fatalf("want app opened, got %+v", out)
	}
}

func TestCreatePromptNewName(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	newProject(t, home, "app")

	out := wdxWith(t, home, answering(t, "n", "web"), "project", "create", "--name", "app")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "created project web (") {
		t.Fatalf("want web created, got %+v", out)
	}
}

// TestCreatePromptCancel answers what is not a choice first: the question
// is asked again.
func TestCreatePromptCancel(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	newProject(t, home, "app")

	out := wdxWith(t, home, answering(t, "x", "c"), "project", "create", "--name", "app")
	if out.exit != api.ExitRejected || strings.Count(out.stderr, "choose [o/n/c]: ") != 2 || !strings.Contains(out.stderr, "[name_taken]") {
		t.Fatalf("want name_taken after asking twice, got %+v", out)
	}
	if v, _ := wdxJSON(t, home, "project", "list"); len(v.Projects) != 1 {
		t.Fatalf("want only app, got %+v", v.Projects)
	}
}

// openedThenLost opens a project from a bare repository and deletes its
// clone, so opening it again asks what to do.
func openedThenLost(t *testing.T, home string) (remote, clone, id string) {
	t.Helper()
	remote = gittest.Bare(t, "app")
	clone = filepath.Join(home, "projects", "app")
	v, exit := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")
	if exit != api.ExitOK {
		t.Fatalf("open %s: %+v", remote, v)
	}
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	return remote, clone, v.Project.ID
}

func TestOpenPromptReclone(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	remote, clone, _ := openedThenLost(t, home)

	out := wdxWith(t, home, answering(t, "c"), "project", "open", "--git", remote)
	if out.exit != api.ExitOK || !strings.Contains(out.stderr, "choose [c/r]: ") {
		t.Fatalf("want app cloned again, got %+v", out)
	}
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Fatalf("want the clone back: %v", err)
	}
}

func TestOpenPromptRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	remote, _, id := openedThenLost(t, home)

	out := wdxWith(t, home, answering(t, "r"), "project", "open", "--git", remote)
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "removed project app ("+id+")") {
		t.Fatalf("want app removed, got %+v", out)
	}
	if v, _ := wdxJSON(t, home, "project", "list"); len(v.Projects) != 0 {
		t.Fatalf("want no project, got %+v", v.Projects)
	}
}

func TestTerminalRemovePromptYes(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")

	out := wdxWith(t, home, func(env *Env) {
		in(app)(env)
		answering(t, "y")(env)
	}, "terminal", "remove", "--name", "build")
	if out.exit != api.ExitOK || !strings.Contains(out.stderr, "stop it and remove it? [y/n]: ") || !strings.HasPrefix(out.stdout, "removed terminal build from app") {
		t.Fatalf("want build removed, got %+v", out)
	}
	if runtest.Alive(*term.Pid) {
		t.Fatalf("want shell %d gone", *term.Pid)
	}
}

func TestTerminalRemovePromptNo(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "build")

	out := wdxWith(t, home, func(env *Env) {
		in(app)(env)
		answering(t, "n")(env)
	}, "terminal", "remove", "--name", "build")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "hint: pass --stop-running to stop it") {
		t.Fatalf("want build kept, got %+v", out)
	}
	if v, exit := wdxJSON(t, home, "terminal", "get", "--id", term.Id); exit != api.ExitOK || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want build running, got exit %d, %+v", exit, v.Terminal)
	}
}
