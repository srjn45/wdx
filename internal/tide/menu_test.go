package tide

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// menuOf is what the menu shows, a line per part: its title and its keys.
func menuOf(m *Model) []string {
	var lines []string
	for _, p := range m.menuParts() {
		var keys []string
		for _, it := range p.items {
			keys = append(keys, shownKey(it.key))
		}
		lines = append(lines, p.title+": "+strings.Join(keys, " "))
	}
	return lines
}

// menuPlaces are the places the menu is opened from, each with what it shows.
func menuPlaces(t *testing.T) []struct {
	name string
	m    *Model
	want []string
} {
	t.Helper()
	noProject := New(context.Background(), Options{Style: style.New(false)})
	noProject.tick = noTick
	noProject.projects.loaded = true

	agents := newAgentsModel(128, 41, demoTree()...)
	agents.layout.setFocus(project)

	worktreeFile := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r", Files: []api.WorktreeFile{changed("a", api.WorktreeFileStatusM)}})
	worktreeFile.list().worktreesOpen = map[string]bool{"main": true}
	worktreeFile.moveToWorktree(1)

	codeFile := fileModel(t)
	codeFile.layout.setFocus(project)
	codeDir := fileModel(t)
	codeDir.layout.setFocus(project)
	codeDir.moveToCode(1)
	codeSelector := fileModel(t)
	codeSelector.layout.setFocus(project)
	codeSelector.moveToCode(0)

	agentTab, _ := tabsModel(2)
	agentTab.layout.setFocus(agent)
	settings := settingsModel()
	stats := statsModel(nil)

	terminalTab, _ := newModel(80, 24)
	terminalTab.layout.setFocus(terminal)
	terminalTab.following.project.Worktrees = []api.Worktree{{Path: "/work/demo"}} // a checkout, for a and t

	projectMenu, _ := newModel(80, 24)
	projectMenu.following.project.State = api.ProjectStateOpen
	projectMenu.openMenu(true)

	return []struct {
		name string
		m    *Model
		want []string
	}{
		{"no project", noProject, []string{"global: p q"}},
		{"an agent in the list", agents, []string{"a-parent (agent): o x c i r R -", "list: / f z", "global: p s S q"}},
		{"a worktree's file", worktreeFile, []string{"a (file): o v", "list: / f z", "global: p s S q"}},
		{"a file in Code", codeFile, []string{"dir/a.go (file): o e n N r x", "list: / f z", "global: p s S q"}},
		{"a directory in Code", codeDir, []string{"dir (directory): n N r x", "list: / f z", "global: p s S q"}},
		{"Code's worktree selector", codeSelector, []string{"main (worktree): n N", "list: / f z", "global: p s S q"}},
		{"an agent's tab", agentTab, []string{"tab-0 (agent): x c i r R -", "global: p s S q"}},
		{"the settings tab", settings, []string{"settings: -", "global: p S q"}},
		{"the stats tab", stats, []string{"stats: -", "global: p s q"}},
		{"a terminal's tab", terminalTab, []string{"shell (terminal): x", "global: a t p s S q"}},
		{"the Project submenu", projectMenu, []string{"Project: o s c x"}},
	}
}

// TestMenuParts: from each place the menu shows the parts that apply, the
// narrowest first, each under its title.
func TestMenuParts(t *testing.T) {
	t.Parallel()
	for _, p := range menuPlaces(t) {
		if got := menuOf(p.m); !slices.Equal(got, p.want) {
			t.Errorf("%s: got %q, want %q", p.name, got, p.want)
		}
	}
}

// TestMenuLettersOnce: no two lines shown together share a letter, as the left
// pane takes them without C-q.
func TestMenuLettersOnce(t *testing.T) {
	t.Parallel()
	for _, p := range menuPlaces(t) {
		seen := map[string]bool{}
		for _, it := range p.m.menuItems() {
			if seen[it.key] {
				t.Errorf("%s: %q twice in %q", p.name, it.key, menuOf(p.m))
			}
			seen[it.key] = true
		}
	}
}

// TestMenuOldLetters: the letters the menu used to have for what moved do nothing;
// o, once open project, is now open and go.
func TestMenuOldLetters(t *testing.T) {
	t.Parallel()
	for _, p := range menuPlaces(t) {
		if p.name == "the Project submenu" {
			continue
		}
		for _, old := range []string{"w", "d"} {
			if p.m.menuOffers(old) {
				t.Errorf("%s: %q still offered", p.name, old)
			}
		}
		if p.m.menuOffers("n") && !strings.Contains(p.name, "Code") {
			t.Errorf("%s: n still offered", p.name)
		}
		if p.m.menuOffers(refreshKey) && p.m.layout.focus != project {
			t.Errorf("%s: f offered out of the left pane", p.name)
		}
	}
}
