package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/warden/internal/gittest"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	m.Run()
}

type output struct {
	exit           int
	stdout, stderr string
}

// wdx runs the command line args against home, which stands in for ~/.wdx.
func wdx(t *testing.T, home string, args ...string) output {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(key string) string {
			if key == "WDX_HOME" {
				return home
			}
			return ""
		},
	}
	exit := Run(context.Background(), args, env)
	return output{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

type jsonOutput struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Name    string `json:"name"`
	Dropped []struct {
		Path string `json:"path"`
	} `json:"dropped"`
	Project *struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		Origin    *string `json:"origin"`
		State     string  `json:"state"`
		Worktrees []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"worktrees"`
	} `json:"project"`
	Error *struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Hint    *string        `json:"hint"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// wdxJSON runs args with --json and checks that stdout holds exactly one
// JSON object and stderr nothing.
func wdxJSON(t *testing.T, home string, args ...string) (jsonOutput, int) {
	t.Helper()
	out := wdx(t, home, append(args, "--json")...)
	if out.stderr != "" {
		t.Fatalf("%v: want empty stderr, got %q", args, out.stderr)
	}
	var v jsonOutput
	dec := json.NewDecoder(strings.NewReader(out.stdout))
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out.stdout)
	}
	if dec.More() {
		t.Fatalf("%v: want one JSON object, got more:\n%s", args, out.stdout)
	}
	if v.OK != (out.exit == exitOK) || v.OK != (v.Error == nil) {
		t.Fatalf("%v: ok %v, exit %d and error %+v disagree", args, v.OK, out.exit, v.Error)
	}
	return v, out.exit
}

func wantError(t *testing.T, v jsonOutput, exit int, code string, wantExit int) {
	t.Helper()
	if v.Error == nil || v.Error.Code != code || exit != wantExit {
		t.Fatalf("want %s with exit %d, got exit %d and %+v", code, wantExit, exit, v.Error)
	}
}

// newProject creates a project named name in home and returns its id.
func newProject(t *testing.T, home, name string) string {
	t.Helper()
	if out := wdx(t, home, "project", "create", "--name", name); out.exit != exitOK {
		t.Fatalf("create %s: %+v", name, out)
	}
	v, _ := wdxJSON(t, home, "project", "close", name)
	return v.Project.ID
}

func TestVersion(t *testing.T) {
	t.Parallel()
	v, exit := wdxJSON(t, t.TempDir(), "version")
	if exit != exitOK || v.Name != "wdx" {
		t.Fatalf("unexpected version: %+v", v)
	}
	if out := wdx(t, t.TempDir(), "version"); !strings.HasPrefix(out.stdout, "wdx ") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestClose(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "close", id)
	if exit != exitOK || v.Action != "closed" || v.Project.ID != id || v.Project.State != "closed" {
		t.Fatalf("unexpected close: %+v", v)
	}
	if v.Project.Origin != nil || len(v.Project.Worktrees) != 1 {
		t.Fatalf("unexpected project: %+v", v.Project)
	}
	if out := wdx(t, home, "project", "close", "app"); !strings.HasPrefix(out.stdout, "closed project app ("+id+")\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "remove", "app")
	if exit != exitOK || v.Action != "removed" || v.Project.ID != id {
		t.Fatalf("unexpected remove: %+v", v)
	}
	v, exit = wdxJSON(t, home, "project", "remove", "app")
	wantError(t, v, exit, codeNotFound, exitRejected)
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	for _, sub := range []string{"close", "remove"} {
		v, exit := wdxJSON(t, t.TempDir(), "project", sub, "nope")
		wantError(t, v, exit, codeNotFound, exitRejected)
		if v.Error.Hint != nil || v.Error.Details != nil {
			t.Fatalf("%s: want null hint and details, got %+v", sub, v.Error)
		}
	}
	out := wdx(t, t.TempDir(), "project", "close", "nope")
	if out.exit != exitRejected || out.stdout != "" || !strings.HasPrefix(out.stderr, "wdx: project not found") {
		t.Fatalf("unexpected text error: %+v", out)
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"bogus"},
		{"project", "bogus"},
		{"project", "close"},
		{"project", "close", "a", "b"},
		{"version", "--bogus"},
		{"project", "create"},
		{"project", "create", "--name", "app", "--open", "--cancel"},
		{"project", "open", "--local", ".", "--git", "x"},
	} {
		v, exit := wdxJSON(t, t.TempDir(), args...)
		wantError(t, v, exit, codeUsage, exitUsage)
	}
	if out := wdx(t, t.TempDir(), "project", "bogus"); out.exit != exitUsage || out.stdout != "" || out.stderr == "" {
		t.Fatalf("unexpected text usage error: %+v", out)
	}
}

func TestSystemError(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(home, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v, exit := wdxJSON(t, home, "project", "close", "app")
	wantError(t, v, exit, codeInternal, exitSystem)
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepo creates a repository with one commit in a temp dir named name.
func gitRepo(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, name)
	gitCmd(t, filepath.Dir(dir), "init", "-q", dir)
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// bareRemote creates a bare repository named name.git to clone from.
func bareRemote(t *testing.T, name string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), name+".git")
	gitCmd(t, gitRepo(t, name), "clone", "-q", "--bare", ".", bare)
	return bare
}

func wantHint(t *testing.T, v jsonOutput, hint string) {
	t.Helper()
	if v.Error.Hint == nil || !strings.Contains(*v.Error.Hint, hint) {
		t.Fatalf("want hint containing %q, got %+v", hint, v.Error)
	}
}

