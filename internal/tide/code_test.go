package tide

import (
	"context"
	"maps"
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
		if got := flatTree(codeTree(tc.paths, nil)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.what, got, tc.want)
		}
	}
}

func TestCodeRows(t *testing.T) {
	t.Parallel()
	nodes := codeTree([]string{"docs/a", "internal/git/x", "internal/tide/code.go", "internal/tide/tabs.go", "internal/z", "README"}, nil)
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
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: paths}, nil))
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
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"0", "a", "b", "c"}}, nil))
	if l.code.cursor != "b" || l.code.row != 3 {
		t.Errorf("a file came above it: %+v", l.code)
	}
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"0", "a"}}, nil))
	if l.code.cursor != "a" || l.code.row != 2 {
		t.Errorf("its file went: %+v", l.code)
	}
	failed := "cannot read the files"
	m.Update(codeLoaded{projectID: "proj-demo", worktree: "main", err: context.Canceled})
	wantText(t, m, failed, "a")
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{}}, nil))
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
	m.Update(newCodeLoaded("proj-demo", "auth-fix", fileList{paths: []string{"dir/x"}}, nil))
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

// markedWorktrees are a main checkout with changes of both kinds, and another
// worktree with a change of its own.
func markedWorktrees() []api.WorktreeStatus {
	return []api.WorktreeStatus{
		{
			Name: "main", Path: "/r",
			Files: []api.WorktreeFile{changed("new.go", api.WorktreeFileStatusEmpty), changed("both.go", api.WorktreeFileStatusM), changed("gone.go", api.WorktreeFileStatusD)},
			CommittedFiles: []api.WorktreeCommittedFile{
				{Path: "both.go", Status: api.WorktreeCommittedFileStatusA},
				{Path: "pr.go", Status: api.WorktreeCommittedFileStatusM},
				{Path: "dir/deleted.go", Status: api.WorktreeCommittedFileStatusD},
			},
		},
		{Name: "other", Path: "/o", Files: []api.WorktreeFile{changed("elsewhere.go", api.WorktreeFileStatusM)}},
	}
}

func TestCodeMarks(t *testing.T) {
	t.Parallel()
	m := codeModel(nil, markedWorktrees()...)
	want := map[string]codeMark{
		"new.go":         {letter: "?"},
		"both.go":        {letter: "M"}, // what is not committed wins
		"gone.go":        {letter: "D"},
		"pr.go":          {letter: "M", committed: true},
		"dir/deleted.go": {letter: "D", committed: true},
	}
	if got := m.codeMarks().files; !maps.Equal(got, want) {
		t.Errorf("marks %v, want %v", got, want)
	}
	m.moveToCode(0)
	keys(m, "right") // to the other worktree
	if got := m.codeMarks().files; !maps.Equal(got, map[string]codeMark{"elsewhere.go": {letter: "M"}}) {
		t.Errorf("the other worktree's marks: %v", got)
	}
}

