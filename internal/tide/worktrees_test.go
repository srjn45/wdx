package tide

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
		{"not read yet, so not clean", api.WorktreeStatus{Unread: true, Base: &main}, "reading…"},
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
	if l.section != sectionWorktrees || l.worktreeKey != "main" {
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
	if l.section == sectionWorktrees || l.row != last {
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
	if m.list().section == sectionWorktrees {
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
		if got := worktreesModel().diffTitle(tc.tab); got != tc.title {
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
		got := ansi.Strip(v.bar(true, nil, width, style.New(false)))
		if ansi.StringWidth(got) > max(width, 8) {
			t.Errorf("%d: %d wide: %q", width, ansi.StringWidth(got), got)
		}
		if width >= 12 && !strings.Contains(got, "?") {
			t.Errorf("%d: the full list cannot be reached: %q", width, got)
		}
	}
}

// worktreeTabModel shows a worktree's tab, with an agent working in it if working.
func worktreeTabModel(t *testing.T, files ...api.WorktreeFile) (*Model, *agentList) {
	t.Helper()
	main := "main"
	m := worktreesModel(api.WorktreeStatus{Name: "w", Path: "/w", Ahead: aheadBy(1), Base: &main, Files: files})
	m.layout.setFocus(agent)
	l := m.list()
	l.openTab(committedTab("w"))
	unified := []string{"diff --git a/f b/f", "@@ -1 +1 @@", "-a", "+b"}
	l.diffs = map[string]*diffView{
		committedTab("w"):       {asked: true, unified: unified},
		uncommittedPrefix + "w": {asked: true, unified: unified, uncommitted: true},
	}
	for view, v := range l.diffs {
		v.seen = m.diffSeen(view)
	}
	return m, l
}

func TestWorktreeTabViews(t *testing.T) {
	t.Parallel()
	m, l := worktreeTabModel(t, changed("f", api.WorktreeFileStatusM))
	got := text(m)
	if !strings.Contains(got, "uncommitted: w") || !strings.Contains(got, "C commit") || !strings.Contains(got, "Tab commits") {
		t.Errorf("with changes it shows them:\n%s", got)
	}

	keys(m, "\t")
	if m.diffViewOf(l.active) != committedTab("w") {
		t.Fatal("Tab did not flip")
	}
	got = text(m)
	if !strings.Contains(got, "pr: w") || strings.Contains(got, "C commit") || !strings.Contains(got, "Tab uncommitted") {
		t.Errorf("the commits offer no commit:\n%s", got)
	}
	keys(m, commitKey)
	if m.commit != nil {
		t.Error("C acted on the commits")
	}
	keys(m, "\t")
	if !strings.HasPrefix(m.diffViewOf(l.active), uncommittedPrefix) {
		t.Fatal("Tab did not flip back")
	}

	// All committed, it moves by itself, and there is nothing to flip to.
	(*m.following.project.WorktreeStatuses)[0].Files = nil
	if m.diffViewOf(l.active) != committedTab("w") {
		t.Error("it stayed on a view with nothing to show")
	}
	got = text(m)
	if !strings.Contains(got, "pr: w") || strings.Contains(got, "Tab ") {
		t.Errorf("all committed:\n%s", got)
	}
	keys(m, "\t")
	if m.diffViewOf(l.active) != committedTab("w") {
		t.Error("Tab flipped to nothing")
	}
}

func TestWorktreeHolding(t *testing.T) {
	t.Parallel()
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r"},
		api.WorktreeStatus{Name: "work", Path: "/r/.worktrees/work"},
		api.WorktreeStatus{Name: "rest", Path: "/rest"},
	)
	for dir, want := range map[string]string{
		"/r": "main", "/r/sub": "main", "/r/.worktrees/work": "work", "/r/.worktrees/work/sub": "work", "/rest/x": "rest", "/r2": "", "/other": "",
	} {
		if got := m.worktreeHolding(dir); got != want {
			t.Errorf("%s: want %q, got %q", dir, want, got)
		}
	}
}

func runningAgent(id, dir string, activity api.AgentActivity) api.Agent {
	return api.Agent{Id: id, Name: id, Dir: dir, State: api.AgentStateRunning, Activity: &activity}
}

func TestCommitBox(t *testing.T) {
	t.Parallel()
	m, _ := worktreeTabModel(t, changed("f", api.WorktreeFileStatusM))

	// None running in the project: it says so.
	*m.following.project.Agents = nil
	keys(m, commitKey)
	if m.commit != nil || !strings.Contains(text(m), "no agent is running in this project") {
		t.Fatalf("with no agent:\n%s", text(m))
	}

	// One idle, in the worktree; what is sent says where it is.
	*m.following.project.Agents = []api.Agent{runningAgent("a", "/w/sub", api.AgentActivityIdle)}
	keys(m, commitKey)
	if m.commit == nil || len(m.commit.agents) != 1 || m.commit.text != commitPrompt+" in /w" {
		t.Fatalf("box: %+v", m.commit)
	}
	got := text(m)
	if !strings.Contains(got, commitPrompt) || strings.Contains(got, "is working") || strings.Contains(got, "← ") {
		t.Errorf("one idle agent:\n%s", got)
	}

	// The text can be edited, and Esc leaves it unsent.
	keys(m, "backspace", "!")
	if m.commit.text != commitPrompt+" in /!" {
		t.Errorf("text: %q", m.commit.text)
	}
	keys(m, "esc")
	if m.commit != nil {
		t.Error("Esc left the box")
	}

	// Several, one working: it can be chosen, and warned of.
	*m.following.project.Agents = []api.Agent{runningAgent("a", "/w", api.AgentActivityIdle), runningAgent("b", "/w", api.AgentActivityWorking)}
	keys(m, commitKey, "right")
	if m.commit.agents[m.commit.chosen].Id != "b" {
		t.Fatalf("chosen: %d", m.commit.chosen)
	}
	got = text(m)
	if !strings.Contains(got, "b is working") || !strings.Contains(got, "→") {
		t.Errorf("a working agent:\n%s", got)
	}

	// A failure keeps the text and says why.
	m.Update(commitSent{agent: "b", err: errors.New("agent_not_running")})
	if m.commit == nil || m.commit.err == "" || !strings.Contains(text(m), "agent_not_running") {
		t.Errorf("failure: %+v", m.commit)
	}
	m.Update(commitSent{agent: "b"})
	if m.commit != nil || !strings.Contains(text(m), "sent to b") {
		t.Errorf("sent: %+v", m.commit)
	}
}

func TestPatchPath(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"diff --git a/f b/f":                   "f",
		"diff --git a/with space b/with space": "with space",
		"diff --git a/dir/b/x b/dir/b/x":       "dir/b/x",
		`diff --git "a/odd\t" "b/odd\t"`:       "",
	} {
		got, ok := patchPath(line)
		if !ok {
			got = ""
		}
		if got != want {
			t.Errorf("%s: %q, want %q", line, got, want)
		}
	}
}

