package tide

import (
	"slices"
	"strings"
	"testing"
)

// headingAt returns the screen row of the section's heading in the project pane,
// -1 when it does not show.
func headingAt(m *Model, title string) int {
	return slices.IndexFunc(paneLines(m), func(line string) bool {
		return strings.HasPrefix(strings.TrimLeft(line, "│ ▸−+"), title)
	})
}

// TestCollapseByClick: a click on a heading, the one kept at the pane's top or one
// below, collapses its section, and a second expands it.
func TestCollapseByClick(t *testing.T) {
	t.Parallel()
	m := docsModel(t) // Docs first, with the cursor on auth
	l := m.list()

	m.Update(clickAt(5, 1)) // the heading at the top
	if !l.collapsed["d"] || !l.onHeading || strings.Contains(leftPane(m), "auth") {
		t.Fatalf("collapsed %v, on the heading %v:\n%s", l.collapsed, l.onHeading, leftPane(m))
	}
	if !strings.Contains(paneLines(m)[1], "▸+ Docs") {
		t.Errorf("the heading: %q", paneLines(m)[1])
	}
	m.Update(clickAt(5, 1))
	if l.collapsed["d"] || l.onHeading || l.section != sectionDocs || l.docCursor != "doc-auth" {
		t.Fatalf("expanded %v, on the heading %v, cursor %v %q", l.collapsed, l.onHeading, l.section, l.docCursor)
	}
	wantRow(t, m, "auth", "▸")

	agents := headingAt(m, "Agents")
	m.Update(clickAt(5, agents))
	if !l.collapsed["a"] || strings.Contains(leftPane(m), "swift-falcon") {
		t.Fatalf("Agents not collapsed:\n%s", leftPane(m))
	}
	if l.onHeading || l.docCursor != "doc-auth" || !strings.Contains(paneLines(m)[agents], " + Agents") {
		t.Errorf("the cursor left auth, or the heading %q", paneLines(m)[agents])
	}
	m.Update(clickAt(5, agents))
	if l.collapsed["a"] || !strings.Contains(leftPane(m), "swift-falcon") {
		t.Errorf("Agents not expanded:\n%s", leftPane(m))
	}
}

// TestCollapseByKey: z, and C-q z, collapse the cursor's section and expand it;
// the cursor stops on a collapsed heading, where only moving and z act.
func TestCollapseByKey(t *testing.T) {
	t.Parallel()
	for _, z := range [][]string{{collapseKey}, {"C-q", collapseKey}} {
		m := docsModel(t)
		l := m.list()
		keys(m, "down") // notes
		press(m, z...)
		if !l.collapsed["d"] || !l.onHeading || m.menu || strings.Contains(leftPane(m), "notes") {
			t.Fatalf("%v: collapsed %v, on the heading %v, menu %v", z, l.collapsed, l.onHeading, m.menu)
		}
		tabs := slices.Clone(l.tabs)
		press(m, "enter", "right", closeTabKey)
		if !slices.Equal(l.tabs, tabs) || !l.onHeading {
			t.Errorf("%v: on the heading, tabs %v, on it %v", z, l.tabs, l.onHeading)
		}
		keys(m, "down")
		if l.onHeading || l.section != sectionAgents {
			t.Errorf("%v: down from the heading: %v, on it %v", z, l.section, l.onHeading)
		}
		keys(m, "up")
		if !l.onHeading || l.section != sectionDocs {
			t.Errorf("%v: up to the heading: %v, on it %v", z, l.section, l.onHeading)
		}
		press(m, z...)
		if l.collapsed["d"] || l.onHeading || l.docCursor != "doc-auth" {
			t.Errorf("%v: expanded %v, cursor on %q, on the heading %v", z, l.collapsed, l.docCursor, l.onHeading)
		}
	}
}

// TestCollapsedHeadingIsAStop: moving down stops on a collapsed heading and goes
// past it; expanding a section with no rows from its heading puts the cursor on the
// nearest row.
func TestCollapsedHeadingIsAStop(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	l := m.list()
	m.toggleSection(sectionAgents, "test")
	keys(m, "down", "down")
	if !l.onHeading || l.section != sectionAgents {
		t.Fatalf("not on Agents' heading: %v, on it %v", l.section, l.onHeading)
	}
	keys(m, "down")
	if l.onHeading || l.section != sectionWorktrees {
		t.Errorf("past Agents: %v, on it %v", l.section, l.onHeading)
	}
	// An empty section expanded from its heading puts the cursor on the nearest row.
	setDocs(m)
	m.moveToWorktree(0)
	m.toggleSection(sectionAgents, "test") // open again
	m.toggleSection(sectionDocs, "test")
	keys(m, "up", "up", "up", "up", "up")
	if !l.onHeading || l.section != sectionDocs {
		t.Fatalf("not on Docs' heading: %v, on it %v", l.section, l.onHeading)
	}
	keys(m, collapseKey)
	if l.onHeading || l.section != sectionAgents {
		t.Errorf("Docs, with none, expanded: cursor on %v, on the heading %v", l.section, l.onHeading)
	}
}

// TestCollapseMenu: the list part says what z does; the selection part has nothing
// on a heading.
func TestCollapseMenu(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	if it, ok := m.menuLine(collapseKey); !ok || it.label != "collapse section" {
		t.Fatalf("on a row: %+v, %v", it, ok)
	}
	keys(m, collapseKey)
	if it, ok := m.menuLine(collapseKey); !ok || it.label != "expand section" {
		t.Errorf("on the heading: %+v, %v", it, ok)
	}
	if p := m.selectionPart(); p.title != "Docs (section)" || len(p.items) != 0 {
		t.Errorf("the selection part on the heading: %q %v", p.title, p.items)
	}
	if strings.Contains(statusRow(m), "Enter") || !strings.Contains(statusRow(m), "z expand") {
		t.Errorf("the hint: %q", statusRow(m))
	}
}

// TestCollapseFilter: the filter shows the matches of a collapsed section, and a
// collapsed section with none as its heading alone; cleared, both are collapsed
// again, with the cursor on the heading of its section.
func TestCollapseFilter(t *testing.T) {
	t.Parallel()
	m := filterModel()
	l := m.list()
	m.toggleSection(sectionCode, "test")
	m.toggleSection(sectionAgents, "test")
	if strings.Contains(leftPane(m), "README") || strings.Contains(leftPane(m), "swift-falcon") {
		t.Fatalf("not collapsed:\n%s", leftPane(m))
	}
	keys(m, "/", "t", "i", "d", "e", " ", "t", "a", "b", "s", "enter")
	got := leftPane(m)
	if !strings.Contains(got, "− internal") || !strings.Contains(got, " + Code") { // Code's tree, not Worktrees' file
		t.Fatalf("no match in the collapsed Code:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	agents := slices.IndexFunc(lines, func(s string) bool { return strings.Contains(s, "+ Agents") })
	if agents < 0 || agents+1 >= len(lines) || !strings.HasPrefix(lines[agents+1], " − ") {
		t.Errorf("Agents, collapsed with no match, shows more than its heading:\n%s", got)
	}
	m.moveToCode(len(m.codeRowsShown()) - 1)
	keys(m, "esc")
	got = leftPane(m)
	if strings.Contains(got, "internal") || strings.Contains(got, "README") || !l.onHeading || l.section != sectionCode {
		t.Errorf("cleared: on the heading %v, section %v:\n%s", l.onHeading, l.section, got)
	}
}
