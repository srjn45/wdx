package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/config"
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
	cfg := config.Default()
	cfg.Home = home
	cfg.LogLevel = slog.LevelDebug
	var stdout, stderr bytes.Buffer
	env := Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Config: cfg}
	exit := Run(context.Background(), args, env)
	out := output{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	wantLogged(t, home, args, out)
	return out
}

type logLine struct {
	Time     string          `json:"time"`
	Level    string          `json:"level"`
	Msg      string          `json:"msg"`
	DebugID  string          `json:"debug_id"`
	Exit     *int            `json:"exit"`
	Response json.RawMessage `json:"response"`
}

// wantLogged checks that the run that produced out is logged in home from the
// command line it received to the response it returned, and that every line
// in the log is a JSON object tagged with a debug id.
func wantLogged(t *testing.T, home string, args []string, out output) {
	t.Helper()
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return // nowhere to log: the run must still work, which its test checks
	}
	files, err := filepath.Glob(filepath.Join(home, "logs", "wdx-*.log"))
	if err != nil || len(files) == 0 {
		t.Fatalf("%v: no log file in %s", args, home)
	}
	var lines []logLine
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for raw := range strings.Lines(string(b)) {
			var l logLine
			if err := json.Unmarshal([]byte(raw), &l); err != nil || l.Time == "" || l.Level == "" || l.Msg == "" || l.DebugID == "" {
				t.Fatalf("%v: bad log line %q: %v", args, raw, err)
			}
			lines = append(lines, l)
		}
	}

	// Runs in one home are sequential, so the last one received is this one.
	var id string
	for _, l := range lines {
		if l.Msg == "command received" {
			id = l.DebugID
		}
	}
	var run []logLine
	for _, l := range lines {
		if l.DebugID == id {
			run = append(run, l)
		}
	}
	first, last := run[0], run[len(run)-1]
	if first.Msg != "command received" || last.Msg != "command returned" {
		t.Fatalf("%v: want the run logged from received to returned, got %q to %q", args, first.Msg, last.Msg)
	}
	if last.Exit == nil || *last.Exit != out.exit || (last.Level == "ERROR") != (out.exit != exitOK) {
		t.Fatalf("%v: logged exit %v at %s, want exit %d", args, last.Exit, last.Level, out.exit)
	}
	if string(last.Response) == "null" || len(last.Response) == 0 {
		t.Fatalf("%v: no response logged", args)
	}
	if slices.Contains(args, "--json") {
		var logged, printed any
		json.Unmarshal(last.Response, &logged)
		json.Unmarshal([]byte(out.stdout), &printed)
		if !reflect.DeepEqual(logged, printed) {
			t.Fatalf("%v: logged response differs from stdout:\n%s\n%s", args, last.Response, out.stdout)
		}
		if out.exit != exitOK && !strings.Contains(out.stdout, `"debug_id": "`+id+`"`) {
			t.Fatalf("%v: want debug_id %s in the error, got %s", args, id, out.stdout)
		}
	} else if out.exit != exitOK && !strings.HasSuffix(out.stderr, " (debug_id "+id+")\n") {
		t.Fatalf("%v: want debug_id %s on the error line, got %q", args, id, out.stderr)
	}
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
		Orphaned  *bool   `json:"orphaned"`
		Worktrees []struct {
			ID      string `json:"id"`
			Path    string `json:"path"`
			Present *bool  `json:"present"`
		} `json:"worktrees"`
	} `json:"project"`
	Projects []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		State         string `json:"state"`
		WorktreeCount int    `json:"worktree_count"`
	} `json:"projects"`
	Conflict *struct {
		Code string `json:"code"`
	} `json:"conflict"`
	Error *struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Hint    *string        `json:"hint"`
		Details map[string]any `json:"details"`
	} `json:"error"`
	Command *struct {
		Path     string `json:"path"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Flags []struct {
			Name string `json:"name"`
		} `json:"flags"`
		FlagRules []struct {
			Rule  string   `json:"rule"`
			Flags []string `json:"flags"`
			When  *string  `json:"when"`
		} `json:"flag_rules"`
		Actions []string `json:"actions"`
		Errors  []struct {
			Code string `json:"code"`
			Exit int    `json:"exit"`
		} `json:"errors"`
	} `json:"command"`
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
	wantDeclared(t, args, v)
	return v, out.exit
}

// wantDeclared checks that the help of the command args ran lists the action
// and error codes it returned.
func wantDeclared(t *testing.T, args []string, v jsonOutput) {
	t.Helper()
	cmd, _, err := newRoot(config.Default()).Find(args)
	if err != nil {
		return
	}
	help := newCommandView(cmd)
	var codes []string
	for _, e := range help.Errors {
		codes = append(codes, e.Code)
	}
	if v.Action != "" && !slices.Contains(help.Actions, v.Action) {
		t.Fatalf("%v: action %q is not in the help of %s", args, v.Action, help.Path)
	}
	if v.Error != nil && !slices.Contains(codes, v.Error.Code) {
		t.Fatalf("%v: error %q is not in the help of %s", args, v.Error.Code, help.Path)
	}
	if v.Conflict != nil && !slices.Contains(codes, v.Conflict.Code) {
		t.Fatalf("%v: conflict %q is not in the help of %s", args, v.Conflict.Code, help.Path)
	}
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
	v, exit := wdxJSON(t, home, "project", "create", "--name", name, "--cancel")
	if exit != exitOK {
		t.Fatalf("create %s: %+v", name, v.Error)
	}
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

	v, exit := wdxJSON(t, home, "project", "close", "--id", id)
	if exit != exitOK || v.Action != "closed" || v.Project.ID != id || v.Project.State != "closed" {
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
	home := t.TempDir()
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "remove", "--id", id)
	if exit != exitOK || v.Action != "removed" || v.Project.ID != id {
		t.Fatalf("unexpected remove: %+v", v)
	}
	v, exit = wdxJSON(t, home, "project", "remove", "--name", "app")
	wantError(t, v, exit, codeNotFound, exitRejected)
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"close", "--id", "nope"},
		{"close", "--name", "nope"},
		{"remove", "--id", "nope"},
		{"remove", "--name", "nope"},
	} {
		v, exit := wdxJSON(t, t.TempDir(), append([]string{"project"}, args...)...)
		wantError(t, v, exit, codeNotFound, exitRejected)
		if v.Error.Hint != nil || v.Error.Details != nil {
			t.Fatalf("%v: want null hint and details, got %+v", args, v.Error)
		}
	}
	out := wdx(t, t.TempDir(), "project", "close", "--name", "nope")
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
		{"project", "close", "app"},
		{"project", "close", "--id", "x", "--name", "y"},
		{"project", "remove"},
		{"project", "remove", "app"},
		{"project", "remove", "--id", "x", "--name", "y"},
		{"version", "--bogus"},
		{"project", "create"},
		{"project", "create", "--name", "app", "--open", "--cancel"},
		{"project", "open", "--local", ".", "--git", "x"},
		{"project", "list", "--open", "--closed"},
		{"project", "list", "app"},
		{"project", "get"},
		{"project", "get", "--id", "x", "--name", "y"},
		{"project", "get", "app"},
	} {
		v, exit := wdxJSON(t, t.TempDir(), args...)
		wantError(t, v, exit, codeUsage, exitUsage)
		wantHint(t, v, "--help'")
	}
	v, _ := wdxJSON(t, t.TempDir(), "project", "create", "--name", "app")
	wantHint(t, v, "pass --open or --cancel; see 'wdx project create --help'")
	out := wdx(t, t.TempDir(), "project", "bogus")
	if out.exit != exitUsage || out.stdout != "" || !strings.Contains(out.stderr, "; see 'wdx project --help' (debug_id dbg-") {
		t.Fatalf("unexpected text usage error: %+v", out)
	}
}

func TestSystemError(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(home, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v, exit := wdxJSON(t, home, "project", "close", "--name", "app")
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
	v, exit := wdxJSON(t, home, "project", "close", "--id", first.Project.ID)
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

func TestCreate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	v, exit := wdxJSON(t, home, "project", "create", "--name", "app", "--cancel")
	want := filepath.Join(home, "projects", "app")
	if exit != exitOK || v.Action != "created" || v.Conflict != nil || v.Project.Worktrees[0].Path != want {
		t.Fatalf("unexpected create: %+v", v)
	}
	if out := wdx(t, home, "project", "create", "--name", "web"); !strings.HasPrefix(out.stdout, "created project web (") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestCreateJSONRequiresAnswer(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	v, exit := wdxJSON(t, home, "project", "create", "--name", "app")
	wantError(t, v, exit, codeUsage, exitUsage)
	wantHint(t, v, "--open or --cancel")
	if _, err := os.Stat(filepath.Join(home, "projects", "app")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be created: %v", err)
	}
}

func TestCreateNameTaken(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	id := newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "project", "create", "--name", "APP", "--cancel")
	wantError(t, v, exit, codeNameTaken, exitRejected)
	wantHint(t, v, "--name")
	if p, _ := v.Error.Details["project"].(map[string]any); p["id"] != id {
		t.Fatalf("want details.project %s, got %+v", id, v.Error.Details)
	}

	v, exit = wdxJSON(t, home, "project", "create", "--name", "APP", "--open")
	if exit != exitOK || v.Action != "opened" || v.Project.ID != id || v.Conflict == nil || v.Conflict.Code != codeNameTaken {
		t.Fatalf("want %s opened after name_taken, got %+v", id, v)
	}

	out := wdx(t, home, "project", "create", "--name", "app")
	if out.exit != exitRejected || !strings.HasPrefix(out.stderr, "wdx: not created: ") || !strings.Contains(out.stderr, "pass --open or --cancel") {
		t.Fatalf("unexpected text error: %+v", out)
	}
	out = wdx(t, home, "project", "create", "--name", "app", "--open")
	if !strings.HasPrefix(out.stdout, "opened existing project app ("+id+"); nothing was created\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestCreateRepoExists(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := gitRepo(t, "app")

	v, exit := wdxJSON(t, home, "project", "create", "--name", "app", "--path", filepath.Dir(repo), "--cancel")
	wantError(t, v, exit, codeRepoExists, exitRejected)
	wantHint(t, v, "--path")
	if v.Error.Details["root"] != repo || v.Error.Details["project"] != nil {
		t.Fatalf("want unknown repo %s in details, got %+v", repo, v.Error.Details)
	}

	v, exit = wdxJSON(t, home, "project", "create", "--name", "sub", "--path", repo, "--open")
	if exit != exitOK || v.Action != "registered" || v.Project.Worktrees[0].Path != repo || v.Conflict.Code != codeRepoExists {
		t.Fatalf("want %s registered after repo_exists, got %+v", repo, v)
	}
	if _, err := os.Stat(filepath.Join(repo, "sub")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be created: %v", err)
	}
}

func TestCreateOpenCheckoutMissing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	id := newProject(t, home, "app")
	if err := os.RemoveAll(filepath.Join(home, "projects", "app")); err != nil {
		t.Fatal(err)
	}

	v, exit := wdxJSON(t, home, "project", "create", "--name", "app", "--open")
	wantError(t, v, exit, codeCheckoutMissing, exitRejected)
	wantHint(t, v, "project remove --id "+id)
}

func TestCreateInvalid(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	v, exit := wdxJSON(t, home, "project", "create", "--name", "../app", "--cancel")
	wantError(t, v, exit, codeInvalidName, exitRejected)
	wantHint(t, v, "--name")

	v, exit = wdxJSON(t, home, "project", "create", "--name", "id:app", "--cancel")
	wantError(t, v, exit, codeInvalidName, exitRejected)

	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSON(t, home, "project", "create", "--name", "app", "--path", file, "--cancel")
	wantError(t, v, exit, codeInvalidPath, exitRejected)
	wantHint(t, v, "--path")

	if out := wdx(t, home, "project", "create"); out.exit != exitUsage || !strings.HasPrefix(out.stderr, "wdx: not created: ") {
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
	home := t.TempDir()
	for filter, text := range map[string]string{"--all": "no projects\n", "--open": "no open projects\n", "--closed": "no closed projects\n"} {
		out := wdx(t, home, "project", "list", filter, "--json")
		if out.exit != exitOK || !strings.Contains(out.stdout, `"projects": []`) {
			t.Fatalf("%s: want an empty array, got %+v", filter, out)
		}
		if out := wdx(t, home, "project", "list", filter); out.stdout != text {
			t.Fatalf("%s: want %q, got %q", filter, text, out.stdout)
		}
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, name := range []string{"cat", "app", "Beta"} {
		newProject(t, home, name)
	}
	wdxJSON(t, home, "project", "close", "--name", "cat")

	for args, want := range map[string]string{"": "app Beta cat", "--all": "app Beta cat", "--open": "app Beta", "--closed": "cat"} {
		v, exit := wdxJSON(t, home, strings.Fields("project list "+args)...)
		if exit != exitOK || v.Action != "" || names(v) != want {
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
	home := t.TempDir()
	id := newProject(t, home, "App")
	wdxJSON(t, home, "project", "close", "--id", id)

	for _, args := range [][]string{{"--id", id}, {"--name", "app"}} {
		v, exit := wdxJSON(t, home, append([]string{"project", "get"}, args...)...)
		if exit != exitOK || v.Action != "" || v.Project.ID != id || v.Project.State != "closed" {
			t.Fatalf("get %v: unexpected %+v", args, v)
		}
		p := v.Project
		if p.Orphaned == nil || *p.Orphaned || len(p.Worktrees) != 1 || p.Worktrees[0].Present == nil || !*p.Worktrees[0].Present {
			t.Fatalf("get %v: want one present checkout, got %+v", args, p)
		}
	}

	out := wdx(t, home, "project", "get", "--name", "app")
	if !strings.HasPrefix(out.stdout, "project App ("+id+")\n  state:     closed\n") || strings.Contains(out.stdout, "orphaned") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestGetOrphaned(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	id := newProject(t, home, "app")
	if err := os.RemoveAll(filepath.Join(home, "projects", "app")); err != nil {
		t.Fatal(err)
	}

	v, exit := wdxJSON(t, home, "project", "get", "--name", "app")
	if exit != exitOK || !*v.Project.Orphaned || *v.Project.Worktrees[0].Present {
		t.Fatalf("want an orphaned project with its missing checkout, got %+v", v.Project)
	}
	out := wdx(t, home, "project", "get", "--name", "app")
	if !strings.Contains(out.stdout, "(missing)\n") || !strings.Contains(out.stdout, "orphaned:  no checkout on disk; remove it with wdx project remove --id "+id) {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestGetNotFound(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	id := newProject(t, home, "app")
	for _, args := range [][]string{{"--id", "proj-nope"}, {"--name", "nope"}, {"--id", "app"}, {"--name", id}} {
		v, exit := wdxJSON(t, home, append([]string{"project", "get"}, args...)...)
		wantError(t, v, exit, codeNotFound, exitRejected)
	}
}

func TestWorktreeFieldsOnlyInGet(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	newProject(t, home, "app")
	out := wdx(t, home, "project", "close", "--name", "app", "--json")
	if strings.Contains(out.stdout, `"present"`) || strings.Contains(out.stdout, `"orphaned"`) || !strings.Contains(out.stdout, `"created_at"`) {
		t.Fatalf("close must not carry disk status: %s", out.stdout)
	}
}

// TestCommandDocs checks that every command documents itself, so a new
// command fails here until its help is complete.
func TestCommandDocs(t *testing.T) {
	t.Parallel()
	for code, info := range codes {
		if info.description == "" || info.exit < exitRejected || info.exit > exitSystem {
			t.Errorf("error code %s: incomplete %+v", code, info)
		}
	}
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		path := cmd.CommandPath()
		if cmd.Short == "" {
			t.Errorf("%s: no summary", path)
		}
		if !cmd.HasAvailableSubCommands() && cmd.Annotations[outputNote] == "" {
			t.Errorf("%s: no output note", path)
		}
		for _, code := range annotationList(cmd, errorList) {
			if _, ok := codes[code]; !ok || code == codeUsage || code == codeInternal {
				t.Errorf("%s: unexpected error code %q", path, code)
			}
		}
		if cmd.Runnable() && cmd.ValidateArgs([]string{"x"}) == nil {
			t.Errorf("%s: takes a positional argument; every input must be a flag", path)
		}
		for _, r := range flagRules(cmd) {
			names := r.Flags
			if r.When != nil {
				names = append(names, *r.When)
			}
			for _, name := range names {
				if cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: rule %s names unknown flag %q", path, r.Rule, name)
				}
			}
		}
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				check(sub)
			}
		}
	}
	check(newRoot(config.Default()))
}

func TestHelp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args       []string
		want, deny []string
	}{
		{[]string{"--help"}, []string{"project ", "version ", "Exit codes:"}, []string{"completion", "\n  help "}},
		{[]string{"project", "-h"}, []string{"close ", "create ", "get ", "list ", "open ", "remove "}, []string{"Errors:"}},
		{[]string{"project", "open", "--help"}, []string{"--local", "--git", "--reclone", "Flag rules:", "Output:", "action: cloned", "checkout_missing  1", "internal          3"}, nil},
		{[]string{"help", "project", "get"}, []string{"wdx project get (--id <id> | --name <name>)", "not_found"}, nil},
	} {
		out := wdx(t, t.TempDir(), tc.args...)
		if out.exit != exitOK || out.stderr != "" {
			t.Fatalf("%v: unexpected %+v", tc.args, out)
		}
		for _, s := range tc.want {
			if !strings.Contains(out.stdout, s) {
				t.Errorf("%v: want %q in:\n%s", tc.args, s, out.stdout)
			}
		}
		for _, s := range tc.deny {
			if strings.Contains(out.stdout, s) {
				t.Errorf("%v: unexpected %q in:\n%s", tc.args, s, out.stdout)
			}
		}
	}
}

// TestHelpJSON walks the whole command tree through --help --json.
func TestHelpJSON(t *testing.T) {
	t.Parallel()
	var walk func(path []string)
	walk = func(path []string) {
		v, exit := wdxJSON(t, t.TempDir(), append(path, "--help")...)
		c := v.Command
		if exit != exitOK || c == nil || c.Path != strings.Join(append([]string{"wdx"}, path...), " ") {
			t.Fatalf("%v: unexpected help %+v", path, v)
		}
		if n := len(c.Errors); n < 2 || c.Errors[n-2].Code != codeUsage || c.Errors[n-1].Code != codeInternal {
			t.Fatalf("%v: errors must end with usage and internal: %+v", path, c.Errors)
		}
		for _, sub := range c.Commands {
			walk(append(slices.Clone(path), sub.Name))
		}
	}
	walk(nil)

	v, _ := wdxJSON(t, t.TempDir(), "help", "project", "open")
	rule := v.Command.FlagRules[len(v.Command.FlagRules)-1]
	if rule.Rule != "one_required_with_json" || *rule.When != "git" || !slices.Equal(rule.Flags, []string{"reclone", "remove"}) {
		t.Fatalf("unexpected json rule: %+v", rule)
	}
	if !slices.Equal(v.Command.Actions, []string{"cloned", "opened", "registered", "removed"}) {
		t.Fatalf("unexpected actions: %v", v.Command.Actions)
	}
}

func TestPanicIsInternalError(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "boom", RunE: func(*cobra.Command, []string) error { panic("kaboom") }}
	cmd.SetContext(context.Background())
	wrapRun(cmd)
	err := cmd.RunE(cmd, nil)
	p, ok := err.(*panicError)
	if !ok || p.Error() != "panic: kaboom" || !strings.Contains(string(p.stack), "TestPanicIsInternalError") {
		t.Fatalf("want a panicError with its stack, got %v", err)
	}
	if v, exit := classify(err); v.Code != codeInternal || exit != exitSystem {
		t.Fatalf("want internal, got %s", v.Code)
	}
}

func TestConfigError(t *testing.T) {
	t.Parallel()
	settings := map[string]string{"WDX_LOG_LEVEL": "loud", "WDX_LOG_RETENTION_DAYS": "0"}
	_, err := config.Load(config.Env(func(key string) string { return settings[key] }))
	if err == nil {
		t.Fatal("want bad settings rejected")
	}
	var stdout, stderr bytes.Buffer
	env := Env{Stdout: &stdout, Stderr: &stderr}
	if exit := ConfigError([]string{"project", "list", "--json"}, env, err); exit != exitUsage {
		t.Fatalf("want exit %d, got %d", exitUsage, exit)
	}
	var v struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Settings []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"settings"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil || stderr.Len() != 0 {
		t.Fatalf("want one JSON error, got %q, %q", stdout.String(), stderr.String())
	}
	if s := v.Error.Details.Settings; v.Error.Code != codeUsage || len(s) != 2 || s[0].Name != "WDX_LOG_LEVEL" || s[0].Value != "loud" {
		t.Fatalf("unexpected error %+v", v.Error)
	}

	stdout.Reset()
	ConfigError([]string{"version"}, env, err)
	want := `wdx: invalid settings: WDX_LOG_LEVEL: "loud" is not one of debug, info, warn, error; WDX_LOG_RETENTION_DAYS: "0" is not a whole number of at least 1; fix or unset them` + "\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("got %q, want %q", stderr.String(), want)
	}
}
