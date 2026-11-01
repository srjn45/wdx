package tide

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/style"
)

// worktreesModel is a model showing a project with two agents and these worktrees.
func worktreesModel(worktrees ...api.WorktreeStatus) *Model {
	m := settingsModel()
	m.Update(tea.WindowSizeMsg{Width: 128, Height: 24}) // 31 cells in the project pane
	m.following.project.WorktreeStatuses = &worktrees
	m.layout.setFocus(project)
	m.worktreesChanged()
	return m
}

func aheadBy(n int) *int { return &n }

func changed(path string, status api.WorktreeFileStatus) api.WorktreeFile {
	at := time.Unix(1, 0).UTC()
	return api.WorktreeFile{Path: path, Status: status, Size: 1, ModifiedAt: &at}
}

func TestWorktreesListed(t *testing.T) {
	t.Parallel()
	main := "main"
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r", Ahead: aheadBy(0), Base: &main},
		api.WorktreeStatus{Name: "auth-fix", Path: "/r/a", Ahead: aheadBy(3), Added: aheadBy(120), Removed: aheadBy(8), Base: &main,
			Files: []api.WorktreeFile{changed("internal/tabs.go", api.WorktreeFileStatusM), changed("new", api.WorktreeFileStatusEmpty)}},
		api.WorktreeStatus{Name: "old-try", Path: "/r/o", Missing: true},
	)
	got := text(m)
	// Folded to begin with, a worktree with files says so: it would look clean.
	for _, want := range []string{"Worktrees", "main", "landed", "auth-fix", "↑3 · 2 files", "old-try", "missing"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "M internal/tabs.go") {
		t.Errorf("a worktree started open:\n%s", got)
	}
	m.foldWorktree("auth-fix", false)
	got = text(m)
	for _, want := range []string{"↑3 +120 −8", "M internal/tabs.go", "? new"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "Agents") > strings.Index(got, "Worktrees") {
		t.Errorf("the agents come first:\n%s", got)
	}
}

func TestWorktreeSummary(t *testing.T) {
	t.Parallel()
	main, remote := "main", "origin/main"
	for _, tc := range []struct {
		name string
		w    api.WorktreeStatus
		want string
	}{
		{"landed", api.WorktreeStatus{Ahead: aheadBy(0), Base: &main}, "landed"},
		{"at work, nothing committed", api.WorktreeStatus{Ahead: aheadBy(0), Base: &main, Files: []api.WorktreeFile{changed("f", api.WorktreeFileStatusM)}}, ""},
		{"all pushed", api.WorktreeStatus{Ahead: aheadBy(0), Base: &remote}, ""},
		{"lines not known", api.WorktreeStatus{Ahead: aheadBy(2), Base: &main}, "↑2"},
		{"no base", api.WorktreeStatus{}, ""},
		{"missing", api.WorktreeStatus{Missing: true}, "missing"},
	} {
		if got := worktreeSummary(tc.w); got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestWorktreeCursorMovesFromTheAgents(t *testing.T) {
	t.Parallel()
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r", Files: []api.WorktreeFile{changed("a", api.WorktreeFileStatusM)}},
		api.WorktreeStatus{Name: "work", Path: "/w"},
	)
	l := m.list()
	last := len(m.listRows()) - 1
	m.moveCursor(last)
	keys(m, "down")
	if !l.onWorktrees || l.worktreeKey != "main" {
		t.Fatalf("down from the last agent: %+v", l)
	}
	if _, ok := m.cursorAgent(); ok {
		t.Error("an agent is under the cursor on a worktree: C-q x would remove it")
	}
	if rows := m.worktreeRows(); len(rows) != 2 {
		t.Errorf("a worktree starts folded: %+v", rows)
	}
	keys(m, "right", "down")
	if l.worktreeKey != "main"+rowSeparator+"a" {
		t.Errorf("down to its file, once opened: %q", l.worktreeKey)
	}
	keys(m, "left")
	if l.worktreeKey != "main" {
		t.Errorf("left goes to the file's worktree: %q", l.worktreeKey)
	}
	keys(m, "left")
	if l.worktreesOpen["main"] {
		t.Error("left on a worktree folds it")
	}
	keys(m, "up")
	if l.onWorktrees || l.row != last {
		t.Errorf("up from the first worktree: %+v", l)
	}
}

func TestWorktreeCursorStaysOnItsRow(t *testing.T) {
	t.Parallel()
	m := worktreesModel(
		api.WorktreeStatus{Name: "a", Path: "/a"},
		api.WorktreeStatus{Name: "b", Path: "/b"},
	)
	m.moveToWorktree(1)
	*m.following.project.WorktreeStatuses = []api.WorktreeStatus{
		{Name: "new", Path: "/new"}, {Name: "a", Path: "/a"}, {Name: "b", Path: "/b"},
	}
	m.worktreesChanged()
	if l := m.list(); l.worktreeRow != 2 {
		t.Errorf("row %d", l.worktreeRow)
	}
	*m.following.project.WorktreeStatuses = nil
	m.worktreesChanged()
	if m.list().onWorktrees {
		t.Error("the cursor stayed in an empty section")
	}
}

func TestWorktreesScrollAsOneList(t *testing.T) {
	t.Parallel()
	var many []api.WorktreeStatus
	for _, name := range []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9", "w10", "w11", "w12", "w13", "w14", "w15", "w16", "w17", "w18", "w19", "w20", "w21", "w22", "w23", "w24", "w25", "last"} {
		many = append(many, api.WorktreeStatus{Name: name, Path: "/" + name})
	}
	m := worktreesModel(many...)
	m.moveToWorktree(len(many) - 1)
	if got := text(m); !strings.Contains(got, "last") || !strings.Contains(got, "Worktrees") {
		t.Errorf("the cursor's row is out of view, or its section's heading:\n%s", got)
	}
}

