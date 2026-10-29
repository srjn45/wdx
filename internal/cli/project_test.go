package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
)

func TestClose(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "close", "--id", id, "--cancel")
	if exit != api.ExitOK || v.Result != "closed" || v.Project.ID != id || v.Project.State != "closed" {
		t.Fatalf("unexpected close: %+v", v)
	}
	if v.Project.Origin != nil || len(v.Project.Worktrees) != 1 {
		t.Fatalf("unexpected project: %+v", v.Project)
	}
	if out := wdx(t, home, "project", "close", "--name", "app"); !strings.HasPrefix(out.stdout, "closed project app ("+id+")\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "remove", "--id", id, "--cancel")
	if exit != api.ExitOK || v.Result != "removed" || v.Project.ID != id {
		t.Fatalf("unexpected remove: %+v", v)
	}
	v, exit = wdxJSON(t, home, "project", "remove", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, args := range [][]string{
		{"close", "--id", "nope", "--cancel"},
		{"close", "--name", "nope", "--cancel"},
		{"remove", "--id", "nope", "--cancel"},
		{"remove", "--name", "nope", "--cancel"},
	} {
		v, exit := wdxJSON(t, home, append([]string{"project"}, args...)...)
		wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
		if v.Hint != nil || v.Details != nil {
			t.Fatalf("%v: want null hint and details, got %+v", args, v)
		}
	}
	out := wdx(t, home, "project", "close", "--name", "nope")
	if out.exit != api.ExitRejected || out.stdout != "" || !strings.HasPrefix(out.stderr, "wdx: project not found") {
		t.Fatalf("unexpected text error: %+v", out)
	}
}

func TestOpenLocal(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := gittest.Repo(t, "app")

	v, exit := wdxJSON(t, home, "project", "open", "--local", repo)
	if exit != api.ExitOK || v.Result != "registered" || v.Project.Name != "app" || v.Project.Worktrees[0].Path != repo {
		t.Fatalf("unexpected first open: %+v", v)
	}
	if v, _ = wdxJSON(t, home, "project", "open", "--local", repo); v.Result != "opened" {
		t.Fatalf("want opened, got %+v", v)
	}
	if out := wdx(t, home, "project", "open", "--local", repo); !strings.HasPrefix(out.stdout, "opened project app (") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestOpenLocalDropsMissingCheckouts(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := gittest.Repo(t, "app")
	wt := filepath.Join(filepath.Dir(repo), "wt")
	gittest.Git(t, repo, "worktree", "add", "-q", wt)
	wdxJSON(t, home, "project", "open", "--local", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	v, _ := wdxJSON(t, home, "project", "open", "--local", repo)
	if v.Result != "opened" || len(v.Dropped) != 1 || v.Dropped[0].Path != wt {
		t.Fatalf("want %s dropped, got %+v", wt, v)
	}
}

func TestOpenLocalErrors(t *testing.T) {
	t.Parallel()
	home := newHome(t)

	v, exit := wdxJSON(t, home, "project", "open", "--local", filepath.Join(home, "nope"))
	wantError(t, v, exit, api.ErrorCodeInvalidPath, api.ExitRejected)

	v, exit = wdxJSON(t, home, "project", "open", "--local", t.TempDir())
	wantError(t, v, exit, api.ErrorCodeNotGitRepo, api.ExitRejected)
	wantHint(t, v, "git init")

	id := newProject(t, home, "app")
	v, exit = wdxJSON(t, home, "project", "open", "--local", gittest.Repo(t, "other"), "--name", "APP")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "--name")
	if p, _ := v.Details["project"].(map[string]any); p["id"] != id {
		t.Fatalf("want details.project %s, got %+v", id, v.Details)
	}
}

func TestOpenLocalProjectMismatch(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	a, b := gittest.Repo(t, "a"), gittest.Repo(t, "b")
	gittest.Git(t, a, "remote", "add", "origin", "https://example.com/a.git")
	wdxJSON(t, home, "project", "open", "--local", a)
	wdxJSON(t, home, "project", "open", "--local", b)
	gittest.Git(t, b, "remote", "add", "origin", "https://example.com/a.git")

	v, exit := wdxJSON(t, home, "project", "open", "--local", b)
	wantError(t, v, exit, api.ErrorCodeProjectMismatch, api.ExitRejected)
}

func TestOpenGit(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	remote := gittest.Bare(t, "app")

	v, exit := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")
	want := filepath.Join(home, "projects", "app")
	if exit != api.ExitOK || v.Result != "cloned" || *v.Project.Origin != remote || v.Project.Worktrees[0].Path != want {
		t.Fatalf("unexpected clone: %+v", v)
	}
	if v, _ = wdxJSON(t, home, "project", "open", "--git", remote, "--reclone"); v.Result != "opened" {
		t.Fatalf("want opened, got %+v", v)
	}
}

func TestOpenGitJSONRequiresAnswer(t *testing.T) {
	t.Parallel()
	v, exit := wdxJSON(t, t.TempDir(), "project", "open", "--git", gittest.Bare(t, "app"))
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	wantHint(t, v, "--reclone or --remove")
}

func TestOpenGitCheckoutMissing(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	remote := gittest.Bare(t, "app")
	clone := filepath.Join(home, "projects", "app")
	first, _ := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")

	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	out := wdx(t, home, "project", "open", "--git", remote)
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "pass --reclone or --remove") {
		t.Fatalf("want checkout missing with hint, got %+v", out)
	}
	v, _ := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")
	if v.Result != "cloned" || v.Project.ID != first.Project.ID {
		t.Fatalf("want %s cloned again, got %+v", first.Project.ID, v)
	}

	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	v, _ = wdxJSON(t, home, "project", "open", "--git", remote, "--remove")
	if v.Result != "removed" || v.Project.ID != first.Project.ID {
		t.Fatalf("want %s removed, got %+v", first.Project.ID, v)
	}
	v, exit := wdxJSON(t, home, "project", "close", "--id", first.Project.ID, "--cancel")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestOpenGitErrors(t *testing.T) {
	t.Parallel()
	home := newHome(t)

	v, exit := wdxJSON(t, home, "project", "open", "--git", "", "--reclone")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)

	v, exit = wdxJSON(t, home, "project", "open", "--git", gittest.Bare(t, "app"), "--name", "../x", "--reclone")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	wantHint(t, v, "--name")

	if err := os.MkdirAll(filepath.Join(home, "projects", "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSON(t, home, "project", "open", "--git", gittest.Bare(t, "taken"), "--reclone")
	wantError(t, v, exit, api.ErrorCodePathExists, api.ExitRejected)
	wantHint(t, v, "--name")

	v, exit = wdxJSON(t, home, "project", "open", "--git", filepath.Join(home, "nope.git"), "--reclone")
	wantError(t, v, exit, api.ErrorCodeCloneFailed, api.ExitRejected)
	if output, _ := v.Details["output"].(string); !strings.Contains(output, "nope.git") {
		t.Fatalf("want git's output in details, got %+v", v.Details)
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	home := newHome(t)

	v, exit := wdxJSON(t, home, "project", "create", "--name", "app", "--cancel")
	want := filepath.Join(home, "projects", "app")
	if exit != api.ExitOK || v.Result != "created" || v.Conflict != nil || v.Project.Worktrees[0].Path != want {
		t.Fatalf("unexpected create: %+v", v)
	}
	if out := wdx(t, home, "project", "create", "--name", "web"); !strings.HasPrefix(out.stdout, "created project web (") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestCreateJSONRequiresAnswer(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "project", "create", "--name", "app")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	wantHint(t, v, "--open or --cancel")
	if _, err := os.Stat(filepath.Join(home, "projects", "app")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be created: %v", err)
	}
}

func TestCreateNameTaken(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "create", "--name", "APP", "--cancel")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "--name")
	if p, _ := v.Details["project"].(map[string]any); p["id"] != id {
		t.Fatalf("want details.project %s, got %+v", id, v.Details)
	}

	v, exit = wdxJSON(t, home, "project", "create", "--name", "APP", "--open")
	if exit != api.ExitOK || v.Result != "opened" || v.Project.ID != id || v.Conflict == nil || v.Conflict.Code != string(api.ErrorCodeNameTaken) {
		t.Fatalf("want %s opened after name_taken, got %+v", id, v)
	}

	out := wdx(t, home, "project", "create", "--name", "app")
	if out.exit != api.ExitRejected || !strings.HasPrefix(out.stderr, "wdx: not created: ") || !strings.Contains(out.stderr, "pass --open or --cancel") {
		t.Fatalf("unexpected text error: %+v", out)
	}
	out = wdx(t, home, "project", "create", "--name", "app", "--open")
	if !strings.HasPrefix(out.stdout, "opened existing project app ("+id+"); nothing was created\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestCreateRepoExists(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := gittest.Repo(t, "app")

	v, exit := wdxJSON(t, home, "project", "create", "--name", "app", "--path", filepath.Dir(repo), "--cancel")
	wantError(t, v, exit, api.ErrorCodeRepoExists, api.ExitRejected)
	wantHint(t, v, "--path")
	if v.Details["root"] != repo || v.Details["project"] != nil {
		t.Fatalf("want unknown repo %s in details, got %+v", repo, v.Details)
	}

	v, exit = wdxJSON(t, home, "project", "create", "--name", "sub", "--path", repo, "--open")
	if exit != api.ExitOK || v.Result != "registered" || v.Project.Worktrees[0].Path != repo || v.Conflict.Code != string(api.ErrorCodeRepoExists) {
		t.Fatalf("want %s registered after repo_exists, got %+v", repo, v)
	}
	if _, err := os.Stat(filepath.Join(repo, "sub")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be created: %v", err)
	}
}

func TestCreateOpenCheckoutMissing(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")
	if err := os.RemoveAll(filepath.Join(home, "projects", "app")); err != nil {
		t.Fatal(err)
	}

	v, exit := wdxJSON(t, home, "project", "create", "--name", "app", "--open")
	wantError(t, v, exit, api.ErrorCodeCheckoutMissing, api.ExitRejected)
	wantHint(t, v, "project remove --id "+id)
}

func TestCreateInvalid(t *testing.T) {
	t.Parallel()
	home := newHome(t)

	v, exit := wdxJSON(t, home, "project", "create", "--name", "../app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	wantHint(t, v, "--name")

	v, exit = wdxJSON(t, home, "project", "create", "--name", "id:app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)

	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSON(t, home, "project", "create", "--name", "app", "--path", file, "--cancel")
	wantError(t, v, exit, api.ErrorCodeInvalidPath, api.ExitRejected)
	wantHint(t, v, "--path")

	if out := wdx(t, home, "project", "create"); out.exit != api.ExitUsage || !strings.HasPrefix(out.stderr, "wdx: not created: ") {
		t.Fatalf("want usage error with prefix, got %+v", out)
	}
}

func names(v jsonOutput) string {
	var names []string
	for _, p := range v.Projects {
		names = append(names, p.Name)
	}
	return strings.Join(names, " ")
}

func TestListEmpty(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for filter, text := range map[string]string{"--all": "no projects\n", "--open": "no open projects\n", "--closed": "no closed projects\n"} {
		out := wdx(t, home, "project", "list", filter, "--json")
		if out.exit != api.ExitOK || !strings.Contains(out.stdout, `"projects": []`) {
			t.Fatalf("%s: want an empty array, got %+v", filter, out)
		}
		if out := wdx(t, home, "project", "list", filter); out.stdout != text {
			t.Fatalf("%s: want %q, got %q", filter, text, out.stdout)
		}
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, name := range []string{"cat", "app", "Beta"} {
		newProject(t, home, name)
	}
	wdxJSON(t, home, "project", "close", "--name", "cat", "--cancel")

	for args, want := range map[string]string{"": "app Beta cat", "--all": "app Beta cat", "--open": "app Beta", "--closed": "cat"} {
		v, exit := wdxJSON(t, home, strings.Fields("project list "+args)...)
		if exit != api.ExitOK || v.Result != "" || names(v) != want {
			t.Fatalf("list %s: want %s, got %+v", args, want, v)
		}
	}

	out := wdx(t, home, "project", "list", "--json")
	var raw struct{ Projects []map[string]any }
	if err := json.Unmarshal([]byte(out.stdout), &raw); err != nil {
		t.Fatal(err)
	}
	if p := raw.Projects[0]; p["worktree_count"] != 1.0 || p["origin"] != nil || p["worktrees"] != nil {
		t.Fatalf("unexpected list item: %v", p)
	}
	if _, ok := raw.Projects[0]["worktrees"]; ok {
		t.Fatalf("a list item must not carry worktrees: %v", raw.Projects[0])
	}

	lines := strings.Split(strings.TrimSpace(wdx(t, home, "project", "list").stdout), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "NAME") || !strings.HasPrefix(lines[1], "app ") || !strings.Contains(lines[3], "closed") {
		t.Fatalf("unexpected table:\n%s", strings.Join(lines, "\n"))
	}
}

func TestGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "App")
	wdxJSON(t, home, "project", "close", "--id", id, "--cancel")

	for _, args := range [][]string{{"--id", id}, {"--name", "app"}} {
		v, exit := wdxJSON(t, home, append([]string{"project", "get"}, args...)...)
		if exit != api.ExitOK || v.Result != "" || v.Project.ID != id || v.Project.State != "closed" {
			t.Fatalf("get %v: unexpected %+v", args, v)
		}
		p := v.Project
		if p.Orphaned == nil || *p.Orphaned || len(p.Worktrees) != 1 || p.Worktrees[0].Present == nil || !*p.Worktrees[0].Present {
			t.Fatalf("get %v: want one present checkout, got %+v", args, p)
		}
		if p.Terminals == nil || len(*p.Terminals) != 0 {
			t.Fatalf("get %v: want an empty list of terminals, got %v", args, p.Terminals)
		}
	}

	out := wdx(t, home, "project", "get", "--name", "app")
	if !strings.HasPrefix(out.stdout, "project App ("+id+")\n  state:     closed\n") || strings.Contains(out.stdout, "orphaned") ||
		!strings.Contains(out.stdout, "\n  terminals: (none)\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestGetOrphaned(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")
	if err := os.RemoveAll(filepath.Join(home, "projects", "app")); err != nil {
		t.Fatal(err)
	}

	v, exit := wdxJSON(t, home, "project", "get", "--name", "app")
	if exit != api.ExitOK || !*v.Project.Orphaned || *v.Project.Worktrees[0].Present {
		t.Fatalf("want an orphaned project with its missing checkout, got %+v", v.Project)
	}
	out := wdx(t, home, "project", "get", "--name", "app")
	if !strings.Contains(out.stdout, "(missing)\n") || !strings.Contains(out.stdout, "orphaned:  no checkout on disk; remove it with wdx project remove --id "+id) {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestGetNotFound(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	id := newProject(t, home, "app")
	for _, args := range [][]string{{"--id", "proj-nope"}, {"--name", "nope"}, {"--id", "app"}, {"--name", id}} {
		v, exit := wdxJSON(t, home, append([]string{"project", "get"}, args...)...)
		wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	}
}

func TestWorktreeFieldsOnlyInGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	newProject(t, home, "app")
	out := wdx(t, home, "project", "close", "--name", "app", "--cancel", "--json")
	if strings.Contains(out.stdout, `"present"`) || strings.Contains(out.stdout, `"orphaned"`) || strings.Contains(out.stdout, `"terminals"`) || !strings.Contains(out.stdout, `"created_at"`) {
		t.Fatalf("close must not carry disk status: %s", out.stdout)
	}
}
