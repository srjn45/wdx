package tide

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
	for _, want := range []string{"Worktrees", "main", "landed", "auth-fix", "↑3 +120 −8", "M internal/tabs.go", "? new", "old-try", "missing"} {
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
	keys(m, "down")
	if l.worktreeKey != "main"+rowSeparator+"a" {
		t.Errorf("down to its file: %q", l.worktreeKey)
	}
	keys(m, "left")
	if l.worktreeKey != "main" {
		t.Errorf("left goes to the file's worktree: %q", l.worktreeKey)
	}
	keys(m, "left")
	if !l.worktreesClosed["main"] {
		t.Error("left on a worktree folds it")
	}
	if rows := m.worktreeRows(); len(rows) != 2 {
		t.Errorf("folded: %+v", rows)
	}
	keys(m, "right", "up")
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

func TestDiffJumpsBetweenChanges(t *testing.T) {
	t.Parallel()
	v := &diffView{changes: []int{10, 30}, lines: make([]string, 50)}
	v.nextChange()
	if v.offset != 8 {
		t.Errorf("first: %d", v.offset)
	}
	v.nextChange()
	if v.offset != 28 {
		t.Errorf("second: %d", v.offset)
	}
	v.nextChange()
	if v.offset != 28 {
		t.Errorf("past the last: %d", v.offset)
	}
	v.previousChange()
	if v.offset != 8 {
		t.Errorf("back: %d", v.offset)
	}
	v.previousChange()
	if v.offset != 8 {
		t.Errorf("before the first: %d", v.offset)
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
	p.until(t, "the file under its worktree", func(m *Model) bool { return strings.Contains(text(m), "M file") })
	m := p.m

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