func TestOpenLocal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := gitRepo(t, "app")

	v, exit := wdxJSON(t, home, "project", "open", "--local", repo)
	if exit != exitOK || v.Action != "registered" || v.Project.Name != "app" || v.Project.Worktrees[0].Path != repo {
		t.Fatalf("unexpected first open: %+v", v)
	}
	if v, _ = wdxJSON(t, home, "project", "open", "--local", repo); v.Action != "opened" {
		t.Fatalf("want opened, got %+v", v)
	}
	if out := wdx(t, home, "project", "open", "--local", repo); !strings.HasPrefix(out.stdout, "opened project app (") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestOpenLocalDropsMissingCheckouts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := gitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(repo), "wt")
	gitCmd(t, repo, "worktree", "add", "-q", wt)
	wdxJSON(t, home, "project", "open", "--local", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	v, _ := wdxJSON(t, home, "project", "open", "--local", repo)
	if v.Action != "opened" || len(v.Dropped) != 1 || v.Dropped[0].Path != wt {
		t.Fatalf("want %s dropped, got %+v", wt, v)
	}
}

func TestOpenLocalErrors(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	v, exit := wdxJSON(t, home, "project", "open", "--local", filepath.Join(home, "nope"))
	wantError(t, v, exit, codeInvalidPath, exitRejected)

	v, exit = wdxJSON(t, home, "project", "open", "--local", t.TempDir())
	wantError(t, v, exit, codeNotGitRepo, exitRejected)
	wantHint(t, v, "git init")

	id := newProject(t, home, "app")
	v, exit = wdxJSON(t, home, "project", "open", "--local", gitRepo(t, "other"), "--name", "APP")
	wantError(t, v, exit, codeNameTaken, exitRejected)
	wantHint(t, v, "--name")
	if p, _ := v.Error.Details["project"].(map[string]any); p["id"] != id {
		t.Fatalf("want details.project %s, got %+v", id, v.Error.Details)
	}
}

func TestOpenLocalProjectMismatch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	a, b := gitRepo(t, "a"), gitRepo(t, "b")
	gitCmd(t, a, "remote", "add", "origin", "https://example.com/a.git")
	wdxJSON(t, home, "project", "open", "--local", a)
	wdxJSON(t, home, "project", "open", "--local", b)
	gitCmd(t, b, "remote", "add", "origin", "https://example.com/a.git")

	v, exit := wdxJSON(t, home, "project", "open", "--local", b)
	wantError(t, v, exit, codeProjectMismatch, exitRejected)
}

func TestOpenGit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	remote := bareRemote(t, "app")

	v, exit := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")
	want := filepath.Join(home, "projects", "app")
	if exit != exitOK || v.Action != "cloned" || *v.Project.Origin != remote || v.Project.Worktrees[0].Path != want {
		t.Fatalf("unexpected clone: %+v", v)
	}
	if v, _ = wdxJSON(t, home, "project", "open", "--git", remote, "--reclone"); v.Action != "opened" {
		t.Fatalf("want opened, got %+v", v)
	}
}

func TestOpenGitJSONRequiresAnswer(t *testing.T) {
	t.Parallel()
	v, exit := wdxJSON(t, t.TempDir(), "project", "open", "--git", bareRemote(t, "app"))
	wantError(t, v, exit, codeUsage, exitUsage)
	wantHint(t, v, "--reclone or --remove")
}

func TestOpenGitCheckoutMissing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	remote := bareRemote(t, "app")
	clone := filepath.Join(home, "projects", "app")
	first, _ := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")

	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	out := wdx(t, home, "project", "open", "--git", remote)
	if out.exit != exitRejected || !strings.Contains(out.stderr, "pass --reclone or --remove") {
		t.Fatalf("want checkout missing with hint, got %+v", out)
	}
	v, _ := wdxJSON(t, home, "project", "open", "--git", remote, "--reclone")
	if v.Action != "cloned" || v.Project.ID != first.Project.ID {
		t.Fatalf("want %s cloned again, got %+v", first.Project.ID, v)
	}

	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	v, _ = wdxJSON(t, home, "project", "open", "--git", remote, "--remove")
	if v.Action != "removed" || v.Project.ID != first.Project.ID {
		t.Fatalf("want %s removed, got %+v", first.Project.ID, v)
	}
	v, exit := wdxJSON(t, home, "project", "close", first.Project.ID)
	wantError(t, v, exit, codeNotFound, exitRejected)
}

func TestOpenGitErrors(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	v, exit := wdxJSON(t, home, "project", "open", "--git", "", "--reclone")
	wantError(t, v, exit, codeInvalidArgument, exitRejected)

	v, exit = wdxJSON(t, home, "project", "open", "--git", bareRemote(t, "app"), "--name", "../x", "--reclone")
	wantError(t, v, exit, codeInvalidName, exitRejected)
	wantHint(t, v, "--name")

	if err := os.MkdirAll(filepath.Join(home, "projects", "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSON(t, home, "project", "open", "--git", bareRemote(t, "taken"), "--reclone")
	wantError(t, v, exit, codePathExists, exitRejected)
	wantHint(t, v, "--name")

	v, exit = wdxJSON(t, home, "project", "open", "--git", filepath.Join(home, "nope.git"), "--reclone")
	wantError(t, v, exit, codeCloneFailed, exitRejected)
	if output, _ := v.Error.Details["output"].(string); !strings.Contains(output, "nope.git") {
		t.Fatalf("want git's output in details, got %+v", v.Error.Details)
	}
}
