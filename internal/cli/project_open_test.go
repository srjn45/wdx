package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/gittest"
)

func closedProject(t *testing.T, home, name string) string {
	t.Helper()
	id := newProject(t, home, name)
	if v, exit := wdxJSON(t, home, "project", "close", "--name", name, "--cancel"); exit != clikit.ExitOK {
		t.Fatalf("close %s: %+v", name, v)
	}
	return id
}

func TestOpenByNameAndID(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := closedProject(t, home, "app")

	// A name opens it, and with no checkout missing --json needs an answer anyway,
	// as with --git: it cannot know before asking the daemon.
	v, exit := wdxJSON(t, home, "project", "open", "--name", "APP", "--reclone")
	if exit != clikit.ExitOK || v.Result != "opened" || v.Project.ID != id || v.Project.State != "open" {
		t.Fatalf("by name: %+v", v)
	}
	// One open already just stays so.
	if v, exit = wdxJSON(t, home, "project", "open", "--name", "app", "--remove"); exit != clikit.ExitOK || v.Result != "opened" {
		t.Fatalf("already open: %+v", v)
	}
	if v, exit = wdxJSON(t, home, "project", "close", "--id", id, "--cancel"); exit != clikit.ExitOK {
		t.Fatalf("close: %+v", v)
	}
	if v, exit = wdxJSON(t, home, "project", "open", "--id", id, "--remove"); exit != clikit.ExitOK || v.Result != "opened" || v.Project.State != "open" {
		t.Fatalf("by id: %+v", v)
	}
	if out := wdx(t, home, "project", "close", "--id", id, "--cancel"); out.Exit != clikit.ExitOK {
		t.Fatalf("close: %+v", out)
	}
	if out := wdx(t, home, "project", "open", "--name", "app"); out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "opened project app (") {
		t.Fatalf("text: %+v", out)
	}
}

func TestOpenUnknownProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "project", "open", "--name", "nope", "--reclone")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "project list --all")
}

func TestOpenFlagRules(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"project", "open"},
		{"project", "open", "--reclone"},
		{"project", "open", "--name", "app"},
		{"project", "open", "--id", "proj-1"},
		{"project", "open", "--git", "x"},
		{"project", "open", "--id", "x", "--name", "y", "--reclone"},
		{"project", "open", "--id", "x", "--local", ".", "--reclone"},
		{"project", "open", "--id", "x", "--git", "y", "--reclone"},
		{"project", "open", "--id", "", "--reclone"},
		{"project", "open", "--name", "", "--reclone"},
	} {
		v, exit := wdxJSON(t, t.TempDir(), args...)
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
		wantHint(t, v, "--help'")
	}
	v, _ := wdxJSON(t, t.TempDir(), "project", "open")
	wantHint(t, v, "pass --local, --git, --id or --name")
	v, _ = wdxJSON(t, t.TempDir(), "project", "open", "--name", "app")
	wantHint(t, v, "pass --reclone or --remove")

	// Naming what --local opens answers no question of a known project's.
	home := newHome(t)
	repo := gittest.Repo(t, "repo")
	if v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app"); exit != clikit.ExitOK || v.Result != "registered" || v.Project.Name != "app" {
		t.Fatalf("--local --name: %+v", v)
	}

	// Without a terminal, nothing asks.
	out := wdx(t, home, "project", "open")
	if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "pass --local, --git, --id or --name") {
		t.Fatalf("no terminal: %+v", out)
	}
}

func TestOpenByNameMissingCheckout(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, clone, id := openedThenLost(t, home)

	if v, exit := wdxJSON(t, home, "project", "open", "--id", id, "--reclone"); exit != clikit.ExitOK || v.Result != "cloned" {
		t.Fatalf("--reclone: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Fatalf("want the clone back: %v", err)
	}
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	if v, exit := wdxJSON(t, home, "project", "open", "--name", "app", "--remove"); exit != clikit.ExitOK || v.Result != "removed" {
		t.Fatalf("--remove: %+v", v)
	}
	if v, _ := wdxJSON(t, home, "project", "list"); len(v.Projects) != 0 {
		t.Fatalf("want no project, got %+v", v.Projects)
	}
}

func TestOpenByNamePrompt(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	_, clone, _ := openedThenLost(t, home)

	out := wdxWith(t, home, answering(t, "c"), "project", "open", "--name", "app")
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stderr, "choose [c/r]: ") || !strings.HasPrefix(out.Stdout, "cloned") {
		t.Fatalf("clone: %+v", out)
	}
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	out = wdxWith(t, home, answering(t, "r"), "project", "open", "--name", "app")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "removed project app") {
		t.Fatalf("remove: %+v", out)
	}

	// Without a terminal it says how to answer.
	home = newHome(t)
	openedThenLost(t, home)
	out = wdx(t, home, "project", "open", "--name", "app")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "--reclone or --remove") && !strings.Contains(out.Stderr, "with --reclone") {
		t.Fatalf("no terminal: %+v", out)
	}
}

