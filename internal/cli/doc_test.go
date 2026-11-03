package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// stdin runs a command with text as its stdin, which is not a terminal.
func stdin(text string) func(*Env) {
	return func(env *Env) { env.Stdin = strings.NewReader(text) }
}

// both applies every adjustment in turn.
func both(adjust ...func(*Env)) func(*Env) {
	return func(env *Env) {
		for _, a := range adjust {
			a(env)
		}
	}
}

// writeDocFile writes content to name in a new directory and returns its path.
func writeDocFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newDoc creates a doc named name holding content in the project dir is in.
func newDoc(t *testing.T, home, dir, name, content string, flags ...string) api.Doc {
	t.Helper()
	args := append([]string{"doc", "create", "--name", name, "--path", "-"}, flags...)
	v, exit := wdxJSONWith(t, home, both(in(dir), stdin(content)), args...)
	if exit != api.ExitOK || v.Result != "created" || v.Doc == nil {
		t.Fatalf("create doc %s: exit %d, %+v", name, exit, v)
	}
	return *v.Doc
}

func TestDocCreate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newProject(t, home, "lib")

	file := writeDocFile(t, "api_design.txt", "the design\n")
	v, exit := wdxJSONWith(t, home, in(app), "doc", "create", "--path", file, "--labels", "rfc,auth")
	if exit != api.ExitOK || v.Result != "created" || v.Doc.Name != "api_design" || v.Doc.Format != "txt" || v.Doc.Size != 11 ||
		v.Doc.ProjectName != "app" || !slices.Equal(v.Doc.Labels, []string{"auth", "rfc"}) || v.Doc.Status != api.DocStatusWip {
		t.Fatalf("from a file: exit %d, %+v", exit, v.Doc)
	}
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("text")), "doc", "create", "--project-name", "lib", "--name", "notes", "--path", "-", "--status", "review")
	if exit != api.ExitOK || v.Doc.ProjectName != "lib" || v.Doc.Format != "md" || v.Doc.Size != 4 || v.Doc.Status != api.DocStatusReview {
		t.Fatalf("from stdin in another project: exit %d, %+v", exit, v.Doc)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "create", "--name", "empty", "--format", "")
	if exit != api.ExitOK || v.Doc.Size != 0 || v.Doc.Format != "" {
		t.Fatalf("empty, with no format: exit %d, %+v", exit, v.Doc)
	}

	v, exit = wdxJSONWith(t, home, in(app), "doc", "create", "--path", file)
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "choose another --name, or change the doc with wdx doc update")
	v, exit = wdxJSONWith(t, home, in(app), "doc", "create", "--path", writeDocFile(t, "my notes.md", "x"))
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	wantHint(t, v, "the name comes from the file: pass --name")
	v, exit = wdxJSONWith(t, home, in(app), "doc", "create", "--path", writeDocFile(t, "binary.md", "\xff\xfe"))
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "doc", "create", "--path", filepath.Join(app, "missing.md"))
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("x")), "doc", "create", "--path", "-")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "doc", "create", "--name", "x")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")
}

func TestDocGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	d := newDoc(t, home, app, "auth", "one\n")
	newDocRevision(t, home, app, "auth", 1, "two\n")

	v, exit := wdxJSONWith(t, home, in(app), "doc", "get", "--name", "auth")
	if exit != api.ExitOK || *v.Content != "two\n" || *v.Revision != 2 || v.Doc.Id != d.Id {
		t.Fatalf("the latest: exit %d, %+v", exit, v)
	}
	out := wdxWith(t, home, in(app), "doc", "get", "--id", d.Id, "--revision", "1")
	if out.exit != api.ExitOK || out.stdout != "one\n" {
		t.Fatalf("revision 1 as text: %+v", out)
	}

	copy := filepath.Join(t.TempDir(), "auth.md")
	v, exit = wdxJSONWith(t, home, in(app), "doc", "get", "--name", "auth", "--out", copy)
	if exit != api.ExitOK || v.Content != nil || *v.Revision != 2 {
		t.Fatalf("--out: exit %d, %+v", exit, v)
	}
	if b, err := os.ReadFile(copy); err != nil || string(b) != "two\n" {
		t.Fatalf("the copy: %q, %v", b, err)
	}
	if info, err := os.Stat(copy); err != nil || info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("want the copy writable: %v, %v", info.Mode(), err)
	}
	if err := os.WriteFile(copy, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "get", "--name", "auth", "--out", copy)
	wantError(t, v, exit, api.ErrorCodePathExists, api.ExitRejected)
	wantHint(t, v, "pass --overwrite to replace it")
	if b, _ := os.ReadFile(copy); string(b) != "edited" {
		t.Fatalf("an existing file was replaced: %q", b)
	}
	if _, exit = wdxJSONWith(t, home, in(app), "doc", "get", "--name", "auth", "--out", copy, "--revision", "1", "--overwrite"); exit != api.ExitOK {
		t.Fatalf("--overwrite: exit %d", exit)
	}
	if b, _ := os.ReadFile(copy); string(b) != "one\n" {
		t.Fatalf("after --overwrite: %q", b)
	}

	v, exit = wdxJSONWith(t, home, in(app), "doc", "get", "--name", "auth", "--revision", "3")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "doc", "get", "--name", "auth", "--project-name", "app", "--overwrite")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "doc", "get", "--name", "auth")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the doc's project, or pass --project-name or --project-id")
}

// newDocRevision writes content as the revision after base of the doc named
// name in the project dir is in.
func newDocRevision(t *testing.T, home, dir, name string, base int, content string) {
	t.Helper()
	file := writeDocFile(t, name+".md", content)
	v, exit := wdxJSONWith(t, home, in(dir), "doc", "update", "--name", name, "--path", file, "--base", strconv.Itoa(base))
	if exit != api.ExitOK || v.Result != "updated" {
		t.Fatalf("update doc %s: exit %d, %+v", name, exit, v)
	}
}

func TestDocUpdate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newDoc(t, home, app, "auth", "one", "--status", "approved")

	v, exit := wdxJSONWith(t, home, both(in(app), stdin("two")), "doc", "update", "--name", "auth", "--path", "-", "--base", "1")
	if exit != api.ExitOK || v.Result != "updated" || v.Doc.Revision != 2 || v.Doc.Status != api.DocStatusWip || *v.PreviousStatus != api.DocStatusApproved {
		t.Fatalf("updated: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("two")), "doc", "update", "--name", "auth", "--path", "-", "--base", "2")
	if exit != api.ExitOK || v.Result != "unchanged" || v.Doc.Revision != 2 || v.PreviousStatus != nil {
		t.Fatalf("unchanged: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("three")), "doc", "update", "--name", "auth", "--path", "-", "--base", "1")
	wantError(t, v, exit, api.ErrorCodeDocChanged, api.ExitRejected)
	wantHint(t, v, "get it again with wdx doc get --name auth --out <file> --overwrite, apply your change, and update with --base 2")
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("\xff")), "doc", "update", "--name", "auth", "--path", "-", "--base", "2")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("x")), "doc", "update", "--name", "nope", "--path", "-", "--base", "1")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("x")), "doc", "update", "--name", "auth", "--path", "-")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
}