func TestCodeDirectoryMarks(t *testing.T) {
	t.Parallel()
	committed := func(p string, status api.WorktreeCommittedFileStatus) api.WorktreeCommittedFile {
		return api.WorktreeCommittedFile{Path: p, Status: status}
	}
	for _, tc := range []struct {
		what      string
		files     []api.WorktreeFile
		committed []api.WorktreeCommittedFile
		want      map[string]codeMark
	}{
		{
			"counted at every level",
			[]api.WorktreeFile{changed("a/b/c.go", api.WorktreeFileStatusM)},
			[]api.WorktreeCommittedFile{committed("a/d.go", api.WorktreeCommittedFileStatusM)},
			map[string]codeMark{"a": {letter: "M", count: 2}, "a/b": {letter: "M", count: 1}},
		},
		{
			"all committed",
			nil,
			[]api.WorktreeCommittedFile{committed("a/x", api.WorktreeCommittedFileStatusA), committed("a/y", api.WorktreeCommittedFileStatusM)},
			map[string]codeMark{"a": {letter: "A", committed: true, count: 2}},
		},
		{
			"what is not committed outweighs what is",
			[]api.WorktreeFile{changed("a/x", api.WorktreeFileStatusM)},
			[]api.WorktreeCommittedFile{committed("a/y", api.WorktreeCommittedFileStatusD)},
			map[string]codeMark{"a": {letter: "M", count: 2}},
		},
		{
			"an addition outweighs a change",
			[]api.WorktreeFile{changed("a/x", api.WorktreeFileStatusM), changed("a/y", api.WorktreeFileStatusEmpty)},
			nil,
			map[string]codeMark{"a": {letter: "?", count: 2}},
		},
		{
			"a deletion or a conflict outweighs an addition",
			[]api.WorktreeFile{changed("a/x", api.WorktreeFileStatusA), changed("a/y", api.WorktreeFileStatusU), changed("b/x", api.WorktreeFileStatusD), changed("b/y", api.WorktreeFileStatusEmpty)},
			nil,
			map[string]codeMark{"a": {letter: "U", count: 2}, "b": {letter: "D", count: 2}},
		},
		{
			"a file changed both ways counted once",
			[]api.WorktreeFile{changed("a/x", api.WorktreeFileStatusM)},
			[]api.WorktreeCommittedFile{committed("a/x", api.WorktreeCommittedFileStatusA)},
			map[string]codeMark{"a": {letter: "M", count: 1}},
		},
		{
			"a file deleted in a commit, where a directory is now",
			[]api.WorktreeFile{changed("x/y", api.WorktreeFileStatusEmpty)},
			[]api.WorktreeCommittedFile{committed("x", api.WorktreeCommittedFileStatusD)},
			map[string]codeMark{"x": {letter: "?", count: 1}},
		},
	} {
		m := codeModel(nil, api.WorktreeStatus{Name: "main", Path: "/r", Files: tc.files, CommittedFiles: tc.committed})
		if got := m.codeMarks().dirs; !maps.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.what, got, tc.want)
		}
	}
}

func TestCodeMarksDrawn(t *testing.T) {
	t.Parallel()
	long := "a-changed-file-with-a-long-name.go"
	m := codeModel([]string{"dir/x", "new.go", "both.go", "pr.go", "plain.go", long}, markedWorktrees()...)
	w := m.following.project.WorktreeStatuses
	(*w)[0].Files = append((*w)[0].Files, changed(long, api.WorktreeFileStatusM))
	m.moveToCode(1)

	ends := map[string]string{"dir": "1", "new.go": "?", "both.go": "M", "pr.go": "M", "plain.go": "", "a-changed": "M"}
	for _, line := range paneLines(m) {
		for name, letter := range ends {
			if !strings.Contains(line, " "+name) {
				continue
			}
			if got := strings.TrimRight(line, " "); !strings.HasSuffix(got, " "+letter) && letter != "" {
				t.Errorf("%s does not end in %q: %q", name, letter, line)
			} else if letter == "" && strings.ContainsAny(got[len(got)-1:], "?MAD") {
				t.Errorf("%s has a mark: %q", name, line)
			}
			if name == "a-changed" && !strings.Contains(line, "…") {
				t.Errorf("a long name is not cut for its letter: %q", line)
			}
		}
	}

	m.st = style.New(true)
	st, marks := m.st, m.codeMarks()
	line := func(path string, cursor bool) string {
		i := slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == path })
		return m.codeLine(m.codeRowsShown()[i], marks, cursor, 31)
	}
	for _, tc := range []struct {
		what, path string
		cursor     bool
		want       []string
	}{
		{"an untracked file, in green", "new.go", false, []string{st.Added("new.go"), " " + st.Added("?")}},
		{"a file changed in both, as not committed", "both.go", false, []string{st.Warning("both.go"), " " + st.Warning("M")}},
		{"a committed file under the cursor, bold in cyan", "pr.go", true, []string{st.Name(st.Committed("pr.go")), " " + st.Committed("M")}},
		{"an unchanged file, plain", "plain.go", false, []string{"\x1b[0mplain.go   "}},
		{"a directory with a file deleted in a commit, its count in cyan", "dir", false, []string{st.Committed("dir"), " " + st.Committed("1")}},
	} {
		got := line(tc.path, tc.cursor)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: no %q in %q", tc.what, want, got)
			}
		}
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

