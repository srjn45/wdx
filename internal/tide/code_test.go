package tide

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/style"
)

// flatTree writes the tree as the paths in the order shown, directories with a slash.
func flatTree(nodes []*codeNode) []string {
	var out []string
	for _, n := range nodes {
		if n.dir {
			out = append(out, n.path+"/")
			out = append(out, flatTree(n.children)...)
		} else {
			out = append(out, n.path)
		}
	}
	return out
}

func TestCodeTree(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		what  string
		paths []string
		want  []string
	}{
		{"nothing", nil, nil},
		{"files by name, whatever their case", []string{"b", "README", "a"}, []string{"a", "b", "README"}},
		{"directories before files", []string{"a", "z/x"}, []string{"z/", "z/x", "a"}},
		{"nested", []string{"a/b/c", "a/b/d", "a/e", "f"}, []string{"a/", "a/b/", "a/b/c", "a/b/d", "a/e", "f"}},
		{"a name with a space and no ascii", []string{"é x/ü"}, []string{"é x/", "é x/ü"}},
	} {
		if got := flatTree(codeTree(tc.paths)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.what, got, tc.want)
		}
	}
}

func TestCodeRows(t *testing.T) {
	t.Parallel()
	nodes := codeTree([]string{"docs/a", "internal/git/x", "internal/tide/code.go", "internal/tide/tabs.go", "internal/z", "README"})
	type row struct{ guide, fold, key string }
	for _, tc := range []struct {
		what string
		open map[string]bool
		want []row
	}{
		{"all folded", nil, []row{{"", "+", "docs"}, {"", "+", "internal"}, {"", " ", "README"}}},
		{"opened", map[string]bool{"internal": true, "internal/tide": true}, []row{
			{"", "+", "docs"},
			{"", "−", "internal"},
			{"  ├ ", "+", "internal/git"},
			{"  ├ ", "−", "internal/tide"},
			{"  │   ├ ", "", "internal/tide/code.go"},
			{"  │   └ ", "", "internal/tide/tabs.go"},
			{"  └ ", "", "internal/z"},
			{"", " ", "README"},
		}},
		{"what is open inside a folded directory stays hidden", map[string]bool{"internal/tide": true}, []row{{"", "+", "docs"}, {"", "+", "internal"}, {"", " ", "README"}}},
	} {
		var got []row
		for _, r := range codeRows(nodes, tc.open) {
			got = append(got, row{r.guide, r.fold, r.key()})
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q", tc.what, got)
		}
	}
}

// codeModel is a model on these worktrees, with the main checkout's files read.
func codeModel(paths []string, worktrees ...api.WorktreeStatus) *Model {
	m := worktreesModel(worktrees...)
	m.Update(nil) // asks for them
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: paths})
	return m
}

func codeWorktreeList() []api.WorktreeStatus {
	return []api.WorktreeStatus{{Name: "main", Path: "/r"}, {Name: "gone", Path: "/g", Missing: true}, {Name: "auth-fix", Path: "/a"}, {Name: "docs", Path: "/d"}}
}

func TestCodeSelector(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"file"}, codeWorktreeList()...)
	l := m.list()
	if l.code.worktree != "main" {
		t.Fatalf("it starts on %q", l.code.worktree)
	}
	m.moveToCode(0)
	wantText(t, m, "< main >", "file")
	for _, tc := range []struct{ key, want string }{
		{"right", "auth-fix"}, // past the missing one
		{"l", "docs"},
		{"right", "main"}, // going round
		{"left", "docs"},
		{"h", "auth-fix"},
		{"left", "main"},
	} {
		keys(m, tc.key)
		if l.code.worktree != tc.want {
			t.Errorf("%s: on %q, want %q", tc.key, l.code.worktree, tc.want)
		}
		if l.section != sectionCode || l.code.row != 0 {
			t.Errorf("%s: the cursor left the selector: %+v", tc.key, l.code)
		}
	}
	keys(m, "right")
	wantText(t, m, "< auth-fix >", "loading…")
	if strings.Contains(text(m), "file") {
		t.Errorf("another worktree's files show:\n%s", text(m))
	}
}

