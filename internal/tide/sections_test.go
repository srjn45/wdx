package tide

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func TestSectionOrder(t *testing.T) {
	t.Parallel()
	for letters, want := range map[string][]section{
		"":      {sectionAgents, sectionWorktrees, sectionDocs, sectionCode},
		"awdpc": {sectionAgents, sectionWorktrees, sectionDocs, sectionCode},
		"awc":   {sectionAgents, sectionWorktrees, sectionCode}, // Docs left out is hidden
		"dcwa":  {sectionDocs, sectionCode, sectionWorktrees, sectionAgents},
		"pca":   {sectionCode, sectionAgents}, // p has no section yet
		"w":     {sectionWorktrees},
		"p":     {sectionAgents, sectionWorktrees, sectionDocs, sectionCode}, // none named: never an empty pane
		"xcc":   {sectionCode},                                               // what the setting refuses, passed over
	} {
		if got := sectionOrder(letters); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", letters, got, want)
		}
	}
}

// headingRows returns the rows of the pane that are the sections' headings, by name.
func headingRows(m *Model) []string {
	var names []string
	for _, line := range paneLines(m) {
		for _, name := range []string{"Agents", "Worktrees", "Code", "Docs"} {
			if strings.HasPrefix(strings.TrimLeft(line, "│ "), name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// TestSectionsInOrder: the pane shows the sections in the order set, and the cursor
// goes through them in that order.
func TestSectionsInOrder(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"file"}, api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("dcwa") // Docs, which has none, first
	if got := headingRows(m); !slices.Equal(got, []string{"Docs", "Code", "Worktrees", "Agents"}) {
		t.Fatalf("headings %q:\n%s", got, text(m))
	}
	l := m.list()
	m.moveToCode(0)
	keys(m, "down") // file
	keys(m, "down")
	if l.section != sectionWorktrees || l.worktreeRow != 0 {
		t.Fatalf("down past Code: %v, %d", l.section, l.worktreeRow)
	}
	keys(m, "down")
	if l.section != sectionAgents || l.row != 0 {
		t.Fatalf("down past Worktrees: %v, %d", l.section, l.row)
	}
	keys(m, "up", "up")
	if l.section != sectionCode || l.code.cursor != "file" {
		t.Fatalf("back up: %v, %q", l.section, l.code.cursor)
	}
	m.moveCursor(len(m.listRows()) - 1)
	keys(m, "down")
	if l.section != sectionAgents || l.row != len(m.listRows())-1 {
		t.Errorf("down from the last section's last row moved: %v, %d", l.section, l.row)
	}
}

// TestSectionOrderSaved: saving the setting in the settings tab orders the sections
// at once; a save that failed changes nothing.
func TestSectionOrderSaved(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"file"}, api.WorktreeStatus{Name: "main", Path: "/r"})
	m.Update(settingSaved{valueSaved: valueSaved{err: errors.New("refused")}, setting: api.Setting{Key: sectionSettingKey, Value: "cwa"}})
	if got := headingRows(m); !slices.Equal(got, []string{"Agents", "Worktrees", "Docs", "Code"}) {
		t.Fatalf("after a failed save %q", got)
	}
	m.Update(settingSaved{setting: api.Setting{Key: sectionSettingKey, Value: "wc"}})
	if got := headingRows(m); !slices.Equal(got, []string{"Worktrees", "Code"}) {
		t.Fatalf("after a save %q:\n%s", got, text(m))
	}
}

// TestHiddenSectionCursor: hiding the section the cursor is in moves it to the first
// row shown, and the cursor never goes into a hidden section.
func TestHiddenSectionCursor(t *testing.T) {
	t.Parallel()
	m := codeModel([]string{"file"}, api.WorktreeStatus{Name: "main", Path: "/r"})
	l := m.list()
	if l.section != sectionAgents {
		t.Fatalf("starts in %v", l.section)
	}
	m.Update(settingSaved{setting: api.Setting{Key: sectionSettingKey, Value: "c"}})
	if l.section != sectionCode {
		t.Fatalf("after hiding Agents the cursor is in %v:\n%s", l.section, text(m))
	}
	keys(m, "down", "down", "up", "up", "up")
	if l.section != sectionCode {
		t.Errorf("moving went to %v", l.section)
	}
	m.moveCursor(0) // as creating an agent here does
	m.Update(nil)
	if l.section != sectionCode {
		t.Errorf("put on an agent, the cursor stayed in %v", l.section)
	}
}