func TestVerifiedProgress(t *testing.T) {
	t.Parallel()
	yes := changed("a", api.WorktreeFileStatusM)
	yes.Verified = true
	no := changed("b", api.WorktreeFileStatusM)
	for _, tc := range []struct {
		w    api.WorktreeStatus
		want string
	}{
		{api.WorktreeStatus{Files: []api.WorktreeFile{no}}, ""},
		{api.WorktreeStatus{Files: []api.WorktreeFile{yes, no}}, "1/2 ✓"},
		{api.WorktreeStatus{CommittedFiles: []api.WorktreeCommittedFile{{Path: "a", Verified: true}, {Path: "b"}, {Path: "c"}}}, "1/3 ✓"},
		// What is not committed comes first: its files are what the tab shows.
		{api.WorktreeStatus{Files: []api.WorktreeFile{no}, CommittedFiles: []api.WorktreeCommittedFile{{Path: "a", Verified: true}}}, ""},
	} {
		if got := verifiedProgress(tc.w); got != tc.want {
			t.Errorf("%+v: want %q, got %q", tc.w, tc.want, got)
		}
	}
}

// TestVerifyInATide goes through a real daemon and git: v on what is on the screen, a
// commit, and an edit that takes it away.
func TestVerifyInATide(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, "file"), "one\n")
	writeFile(t, filepath.Join(dir, "other"), "one\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work")
	writeFile(t, filepath.Join(work, "file"), "two\n")
	writeFile(t, filepath.Join(work, "other"), "two\n")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the worktree", func(m *Model) bool { return m.hasUncommitted("work") })
	m := p.m
	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.worktree.Name == "work" }))
	p.keys("enter")
	p.until(t, "the uncommitted view", func(m *Model) bool { return strings.Contains(text(m), "v verified") })

	// v marks the file the current line is in.
	p.keys(verifyKey)
	p.until(t, "the mark", func(m *Model) bool {
		got := text(m)
		return m.verifiedIn("work")["file"] && strings.Contains(got, "✓ diff --git a/file b/file") && strings.Contains(got, "1/2 ✓") && strings.Contains(got, "v unverified")
	})
	if m.verifiedIn("work")["other"] {
		t.Error("another file was marked")
	}

	// It holds through a commit of what was verified.
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "two")
	p.until(t, "the commits, with the mark", func(m *Model) bool {
		got := text(m) // the first draw of a view: the bar knows the current file then too
		return strings.Contains(got, "pr: work") && strings.Contains(got, "✓ diff --git a/file b/file") && strings.Contains(got, "v unverified")
	})

	// An edit takes it away.
	writeFile(t, filepath.Join(work, "file"), "three\n")
	gittest.Git(t, work, "add", "file")
	p.until(t, "the mark gone", func(m *Model) bool { return !m.verifiedIn("work")["file"] })
	gittest.Git(t, work, "reset", "-q", "--hard")
	p.until(t, "all committed", func(m *Model) bool { return !m.hasUncommitted("work") })

	// v again takes a mark away.
	p.keys(verifyKey)
	p.until(t, "the mark", func(m *Model) bool { return len(m.verifiedIn("work")) == 1 })
	p.keys(verifyKey)
	p.until(t, "no mark", func(m *Model) bool { return len(m.verifiedIn("work")) == 0 })
}

