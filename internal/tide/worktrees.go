package tide

import (
	"fmt"
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The project pane lists the worktrees under the agents, in one scrolling list
// (docs/TIDE_WORKTREES.md). A worktree's children are its files with uncommitted
// changes. Enter on a file opens its diff, on a worktree what it has committed.

const (
	refreshKey   = "f" // of the menu
	worktreeHint = "↑↓ select  ←→ files  Enter diff  f refresh  Alt+arrows move  C-q menu"
	rowSeparator = "\x00"
)

// worktreeRow is a row of the Worktrees section: a worktree, or a file of it.
type worktreeRow struct {
	worktree api.WorktreeStatus
	file     *api.WorktreeFile
}

// key names the row across events, as an agent's id does its row.
func (r worktreeRow) key() string {
	if r.file != nil {
		return r.worktree.Name + rowSeparator + r.file.Path
	}
	return r.worktree.Name
}

// tab is the tab the row opens.
func (r worktreeRow) tab() string {
	if r.file != nil {
		return fileDiffTab(r.worktree.Name, r.file.Path)
	}
	return committedTab(r.worktree.Name)
}

// worktreeStatuses returns what the stream last said of the worktrees: nil until
// the daemon has read them.
func (m *Model) worktreeStatuses() []api.WorktreeStatus {
	if f := m.following; f != nil && f.project != nil && f.project.WorktreeStatuses != nil {
		return *f.project.WorktreeStatuses
	}
	return nil
}

// worktreeRows returns the rows the section shows: a folded worktree hides its
// files.
func (m *Model) worktreeRows() []worktreeRow {
	l := m.list()
	if l == nil {
		return nil
	}
	var rows []worktreeRow
	for _, w := range m.worktreeStatuses() {
		rows = append(rows, worktreeRow{worktree: w})
		if !l.worktreesOpen[w.Name] {
			continue
		}
		for i := range w.Files {
			rows = append(rows, worktreeRow{worktree: w, file: &w.Files[i]})
		}
	}
	return rows
}

// worktreesChanged keeps the cursor on its row, or on the one that took its place;
// with none left it goes back to the agents.
func (m *Model) worktreesChanged() {
	l, rows := m.list(), m.worktreeRows()
	if l == nil {
		return
	}
	if i := slices.IndexFunc(rows, func(r worktreeRow) bool { return r.key() == l.worktreeKey }); i >= 0 {
		l.worktreeRow = i
		return
	}
	if len(rows) == 0 {
		l.onWorktrees, l.worktreeRow, l.worktreeKey = false, 0, ""
		return
	}
	l.worktreeRow = max(min(l.worktreeRow, len(rows)-1), 0)
	l.worktreeKey = rows[l.worktreeRow].key()
}

// moveToWorktree puts the cursor on the row i of the section.
func (m *Model) moveToWorktree(i int) {
	l, rows := m.list(), m.worktreeRows()
	if i < 0 || i >= len(rows) {
		return
	}
	l.onWorktrees, l.worktreeRow, l.worktreeKey = true, i, rows[i].key()
}

// moveUp moves the cursor up the one list both sections make.
func (m *Model) moveUp() {
	l := m.list()
	switch {
	case !l.onWorktrees:
		m.moveCursor(l.row - 1)
	case l.worktreeRow > 0:
		m.moveToWorktree(l.worktreeRow - 1)
	case len(m.listRows()) > 0:
		l.onWorktrees = false
	}
}

// moveDown moves the cursor down the one list both sections make.
func (m *Model) moveDown() {
	l := m.list()
	switch {
	case l.onWorktrees:
		m.moveToWorktree(l.worktreeRow + 1)
	case l.row < len(m.listRows())-1:
		m.moveCursor(l.row + 1)
	default:
		m.moveToWorktree(0)
	}
}

// worktreeFoldLeft folds the worktree under the cursor; from a file it moves to
// the file's worktree.
func (m *Model) worktreeFoldLeft() {
	l, rows := m.list(), m.worktreeRows()
	if l.worktreeRow >= len(rows) {
		return
	}
	r := rows[l.worktreeRow]
	if r.file != nil {
		m.moveToWorktree(slices.IndexFunc(rows, func(o worktreeRow) bool { return o.file == nil && o.worktree.Name == r.worktree.Name }))
		return
	}
	m.foldWorktree(r.worktree.Name, true)
}

// worktreeFoldRight opens the worktree under the cursor.
func (m *Model) worktreeFoldRight() {
	l, rows := m.list(), m.worktreeRows()
	if l.worktreeRow < len(rows) && rows[l.worktreeRow].file == nil {
		m.foldWorktree(rows[l.worktreeRow].worktree.Name, false)
	}
}

func (m *Model) foldWorktree(name string, closed bool) {
	l := m.list()
	if l.worktreesOpen == nil {
		l.worktreesOpen = map[string]bool{}
	}
	if closed {
		delete(l.worktreesOpen, name)
	} else {
		l.worktreesOpen[name] = true
	}
}

// openWorktreeRow opens the tab of the row under the cursor, or goes to it, and
// focuses it: it is opened to be read.
func (m *Model) openWorktreeRow() tea.Cmd {
	l, rows := m.list(), m.worktreeRows()
	if l.worktreeRow >= len(rows) {
		return nil
	}
	r := rows[l.worktreeRow]
	l.openTab(r.tab())
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("diff")
	m.log.Info("diff opened", "worktree", r.worktree.Name, "tab", r.key())
	return m.refreshWorktrees(r.worktree.Name)
}

// refreshWorktrees asks the daemon to read the worktree called name again, or all
// of them when it is "". What it finds comes with the stream.
func (m *Model) refreshWorktrees(name string) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		return nil
	}
	ref := "id:" + f.project.Id
	return func() tea.Msg {
		if err := m.client.RefreshWorktrees(m.ctx, ref, name); err != nil {
			m.log.Info("worktrees not refreshed", "err", err)
		}
		return nil
	}
}