func TestDiffTabs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tab, worktree, file, title string
	}{
		{fileDiffTab("work", "internal/tabs.go"), "work", "internal/tabs.go", "diff: work/tabs.go"},
		{committedTab("work"), "work", "", "pr: work"},
	} {
		if w, f := diffTabOf(tc.tab); w != tc.worktree || f != tc.file {
			t.Errorf("%q: %q, %q", tc.tab, w, f)
		}
		if got := diffTitle(tc.tab); got != tc.title {
			t.Errorf("title of %q: %q", tc.tab, got)
		}
		if !isDiffTab(tc.tab) || isDiffTab("agent-1") || isDiffTab(settingsTab) || isDiffTab(infoTab("a")) {
			t.Errorf("isDiffTab of %q", tc.tab)
		}
		if tabAgent(tc.tab) == "" {
			t.Errorf("%q names an agent", tc.tab)
		}
	}
}

func TestSplitPatch(t *testing.T) {
	t.Parallel()
	rows := splitPatch(`diff --git a/f b/f
index 1..2 100644
--- a/f
+++ b/f
@@ -1,4 +1,5 @@
 one
-two
-three
+2
+3
+and more
 four
\ No newline at end of file
`)
	want := []splitRow{
		{leftNo: 1, rightNo: 1, left: "one", right: "one"},
		{leftNo: 2, rightNo: 2, left: "two", right: "2", changed: true},
		{leftNo: 3, rightNo: 3, left: "three", right: "3", changed: true},
		{rightNo: 4, right: "and more", changed: true},
		{leftNo: 4, rightNo: 5, left: "four", right: "four"},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("got  %+v\nwant %+v", rows, want)
	}
	if got := splitPatch("@@ -0,0 +1 @@\n+all new\n"); len(got) != 1 || got[0].leftNo != 0 || got[0].right != "all new" {
		t.Errorf("a new file: %+v", got)
	}
	if got := splitPatch("@@ -1 +0,0 @@\n-all gone\n"); len(got) != 1 || got[0].rightNo != 0 || got[0].left != "all gone" {
		t.Errorf("a deleted file: %+v", got)
	}
}

func TestSplitViewDrawn(t *testing.T) {
	t.Parallel()
	v := &diffView{split: []splitRow{
		{leftNo: 1, rightNo: 1, left: "same", right: "same"},
		{leftNo: 2, rightNo: 2, left: "old line", right: "new line that is much longer than its column has room for", changed: true},
		{leftNo: 3, rightNo: 3, left: "after", right: "after"},
	}}
	v.prepare(41, style.New(false))
	if len(v.lines) < 4 {
		t.Fatalf("the long line did not wrap: %q", v.lines)
	}
	for i, line := range v.lines {
		if !strings.Contains(line, "│") || strings.Index(line, "│") != strings.Index(v.lines[0], "│") {
			t.Errorf("line %d is out of line: %q", i, line)
		}
	}
	if !strings.Contains(v.lines[1], "2 - old line") || !strings.Contains(v.lines[1], "2 + new") {
		t.Errorf("with color off the signs tell: %q", v.lines[1])
	}
	if !slices.Equal(v.changes, []int{1}) {
		t.Errorf("changes at %v", v.changes)
	}
}