func TestUnifiedAnchors(t *testing.T) {
	t.Parallel()
	v := &diffView{unified: []string{
		"diff --git a/f b/f", "index 1..abc 100644", "--- a/f", "+++ b/f", "@@ -5,3 +5,3 @@ fn",
		" same", "-old", "+new", `\ No newline at end of file`,
	}}
	v.prepare(80, style.New(false))
	want := map[int]lineAnchor{
		5: {"f", 5, api.WorktreeCommentSideNew, "same", "abc"},
		6: {"f", 6, api.WorktreeCommentSideOld, "old", "abc"},
		7: {"f", 6, api.WorktreeCommentSideNew, "new", "abc"},
	}
	for i, a := range v.anchors {
		if a != want[i] {
			t.Errorf("line %d (%q): %+v, want %+v", i, v.unified[i], a, want[i])
		}
	}
}

// TestCommentsShowUnderTheirLines keeps the current line on its row as comments come,
// and only shows those written on this view.
func TestCommentsShowUnderTheirLines(t *testing.T) {
	t.Parallel()
	main := "main"
	comment := func(id string, line int, view api.WorktreeCommentView, changed bool) api.WorktreeComment {
		return api.WorktreeComment{Id: id, File: "f", Line: line, Side: api.WorktreeCommentSideNew, View: view, Body: "text of " + id, Changed: changed}
	}
	m := worktreesModel(api.WorktreeStatus{
		Name: "w", Path: "/w", Base: &main, Ahead: aheadBy(0),
		Comments: []api.WorktreeComment{comment("one", 3, api.WorktreeCommentViewUncommitted, false), comment("two", 10, api.WorktreeCommentViewUncommitted, true), comment("gone", 90, api.WorktreeCommentViewUncommitted, false), comment("other", 5, api.WorktreeCommentViewCommitted, false)},
	})
	m.layout.setFocus(agent)
	l := m.list()
	tab := fileDiffTab("w", "f")
	l.openTab(tab)
	v := &diffView{asked: true, file: "f", split: longFile(30)}
	v.seen = m.diffSeen(tab)
	l.diffs = map[string]*diffView{tab: v}
	got := text(m)
	for _, want := range []string{"✎ text of one", "✎ text of two", "file changed", "1 comment on lines not shown here", "s send (4)", "c comment"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "text of other") {
		t.Errorf("a comment of the other view shows:\n%s", got)
	}
	keys(m, "down", "down")
	if c, ok := v.commentOf(); !ok || c.Id != "one" {
		t.Errorf("on line 3: %+v, %v", c, ok)
	}
	if got := text(m); !strings.Contains(got, "e edit") || !strings.Contains(got, "d delete") {
		t.Errorf("no keys for it:\n%s", got)
	}
	keys(m, "down")
	if c, ok := v.commentOf(); !ok || c.Id != "one" {
		t.Errorf("on the comment itself: %+v, %v", c, ok)
	}
	if _, ok := v.anchorOf(); ok {
		t.Error("a comment's own line can be commented on")
	}
	keys(m, commentKey)
	if m.comment != nil || !strings.Contains(text(m), "not on a line of a file") {
		t.Errorf("c on a comment:\n%s", text(m))
	}
	keys(m, "up")

	// c opens a box on the line, and Esc leaves it.
	keys(m, commentKey)
	if m.comment == nil || m.comment.anchor.line != 3 || m.comment.anchor.quote != "line3" {
		t.Fatalf("box: %+v", m.comment)
	}
	got = text(m)
	if !strings.Contains(got, "f, line 3") || !strings.Contains(got, "line3") {
		t.Errorf("the box does not say the line:\n%s", got)
	}
	keys(m, "hello", "backspace")
	if m.comment.text != "hell" {
		t.Errorf("text: %q", m.comment.text)
	}
	keys(m, "esc")
	if m.comment != nil {
		t.Error("Esc left the box")
	}

	// A comment coming above leaves the current line on its row.
	keys(m, "G")
	text(m)
	row := v.rows[v.cursor]
	(*m.following.project.WorktreeStatuses)[0].Comments = append((*m.following.project.WorktreeStatuses)[0].Comments, comment("new", 1, api.WorktreeCommentViewUncommitted, false))
	text(m)
	if v.rows[v.cursor] != row {
		t.Errorf("the current line moved from row %d to %d", row, v.rows[v.cursor])
	}

	// e opens on its text.
	v.cursor = v.starts[2]
	keys(m, editKey)
	if m.comment == nil || m.comment.editing != "one" || m.comment.text != "text of one" {
		t.Errorf("edit: %+v", m.comment)
	}
	keys(m, "esc")

	// A failure leaves the box with its text, and why.
	m.comment = &commentBox{worktree: "w", anchor: lineAnchor{file: "f", line: 1}, text: "keep me", sending: true}
	m.Update(commentAnswered{err: errors.New("no such worktree")})
	if m.comment == nil || m.comment.text != "keep me" || !strings.Contains(text(m), "no such worktree") {
		t.Errorf("failure: %+v", m.comment)
	}
	m.Update(commentAnswered{})
	if m.comment != nil {
		t.Error("a success left the box")
	}
}