// TestCodeKeepsTheTreeOfTheSameList reads a list like the one shown: nothing is
// built, and the tree, its folds and what the filter matched stay.
func TestCodeKeepsTheTreeOfTheSameList(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.Update(nil)
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"dir/a", "b"}}, nil))
	f := m.codeFilesShown()
	tree, read := f.tree, f.read

	again := newCodeLoaded("proj-demo", "main", fileList{paths: []string{"dir/a", "b"}, emptyDirs: []string{}}, &fileList{paths: f.paths, emptyDirs: f.emptyDirs})
	if !again.same || again.tree != nil {
		t.Fatalf("the same list was built again: %+v", again)
	}
	m.Update(again)
	if len(f.tree) == 0 || &f.tree[0] != &tree[0] || f.read != read {
		t.Errorf("the tree was replaced: read %d, then %d", read, f.read)
	}

	other := newCodeLoaded("proj-demo", "main", fileList{paths: []string{"dir/a", "c"}}, &fileList{paths: f.paths, emptyDirs: f.emptyDirs})
	m.Update(other)
	if other.same || f.read != read+1 || !slices.Equal(f.paths, []string{"dir/a", "c"}) {
		t.Errorf("a new list was not taken: %+v", f.paths)
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
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"a"}}, nil))
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
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"a"}}, nil))
	(*m.following.project.WorktreeStatuses)[0].Files = []api.WorktreeFile{changed("new", api.WorktreeFileStatusEmpty)}
	m.Update(nil)
	if !f.loading {
		t.Error("not read again on a file new to the status")
	}
	m.Update(newCodeLoaded("proj-demo", "main", fileList{paths: []string{"a", "new"}}, nil))
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

// fileModel is a model on the main checkout with the file "dir/a.go" and "top", with
// the tree open and the cursor on dir/a.go.
func fileModel(t *testing.T) *Model {
	t.Helper()
	m := codeModel([]string{"dir/a.go", "top"}, codeWorktreeList()...)
	m.moveToCode(1)
	keys(m, "enter", "down")
	if m.list().code.cursor != "dir/a.go" {
		t.Fatalf("on %q", m.list().code.cursor)
	}
	return m
}

func sourceOf(s string) *api.WorktreeFileContent {
	return &api.WorktreeFileContent{Content: &s, Size: int64(len(s))}
}

func loadFile(m *Model, tab string, c *api.WorktreeFileContent) {
	m.Update(diffLoaded{projectID: "proj-demo", tab: tab, content: c})
}

const aFile = "file:main\x00dir/a.go"

func TestFileOpens(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	l := m.list()
	if l.active != aFile || !m.textShown() {
		t.Fatalf("tab %q", l.active)
	}
	if m.layout.focus != project {
		t.Errorf("the focus left the list: %v", m.layout.focus)
	}
	if !strings.Contains(titleRow(m), "[file: main/a.go") {
		t.Errorf("a file has a title and no state mark: %q", titleRow(m))
	}
	wantText(t, m, "loading…", "◂")
	loadFile(m, aFile, sourceOf("package a\n\nfunc A() {}\n"))
	wantText(t, m, "1 package a", "3 func A() {}", "line 1 of 3")
	if strings.Contains(text(m), "change ") {
		t.Errorf("a file has no changes:\n%s", text(m))
	}

	// Again goes to the same tab.
	n := len(l.tabs)
	keys(m, "enter")
	if len(l.tabs) != n {
		t.Errorf("tabs: %v", l.tabs)
	}

	// A folded directory carries the mark of what it hides; another worktree's tree none.
	keys(m, "left", "left")
	if !m.codeRowActive(m.codeRowsShown()[1]) {
		t.Error("the folded directory has no mark")
	}
	m.moveToCode(0)
	keys(m, "right")
	m.Update(newCodeLoaded("proj-demo", "auth-fix", fileList{paths: []string{"dir/a.go"}}, nil))
	if slices.ContainsFunc(m.codeRowsShown(), m.codeRowActive) {
		t.Error("another worktree's file has the mark")
	}
}

func TestFileOpensOverAZoomedList(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	m.layout.toggleZoom()
	keys(m, "enter")
	if m.layout.zoomed != agent || m.layout.focus != agent {
		t.Errorf("zoomed %v, focus %v", m.layout.zoomed, m.layout.focus)
	}
}