// worktreesSync reads the worktrees again when the focus comes to the list, since
// the user is about to look at it: an edit in a plain terminal shows no other way.
func (m *Model) worktreesSync() tea.Cmd {
	came := m.layout.focus == project && m.lastFocus != project
	m.lastFocus = m.layout.focus
	if !came {
		return nil
	}
	return m.refreshWorktrees("")
}

// worktreeRowActive reports whether the tab shown is the row's; a folded worktree
// carries the mark of its file's tab.
func (m *Model) worktreeRowActive(r worktreeRow) bool {
	l := m.list()
	if r.tab() == l.active {
		return true
	}
	if worktree, file := diffTabOf(l.active); r.file == nil && isDiffTab(l.active) && file != "" {
		return worktree == r.worktree.Name && !l.worktreesOpen[worktree]
	}
	return false
}

// worktreeMark is a file's status, which tells it with color off too.
func worktreeMark(st style.Style, status api.WorktreeFileStatus) string {
	switch status {
	case api.WorktreeFileStatusA, api.WorktreeFileStatusEmpty:
		return st.Added(string(status))
	case api.WorktreeFileStatusD, api.WorktreeFileStatusU:
		return st.Removed(string(status))
	}
	return st.Warning(string(status))
}

// worktreeSummary is what a worktree's row says of its commits. "landed" is for a
// worktree with nothing of its own: one with changes not committed is still at work.
func worktreeSummary(w api.WorktreeStatus) string {
	switch {
	case w.Missing:
		return "missing"
	case w.Ahead == nil:
		return ""
	case *w.Ahead == 0:
		if len(w.Files) > 0 || w.Base == nil || strings.HasPrefix(*w.Base, "origin/") {
			return "" // compared with its own branch on origin, nothing is unpushed
		}
		return "landed"
	}
	s := fmt.Sprintf("↑%d", *w.Ahead)
	if w.Added != nil && w.Removed != nil {
		s += fmt.Sprintf(" +%d −%d", *w.Added, *w.Removed)
	}
	return s
}

// worktreeHolding names the worktree dir is in: the deepest, since worktrees are
// often inside the clone. It goes by the path, as an agent's own worktree field
// knows only those wdx registered, and every worktree git lists counts here.
func (m *Model) worktreeHolding(dir string) string {
	name, longest := "", 0
	for _, w := range m.worktreeStatuses() {
		if (dir == w.Path || strings.HasPrefix(dir, w.Path+"/")) && len(w.Path) > longest {
			name, longest = w.Name, len(w.Path)
		}
	}
	return name
}