func TestReviewBox(t *testing.T) {
	t.Parallel()
	main := "main"
	drafts := []api.WorktreeComment{{Id: "a", File: "f", Line: 1, Side: api.WorktreeCommentSideNew, View: api.WorktreeCommentViewUncommitted, Body: "x"}}
	m := worktreesModel(api.WorktreeStatus{Name: "w", Path: "/w", Base: &main, Ahead: aheadBy(0), Comments: drafts, Files: []api.WorktreeFile{changed("f", api.WorktreeFileStatusM)}})
	m.layout.setFocus(agent)
	l := m.list()
	l.openTab(committedTab("w"))
	view := uncommittedPrefix + "w"
	v := &diffView{asked: true, uncommitted: true, unified: []string{"diff --git a/f b/f", "@@ -1 +1 @@", "-a", "+b"}}
	v.seen = m.diffSeen(view)
	l.diffs = map[string]*diffView{view: v}

	*m.following.project.Agents = nil
	keys(m, reviewKey)
	if m.commit != nil || !strings.Contains(text(m), "the comments stay") {
		t.Fatalf("no agent:\n%s", text(m))
	}
	*m.following.project.Agents = []api.Agent{runningAgent("a", "/w", api.AgentActivityIdle)}
	keys(m, reviewKey)
	if m.commit == nil || m.commit.review != 1 {
		t.Fatalf("box: %+v", m.commit)
	}
	if got := text(m); !strings.Contains(got, "Send review") || !strings.Contains(got, "1 comment to a") {
		t.Errorf("box:\n%s", got)
	}
	keys(m, "esc")

	// Asking to commit warns of them.
	keys(m, commitKey)
	if got := text(m); !strings.Contains(got, "1 comment not sent") {
		t.Errorf("no warning:\n%s", got)
	}
}