func TestCodeMoves(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"dir/inner/deep", "dir/x", "top"}, api.WorktreeStatus{Name: "main", Path: "/r"})
	l := m.list()
	m.moveCursor(len(m.listRows()) - 1)
	keys(m, "down", "down")
	if l.section != sectionCode || l.code.row != 0 {
		t.Fatalf("down past the worktrees: section %v, %+v", l.section, l.code)
	}
	if m.layout.focus != project || !strings.Contains(statusRow(m), "←→ worktree") {
		t.Errorf("on the selector: %q", statusRow(m))
	}
	if _, ok := m.cursorAgent(); ok {
		t.Error("an agent is under the cursor in Code: C-q x would remove it")
	}
	keys(m, "down")
	if l.code.cursor != "dir" {
		t.Fatalf("under the selector: %q", l.code.cursor)
	}
	if !strings.Contains(statusRow(m), "←→ fold") {
		t.Errorf("on the tree: %q", statusRow(m))
	}
	keys(m, "enter")
	wantText(t, m, "− dir", "+ inner", "x")
	keys(m, "down", "right", "down")
	if l.code.cursor != "dir/inner/deep" {
		t.Fatalf("down the tree: %q", l.code.cursor)
	}
	keys(m, "enter")
	if !m.codeFilesShown().open["dir"] || l.code.cursor != "dir/inner/deep" {
		t.Errorf("Enter on a file folded something: %+v", l.code)
	}
	keys(m, "left")
	if l.code.cursor != "dir/inner" {
		t.Errorf("left from a file goes to its directory: %q", l.code.cursor)
	}
	keys(m, "left")
	if m.codeFilesShown().open["dir/inner"] || l.code.cursor != "dir/inner" {
		t.Errorf("left on an open directory folds it: %+v", l.code)
	}
	keys(m, "left")
	if l.code.cursor != "dir" {
		t.Errorf("left on a folded directory goes to its parent: %q", l.code.cursor)
	}
	keys(m, "enter", "left")
	if l.code.cursor != "dir" {
		t.Errorf("left at the top level goes nowhere: %q", l.code.cursor)
	}
	keys(m, "up", "up")
	if l.section != sectionWorktrees {
		t.Errorf("up from the selector: section %v", l.section)
	}
	keys(m, "up")
	if l.section != sectionAgents {
		t.Errorf("up from the first worktree: section %v", l.section)
	}
}

func TestCodeKeepsItsPlace(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"a", "b", "c"}, codeWorktreeList()...)
	l := m.list()
	m.moveToCode(2)
	if l.code.cursor != "b" {
		t.Fatalf("on %q", l.code.cursor)
	}
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{"0", "a", "b", "c"}})
	if l.code.cursor != "b" || l.code.row != 3 {
		t.Errorf("a file came above it: %+v", l.code)
	}
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{"0", "a"}})
	if l.code.cursor != "a" || l.code.row != 2 {
		t.Errorf("its file went: %+v", l.code)
	}
	failed := "cannot read the files"
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", err: context.Canceled})
	wantText(t, m, failed, "a")
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{"a"}, truncated: true})
	wantText(t, m, "too many files")
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{}})
	wantText(t, m, "no files")
	if l.code.row != 0 {
		t.Errorf("nothing left but the selector: %+v", l.code)
	}
}

func TestCodeBackToMainWhenItsWorktreeGoes(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"file"}, codeWorktreeList()...)
	l := m.list()
	m.moveToCode(0)
	keys(m, "right")
	if l.code.worktree != "auth-fix" {
		t.Fatalf("on %q", l.code.worktree)
	}
	*m.following.project.WorktreeStatuses = []api.WorktreeStatus{{Name: "main", Path: "/r"}, {Name: "docs", Path: "/d"}}
	m.worktreesChanged()
	if l.code.worktree != "main" {
		t.Errorf("removed: on %q", l.code.worktree)
	}
	m.moveToCode(0)
	keys(m, "right")
	*m.following.project.WorktreeStatuses = []api.WorktreeStatus{{Name: "main", Path: "/r"}, {Name: "docs", Path: "/d", Missing: true}}
	m.worktreesChanged()
	if l.code.worktree != "main" {
		t.Errorf("gone from disk: on %q", l.code.worktree)
	}
	*m.following.project.WorktreeStatuses = []api.WorktreeStatus{}
	m.worktreesChanged()
	if l.code.worktree != "" || l.section != sectionAgents {
		t.Errorf("none left: on %q, section %v", l.code.worktree, l.section)
	}
	wantText(t, m, "no worktrees")
}

