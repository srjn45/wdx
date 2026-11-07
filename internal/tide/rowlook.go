package tide

import (
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/style"
)

// rowLook is what marks a row of the left pane's lists: the cursor on it, and
// its tab being the one shown or another one open.
type rowLook struct {
	cursor, active, open bool
}

// lead is the row's first two cells: ▌ for the tab shown, then ▸ for the
// cursor, which tell them with color off too.
func (r rowLook) lead(st style.Style) string {
	bar, pointer := " ", " "
	if r.active {
		bar = st.Focused("▌")
	}
	if r.cursor {
		pointer = st.Focused("▸")
	}
	return bar + pointer
}

// bold reports whether the row's name is bold: under the cursor or shown.
func (r rowLook) bold() bool { return r.cursor || r.active }

// paint tints row: the cursor's tint over the tab shown's over an open tab's.
func (r rowLook) paint(st style.Style, row string) string {
	switch {
	case r.cursor:
		return st.CursorRow(row)
	case r.active:
		return st.ActiveRow(row)
	case r.open:
		return st.OpenRow(row)
	}
	return row
}

// lookOf is a row's look, shows telling whether a tab is the row's: its own,
// or one a folded row hides.
func (m *Model) lookOf(cursor bool, shows func(tab string) bool) rowLook {
	look := rowLook{cursor: cursor}
	l := m.list()
	if l == nil {
		return look
	}
	look.active = l.active != "" && shows(l.active)
	look.open = !look.active && slices.ContainsFunc(l.tabs, shows)
	return look
}

// padRow fills row with blanks to width, so that its tint goes across the pane.
func padRow(row string, width int) string {
	return row + strings.Repeat(" ", max(width-style.Width(row), 0))
}