// TestReviewInATide goes through a real daemon, to a real agent's inbox.
func TestReviewInATide(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	writeFile(t, filepath.Join(dir, "file"), "one\n")
	if _, err := h.client().CreateAgent(t.Context(), api.CreateAgent{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the worktree and its agent", func(m *Model) bool { return m.hasUncommitted("main") && len(m.runningAgentsIn("main")) == 1 })
	m := p.m
	m.moveToWorktree(0)
	p.keys("enter")
	p.until(t, "the uncommitted view", func(m *Model) bool { return strings.Contains(text(m), "+one") })
	v := m.shownDiff()
	line := slices.IndexFunc(v.unified, func(s string) bool { return s == "+one" })
	v.cursor = v.starts[line]

	p.keys(commentKey, "keep this", "enter")
	p.until(t, "the comment under its line", func(m *Model) bool {
		got := text(m)
		return m.comment == nil && strings.Contains(got, "✎ keep this") && strings.Contains(got, "s send (1)")
	})
	v.cursor = v.starts[line]
	p.keys(editKey, "!", "enter")
	p.until(t, "the comment edited", func(m *Model) bool { return strings.Contains(text(m), "✎ keep this!") })

	p.keys(reviewKey, "first note", "enter")
	p.until(t, "the comment sent", func(m *Model) bool { return m.commit == nil && len(m.draftComments("main")) == 0 })
	if got := text(m); strings.Contains(got, "keep this") || strings.Contains(got, "s send") {
		t.Errorf("a sent comment shows:\n%s", got)
	}
	list, err := h.client().WorktreeComments(t.Context(), "main", "app", "")
	if err != nil || len(list.Comments) != 1 || list.Comments[0].State != api.WorktreeCommentStateSent || list.Comments[0].Body != "keep this!" || list.Comments[0].Line != 1 {
		t.Errorf("kept: %+v, %v", list, err)
	}

	// d deletes one not sent.
	v = m.shownDiff()
	line = slices.IndexFunc(v.unified, func(s string) bool { return s == "+one" })
	v.cursor = v.starts[line]
	p.keys(commentKey, "to go", "enter")
	p.until(t, "another", func(m *Model) bool {
		return m.comment == nil && len(m.draftComments("main")) == 1 && strings.Contains(text(m), "✎ to go")
	})
	v.cursor = v.starts[line]
	p.keys(deleteKey)
	p.until(t, "it deleted", func(m *Model) bool { return len(m.draftComments("main")) == 0 })
}

// TestAgentsToAsk is an agent that made a worktree itself: it counts by where it says
// it is, and one that does not say can still be asked, from anywhere in the project.
func TestAgentsToAsk(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"}, api.WorktreeStatus{Name: "work", Path: "/r/.worktrees/work"})
	moved := runningAgent("moved", "/r", api.AgentActivityIdle)
	dir := "/r/.worktrees/work/sub"
	moved.CurrentDir = &dir
	silent := runningAgent("silent", "/r", api.AgentActivityIdle)
	away := runningAgent("away", "/r", api.AgentActivityIdle)
	outside := "/tmp"
	away.CurrentDir = &outside
	stopped := runningAgent("stopped", "/r/.worktrees/work", api.AgentActivityIdle)
	stopped.State = api.AgentStateExited
	*m.following.project.Agents = []api.Agent{silent, away, moved, stopped}

	if got := m.worktreeOf(moved); got != "work" {
		t.Errorf("moved: %q", got)
	}
	if got := m.worktreeOf(away); got != "main" {
		t.Errorf("somewhere that is no worktree falls back to where it started: %q", got)
	}
	if got := m.agentsIn("work"); !slices.Equal(got, []string{"moved"}) {
		t.Errorf("on the row: %v", got)
	}

	agents, chosen := m.agentsToAsk("work")
	var ids []string
	for _, a := range agents {
		ids = append(ids, a.Id)
	}
	if !slices.Equal(ids, []string{"moved", "silent", "away"}) || chosen != 0 {
		t.Errorf("order %v, chosen %d", ids, chosen)
	}

	// The one sent to is offered next time.
	m.list().lastAsked = map[string]string{"work": "away"}
	if _, chosen = m.agentsToAsk("work"); chosen != 2 {
		t.Errorf("remembered: %d", chosen)
	}
	if _, chosen = m.agentsToAsk("main"); chosen != 0 {
		t.Errorf("another worktree's: %d", chosen)
	}

	// It says when one is elsewhere.
	m.commit = &commitBox{worktree: "work", agents: agents, chosen: 1}
	if got := text(m); !strings.Contains(got, "silent (in main)") || !strings.Contains(got, "← ") {
		t.Errorf("box:\n%s", got)
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
	if m.layout.focus != agent || !m.textShown() {
		t.Fatalf("focus %v, tab %q", m.layout.focus, m.list().active)
	}
	p.until(t, "the split view", func(m *Model) bool {
		got := text(m)
		return strings.Contains(got, "HEAD") && strings.Contains(got, "- two") && strings.Contains(got, "+ 2")
	})

	writeFile(t, filepath.Join(work, "file"), "one\nedited\nthree\n")
	m.layout.setFocus(project) // f reads again from the left pane, the open tab too
	p.keys("C-q", refreshKey)
	p.until(t, "the edit in the open tab", func(m *Model) bool { return strings.Contains(text(m), "+ edited") })

	// git run by anyone needs no key.
	gittest.Git(t, work, "add", "file")
	gittest.Git(t, work, "commit", "-qm", "edited")
	p.until(t, "the commit", func(m *Model) bool {
		return strings.Contains(text(m), "no uncommitted changes") && strings.Contains(text(m), "↑2")
	})

	// Enter on the worktree shows what is not committed, while there is any.
	writeFile(t, filepath.Join(work, "later"), "still to commit\n")
	m.layout.setFocus(project)
	p.keys("C-q", refreshKey)
	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.file == nil && r.worktree.Name == "work" }))
	p.keys("enter")
	p.until(t, "the uncommitted view", func(m *Model) bool {
		got := text(m)
		return strings.Contains(got, "uncommitted: work") && strings.Contains(got, "+still to commit") && strings.Contains(got, "C commit")
	})
	if got := text(m); strings.Contains(got, "+committed") {
		t.Errorf("what is committed is in the uncommitted view:\n%s", got)
	}
	p.keys("\t")
	p.until(t, "the commits, by Tab", func(m *Model) bool {
		got := text(m)
		return strings.Contains(got, "pr: work") && strings.Contains(got, "base: main") && strings.Contains(got, "+committed")
	})
	p.keys("\t")
	p.until(t, "the uncommitted view again", func(m *Model) bool { return strings.Contains(text(m), "uncommitted: work") })

	// Once it is committed, by anyone, the tab moves by itself.
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "later")
	p.until(t, "the commits, by themselves", func(m *Model) bool {
		v := m.shownDiff() // the line may be below what is on the screen
		return strings.Contains(text(m), "pr: work") && v != nil && slices.Contains(v.unified, "+still to commit")
	})

	// A diff's tab stays when its worktree is removed outside wdx, marked missing,
	// and says what is gone.
	if len(m.list().tabs) != 2 {
		t.Errorf("tabs: %v", m.list().tabs)
	}
	gittest.Git(t, dir, "worktree", "remove", "--force", work)
	p.until(t, "the worktree gone", func(m *Model) bool { return !strings.Contains(text(m), "↑2") })
	if len(m.list().tabs) != 2 {
		t.Errorf("tabs after: %v", m.list().tabs)
	}
	p.until(t, "the tab saying so", func(m *Model) bool {
		return strings.Contains(text(m), "worktree work is no longer there") && strings.Contains(titleRow(m), "✗")
	})
}