func TestCodeKeepsEachWorktreesFolds(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"dir/x"}, codeWorktreeList()...)
	l := m.list()
	m.moveToCode(1)
	keys(m, "enter")
	m.moveToCode(0)
	keys(m, "right")
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "auth-fix", paths: []string{"dir/x"}})
	if m.codeFilesShown().open["dir"] {
		t.Error("another worktree's folds apply")
	}
	keys(m, "left")
	if l.code.worktree != "main" || !m.codeFilesShown().open["dir"] {
		t.Errorf("its folds are lost: %+v", m.codeFilesShown())
	}
}

func TestCodeClicks(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"dir/inner/deep", "dir/x", "top"}, codeWorktreeList()...)
	l := m.list()
	s := &projectScreen{m: m}
	const width = 31
	rowOf := func(key string) int {
		shown, _ := m.visibleItems(100)
		return slices.IndexFunc(shown, func(it paneItem) bool {
			return it.section == sectionCode && !it.heading && !it.empty && m.codeRowsShown()[it.row].key() == key
		}) + 1
	}
	s.click(2, rowOf(""), width, 100)
	if l.code.worktree != "docs" {
		t.Errorf("< goes back round: on %q", l.code.worktree)
	}
	col := 2 + len("< docs ")
	s.click(col, rowOf(""), width, 100)
	if l.code.worktree != "main" {
		t.Errorf("> goes on: on %q", l.code.worktree)
	}
	s.click(col+3, rowOf(""), width, 100)
	if l.code.worktree != "main" {
		t.Errorf("past the selector: on %q", l.code.worktree)
	}

	m.moveToCode(0)
	s.click(2, rowOf("dir"), width, 100) // on the fold mark, which only folds
	if !m.codeFilesShown().open["dir"] || l.code.cursor != "" {
		t.Errorf("a click on + : %+v", l.code)
	}
	s.click(4, rowOf("dir"), width, 100) // on the name, which moves the cursor too
	if m.codeFilesShown().open["dir"] || l.code.cursor != "dir" {
		t.Errorf("a click on a directory's name: %+v", l.code)
	}
	s.click(6, rowOf("top"), width, 100)
	if l.code.cursor != "top" {
		t.Errorf("a click on a file: %+v", l.code)
	}

	// Folding from above with the cursor inside brings it out.
	s.click(4, rowOf("dir"), width, 100)
	s.click(8, rowOf("dir/x"), width, 100)
	if l.code.cursor != "dir/x" {
		t.Fatalf("on %q", l.code.cursor)
	}
	s.click(2, rowOf("dir"), width, 100)
	if l.code.cursor != "dir" {
		t.Errorf("folded with the cursor inside: %+v", l.code)
	}
}

func TestCodeDrawn(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("deep/", 12) + "a-file-with-a-long-name-that-has-no-room.go"
	m := codeModel([]string{long, "plain"}, api.WorktreeStatus{Name: "main", Path: "/r"})
	f := m.codeFilesShown()
	for i, p := 0, "deep"; i < 12; i, p = i+1, p+"/deep" {
		m.moveToCode(0)
		f.open = mapWith(f.open, p)
	}
	m.Update(tea.WindowSizeMsg{Width: 128, Height: 60})
	m.moveToCode(13)
	got := text(m)
	for _, want := range []string{" Code", "< main >", "▸ ", "⋯", "…"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no-room.go") {
		t.Errorf("a long name is not cut:\n%s", got)
	}
	m.layout.setFocus(agent)
	if strings.Contains(strings.Join(paneLines(m), "\n"), "▸") {
		t.Errorf("the cursor shows without the focus:\n%s", text(m))
	}
}

func mapWith(m map[string]bool, key string) map[string]bool {
	if m == nil {
		m = map[string]bool{}
	}
	m[key] = true
	return m
}