// TestDiffShowsNothingOfAProgram keeps a file's bytes from reaching the terminal.
func TestDiffShowsNothingOfAProgram(t *testing.T) {
	t.Parallel()
	got := shownText("a\tb\x1b[2Jc\x07\u009b")
	if strings.ContainsAny(got, "\t\x1b\x07\u009b") {
		t.Errorf("control characters kept: %q", got)
	}
}

// diffShowing is a model with the focus on a diff's tab, holding v.
func diffShowing(t *testing.T, tab string, v *diffView) *Model {
	t.Helper()
	m := worktreesModel(api.WorktreeStatus{Name: "w", Path: "/w"})
	m.layout.setFocus(agent)
	l := m.list()
	l.openTab(tab)
	v.asked, v.seen = true, m.diffSeen(tab)
	l.diffs = map[string]*diffView{tab: v}
	text(m) // works out its lines
	return m
}

// longFile is a file of n lines with the given lines changed.
func longFile(n int, changed ...int) []splitRow {
	var rows []splitRow
	for i := 1; i <= n; i++ {
		r := splitRow{leftNo: i, rightNo: i, left: fmt.Sprintf("line%d", i), right: fmt.Sprintf("line%d", i)}
		if slices.Contains(changed, i) {
			r.right, r.changed = fmt.Sprintf("Edited%d", i), true
		}
		rows = append(rows, r)
	}
	return rows
}

func TestDiffJumpsBetweenChanges(t *testing.T) {
	t.Parallel()
	v := &diffView{split: longFile(200, 50, 150), jump: true}
	m := diffShowing(t, fileDiffTab("w", "f"), v)
	if v.numbers[v.cursor] != 50 {
		t.Errorf("opens on the first change: line %d", v.numbers[v.cursor])
	}
	for _, tc := range []struct {
		key  string
		line int
	}{{"]", 150}, {"]", 150}, {"[", 50}, {"[", 50}} {
		keys(m, tc.key)
		if got := v.numbers[v.cursor]; got != tc.line {
			t.Errorf("%s: line %d, want %d", tc.key, got, tc.line)
		}
	}
	if got := text(m); !strings.Contains(got, "change 1 of 2 · line 50 of 200") || !strings.Contains(got, "▸") {
		t.Errorf("where it is not said:\n%s", got)
	}
}

func TestDiffCursorMovesTheView(t *testing.T) {
	t.Parallel()
	v := &diffView{split: longFile(200)}
	m := diffShowing(t, fileDiffTab("w", "f"), v)
	if v.cursor != 0 || v.offset != 0 {
		t.Fatalf("start: %d, %d", v.cursor, v.offset)
	}
	keys(m, "down", "down")
	if v.cursor != 2 {
		t.Errorf("down: %d", v.cursor)
	}
	keys(m, "pgdown")
	page := v.cursor
	if page <= 2 {
		t.Errorf("a page: %d", page)
	}
	keys(m, " ")
	if v.cursor <= page {
		t.Errorf("Space pages too: %d", v.cursor)
	}
	keys(m, "b")
	if v.cursor != page {
		t.Errorf("b pages back: %d, want %d", v.cursor, page)
	}
	keys(m, "G")
	got := text(m)
	if !strings.Contains(got, "line200") || v.cursor != len(v.lines)-1 {
		t.Errorf("the last line is out of view:\n%s", got)
	}
	keys(m, "g")
	text(m)
	if v.cursor != 0 || v.offset != 0 {
		t.Errorf("g: %d, %d", v.cursor, v.offset)
	}
	m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelDown})
	if v.offset != diffWheelLines || v.cursor != v.offset {
		t.Errorf("the wheel leaves the current line in view: %d, %d", v.cursor, v.offset)
	}
}

func TestDiffGoToLine(t *testing.T) {
	t.Parallel()
	v := &diffView{split: longFile(200)}
	m := diffShowing(t, fileDiffTab("w", "f"), v)
	keys(m, ":")
	if !strings.Contains(text(m), ":_") {
		t.Errorf("no prompt:\n%s", text(m))
	}
	keys(m, "120", "enter")
	if got := v.numbers[v.cursor]; got != 120 {
		t.Errorf("line %d", got)
	}
	keys(m, ":", "9999", "enter")
	if got := v.numbers[v.cursor]; got != 200 {
		t.Errorf("past the end: line %d", got)
	}
	keys(m, ":", "12x", "enter")
	if got := text(m); !strings.Contains(got, "not a line number: 12x") {
		t.Errorf("no notice:\n%s", got)
	}
	keys(m, ":", "5", "esc")
	if got := v.numbers[v.cursor]; got != 200 {
		t.Errorf("Esc went: line %d", got)
	}
}