// TestCommitSent goes through a real daemon to a real agent's inbox: what Enter sends
// is what the agent is given.
func TestCommitSent(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	writeFile(t, filepath.Join(dir, "file"), "changed\n")
	if _, err := h.client().CreateAgent(t.Context(), api.CreateAgent{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the worktree and its agent", func(m *Model) bool { return m.hasUncommitted("main") && len(m.runningAgentsIn("main")) == 1 })
	m := p.m
	m.moveToWorktree(0)
	p.keys("enter")
	p.until(t, "the uncommitted view", func(m *Model) bool { return strings.Contains(text(m), "C commit") })
	p.keys(commitKey)
	if m.commit == nil {
		t.Fatalf("no box:\n%s", text(m))
	}
	p.keys("enter")
	p.until(t, "the answer", func(m *Model) bool { return m.commit == nil })
	if m.statusNotice == "" || !strings.HasPrefix(m.statusNotice, "sent to ") {
		t.Errorf("notice: %q", m.statusNotice)
	}
}

func TestRemovableWorktree(t *testing.T) {
	t.Parallel()
	main := "main"
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r", Ahead: aheadBy(0), Base: &main},
		api.WorktreeStatus{Name: "landed-one", Path: "/r/l", Ahead: aheadBy(0), Base: &main},
		api.WorktreeStatus{Name: "ahead", Path: "/r/a", Ahead: aheadBy(1), Base: &main},
		api.WorktreeStatus{Name: "dirty", Path: "/r/d", Ahead: aheadBy(0), Base: &main, Files: []api.WorktreeFile{changed("f", api.WorktreeFileStatusM)}},
		api.WorktreeStatus{Name: "gone", Path: "/r/g", Missing: true},
	)
	for i, want := range []bool{false, true, false, false, true} {
		m.moveToWorktree(i)
		r, ok := m.removable()
		if ok != want || (ok && r.kind != "worktree") {
			t.Errorf("row %d: %+v, %v", i, r, ok)
		}
	}
	m.moveToWorktree(1)
	wantLine := func(r removal, want string) {
		t.Helper()
		if !slices.ContainsFunc(r.worktree.lines(), func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("no %q in %q", want, r.worktree.lines())
		}
	}
	r, _ := m.removable()
	wantLine(r, "Its branch goes too")
	m.moveToWorktree(4)
	r, _ = m.removable()
	wantLine(r, "only git's entry")
	r.worktree.agents, r.worktree.terminals = []string{"swift"}, []string{"shell"}
	wantLine(r, "Told to work from the clone: swift")
	wantLine(r, "Left running, with no directory: shell")
}

func TestRemoveWorktreeInATide(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	gittest.Git(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work")
	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	row := func(m *Model) int {
		return slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.worktree.Name == "work" })
	}
	p.until(t, "the worktree, landed", func(m *Model) bool {
		i := row(m)
		return i >= 0 && worktreeSummary(m.worktreeRows()[i].worktree) == "landed"
	})
	m := p.m
	m.moveToWorktree(row(m))
	p.keys("enter")
	if len(m.list().tabs) != 1 {
		t.Fatalf("tabs: %v", m.list().tabs)
	}
	p.keys("alt+left", "C-q")
	wantText(t, m, "work (worktree)", "x  remove")
	p.keys("x")
	wantText(t, m, "Remove worktree", "Remove work?", "Its branch goes too")
	p.keys("n")
	if _, err := os.Stat(work); err != nil || m.confirm != nil {
		t.Fatalf("n: %v, %+v", err, m.confirm)
	}
	p.keys("C-q", "x", "y")
	p.until(t, "the answer and the row gone", func(m *Model) bool { return m.confirm == nil && row(m) < 0 })
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Errorf("the directory: %v", err)
	}
	if len(m.list().tabs) != 0 {
		t.Errorf("its tab stayed: %v", m.list().tabs)
	}
	if !strings.Contains(m.statusNotice, "removed work") {
		t.Errorf("notice: %q", m.statusNotice)
	}
}

func TestLandingSummary(t *testing.T) {
	t.Parallel()
	code := func(c api.ErrorCode) *string { s := string(c); return &s }
	for _, tc := range []struct {
		state api.WorktreeLandingState
		code  *string
		ahead int
		want  string
	}{
		{api.WorktreeLandingStateRebasing, nil, 1, "landing: rebase"},
		{api.WorktreeLandingStateChecking, nil, 1, "landing: check"},
		{api.WorktreeLandingStateMoving, nil, 1, "landing"},
		{api.WorktreeLandingStatePushing, nil, 1, "landing: push"},
		{api.WorktreeLandingStateOpening, nil, 1, "landing: PR"},
		{api.WorktreeLandingStateOpened, nil, 1, ""},
		{api.WorktreeLandingStateFailed, code(api.ErrorCodePushFailed), 1, "push failed"},
		{api.WorktreeLandingStateFailed, code(api.ErrorCodePrFailed), 1, "PR failed"},
		{api.WorktreeLandingStateFailed, code(api.ErrorCodeRebaseFailed), 1, "rebase conflict"},
		{api.WorktreeLandingStateFailed, code(api.ErrorCodeCheckFailed), 1, "check failed"},
		{api.WorktreeLandingStateFailed, code(api.ErrorCodeBaseMoved), 1, "base moved"},
		{api.WorktreeLandingStateFailed, code(api.ErrorCodeFastForwardFailed), 1, "not landed"},
		// What failed says nothing once its commits got there another way.
		{api.WorktreeLandingStateFailed, code(api.ErrorCodeCheckFailed), 0, ""},
		{api.WorktreeLandingStateLanded, nil, 0, ""},
	} {
		w := api.WorktreeStatus{Ahead: aheadBy(tc.ahead), Landing: &api.WorktreeLanding{State: tc.state, Code: tc.code}}
		if got := landingSummary(w); got != tc.want {
			t.Errorf("%s %v: %q, want %q", tc.state, deref(tc.code), got, tc.want)
		}
	}
}