// paneLines are the rows of the project pane, the screen's left part.
func paneLines(m *Model) []string {
	var lines []string
	for _, l := range strings.Split(text(m), "\n") {
		r := []rune(l)
		lines = append(lines, string(r[:min(len(r), 31)]))
	}
	return lines
}

func TestCodeScrollsAsOneList(t *testing.T) {
	t.Parallel()
	var many []string
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		for _, d := range "abc" {
			many = append(many, string(c)+string(d))
		}
	}
	m := codeModel(many, api.WorktreeStatus{Name: "main", Path: "/r"})
	m.moveToCode(len(many))
	got := text(m)
	if !strings.Contains(got, "Code") || !strings.Contains(got, "zc") {
		t.Errorf("the cursor's row is out of view, or its section's heading:\n%s", got)
	}
}

func TestCodeReadsAgainOnWhatItDependsOn(t *testing.T) {
	t.Parallel()
	head := "one"
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r", Head: &head})
	m.Update(nil)
	f := m.codeFilesShown()
	if f == nil || !f.loading {
		t.Fatalf("not read once a worktree is chosen: %+v", f)
	}
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{"a"}})
	m.Update(nil)
	if f.loading {
		t.Error("read again with nothing changed")
	}
	next := "two"
	(*m.following.project.WorktreeStatuses)[0].Head = &next
	m.Update(nil)
	if !f.loading {
		t.Error("not read again on a commit")
	}
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{"a"}})
	(*m.following.project.WorktreeStatuses)[0].Files = []api.WorktreeFile{changed("new", api.WorktreeFileStatusEmpty)}
	m.Update(nil)
	if !f.loading {
		t.Error("not read again on a file new to the status")
	}
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", paths: []string{"a", "new"}})
	m.Update(nil)
	if f.loading {
		t.Fatal("read again with nothing changed")
	}
	m.layout.setFocus(project)
	keys(m, "C-q", refreshKey)
	if !f.loading {
		t.Error("not read again on f")
	}
}

// C-q r keeps the worktree chosen.
func TestCodeWorktreeThroughReload(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"file"}, codeWorktreeList()...)
	m.moveToCode(0)
	keys(m, "right")
	m.executable = executableFile(t)
	m.Update(key('q', tea.ModCtrl))
	m.update(key('r', 0))
	r := m.Reload()
	if r == nil || r.CodeWorktree != "auth-fix" {
		t.Fatalf("reload: %+v", r)
	}
	s, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseResume(s)
	if err != nil {
		t.Fatal(err)
	}

	next := New(context.Background(), Options{Style: style.New(false), Resume: back})
	next.tick = noTick
	next.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	worktrees := codeWorktreeList()
	next.Update(projectEvent{gen: next.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", WorktreeStatuses: &worktrees}}})
	if got := next.list().code.worktree; got != "auth-fix" {
		t.Errorf("after the reload: on %q", got)
	}
}

func TestCodeLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, ".gitignore"), "secret-ignored\n")
	writeFile(t, filepath.Join(dir, "secret-ignored"), "x\n")
	writeFile(t, filepath.Join(dir, "tracked"), "x\n")
	gittest.Git(t, dir, "add", ".gitignore", "tracked")
	gittest.Git(t, dir, "commit", "-qm", "base")
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work")
	writeFile(t, filepath.Join(work, "only-in-work"), "x\n")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the main checkout's files", func(m *Model) bool { return strings.Contains(text(m), "tracked") })
	if strings.Contains(text(p.m), "secret-ignored") {
		t.Errorf("a gitignored file shows:\n%s", text(p.m))
	}
	m := p.m
	m.layout.setFocus(project)
	m.moveToCode(0)
	p.keys("right")
	p.until(t, "the other worktree's files", func(m *Model) bool { return strings.Contains(text(m), "only-in-work") })

	writeFile(t, filepath.Join(work, "new-file"), "x\n")
	p.until(t, "the new file, with a refresh", func(m *Model) bool {
		if strings.Contains(text(m), "new-file") {
			return true
		}
		p.keys("C-q", refreshKey)
		return false
	})
}
