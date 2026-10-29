package tide

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// tree makes the directories of paths under a new directory, which it
// returns; a path ending in /.git is made a git repository's.
func tree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		dir := filepath.Join(root, p)
		if repo, ok := strings.CutSuffix(dir, "/.git"); ok {
			if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// browse starts tide on h in dir, a directory under the user's home home,
// and opens the directory browser.
func (h testHome) browse(t *testing.T, dir, home string) *Model {
	t.Helper()
	m := h.start(t)
	m.workDir, m.userHome = dir, home
	if m.switcher != nil {
		keys(m, "esc")
	}
	keys(m, "enter")
	if _, ok := m.top().(*dirBrowser); !ok {
		t.Fatalf("no browser on top:\n%s", text(m))
	}
	return m
}

func TestBrowser(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	root := tree(t, "api/.git", "beta/inner", "Bravo", "abc", ".hidden")
	m := h.browse(t, root, filepath.Dir(root))
	wantText(t, m, "type to filter  → into  ← up", "[Open Project › Local]", "~/"+filepath.Base(root), "▸ abc/", "  api/ (git)", "  beta/", "  Bravo/")
	if got := text(m); strings.Contains(got, ".hidden") || strings.Contains(got, "notes") || strings.Contains(got, openHere) {
		t.Fatalf("listed what it should not:\n%s", got)
	}

	// Typing filters, ignoring case, the names it starts first.
	keys(m, "B")
	if got := m.top().(*dirBrowser).items(); strings.Join(got, " ") != "beta Bravo abc" {
		t.Fatalf("b matches %v", got)
	}
	keys(m, "backspace", ".")
	wantText(t, m, "> ._", "▸ .hidden/")

	// Into a directory and out again, which stays selected.
	keys(m, "backspace", "down", "down", "right")
	wantText(t, m, "~/"+filepath.Base(root)+"/beta", "▸ inner/")
	keys(m, "left")
	wantText(t, m, "▸ beta/")
	keys(m, "end")
	wantText(t, m, "▸ Bravo/")
	keys(m, "home", "backspace") // up, with nothing typed
	wantText(t, m, "▸ "+filepath.Base(root)+"/")

	keys(m, "esc")
	wantText(t, m, "[Open Project]")
}

func TestOpenLocal(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	root := tree(t, "api/.git", "web/.git")
	if err := os.MkdirAll(filepath.Join(root, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	// enter on a repository opens it.
	m := h.browse(t, root, "")
	keys(m, "enter")
	if m.shown != "api" {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
	wantText(t, m, "[Project: api]")

	// Inside one, the first item opens the repository it is in.
	m = h.browse(t, filepath.Join(root, "web", "src"), "")
	wantText(t, m, "▸ "+openHere)
	keys(m, "enter")
	if m.shown != "web" {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
	list, err := h.client().ListProjects(t.Context(), api.ProjectStateOpen)
	if err != nil || len(list.Projects) != 2 {
		t.Fatalf("open projects: %+v, %v", list.Projects, err)
	}
}

func TestOpenLocalFails(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateClosed)
	root := tree(t, "api/.git")
	m := h.browse(t, root, "")
	keys(m, "enter")
	if m.shown != "" {
		t.Fatalf("shown %q", m.shown)
	}
	wantText(t, m, "[Open Project › Local]", "▸ api/ (git)", "is already")
	keys(m, "down") // the failure goes with the next key
	if strings.Contains(text(m), "is already") {
		t.Fatalf("the failure stayed:\n%s", text(m))
	}
}

func TestBrowserUnreadable(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	h := newTestHome(t)
	h.serve(t)
	root := tree(t, "locked")
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // or t.TempDir cannot remove it
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Error(err)
		}
	})
	m := h.browse(t, root, "")
	keys(m, "enter")
	wantText(t, m, "/locked", "cannot read it: permission", "denied")
	keys(m, "left")
	wantText(t, m, "▸ locked/")
}

func TestTildePath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ path, home, want string }{
		{"/home/u", "/home/u", "~"},
		{"/home/u/dev", "/home/u", "~/dev"},
		{"/home/user2", "/home/u", "/home/user2"},
		{"/home/u", "", "/home/u"},
		{"/etc", "/", "/etc"},
	} {
		if got := tildePath(tc.path, tc.home); got != tc.want {
			t.Errorf("tildePath(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}
