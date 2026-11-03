package tide

import (
	"path"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The left pane's filter: / opens a line at the pane's foot, and the list shows
// only what matches what is typed there, with the parents of each match, opened
// (docs/TIDE_CODE.md).

const (
	filterKey        = "/"
	filterTypingHint = "type to filter  ↑↓ select  Enter to the list  Esc clear"
	filterOnHint     = "Esc clear filter  "
)

// listFilter is the filter of a project's list.
type listFilter struct {
	text   string
	typing bool // the keys go to the line
}

// on reports whether the line shows.
func (f listFilter) on() bool { return f.typing || f.text != "" }

// words are what a match must hold, each in any case; nil filters nothing.
func (f listFilter) words() []string {
	words := strings.Fields(strings.ToLower(f.text))
	if len(words) == 0 {
		return nil
	}
	return words
}

// matches reports whether s holds every word, whatever its case.
func matches(s string, words []string) bool {
	s = strings.ToLower(s)
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}

// filterWords returns the words the list of the project shown filters by.
func (m *Model) filterWords() []string {
	if l := m.list(); l != nil {
		return l.filter.words()
	}
	return nil
}

// filtering reports whether the list shows only what matches.
func (m *Model) filtering() bool { return len(m.filterWords()) > 0 }

// filterTyping reports whether the keys typed in the project pane go to the line.
func (m *Model) filterTyping() bool {
	l := m.list()
	return l != nil && l.filter.typing
}

// openFilter opens the line, or goes back to it, to type.
func (m *Model) openFilter() {
	m.list().filter.typing = true
	m.log.Info("filter opened", "text", m.list().filter.text)
}

// filterKeyTyped takes a key while the line has them: plain keys go into it, as
// the list's own letters would act.
func (m *Model) filterKeyTyped(msg tea.KeyPressMsg) tea.Cmd {
	l := m.list()
	switch name := msg.String(); {
	case name == "esc":
		m.clearFilter()
	case name == "enter":
		l.filter.typing = false
		m.log.Info("filter kept", "text", l.filter.text)
	case name == "up" || name == "down":
		return m.root.update(msg)
	case name == "backspace":
		if r := []rune(l.filter.text); len(r) > 0 {
			l.filter.text = string(r[:len(r)-1])
			m.filterChanged()
		}
	case typed(msg):
		l.filter.text += msg.Text
		m.filterChanged()
	}
	return nil
}

// clearFilter closes the line and shows the list whole again, with its folds: the
// cursor stays on its row, or the nearest one shown above it.
func (m *Model) clearFilter() {
	l := m.list()
	m.log.Info("filter cleared", "text", l.filter.text)
	l.filter = listFilter{}
	for _, f := range l.code.files {
		f.match, f.matching = nil, codeMatchKey{}
	}
	if l.section == sectionWorktrees {
		rows := m.worktreeRows()
		if !slices.ContainsFunc(rows, func(r worktreeRow) bool { return r.key() == l.worktreeKey }) {
			l.worktreeKey, _, _ = strings.Cut(l.worktreeKey, rowSeparator)
		}
	}
	if l.section == sectionCode {
		rows := m.codeRowsShown()
		for strings.Contains(l.code.cursor, "/") && !slices.ContainsFunc(rows, func(r codeRow) bool { return r.key() == l.code.cursor }) {
			l.code.cursor = path.Dir(l.code.cursor)
		}
	}
	m.keepCursors()
}

// filterChanged keeps the cursor on its row while that matches, and otherwise puts
// it on the first match.
func (m *Model) filterChanged() {
	m.log.Debug("filter changed", "text", m.list().filter.text)
	m.keepCursors()
	m.placeOnMatch()
}

// placeOnMatch puts the cursor on the first match, unless it is on one.
func (m *Model) placeOnMatch() {
	if !m.filtering() || m.cursorOnMatch() {
		return
	}
	for i, r := range m.listRows() {
		if r.match {
			m.moveCursor(i)
			return
		}
	}
	for i, r := range m.worktreeRows() {
		if r.match {
			m.moveToWorktree(i)
			return
		}
	}
	for i, r := range m.codeRowsShown() {
		if r.match {
			m.moveToCode(i)
			return
		}
	}
	for i, r := range m.docRows() {
		if r.match {
			m.moveToDoc(i)
			return
		}
	}
}

// cursorOnMatch reports whether the row under the cursor matches the filter, rather
// than being shown as the parent of one.
func (m *Model) cursorOnMatch() bool {
	l := m.list()
	switch l.section {
	case sectionWorktrees:
		rows := m.worktreeRows()
		return l.worktreeRow < len(rows) && rows[l.worktreeRow].match
	case sectionCode:
		rows := m.codeRowsShown()
		return l.code.row < len(rows) && rows[l.code.row].match
	case sectionDocs:
		rows := m.docRows()
		return l.docRow < len(rows) && rows[l.docRow].match
	}
	rows := m.listRows()
	return l.row < len(rows) && rows[l.row].match
}

// keepCursors keeps each section's cursor on its row, or on the one that took its
// place, as the rows shown changed.
func (m *Model) keepCursors() {
	m.keepAgentCursor()
	m.keepWorktreeCursor()
	m.keepCodeCursor()
	m.keepDocCursor()
}

// filterMatches counts the rows that match, not those shown as their parents.
func (m *Model) filterMatches() int {
	n := 0
	for _, r := range m.listRows() {
		if r.match {
			n++
		}
	}
	for _, r := range m.worktreeRows() {
		if r.match {
			n++
		}
	}
	for _, r := range m.codeRowsShown() {
		if r.match {
			n++
		}
	}
	for _, r := range m.docRows() {
		if r.match {
			n++
		}
	}
	return n
}

// filterLine is the line at the pane's foot, width wide: what is typed, and how
// many rows match.
func (m *Model) filterLine(st style.Style, width int) string {
	l := m.list()
	count := ""
	switch {
	case !m.filtering():
	case m.codeMatchPending():
		count = "… "
	default:
		count = strconv.Itoa(m.filterMatches()) + " "
	}
	cursor := ""
	if l.filter.typing && m.layout.focus == project {
		cursor = st.Focused("_")
	}
	room := max(width-ansi.StringWidth(" / ")-style.Width(cursor)-ansi.StringWidth(count)-1, 1)
	text := ansi.TruncateLeft(l.filter.text, max(ansi.StringWidth(l.filter.text)-room, 0), "…")
	gap := strings.Repeat(" ", max(width-ansi.StringWidth(" / "+text)-style.Width(cursor)-ansi.StringWidth(count), 0))
	return " " + st.Command(filterKey) + " " + st.Name(text) + cursor + gap + st.Muted(count)
}

// keptAgents returns the ids of the agents that match, and of their parents.
func keptAgents(top []api.Agent, words []string) map[string]bool {
	kept := map[string]bool{}
	var keep func(a api.Agent) bool
	keep = func(a api.Agent) bool {
		k := matches(a.Name, words)
		for _, c := range childrenOf(a) {
			if keep(c) {
				k = true
			}
		}
		if k {
			kept[a.Id] = true
		}
		return k
	}
	for _, a := range top {
		keep(a)
	}
	return kept
}

// worktreeMatches reports whether the worktree's name or branch matches.
func worktreeMatches(w api.WorktreeStatus, words []string) bool {
	branch := ""
	if w.Branch != nil {
		branch = *w.Branch
	}
	return matches(w.Name+" "+branch, words)
}

// The Code section's matches are found away from Update, as a worktree may have
// many files: each key is taken at once, and the latest text wins.

// codeMatchKey names what a match was found for: the filter's text, over a reading
// of the worktree's files.
type codeMatchKey struct {
	text string
	read int
}

// codeMatch is the files that match, as a tree whose directories are all open.
type codeMatch struct {
	key  codeMatchKey
	tree []*codeNode
	open map[string]bool
}

// codeMatched is the answer to matching a worktree's files.
type codeMatched struct {
	projectID, worktree string
	match               *codeMatch
}

// matchCode returns the paths that hold every word, as a tree.
func matchCode(key codeMatchKey, paths []string) *codeMatch {
	words := listFilter{text: key.text}.words()
	var hits []string
	open := map[string]bool{}
	for _, p := range paths {
		if !matches(p, words) {
			continue
		}
		hits = append(hits, p)
		for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
			open[dir] = true
		}
	}
	return &codeMatch{key: key, tree: codeTree(hits, nil), open: open}
}