// agentsIn names the running agents in the worktree called name.
func (m *Model) agentsIn(name string) []string {
	var names []string
	for _, a := range m.agents() {
		if a.State == api.AgentStateRunning && m.worktreeHolding(a.Dir) == name {
			names = append(names, a.Name)
		}
	}
	return names
}

// worktreeLine draws a row of the section, width wide: its worktree's name with
// what it says at the end, or a file's status and path, whose end is what tells it.
func (m *Model) worktreeLine(r worktreeRow, cursor, active bool, width int) string {
	st := m.st
	pointer, end := "  ", " "
	if cursor {
		pointer = st.Focused("▸ ")
	}
	if active {
		end = " " + st.Focused("◂") + " "
	}
	room := width - ansi.StringWidth("▸ ") - style.Width(end)
	var row string
	if r.file != nil {
		// Under its worktree's name: past the fold mark.
		room -= ansi.StringWidth("  M ")
		p := ansi.TruncateLeft(r.file.Path, max(ansi.StringWidth(r.file.Path)-max(room, 1)+1, 0), "…")
		if ansi.StringWidth(r.file.Path) <= room {
			p = r.file.Path
		}
		if cursor || active {
			p = st.Name(p)
		}
		row = pointer + "  " + worktreeMark(st, r.file.Status) + " " + p + strings.Repeat(" ", max(room-ansi.StringWidth(stripped(p)), 0)) + end
	} else {
		row = pointer + m.worktreeHeading(r.worktree, room, cursor || active) + end
	}
	if active {
		return st.ActiveRow(row)
	}
	return row
}

// stripped is s as it shows, without its style.
func stripped(s string) string { return ansi.Strip(s) }

// worktreeHeading is a worktree's row after the pointer: its fold mark, its name,
// and at the end its summary, then the agents there when they fit.
func (m *Model) worktreeHeading(w api.WorktreeStatus, room int, strong bool) string {
	st := m.st
	fold := "  "
	folded := !m.list().worktreesOpen[w.Name]
	if len(w.Files) > 0 {
		fold = foldOpen + " "
		if folded {
			fold = foldClosed + " "
		}
	}
	room -= ansi.StringWidth(fold)
	detail := ""
	for _, d := range worktreeDetails(w, folded, m.agentsIn(w.Name)) {
		if room-ansi.StringWidth(d)-1 >= minAgentName {
			detail = d
			break
		}
	}
	if detail != "" {
		room -= ansi.StringWidth(detail) + 1
	}
	name := ansi.Truncate(w.Name, max(room, 0), "…")
	gap := strings.Repeat(" ", max(room-ansi.StringWidth(name), 0))
	switch {
	case strong:
		name = st.Name(name)
	case w.Missing:
		name = st.Muted(name)
	}
	styled := detail
	if detail != "" {
		if w.Missing {
			styled = " " + st.Warning(detail)
		} else {
			styled = " " + st.Muted(detail)
		}
	}
	return st.Muted(fold) + name + gap + styled
}

// worktreeDetails are what a worktree's row may say at its end, the most first: a
// narrow pane takes the longest that fits. A folded row must say it has files, or
// it would look clean.
func worktreeDetails(w api.WorktreeStatus, folded bool, agents []string) []string {
	summary, short := worktreeSummary(w), worktreeSummary(w)
	if w.Ahead != nil && *w.Ahead > 0 {
		short = fmt.Sprintf("↑%d", *w.Ahead)
	}
	files := ""
	if folded && len(w.Files) == 1 {
		files = "1 file"
	} else if folded && len(w.Files) > 1 {
		files = fmt.Sprintf("%d files", len(w.Files))
	}
	mark := ""
	if len(agents) == 1 {
		mark = "● " + agents[0]
	} else if len(agents) > 1 {
		mark = fmt.Sprintf("● %d agents", len(agents))
	}
	joined := func(parts ...string) string {
		kept := slices.DeleteFunc(parts, func(p string) bool { return p == "" })
		return strings.Join(kept, " · ")
	}
	return []string{joined(summary, files, mark), joined(summary, files), joined(short, files), joined(summary), joined(files), ""}
}

// tabLeaf is the end of a path, as a tab's title has room for.
func tabLeaf(p string) string { return path.Base(p) }
