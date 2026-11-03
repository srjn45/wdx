package tide

import (
	"slices"
	"testing"
)

// TestMinusClosesTheRowsTab: - and C-q - in the left pane close the tab Enter on
// the row opened, and the focus stays in the list.
func TestMinusClosesTheRowsTab(t *testing.T) {
	t.Parallel()
	for _, r := range openRows(t) {
		for _, k := range [][]string{{closeTabKey}, {"C-q", closeTabKey}} {
			m := r.model()
			keys(m, "enter")
			if !slices.Contains(m.list().tabs, r.tab) {
				t.Fatalf("%s: tab %q not opened", r.name, r.tab)
			}
			press(m, k...)
			if slices.Contains(m.list().tabs, r.tab) || m.layout.focus != project || m.menu {
				t.Errorf("%s, %v: tabs %v, focus %v, menu %v; want %q closed in the list", r.name, k, m.list().tabs, m.layout.focus, m.menu, r.tab)
			}
		}
	}
}

// TestMinusOnARowWithoutATab: with the row's tab not open, the menu has no
// close tab line and - closes nothing.
func TestMinusOnARowWithoutATab(t *testing.T) {
	t.Parallel()
	for _, r := range openRows(t) {
		m := r.model()
		other := fileTab("main", "other")
		m.list().openTab(other)
		if m.menuOffers(closeTabKey) {
			t.Errorf("%s: close tab offered", r.name)
		}
		tabs := slices.Clone(m.list().tabs)
		press(m, closeTabKey)
		if !slices.Equal(m.list().tabs, tabs) {
			t.Errorf("%s: tabs %v, want %v", r.name, m.list().tabs, tabs)
		}
	}
	// A directory has no tab.
	m := fileModel(t)
	keys(m, "enter")
	m.layout.setFocus(project)
	m.moveToCode(1)
	if m.menuOffers(closeTabKey) {
		t.Error("close tab offered on a directory")
	}
}

// TestMinusAsksAnEditor: a file's tab an editor runs in asks to save or discard
// first, showing the tab.
func TestMinusAsksAnEditor(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	keys(m, "enter")
	withEditors(m, aEditor)
	m.layout.setFocus(project)
	press(m, closeTabKey)
	if m.editorClose == nil || !slices.Contains(m.list().tabs, aFile) || m.list().active != aFile || m.layout.focus != agent {
		t.Fatalf("box %v, tabs %v, tab %q, focus %v", m.editorClose, m.list().tabs, m.list().active, m.layout.focus)
	}
}

// press sends m each of names, leaving what follows from them unrun: closing a
// tab can show the settings' tab, which asks a daemon these models have none of.
func press(m *Model, names ...string) {
	for _, k := range keyMsgs(names...) {
		m.Update(k)
	}
}