// codeFilterSync matches the files of the worktree chosen when the filter or the
// files changed since they last were.
func (m *Model) codeFilterSync() tea.Cmd {
	l, f := m.list(), m.codeFilesShown()
	if l == nil || f == nil || !f.loaded || !m.filtering() {
		return nil
	}
	key := codeMatchKey{text: l.filter.text, read: f.read}
	if (f.match != nil && f.match.key == key) || f.matching == key {
		return nil
	}
	f.matching = key
	id, worktree, paths := m.following.project.Id, l.code.worktree, f.paths
	return func() tea.Msg {
		return codeMatched{projectID: id, worktree: worktree, match: matchCode(key, paths)}
	}
}

// codeMatchedMsg takes a match, unless the filter or the files changed since it was
// asked for. A new text puts the cursor on a match, as the others' did at once.
func (m *Model) codeMatchedMsg(msg codeMatched) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.code.files[msg.worktree] == nil {
		return
	}
	f := l.code.files[msg.worktree]
	if f.matching == msg.match.key {
		f.matching = codeMatchKey{}
	}
	if msg.match.key != (codeMatchKey{text: l.filter.text, read: f.read}) {
		return
	}
	newText := f.match == nil || f.match.key.text != msg.match.key.text
	f.match = msg.match
	if l != m.list() || l.code.worktree != msg.worktree {
		return
	}
	m.keepCodeCursor()
	if newText {
		m.placeOnMatch()
	}
}

// codeMatchPending reports whether the Code section shows no match yet, or one for
// what was typed before.
func (m *Model) codeMatchPending() bool {
	l, f := m.list(), m.codeFilesShown()
	if f == nil || !f.loaded {
		return false
	}
	return f.match == nil || f.match.key != (codeMatchKey{text: l.filter.text, read: f.read})
}