func TestDiffSearch(t *testing.T) {
	t.Parallel()
	v := &diffView{split: longFile(30, 5, 20)}
	m := diffShowing(t, fileDiffTab("w", "f"), v)
	keys(m, "/", "edited", "enter")
	if got := v.numbers[v.cursor]; got != 5 {
		t.Fatalf("first match: line %d", got)
	}
	if got := text(m); !strings.Contains(got, "n N match") {
		t.Errorf("the bar does not offer the matches:\n%s", got)
	}
	keys(m, "n")
	if got := v.numbers[v.cursor]; got != 20 {
		t.Errorf("n: line %d", got)
	}
	keys(m, "n")
	if got := v.numbers[v.cursor]; got != 5 {
		t.Errorf("n goes round: line %d", got)
	}
	keys(m, "N")
	if got := v.numbers[v.cursor]; got != 20 {
		t.Errorf("N goes round: line %d", got)
	}

	// Capitals make it exact.
	keys(m, "/", "EDITED", "enter")
	if got := text(m); !strings.Contains(got, "no match: EDITED") {
		t.Errorf("no notice:\n%s", got)
	}
	keys(m, "esc")
	if got := text(m); v.search != nil || strings.Contains(got, "n N match") {
		t.Errorf("Esc did not end the search:\n%s", got)
	}

	// A query is text, not a pattern.
	keys(m, "/", "line.", "enter")
	if got := text(m); !strings.Contains(got, "no match: line.") {
		t.Errorf("a pattern was applied:\n%s", got)
	}
}

func TestDiffKeysAreItsOwn(t *testing.T) {
	t.Parallel()
	file := diffShowing(t, fileDiffTab("w", "f"), &diffView{split: longFile(30, 5)})
	got := text(file)
	for _, want := range []string{"[ ] change", ": line", "/ search", "? keys"} {
		if !strings.Contains(got, want) {
			t.Errorf("file diff: no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "{ } file") || strings.Contains(got, "n N") {
		t.Errorf("file diff offers what it cannot do:\n%s", got)
	}

	v := &diffView{unified: []string{
		"diff --git a/f b/f", "--- a/f", "+++ b/f", "@@ -1 +1 @@", "-a", "+b", "@@ -9 +9 @@", "-c", "+d",
		"diff --git a/g b/g", "--- a/g", "+++ b/g", "@@ -1 +1 @@", "-e", "+f",
	}}
	worktree := diffShowing(t, committedTab("w"), v)
	got = text(worktree)
	if !strings.Contains(got, "{ } file") || strings.Contains(got, ": line") {
		t.Errorf("worktree diff:\n%s", got)
	}
	if !strings.Contains(got, "change 0 of 3 · file 1 of 2") {
		t.Errorf("no position:\n%s", got)
	}
	keys(worktree, "]")
	if v.cursor != 3 {
		t.Errorf("] goes by change: %d", v.cursor)
	}
	keys(worktree, "}")
	if v.cursor != 9 {
		t.Errorf("} goes by file: %d", v.cursor)
	}
	keys(worktree, "{")
	if v.cursor != 0 {
		t.Errorf("{ goes back: %d", v.cursor)
	}
	keys(worktree, ":")
	if v.prompt != "" {
		t.Error("a line number means nothing across files")
	}

	keys(worktree, "?")
	if got := text(worktree); !strings.Contains(got, "the file before or after") {
		t.Errorf("? shows no keys:\n%s", got)
	}
	keys(worktree, "esc")
	if v.keyHelp {
		t.Error("Esc leaves the keys")
	}
}

// TestDiffBarNarrow keeps what is needed most in a pane short of room.
func TestDiffBarNarrow(t *testing.T) {
	t.Parallel()
	v := &diffView{split: longFile(30, 5)}
	v.prepare(100, style.New(false))
	for _, width := range []int{100, 60, 40, 30, 12} {
		got := ansi.Strip(v.bar(true, width, style.New(false)))
		if ansi.StringWidth(got) > max(width, 8) {
			t.Errorf("%d: %d wide: %q", width, ansi.StringWidth(got), got)
		}
		if width >= 12 && !strings.Contains(got, "?") {
			t.Errorf("%d: the full list cannot be reached: %q", width, got)
		}
	}
}

// TestBarOnlyInDiffs: the bar is the diff tabs', not tide's.
func TestBarOnlyInDiffs(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "w", Path: "/w"})
	if got := text(m); strings.Contains(got, "[ ] change") || strings.Contains(got, "? keys") {
		t.Errorf("another tab has the bar:\n%s", got)
	}
}

