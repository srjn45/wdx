package tide

import (
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func TestFilterMatches(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		text, s string
		want    bool
	}{
		{"tide tabs", "internal/tide/tabs.go", true},
		{"tabs tide", "internal/tide/tabs.go", true}, // in any order
		{"TABS", "internal/tide/tabs.go", true},
		{"tabs", "internal/tide/TABS.go", true},
		{"tide code", "internal/tide/tabs.go", false}, // every word
		{"  tabs  ", "tabs.go", true},
		{"é", "café", true},
	} {
		if got := matches(tc.s, listFilter{text: tc.text}.words()); got != tc.want {
			t.Errorf("%q in %q: %v", tc.text, tc.s, got)
		}
	}
	if (listFilter{text: "  "}).words() != nil {
		t.Error("spaces filter")
	}
}

// leftPane is what the project pane shows, a line a row.
func leftPane(m *Model) string {
	var rows []string
	for line := range strings.Lines(text(m)) {
		if cells := strings.SplitN(lightSides(line), "│", 3); len(cells) == 3 {
			rows = append(rows, cells[1])
		}
	}
	return strings.Join(rows, "\n")
}

// filterModel has agents, two worktrees, and the main checkout's files read.
func filterModel() *Model {
	main, fix := "main", "fix-login"
	return codeModel([]string{"internal/tide/tabs.go", "internal/tide/code.go", "docs/TIDE.md", "README"},
		api.WorktreeStatus{Name: "main", Path: "/r", Branch: &main},
		api.WorktreeStatus{Name: "auth-fix", Path: "/a", Branch: &fix, Files: []api.WorktreeFile{
			changed("internal/tide/tabs.go", api.WorktreeFileStatusM), changed("go.mod", api.WorktreeFileStatusM),
		}},
	)
}

func TestFilterAsTyped(t *testing.T) {
	t.Parallel()
	m := filterModel()
	l := m.list()
	keys(m, "/")
	if !strings.Contains(statusRow(m), "type to filter") {
		t.Fatalf("the line did not open: %q", statusRow(m))
	}
	// t, a and s are the list's own letters: a terminal, an agent, the settings.
	keys(m, "t", "i", "d", "e", " ", "t", "a", "b", "s")
	got := leftPane(m)
	for _, want := range []string{" / tide tabs_", "no match", "auth-fix", "M internal/tide/tabs.go", "< main >", "− internal", "− tide", "tabs.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in:\n%s", want, got)
		}
	}
	for _, gone := range []string{"swift-falcon", "go.mod", "code.go", "README", "docs"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%q shows:\n%s", gone, got)
		}
	}
	if !strings.Contains(row(t, m, " / tide tabs"), "2") {
		t.Errorf("the count: %q", row(t, m, " / tide tabs"))
	}
	// The cursor goes to the first match.
	if l.section != sectionWorktrees || l.worktreeKey != "auth-fix"+rowSeparator+"internal/tide/tabs.go" {
		t.Errorf("the cursor is on %v %q", l.section, l.worktreeKey)
	}

	// Enter goes to the list, and its keys act again.
	keys(m, "enter")
	if !strings.Contains(statusRow(m), "Esc clear filter  ↑↓ select") {
		t.Errorf("in the list: %q", statusRow(m))
	}
	keys(m, "down", "down", "down", "down")
	if r, ok := m.codeCursorRow(); !ok || r.key() != "internal/tide/tabs.go" {
		t.Errorf("down went to %+v", r)
	}
	wantText(t, m, " / tide tabs")
	// / goes back to the line, with what it had.
	keys(m, "/", "backspace", "backspace", "backspace", "backspace", "backspace")
	wantText(t, m, " / tide_", "code.go", "TIDE.md")

	// Esc clears it from the line, and the folds come back.
	keys(m, "esc")
	got = leftPane(m)
	if strings.Contains(got, " / ") || !strings.Contains(got, "+ internal") || !strings.Contains(got, "swift-falcon") {
		t.Errorf("not cleared:\n%s", got)
	}
	if l.section != sectionCode || l.code.cursor != "internal" {
		t.Errorf("the cursor did not go to what holds its file: %v %q", l.section, l.code.cursor)
	}
}

func TestFilterEscFromTheList(t *testing.T) {
	t.Parallel()
	m := filterModel()
	keys(m, "/", "g", "o", "enter")
	wantText(t, m, "go.mod")
	keys(m, "esc")
	if m.list().filter.on() || strings.Contains(text(m), " / go") {
		t.Errorf("not cleared:\n%s", text(m))
	}
}

func TestFilterKeepsParentsOpen(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(128, 41, demoTree()...)
	m.layout.setFocus(project)
	keys(m, "left") // folds a-parent
	wantRow(t, m, "a-parent", "+ ● a-parent")

	keys(m, "/", "d", "e", "e", "p")
	wantRow(t, m, "a-parent", "− ● a-parent")
	wantRow(t, m, "c-middle", "└ − ● c-middle")
	wantRow(t, m, "d-deep", "▸      ├ ● d-deep")
	wantRow(t, m, "e-deep", "└ ○ e-deep")
	for _, gone := range []string{"b-child", "f-alone"} {
		if strings.Contains(text(m), gone) {
			t.Fatalf("%s shows:\n%s", gone, text(m))
		}
	}
	// ← only moves to the parent: what matches stays open.
	keys(m, "enter", "left", "left")
	wantRow(t, m, "a-parent", "▸− ● a-parent")
	wantRow(t, m, "d-deep", "d-deep")

	keys(m, "esc")
	wantRow(t, m, "a-parent", "▸+ ● a-parent")
}

func TestFilterCursorStaysOnAMatch(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(128, 41, demoAgents()...)
	m.layout.setFocus(project)
	l := m.list()
	keys(m, "/", "w", "i")
	if l.cursor != "agent-swift-falcon" {
		t.Fatalf("the cursor left a match: %q", l.cursor)
	}
	keys(m, "s")
	if l.cursor != "agent-wise-owl" {
		t.Fatalf("not on the one match: %q", l.cursor)
	}
	keys(m, "backspace")
	if l.cursor != "agent-wise-owl" {
		t.Fatalf("the cursor left a match: %q", l.cursor)
	}
}

// TestFilterLatestWins: each key is taken at once, and a match for what was typed
// before is dropped.
func TestFilterLatestWins(t *testing.T) {
	t.Parallel()
	m := filterModel()
	keys(m, "/")
	_, before := m.Update(keyMsgs("t")[0])
	_, latest := m.Update(keyMsgs("a")[0])
	wantText(t, m, " / ta_", "filtering…")
	if !strings.HasSuffix(row(t, m, " / ta"), "… ") {
		t.Errorf("a count before the files matched: %q", row(t, m, " / ta"))
	}
	m.Update(drain(latest))
	m.Update(drain(before))
	wantText(t, m, "tabs.go")
	if strings.Contains(text(m), "TIDE.md") {
		t.Errorf("the match for t replaced the latest:\n%s", text(m))
	}
}

func TestFilterLineClicked(t *testing.T) {
	t.Parallel()
	m := filterModel()
	keys(m, "/", "g", "o", "enter")
	s := m.root.(*projectScreen)
	s.click(3, 23, 31, 24)
	if !m.filterTyping() {
		t.Error("a click on the line does not go back to it")
	}
}