func TestFileClick(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	s := &projectScreen{m: m}
	shown, _ := m.visibleItems(100)
	row := slices.IndexFunc(shown, func(it paneItem) bool {
		return it.section == sectionCode && !it.heading && !it.empty && m.codeRowsShown()[it.row].key() == "top"
	}) + 1
	s.click(6, row, 31, 100)
	if m.list().active != "file:main\x00top" {
		t.Errorf("tab %q", m.list().active)
	}
}

func TestFileViewKeys(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	var lines []string
	for range 100 {
		lines = append(lines, "line")
	}
	lines[49] = "the needle"
	loadFile(m, aFile, sourceOf(strings.Join(lines, "\n")))
	keys(m, "alt+right")
	if m.layout.focus != agent {
		t.Fatalf("focus %v", m.layout.focus)
	}
	v := m.shownDiff()
	text(m)
	keys(m, ":", "7", "enter")
	text(m)
	if v.lineNumber() != 7 {
		t.Errorf(": goes to line %d", v.lineNumber())
	}
	keys(m, "/", "n", "e", "e", "d", "l", "e", "enter")
	text(m)
	if v.lineNumber() != 50 {
		t.Errorf("/ goes to line %d", v.lineNumber())
	}
	wantText(t, m, "n N", "match")
	keys(m, "g")
	text(m)
	keys(m, "n")
	if v.lineNumber() != 50 {
		t.Errorf("n goes to line %d", v.lineNumber())
	}
	keys(m, "G")
	text(m)
	if v.lineNumber() != 100 {
		t.Errorf("G goes to line %d", v.lineNumber())
	}
	keys(m, "g", "pgdown")
	text(m)
	if v.lineNumber() <= 1 {
		t.Errorf("a page down stays on line %d", v.lineNumber())
	}
	keys(m, "?")
	wantText(t, m, "go to that line")
	if strings.Contains(text(m), "comment") || strings.Contains(text(m), "verified") {
		t.Errorf("? lists what a file does not do:\n%s", text(m))
	}
	keys(m, "esc")

	// With changes in the worktree, so that the keys have something to act on.
	(*m.following.project.WorktreeStatuses)[0].Files = []api.WorktreeFile{changed("dir/a.go", api.WorktreeFileStatusM)}
	at := v.cursor
	for _, k := range []string{"[", "]", "{", "}", "\t", commitKey, landKey, commentKey, editKey, deleteKey, reviewKey, verifyKey} {
		keys(m, k)
		if v.cursor != at || v.prompt != "" || v.notice != "" || m.list().showCommits["main"] || m.commit != nil || m.comment != nil || m.confirm != nil || m.statusNotice != "" {
			t.Errorf("%q acts on a file", k)
		}
	}
}

func TestFileShownAsItCan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		what    string
		content *api.WorktreeFileContent
		err     error
		want    string
		no      string
	}{
		{"binary", &api.WorktreeFileContent{Binary: true, Size: 3}, nil, "a binary file", ""},
		{"too large", &api.WorktreeFileContent{TooLarge: true, Size: 3 << 20}, nil, "too large to show: 3.0 MiB", ""},
		{"empty", sourceOf(""), nil, "an empty file", ""},
		{"one blank line", sourceOf("\n"), nil, "line 1 of 1", "an empty file"},
		{"no newline at the end", sourceOf("a\nb"), nil, "line 1 of 2", ""},
		{"windows line ends", sourceOf("a\r\nb\r\n"), nil, "1 a", "a·"},
		{"tabs", sourceOf("\tx\n"), nil, "1     x", ""},
		{"escapes kept from the terminal", sourceOf("a\x1b[31mb\x07\n"), nil, "a·[31mb·", ""},
		{"gone", &api.WorktreeFileContent{}, &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeNotFound}}, "not on disk", "could not be read"},
		{"out of the worktree", &api.WorktreeFileContent{}, &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeInvalidPath}}, "not a file in this worktree", "could not be read"},
		{"any other failure", &api.WorktreeFileContent{}, context.Canceled, "the file could not be read", ""},
	} {
		m := fileModel(t)
		keys(m, "enter")
		m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, content: tc.content, err: tc.err})
		got := text(m)
		if !strings.Contains(got, tc.want) || (tc.no != "" && strings.Contains(got, tc.no)) {
			t.Errorf("%s: want %q, not %q, in:\n%s", tc.what, tc.want, tc.no, got)
		}
	}
}