func TestDocList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newProject(t, home, "lib")
	newDoc(t, home, app, "zeta", "", "--labels", "auth,rfc")
	newDoc(t, home, app, "alpha", "", "--labels", "auth", "--status", "approved")

	names := func(args ...string) []string {
		t.Helper()
		v, exit := wdxJSONWith(t, home, in(app), append([]string{"doc", "list"}, args...)...)
		if exit != api.ExitOK || v.Docs == nil {
			t.Fatalf("list %v: exit %d, %+v", args, exit, v)
		}
		var names []string
		for _, d := range v.Docs {
			names = append(names, d.Name)
		}
		return names
	}
	if got := names(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Errorf("all: %q", got)
	}
	if got := names("--labels", "auth,rfc"); !slices.Equal(got, []string{"zeta"}) {
		t.Errorf("both labels: %q", got)
	}
	if got := names("--status", "approved"); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("approved: %q", got)
	}
	if got := names("--project-name", "lib"); len(got) != 0 {
		t.Errorf("another project: %q", got)
	}
	v, exit := wdxJSONWith(t, home, in(app), "doc", "list", "--labels", "Auth")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "doc", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestDocHistory(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newDoc(t, home, app, "auth", "one")
	newDocRevision(t, home, app, "auth", 1, "two!")

	v, exit := wdxJSONWith(t, home, in(app), "doc", "history", "--name", "auth")
	if exit != api.ExitOK || len(v.Revisions) != 2 || v.Revisions[0].Number != 2 || v.Revisions[0].Size != 4 || v.Revisions[1].Size != 3 {
		t.Fatalf("history: exit %d, %+v", exit, v.Revisions)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "history", "--name", "nope")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestDocSet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newDoc(t, home, app, "auth", "", "--labels", "draft")

	v, exit := wdxJSONWith(t, home, in(app), "doc", "set", "--name", "auth", "--status", "review", "--add-labels", "rfc,auth", "--remove-labels", "draft")
	if exit != api.ExitOK || v.Result != "updated" || v.Doc.Status != api.DocStatusReview || !slices.Equal(v.Doc.Labels, []string{"auth", "rfc"}) {
		t.Fatalf("set: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "set", "--name", "auth", "--status", "review")
	if exit != api.ExitOK || v.Result != "unchanged" {
		t.Fatalf("setting what it has: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "set", "--name", "auth", "--add-labels", "RFC")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "doc", "set", "--name", "auth", "--status", "done")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "doc", "set", "--name", "auth")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "doc", "set", "--name", "nope", "--status", "wip")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestDocRename(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	d := newDoc(t, home, app, "auth", "")
	newDoc(t, home, app, "design", "")

	v, exit := wdxJSONWith(t, home, in(app), "doc", "rename", "--name", "auth", "--new-name", "auth-rfc")
	if exit != api.ExitOK || v.Result != "updated" || v.Doc.Name != "auth-rfc" || v.Doc.Id != d.Id {
		t.Fatalf("rename: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "rename", "--id", d.Id, "--new-name", "auth-rfc")
	if exit != api.ExitOK || v.Result != "unchanged" {
		t.Fatalf("to its name: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "rename", "--name", "auth-rfc", "--new-name", "Design")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "choose another --new-name")
	v, exit = wdxJSONWith(t, home, in(app), "doc", "rename", "--name", "auth-rfc", "--new-name", "a b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "doc", "rename", "--name", "auth", "--new-name", "x")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestDocRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	d := newDoc(t, home, app, "auth", "one")
	newDocRevision(t, home, app, "auth", 1, "two")
	newDoc(t, home, app, "design", "")

	v, exit := wdxJSONWith(t, home, in(app), "doc", "remove", "--name", "auth")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	out := wdxWith(t, home, in(app), "doc", "remove", "--name", "auth")
	if out.exit != api.ExitUsage || !strings.Contains(out.stderr, "pass --yes") {
		t.Fatalf("without a terminal or --yes: %+v", out)
	}

	out = wdxWith(t, home, both(in(app), answering(t, "n")), "doc", "remove", "--name", "auth")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "remove doc auth and its 2 revisions? [y/N]: ") || !strings.Contains(out.stderr, "[cancelled]") {
		t.Fatalf("answered no: %+v", out)
	}
	out = wdxWith(t, home, both(in(app), answering(t, "y")), "doc", "remove", "--name", "auth")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "doc auth removed") {
		t.Fatalf("answered yes: %+v", out)
	}

	v, exit = wdxJSONWith(t, home, in(app), "doc", "remove", "--name", "design", "--yes")
	if exit != api.ExitOK || v.Result != "removed" || v.Doc.Name != "design" {
		t.Fatalf("--yes: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "doc", "remove", "--id", d.Id, "--yes")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestDocText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	file := writeDocFile(t, "auth.md", "one")

	for _, c := range []struct {
		args   []string
		stdout string
		stderr string
	}{
		{[]string{"doc", "create", "--path", file, "--labels", "rfc", "--status", "approved"}, "doc auth created\n", ""},
		{[]string{"doc", "list"}, "NAME", ""},
		{[]string{"doc", "update", "--name", "auth", "--path", writeDocFile(t, "new.md", "two"), "--base", "1"}, "auth is at revision 2\n", "auth was approved; it is wip again\n"},
		{[]string{"doc", "update", "--name", "auth", "--path", writeDocFile(t, "same.md", "two"), "--base", "2"}, "auth is unchanged at revision 2\n", ""},
		{[]string{"doc", "history", "--name", "auth"}, "REVISION", ""},
		{[]string{"doc", "get", "--name", "auth"}, "two", ""},
		{[]string{"doc", "get", "--name", "auth", "--out", filepath.Join(t.TempDir(), "a.md")}, "revision 2 of auth written to ", ""},
		{[]string{"doc", "set", "--name", "auth", "--status", "review", "--remove-labels", "rfc"}, "doc auth is review, with no labels\n", ""},
		{[]string{"doc", "rename", "--name", "auth", "--new-name", "auth-rfc"}, "doc auth renamed to auth-rfc\n", ""},
		{[]string{"doc", "remove", "--name", "auth-rfc", "--yes"}, "doc auth-rfc removed\n", ""},
		{[]string{"doc", "list"}, "no docs\n", ""},
	} {
		out := wdxWith(t, home, in(app), c.args...)
		if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, c.stdout) || out.stderr != c.stderr {
			t.Errorf("%v: want stdout from %q and stderr %q, got %+v", c.args, c.stdout, c.stderr, out)
		}
	}
}

func TestDocSyncToGit(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newDoc(t, home, app, "auth", "auth")
	newDoc(t, home, app, "design", "")
	sub := filepath.Join(app, "src")
	if err := os.MkdirAll(filepath.Join(app, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "docs", "design.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run in a subdirectory, the path is still the repository's.
	v, exit := wdxJSONWith(t, home, in(sub), "doc", "sync-to-git", "--path", "docs")
	if exit != api.ExitOK || v.Result != "synced" || v.Path != filepath.Join(app, "docs") ||
		!slices.Equal(v.Written, []string{"auth.md"}) || !slices.Equal(v.Skipped, []string{"design.md"}) || len(v.Removed) != 0 {
		t.Fatalf("sync: exit %d, %+v", exit, v)
	}
	if b, err := os.ReadFile(filepath.Join(app, "docs", "auth.md")); err != nil || string(b) != "auth" {
		t.Fatalf("the file: %q, %v", b, err)
	}
	out := wdxWith(t, home, in(app), "doc", "sync-to-git", "--path", "docs")
	if out.exit != api.ExitOK || out.stdout != "1 doc written to docs\nskipped design.md, as a file wdx did not write has the name\n" {
		t.Fatalf("text: %+v", out)
	}

	v, exit = wdxJSONWith(t, home, in(app), "doc", "sync-to-git", "--path", "../out")
	wantError(t, v, exit, api.ErrorCodeInvalidPath, api.ExitRejected)
	wantHint(t, v, "give a directory relative to the project's main checkout, outside .git")
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "doc", "sync-to-git", "--path", "docs")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "doc", "sync-to-git")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	if err := os.RemoveAll(app); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "doc", "sync-to-git", "--project-name", "app", "--path", "docs")
	wantError(t, v, exit, api.ErrorCodeCheckoutMissing, api.ExitRejected)
}