func TestLandBarKey(t *testing.T) {
	t.Parallel()
	main, origin, here := "main", "origin/main", "abc"
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r", Ahead: aheadBy(2), Base: &main},
		api.WorktreeStatus{Name: "ready", Path: "/r/a", Ahead: aheadBy(1), Base: &main},
		api.WorktreeStatus{Name: "dirty", Path: "/r/b", Ahead: aheadBy(1), Base: &main, Files: []api.WorktreeFile{changed("f", api.WorktreeFileStatusM)}},
		api.WorktreeStatus{Name: "nothing", Path: "/r/c", Ahead: aheadBy(0), Base: &main},
		api.WorktreeStatus{Name: "remote", Path: "/r/d", Ahead: aheadBy(1), Base: &origin},
		api.WorktreeStatus{Name: "going", Path: "/r/e", Ahead: aheadBy(1), Base: &main, Landing: &api.WorktreeLanding{State: api.WorktreeLandingStateChecking}},
		api.WorktreeStatus{Name: "merged", Path: "/r/f", Ahead: aheadBy(1), Base: &main, Head: &here, PullRequest: &api.WorktreePullRequest{Number: 12, State: api.WorktreePullRequestStateMerged, Head: here}},
		api.WorktreeStatus{Name: "open", Path: "/r/g", Ahead: aheadBy(1), Base: &main, Head: &here, PullRequest: &api.WorktreePullRequest{Number: 12, State: api.WorktreePullRequestStateOpen, Head: here}},
		api.WorktreeStatus{Name: "newer", Path: "/r/h", Ahead: aheadBy(1), Base: &main, Head: &here, PullRequest: &api.WorktreePullRequest{Number: 12, State: api.WorktreePullRequestStateOpen, Head: "older"}},
	)
	for name, want := range map[string]string{"main": "", "ready": "land", "dirty": commitFirst, "nothing": "", "remote": "land", "merged": "", "open": "PR #12 open", "newer": "land", "going": "landing…", "gone": ""} {
		key, ok := m.landBarKey(name)
		if ok != (want != "") || key.label != want {
			t.Errorf("%s: %+v, %v, want %q", name, key, ok, want)
		}
	}
	// A file's diff has none, and the keys say so.
	for _, key := range m.worktreeBarKeys(fileDiffTab("ready", "f")) {
		if key.keys == landKey {
			t.Errorf("a file's diff offers %+v", key)
		}
	}
	if !slices.ContainsFunc(m.worktreeBarKeys(committedTab("ready")), func(k barKey) bool { return k.keys == landKey && k.label == "land" }) {
		t.Errorf("no L: %+v", m.worktreeBarKeys(committedTab("ready")))
	}
	m.flipWorktreeView(committedTab("dirty")) // on what is not committed, C is there instead
	if !slices.ContainsFunc(m.worktreeBarKeys(committedTab("dirty")), func(k barKey) bool { return k.keys == landKey && k.label == commitFirst }) {
		t.Errorf("no commit first: %+v", m.worktreeBarKeys(committedTab("dirty")))
	}
}

func TestWorktreeRowSaysItsPR(t *testing.T) {
	t.Parallel()
	here := "abc"
	merged := api.WorktreeStatus{Name: "work", Ahead: aheadBy(1), Head: &here, PullRequest: &api.WorktreePullRequest{Number: 12, State: api.WorktreePullRequestStateMerged, Head: here}}
	if got := worktreeSummary(merged); got != "PR #12 merged ↑1" {
		t.Errorf("summary: %q", got)
	}
	if !mergedThroughPR(merged) {
		t.Errorf("merged: not removable")
	}
	merged.Files = []api.WorktreeFile{changed("f", api.WorktreeFileStatusM)}
	if mergedThroughPR(merged) {
		t.Errorf("merged, with a file changed: removable")
	}
	other := "def"
	merged.Files, merged.Head = nil, &other
	if mergedThroughPR(merged) {
		t.Errorf("merged with other commits: removable")
	}
}

func TestLandByPRInATide(t *testing.T) {
	t.Parallel()
	p, dir, work := landInTide(t)
	m := p.m
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, dir, "init", "-q", "--bare", origin)
	gittest.Git(t, dir, "remote", "add", "origin", origin)

	p.keys(landKey)
	p.until(t, "the box", func(m *Model) bool { return m.land != nil && m.land.editing })
	wantText(t, m, "Method:", "rebase and move the base", "Push the base to origin")
	p.keys("right")
	p.until(t, "the method changed", func(m *Model) bool { return m.land.method == api.ProjectLandingMethodPr })
	wantText(t, m, "open a PR")
	if strings.Contains(text(m), "Push the base to origin") {
		t.Errorf("a PR has no base to push:\n%s", text(m))
	}
	p.keys("enter")
	p.until(t, "the settings saved", func(m *Model) bool { return m.land != nil && !m.land.editing && !m.land.busy })
	wantText(t, m, "Open a PR of work onto main?", "Check: none", "open PR")

	p.keys("y")
	p.until(t, "the PR on its row", func(m *Model) bool {
		w, ok := m.worktreeNamed("work")
		return m.land == nil && ok && w.PullRequest != nil && strings.Contains(m.statusNotice, "PR #12 open")
	})
	if m.confirm != nil {
		t.Errorf("offered to remove what has not merged: %+v", m.confirm)
	}
	if strings.Contains(gitLog(t, dir), "work") {
		t.Errorf("reached main: %s", gitLog(t, dir))
	}
	wantText(t, m, "PR #12 open")
	if _, err := os.Stat(work); err != nil {
		t.Errorf("the worktree: %v", err)
	}
}

