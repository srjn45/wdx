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
	refreshKey   = "f" // of the menu's list part
	worktreeHint = "↑↓ select  ←→ files  Enter diff  v verified  f refresh  / filter  Alt+arrows move  C-q menu"
	rowSeparator = "\x00"
)

// worktreeRow is a row of the Worktrees section: a worktree, or a file of it.
type worktreeRow struct {
	worktree api.WorktreeStatus
	file     *api.WorktreeFile
	match    bool // it matches the filter
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
// files. With a filter, a worktree shows when its name or branch matches, or a file
// it changed does, with those files.
func (m *Model) worktreeRows() []worktreeRow {
	l := m.list()
	if l == nil {
		return nil
	}
	var rows []worktreeRow
	words := l.filter.words()
	for _, w := range m.worktreeStatuses() {
		if words != nil {
			rows = append(rows, filteredWorktreeRows(w, words)...)
			continue
		}
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

// filteredWorktreeRows returns the rows of w that the filter of words shows.
func filteredWorktreeRows(w api.WorktreeStatus, words []string) []worktreeRow {
	rows := []worktreeRow{{worktree: w, match: worktreeMatches(w, words)}}
	for i := range w.Files {
		if matches(w.Files[i].Path, words) {
			rows = append(rows, worktreeRow{worktree: w, file: &w.Files[i], match: true})
		}
	}
	if len(rows) == 1 && !rows[0].match {
		return nil
	}
	return rows
}

// worktreeFolded reports whether the worktree's row hides its files: with a filter,
// when none of them matches.
func (m *Model) worktreeFolded(w api.WorktreeStatus) bool {
	if words := m.filterWords(); words != nil {
		return !slices.ContainsFunc(w.Files, func(f api.WorktreeFile) bool { return matches(f.Path, words) })
	}
	return !m.list().worktreesOpen[w.Name]
}

// worktreesChanged keeps the cursor on its row, or on the one that took its place;
// with none left it goes back to the agents.
func (m *Model) worktreesChanged() {
	if m.list() == nil {
		return
	}
	m.codeChanged()
	m.landingsChanged()
	m.keepWorktreeCursor()
}

// keepWorktreeCursor keeps the cursor on its row, or on the one that took its
// place; with none left it goes back to the agents.
func (m *Model) keepWorktreeCursor() {
	l, rows := m.list(), m.worktreeRows()
	if i := slices.IndexFunc(rows, func(r worktreeRow) bool { return r.key() == l.worktreeKey }); i >= 0 {
		l.worktreeRow = i
		return
	}
	if len(rows) == 0 {
		if l.section == sectionWorktrees {
			l.section = sectionAgents
		}
		l.worktreeRow, l.worktreeKey = 0, ""
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
	l.section, l.worktreeRow, l.worktreeKey, l.onHeading = sectionWorktrees, i, rows[i].key(), false
}

// moveUp moves the cursor up the one list the sections make.
func (m *Model) moveUp() { m.moveBy(-1) }

// moveDown moves the cursor down the one list the sections make.
func (m *Model) moveDown() { m.moveBy(1) }

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
	if m.filtering() {
		return // what matches stays open
	}
	if l.worktreesOpen == nil {
		l.worktreesOpen = map[string]bool{}
	}
	if closed {
		delete(l.worktreesOpen, name)
	} else {
		l.worktreesOpen[name] = true
	}
}

// openWorktreeRow opens the tab of the row under the cursor, or goes to it, the focus
// following as showOpened says.
func (m *Model) openWorktreeRow(goToTab bool) tea.Cmd {
	l, rows := m.list(), m.worktreeRows()
	if l.worktreeRow >= len(rows) {
		return nil
	}
	r := rows[l.worktreeRow]
	l.openTab(r.tab())
	m.showOpened(goToTab)
	m.changed("diff")
	m.log.Info("diff opened", "worktree", r.worktree.Name, "tab", r.key())
	return m.refreshWorktrees(r.worktree.Name, true)
}

// refreshWorktrees asks the daemon to read the worktree called name again, or all
// of them when it is "", and with pullRequests what gh says of their PRs, which only
// what the user asks for may do, as it uses the network. What it finds comes with the
// stream.
func (m *Model) refreshWorktrees(name string, pullRequests bool) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		return nil
	}
	ref := "id:" + f.project.Id
	return func() tea.Msg {
		if err := m.client.RefreshWorktrees(m.ctx, ref, name, pullRequests); err != nil {
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
	return m.refreshWorktrees("", false)
}

// worktreeRowActive reports whether the tab shown is the row's; a folded worktree
// carries the mark of its file's tab.
func (m *Model) worktreeRowActive(r worktreeRow) bool {
	l := m.list()
	if r.tab() == l.active {
		return true
	}
	if worktree, file := diffTabOf(l.active); r.file == nil && isDiffTab(l.active) && file != "" && worktree == r.worktree.Name {
		if words := m.filterWords(); words != nil {
			return !matches(file, words)
		}
		return !l.worktreesOpen[worktree]
	}
	return false
}

// worktreeMark is a file's status, which tells it with color off too.
func worktreeMark(st style.Style, status api.WorktreeFileStatus) string {
	return codeMark{letter: string(status)}.paint(st, string(status))
}

// worktreeSummary is what a worktree's row says of its commits. "landed" is for a
// worktree with nothing of its own: one with changes not committed is still at work.
func worktreeSummary(w api.WorktreeStatus) string {
	if s := landingSummary(w); s != "" {
		return s
	}
	switch {
	case w.Missing:
		return "missing"
	case w.Unread:
		return "reading…" // its files and commits are not known yet, nor that it has none
	case w.Ahead == nil:
		return pullRequestSummary(w)
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
	if pr := pullRequestSummary(w); pr != "" {
		return pr + " " + s
	}
	return s
}

// removableWorktree returns the worktree under the cursor, if it landed or its
// directory is gone: C-q x removes nothing else, and the clone never.
func (m *Model) removableWorktree() (removal, bool) {
	l, rows := m.list(), m.worktreeRows()
	if m.layout.focus != project || l == nil || l.section != sectionWorktrees || l.worktreeRow >= len(rows) {
		return removal{}, false
	}
	r := rows[l.worktreeRow]
	w := r.worktree
	if r.file != nil || w.Name == "main" {
		return removal{}, false
	}
	if s := worktreeSummary(w); s != "landed" && s != "missing" && !mergedThroughPR(w) {
		return removal{}, false
	}
	return m.worktreeRemovalOf(w), true
}

// worktreeRemovalOf is what removing the worktree w comes to.
func (m *Model) worktreeRemovalOf(w api.WorktreeStatus) removal {
	return removal{kind: "worktree", name: w.Name, worktree: &worktreeRemoval{
		missing:   w.Missing,
		agents:    m.agentsIn(w.Name),
		terminals: m.terminalsIn(w.Path),
		editors:   m.editorsIn(w.Name),
	}}
}

// terminalsIn names the running terminals that started in the worktree at path.
func (m *Model) terminalsIn(path string) []string {
	var names []string
	for _, t := range m.terminals() {
		if t.State == api.TerminalStateRunning && (t.Dir == path || strings.HasPrefix(t.Dir, path+"/")) {
			names = append(names, t.Name)
		}
	}
	return names
}

// lines is what the confirmation says of a worktree's removal.
func (r *worktreeRemoval) lines() []string {
	var lines []string
	if r.missing {
		lines = append(lines, "Its directory is gone: only git's entry for it goes.")
	} else {
		lines = append(lines, "Its branch goes too, if git says it is merged.", "Ignored files go with its directory.")
	}
	if len(r.agents) > 0 {
		lines = append(lines, "Told to work from the clone: "+strings.Join(r.agents, ", "))
	}
	if len(r.terminals) > 0 {
		lines = append(lines, "Left running, with no directory: "+strings.Join(r.terminals, ", "))
	}
	if len(r.editors) > 0 {
		lines = append(lines, "Stopped, losing what they hold unsaved: "+strings.Join(r.editors, ", "))
	}
	return lines
}

// removeWorktree asks the daemon to remove the worktree r names.
func (m *Model) removeWorktree(r removal) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		m.confirm = nil
		return nil
	}
	ref := "id:" + f.project.Id
	return func() tea.Msg {
		answer, err := m.client.RemoveWorktree(m.ctx, r.name, ref, "")
		msg := removed{r: r, err: err, told: answer.Notified}
		if answer.BranchKept != nil {
			msg.branch = *answer.BranchKept
		}
		return msg
	}
}

// worktreeRemoved closes the tabs of the worktree that went, as nothing is left for
// them to show, and says what is worth knowing.
func (m *Model) worktreeRemoved(msg removed) {
	if l := m.list(); l != nil {
		for _, tab := range slices.Clone(l.tabs) {
			if name, _ := diffTabOf(tab); isTextTab(tab) && name == msg.r.name {
				m.closeTab(tab, "worktree removed")
			}
		}
	}
	m.statusNotice = "removed " + msg.r.name
	if msg.branch != "" {
		m.statusNotice += ", branch " + msg.branch + " kept"
	}
	if len(msg.told) > 0 {
		m.statusNotice += ", told " + strings.Join(msg.told, ", ")
	}
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

// worktreeOf names the worktree the agent works in: where its AI CLI last said, as
// it may have moved into one it made itself, or where it started, when that says
// nothing or is in no worktree.
func (m *Model) worktreeOf(a api.Agent) string {
	if a.CurrentDir != nil {
		if name := m.worktreeHolding(*a.CurrentDir); name != "" {
			return name
		}
	}
	return m.worktreeHolding(a.Dir)
}

// agentsIn names the running agents in the worktree called name.
func (m *Model) agentsIn(name string) []string {
	var names []string
	for _, a := range m.agents() {
		if a.State == api.AgentStateRunning && m.worktreeOf(a) == name {
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
		check := "  "
		if r.file.Verified {
			check = st.Success("✓") + " "
		}
		row = pointer + check + worktreeMark(st, r.file.Status) + " " + p + strings.Repeat(" ", max(room-ansi.StringWidth(stripped(p)), 0)) + end
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
	folded := m.worktreeFolded(w)
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
	progress := verifiedProgress(w)
	comments := ""
	if len(w.Comments) > 0 {
		comments = countOf(len(w.Comments))
	}
	if w.Ahead != nil && *w.Ahead > 0 {
		short = fmt.Sprintf("↑%d", *w.Ahead)
		if w.PullRequest != nil {
			short = fmt.Sprintf("PR #%d", w.PullRequest.Number)
		}
	}
	if s := landingShort(w); s != "" {
		short = s // a landing matters more than how far ahead it is, in a narrow pane too
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
	return []string{
		joined(summary, files, progress, comments, mark), joined(summary, files, progress, comments), joined(short, files, progress, comments),
		joined(short, progress, comments), joined(short, comments), joined(summary, files), joined(short, files), joined(summary), joined(files), "",
	}
}

// verifiedProgress says how much of what the worktree's tab shows is verified: what
// is not committed, while there is any, else its commits. Nothing before any is, as
// most reviews have no use for it.
func verifiedProgress(w api.WorktreeStatus) string {
	done, all := 0, len(w.Files)
	for _, f := range w.Files {
		if f.Verified {
			done++
		}
	}
	if all == 0 {
		all = len(w.CommittedFiles)
		for _, f := range w.CommittedFiles {
			if f.Verified {
				done++
			}
		}
	}
	if done == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d ✓", done, all)
}

// verifiedIn returns the files of the worktree called name that are verified.
func (m *Model) verifiedIn(name string) map[string]bool {
	verified := map[string]bool{}
	for _, w := range m.worktreeStatuses() {
		if w.Name != name {
			continue
		}
		for _, f := range w.Files {
			if f.Verified {
				verified[f.Path] = true
			}
		}
		for _, f := range w.CommittedFiles {
			if f.Verified {
				verified[f.Path] = true
			}
		}
	}
	return verified
}

// verifyAnswered is the answer to marking a file.
type verifyAnswered struct{ err error }

// sendVerify marks the file at path of the worktree called name, with the content
// seen as id, or takes its mark away when it has one. The stream shows the outcome.
func (m *Model) sendVerify(name, path, id string) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		return nil
	}
	ref := "id:" + f.project.Id
	unmark := m.verifiedIn(name)[path]
	m.log.Info("file mark asked", "worktree", name, "file", path, "unmark", unmark)
	return func() tea.Msg {
		if unmark {
			return verifyAnswered{m.client.UnverifyWorktreeFile(m.ctx, name, path, ref)}
		}
		return verifyAnswered{m.client.VerifyWorktreeFile(m.ctx, name, path, id, ref)}
	}
}

// verifyAnsweredMsg says why a mark was not made.
func (m *Model) verifyAnsweredMsg(msg verifyAnswered) {
	if msg.err != nil {
		m.log.Info("file not marked", "err", msg.err)
		m.statusNotice = msg.err.Error()
	}
}

// verifyRow marks the file under the cursor in the list: no diff was on the screen,
// so what it has now is what is marked.
func (m *Model) verifyRow() tea.Cmd {
	if !m.verifiable() {
		return nil
	}
	l, rows := m.list(), m.worktreeRows()
	r := rows[l.worktreeRow]
	return m.sendVerify(r.worktree.Name, r.file.Path, "")
}

// verifiable reports whether the cursor is on a worktree's file in the left pane,
// which v marks verified.
func (m *Model) verifiable() bool {
	l, rows := m.list(), m.worktreeRows()
	return m.layout.focus == project && l != nil && l.section == sectionWorktrees && l.worktreeRow < len(rows) && rows[l.worktreeRow].file != nil
}

// tabLeaf is the end of a path, as a tab's title has room for.
func tabLeaf(p string) string { return path.Base(p) }