// noOriginLost is a project with no origin whose only checkout is gone.
func noOriginLost(t *testing.T, home string) (string, string) {
	t.Helper()
	repo := gittest.Repo(t, "repo")
	v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app")
	if exit != clikit.ExitOK {
		t.Fatalf("open: %+v", v)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	return repo, v.Project.ID
}

func TestOpenNoOriginMissingCheckout(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	noOriginLost(t, home)

	// Nothing to clone from, so the question does not offer it.
	out := wdxWith(t, home, answering(t, "c"), "project", "open", "--name", "app")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "choose [r/c]: ") || strings.Contains(out.Stderr, "clone again") {
		t.Fatalf("cancel: %+v", out)
	}
	v, exit := wdxJSON(t, home, "project", "open", "--name", "app", "--reclone")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, clikit.ExitRejected)

	out = wdxWith(t, home, answering(t, "r"), "project", "open", "--name", "app")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "removed project app") {
		t.Fatalf("remove: %+v", out)
	}
}

func TestOpenMenuClosedProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	closedProject(t, home, "alpha")
	id := closedProject(t, home, "beta")
	newProject(t, home, "gamma")

	out := wdxWith(t, home, answering(t, "9", "5"), "project", "open")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "opened project beta ("+id+")") {
		t.Fatalf("beta: %+v", out)
	}
	for _, want := range []string{"[1] create a new project", "[2] open a local repository", "[3] clone a git repository", "[4] alpha closed", "[5] beta closed", "choose [1-5] (empty to cancel): "} {
		if !strings.Contains(out.Stderr, want) {
			t.Fatalf("want %q in:\n%s", want, out.Stderr)
		}
	}
	if strings.Contains(out.Stderr, "gamma") {
		t.Fatalf("an open project listed:\n%s", out.Stderr)
	}
}

func TestOpenMenuCreate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	parent := t.TempDir()
	out := wdxWith(t, home, answering(t, "1", "web", parent), "project", "open")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "created project web (") {
		t.Fatalf("create: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(parent, "web", ".git")); err != nil {
		t.Fatal(err)
	}
	// The conflict's question reads the same answers.
	out = wdxWith(t, home, answering(t, "1", "web", parent, "o"), "project", "open")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "opened existing project web (") {
		t.Fatalf("conflict: %+v", out)
	}
}

func TestOpenMenuLocal(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	userHome := t.TempDir()
	code := filepath.Join(userHome, "code")
	if err := os.Mkdir(code, 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, code, "init")
	inHome := func(env *Env) { env.UserHome = userHome }
	stdin := answering(t, "2", "~/code")
	out := wdxWith(t, home, func(env *Env) { stdin(env); inHome(env) }, "project", "open")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "registered project code (") {
		t.Fatalf("local: %+v", out)
	}
}

func TestOpenMenuGit(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	remote := gittest.Bare(t, "web")
	out := wdxWith(t, home, answering(t, "3", remote), "project", "open")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "cloned project web (") {
		t.Fatalf("git: %+v", out)
	}
}

func TestOpenMenuCancel(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, answers := range [][]string{{""}, {"1", ""}, {"2", ""}, {"3", ""}} {
		out := wdxWith(t, home, answering(t, answers...), "project", "open")
		if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[cancelled]") {
			t.Fatalf("%q: %+v", answers, out)
		}
	}
	if v, _ := wdxJSON(t, home, "project", "list"); len(v.Projects) != 0 {
		t.Fatalf("projects: %+v", v.Projects)
	}
}