// landInTide is a tide showing a project with a worktree ready to land, on its tab.
func landInTide(t *testing.T) (*program, string, string) {
	t.Helper()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	gittest.Git(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work")
	writeFile(t, filepath.Join(work, "more"), "a\n")
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "work")
	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the worktree, ahead", func(m *Model) bool {
		w, ok := m.worktreeNamed("work")
		return ok && w.Ahead != nil && *w.Ahead == 1
	})
	m := p.m
	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.worktree.Name == "work" }))
	p.keys("enter")
	p.until(t, "the bar", func(m *Model) bool { return strings.Contains(text(m), "L land") })
	return p, dir, work
}

func TestLandInATide(t *testing.T) {
	t.Parallel()
	p, dir, work := landInTide(t)
	m := p.m

	// The first time, it asks how the project lands.
	p.keys(landKey)
	p.until(t, "the box", func(m *Model) bool { return m.land != nil })
	if !m.land.editing {
		t.Fatalf("not asked: %+v", m.land)
	}
	wantText(t, m, "Landing settings", "Check to run first")
	p.keys("enter") // no check: a test's shell runs no command
	p.until(t, "the settings saved", func(m *Model) bool { return m.land != nil && !m.land.editing && !m.land.busy })
	wantText(t, m, "Land work on main?", "↑1", "Check: none", "Push: no")
	p.keys("n")
	if m.land != nil {
		t.Fatalf("n: %+v", m.land)
	}
	if out := gitLog(t, dir); strings.Contains(out, "work") {
		t.Fatalf("n landed it: %s", out)
	}

	// The second time, it knows, and lands; what landed is offered for removal.
	p.keys(landKey)
	p.until(t, "the box", func(m *Model) bool { return m.land != nil })
	if m.land.editing {
		t.Fatalf("asked again: %+v", m.land)
	}
	p.keys("y")
	p.until(t, "the offer to remove", func(m *Model) bool { return m.land == nil && m.confirm != nil })
	wantText(t, m, "Remove work?")
	if !strings.Contains(gitLog(t, dir), "work") {
		t.Errorf("not on main: %s", gitLog(t, dir))
	}
	p.keys("y")
	p.until(t, "removed", func(m *Model) bool { _, ok := m.worktreeNamed("work"); return !ok && m.confirm == nil })
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Errorf("the directory: %v", err)
	}
}

// wdx's own rebase stopping leads to asking an agent, which is what L then does.
func TestLandAsksAnAgentAfterAConflict(t *testing.T) {
	t.Parallel()
	p, dir, work := landInTide(t)
	m := p.m
	writeFile(t, filepath.Join(dir, "more"), "ours\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "ours")
	if _, err := m.client.SetProjectLanding(t.Context(), "app", api.ProjectLandingUpdate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.client.CreateAgent(t.Context(), api.CreateAgent{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	p.keys(landKey)
	p.until(t, "the box", func(m *Model) bool { return m.land != nil })
	p.keys("y")
	// The landing fails at the head the rebase was undone to, which the worktree's own
	// status may show only later: until then L takes it as rebased since, and lands. A
	// status read while the rebase was undone can show that head with the conflicted
	// files still there, when L says to commit first.
	p.until(t, "the conflict", func(m *Model) bool {
		w, ok := m.worktreeNamed("work")
		return ok && landingFailed(w) && deref(w.Head) == deref(w.Landing.Head) && len(w.Files) == 0 && m.land == nil &&
			len(m.agents()) == 1 && m.agents()[0].State == api.AgentStateRunning
	})
	if !strings.Contains(m.statusNotice, "L asks an agent") {
		t.Errorf("notice: %q", m.statusNotice)
	}
	if !strings.Contains(text(m), "conflict ") {
		t.Errorf("row:\n%s", text(m))
	}
	p.keys(landKey)
	if m.commit == nil {
		t.Fatalf("no box:\n%s", text(m))
	}
	if !strings.HasPrefix(m.commit.text, "Rebase work onto main in "+work) {
		t.Errorf("text: %q", m.commit.text)
	}
	wantText(t, m, "Ask to rebase", "Stopped on: more")
	p.keys("enter")
	p.until(t, "sent", func(m *Model) bool { return m.commit == nil })

	// Rebased by someone, it lands again rather than asking again.
	gittest.Git(t, work, "reset", "-q", "--hard", "main")
	writeFile(t, filepath.Join(work, "again"), "b\n")
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "again")
	// The head moved at the reset too, with nothing to land yet, and L does nothing then.
	p.until(t, "the new commit", func(m *Model) bool {
		w, ok := m.worktreeNamed("work")
		return ok && deref(w.Head) != deref(w.Landing.Head) && len(w.Files) == 0 && w.Ahead != nil && *w.Ahead == 1
	})
	p.keys(landKey)
	p.until(t, "the land box", func(m *Model) bool { return m.land != nil })
}

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
