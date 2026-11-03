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
		"":     {sectionAgents, sectionWorktrees, sectionCode, sectionDocs},
		"awcd": {sectionAgents, sectionWorktrees, sectionCode, sectionDocs},
		"awc":  {sectionAgents, sectionWorktrees, sectionCode, sectionDocs}, // saved before docs came
		"dcwa": {sectionDocs, sectionCode, sectionWorktrees, sectionAgents},
		"ca":   {sectionCode, sectionAgents, sectionWorktrees, sectionDocs}, // those left out after
		"w":    {sectionWorktrees, sectionAgents, sectionCode, sectionDocs},
		"xcc":  {sectionCode, sectionAgents, sectionWorktrees, sectionDocs}, // what the setting refuses, passed over
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
	if got := headingRows(m); !slices.Equal(got, []string{"Agents", "Worktrees", "Code", "Docs"}) {
		t.Fatalf("after a failed save %q", got)
	}
	m.Update(settingSaved{setting: api.Setting{Key: sectionSettingKey, Value: "wc"}})
	if got := headingRows(m); !slices.Equal(got, []string{"Worktrees", "Code", "Agents", "Docs"}) {
		t.Fatalf("after a save %q:\n%s", got, text(m))
	}
}