func TestFileWraps(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	loadFile(m, aFile, sourceOf(strings.Repeat("x", 300)+"\nsecond\n"))
	text(m)
	v := m.shownDiff()
	if len(v.lines) < 4 || v.numbers[0] != 1 || v.numbers[1] != 0 {
		t.Fatalf("lines %d, numbers %v", len(v.lines), v.numbers)
	}
	if v.numbers[len(v.numbers)-1] != 2 {
		t.Errorf("the line after is numbered %v", v.numbers)
	}
}

func TestFileGoneThenBack(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	loadFile(m, aFile, sourceOf("first\n"))
	wantText(t, m, "first")
	m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, err: &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeNotFound}}})
	if strings.Contains(text(m), "first") {
		t.Errorf("what is gone still shows:\n%s", text(m))
	}
	loadFile(m, aFile, sourceOf("second\n"))
	got := text(m)
	if strings.Contains(got, "not on disk") || !strings.Contains(got, "second") {
		t.Errorf("back:\n%s", got)
	}
}

func TestFileReadsAgain(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	loadFile(m, aFile, sourceOf("one\n"))
	v := m.shownDiff()
	m.Update(nil)
	if v.loading {
		t.Fatal("read again with nothing changed")
	}

	// The stream says it changed.
	(*m.following.project.WorktreeStatuses)[0].Files = []api.WorktreeFile{changed("dir/a.go", api.WorktreeFileStatusM)}
	m.Update(nil)
	if !v.loading {
		t.Error("not read again on a change")
	}
	loadFile(m, aFile, sourceOf("two\n"))
	m.Update(nil)

	// It comes to show after another tab.
	l := m.list()
	l.openTab(settingsTab)
	m.Update(nil)
	if v.loading {
		t.Error("read while not shown")
	}
	l.active = aFile
	m.Update(nil)
	if !v.loading {
		t.Error("not read again on coming to show")
	}
	loadFile(m, aFile, sourceOf("three\n"))
	m.Update(nil)

	// On f.
	keys(m, "C-q", refreshKey)
	if !v.loading {
		t.Error("not read again on f")
	}
}

func TestFileTabKeptThroughReload(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	loadFile(m, aFile, sourceOf("one\n"))
	m.executable = executableFile(t)
	m.Update(key('q', tea.ModCtrl))
	m.update(key('r', 0))
	r := m.Reload()
	if r == nil || r.Agent != aFile || !slices.Contains(r.Tabs, aFile) {
		t.Fatalf("reload: %+v", r)
	}
	next := New(context.Background(), Options{Style: style.New(false), Resume: r})
	next.tick = noTick
	next.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	worktrees := codeWorktreeList()
	next.Update(projectEvent{gen: next.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", WorktreeStatuses: &worktrees}}})
	if got := next.list().active; got != aFile {
		t.Errorf("after the reload: tab %q", got)
	}
}

func TestFileTabClosesWithItsWorktree(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	other := fileTab("auth-fix", "x")
	m.list().openTab(other)
	m.worktreeRemoved(removed{r: removal{name: "auth-fix"}})
	if slices.Contains(m.list().tabs, other) || !slices.Contains(m.list().tabs, aFile) {
		t.Errorf("tabs %v", m.list().tabs)
	}
}

func TestFileLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, "file.go"), "package one\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the file listed", func(m *Model) bool { return strings.Contains(text(m), "file.go") })
	m := p.m
	m.layout.setFocus(project)
	m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "file.go" }))
	p.keys("enter")
	p.until(t, "the file's text", func(m *Model) bool { return strings.Contains(text(m), "1 package one") })

	writeFile(t, filepath.Join(dir, "file.go"), "package two\n")
	p.until(t, "the edit, with a refresh", func(m *Model) bool {
		if strings.Contains(text(m), "1 package two") {
			return true
		}
		p.keys("C-q", refreshKey)
		return false
	})
}