func TestCommittedHeadings(t *testing.T) {
	t.Parallel()
	base, merge := "main", "0123456789"
	for _, tc := range []struct {
		d    api.WorktreeDiff
		want string
	}{
		{api.WorktreeDiff{}, "nothing to compare with"},
		{api.WorktreeDiff{Base: &base}, "no history in common with main"},
		{api.WorktreeDiff{Base: &base, MergeBase: &merge}, "nothing ahead of main"},
	} {
		v := &diffView{}
		v.setCommitted(tc.d)
		if !strings.Contains(v.message, tc.want) {
			t.Errorf("want %q, got %q", tc.want, v.message)
		}
	}
	v := &diffView{}
	v.setCommitted(api.WorktreeDiff{Base: &base, MergeBase: &merge, Ahead: aheadBy(1), Patch: "diff --git a/f b/f\n+x\n"})
	if v.heading != "base: main · merge-base 0123456 · 1 commit" || len(v.unified) != 2 {
		t.Errorf("%+v", v)
	}
}

// TestWorktreeDiffsLive goes through a real daemon and git: the section comes from
// the stream, and an edit the stream says of shows in the open tab.
func TestWorktreeDiffsLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, "file"), "one\ntwo\nthree\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work") // not by wdx: git's list is what counts
	writeFile(t, filepath.Join(work, "landing"), "committed\n")
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "work")
	writeFile(t, filepath.Join(work, "file"), "one\n2\nthree\n")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the worktree with its file", func(m *Model) bool {
		return strings.Contains(text(m), "2 files") || strings.Contains(text(m), "1 file")
	})
	m := p.m
	m.foldWorktree("work", false)

	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.file != nil && r.file.Path == "file" }))
	p.keys("enter")
	if m.layout.focus != agent || !m.diffShown() {
		t.Fatalf("focus %v, tab %q", m.layout.focus, m.list().active)
	}
	p.until(t, "the split view", func(m *Model) bool {
		got := text(m)
		return strings.Contains(got, "HEAD") && strings.Contains(got, "- two") && strings.Contains(got, "+ 2")
	})

	writeFile(t, filepath.Join(work, "file"), "one\nedited\nthree\n")
	p.keys("C-q", refreshKey)
	p.until(t, "the edit in the open tab", func(m *Model) bool { return strings.Contains(text(m), "+ edited") })

	// git run by anyone needs no key.
	gittest.Git(t, work, "add", "file")
	gittest.Git(t, work, "commit", "-qm", "edited")
	p.until(t, "the commit", func(m *Model) bool {
		return strings.Contains(text(m), "no uncommitted changes") && strings.Contains(text(m), "↑2")
	})

	// Enter on the worktree shows what it committed.
	writeFile(t, filepath.Join(work, "later"), "not committed\n")
	m.layout.setFocus(project)
	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.file == nil && r.worktree.Name == "work" }))
	p.keys("enter")
	p.until(t, "the PR view", func(m *Model) bool {
		got := text(m)
		return strings.Contains(got, "pr: work") && strings.Contains(got, "base: main") && strings.Contains(got, "+committed")
	})
	if got := text(m); strings.Contains(got, "not committed") {
		t.Errorf("what is not committed is in the PR view:\n%s", got)
	}

	// A diff's tab stays when what it shows is gone, and says why.
	if len(m.list().tabs) != 2 {
		t.Errorf("tabs: %v", m.list().tabs)
	}
	gittest.Git(t, dir, "worktree", "remove", "--force", work)
	p.until(t, "the worktree gone", func(m *Model) bool { return !strings.Contains(text(m), "↑2") })
	if len(m.list().tabs) != 2 {
		t.Errorf("tabs after: %v", m.list().tabs)
	}
	p.until(t, "the tab saying so", func(m *Model) bool { return strings.Contains(text(m), "could not be read") })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
