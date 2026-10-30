package tide

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// bareRemote returns a bare repository named name.git, with one commit, to
// clone from.
func bareRemote(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	repo, bare := filepath.Join(dir, name), filepath.Join(dir, name+".git")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "first"},
		{"clone", "-q", "--bare", repo, bare},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return bare
}

func TestClone(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	url := bareRemote(t, "api")
	m := h.start(t)
	keys(m, "down", "enter", "enter")
	wantText(t, m, "[Open Project › Clone]", "type  enter submit", "type a URL")
	m.Update(tea.PasteMsg{Content: url + "\n"})
	keys(m, "enter")
	if m.shown != "api" {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
	clone := filepath.Join(h.home, "projects", "api")
	if !exists(filepath.Join(clone, ".git")) {
		t.Fatalf("no clone in %s", clone)
	}

	// Its checkout gone, reopening it offers to clone it again.
	ctx := context.Background()
	if _, err := h.client().UpdateProject(ctx, "api", api.ProjectStateClosed, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	keys(m, "C-q", "o", "down", "down", "down", "enter")
	wantText(t, m, "[Open Project › api]", "no checkout", "▸ clone it again", "  remove it from wdx")
	keys(m, "enter")
	if m.shown != "api" || !exists(filepath.Join(clone, ".git")) {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
}

func TestCloneFails(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	m := h.start(t)
	keys(m, "down", "enter", filepath.Join(t.TempDir(), "none.git"), "enter")
	wantText(t, m, "[Open Project › Clone]", "none.git_")
	if m.shown != "" || !strings.Contains(text(m), "clon") {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
}

func TestCloneNameTaken(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	url := bareRemote(t, "api")
	m := h.start(t)
	keys(m, "esc", "down", "enter", url, "enter")
	wantText(t, m, "[Open Project › Clone]", "is already", "Another name", "> _")
	keys(m, "esc") // back to the URL
	wantText(t, m, "api.git_")
	keys(m, "enter", "api-2", "enter")
	if m.shown != "api-2" {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	m := h.start(t)
	// Created where projects are, or the closest directory that exists.
	m.projectsDir = filepath.Join(h.home, "projects")
	keys(m, "down", "down", "enter")
	wantText(t, m, "[Open Project › Create]", "▸ create the project here", "enter choose")
	if b := m.top().(*dirBrowser); b.dir != h.home {
		t.Fatalf("browser in %s", b.dir)
	}
	keys(m, "enter", "enter")
	wantText(t, m, "Name of the project, created in", "type a name")
	keys(m, "web", "enter")
	if m.shown != "web" || !exists(filepath.Join(h.home, "web", ".git")) {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
}

func TestCreateConflict(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "web", api.ProjectStateClosed)
	m := h.start(t)
	m.projectsDir = t.TempDir()
	keys(m, "down", "down", "enter", "enter", "web", "enter")
	wantText(t, m, "[Open Project › Create]", "is already", "▸ open that project", "  try another name", "  cancel")

	keys(m, "down", "enter") // another name
	wantText(t, m, "> web_")
	keys(m, "enter", "down", "down", "enter") // cancel
	wantText(t, m, "[Open Project]", "closed projects")

	keys(m, "down", "down", "enter", "enter", "web", "enter", "enter") // open it
	if m.shown != "web" {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
}
