package tide

import (
	"cmp"
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// The Code section lists the files of a worktree chosen on its first row, the
// selector, as a tree (docs/TIDE_CODE.md).

const (
	codeSelectorHint = "↑↓ select  ←→ worktree  Alt+arrows move  C-q menu"
	codeHint         = "↑↓ select  ←→ fold  Enter fold  Alt+arrows move  C-q menu"
	mainWorktree     = "main"
)

// codeSection is what tide remembers of a project's Code section for the run.
type codeSection struct {
	worktree string                // the worktree chosen; "" until one is known
	row      int                   // the cursor's row: the selector is the first
	cursor   string                // what that row is, which it stays on as rows come and go
	files    map[string]*codeFiles // by worktree, each keeping its folds
}

// codeFiles is what the section knows of a worktree's files.
type codeFiles struct {
	tree      []*codeNode
	open      map[string]bool // the directories unfolded: they start folded
	truncated bool
	loaded    bool
	failed    bool
	asked     bool // it was read for seen, or is being
	loading   bool
	seen      string
}

// codeNode is a file, or a directory with what is in it.
type codeNode struct {
	name     string
	path     string
	dir      bool
	children []*codeNode
}

// codeTree makes the tree of paths, whose directories are what the paths say:
// directories first, then files, each by name.
func codeTree(paths []string) []*codeNode {
	root := &codeNode{dir: true}
	dirs := map[string]*codeNode{"": root}
	for _, p := range paths {
		parent := root
		for i, r := range p {
			if r != '/' {
				continue
			}
			d, ok := dirs[p[:i]]
			if !ok {
				d = &codeNode{name: path.Base(p[:i]), path: p[:i], dir: true}
				dirs[p[:i]] = d
				parent.children = append(parent.children, d)
			}
			parent = d
		}
		parent.children = append(parent.children, &codeNode{name: path.Base(p), path: p})
	}
	for _, d := range dirs {
		slices.SortFunc(d.children, compareCodeNodes)
	}
	return root.children
}

func compareCodeNodes(a, b *codeNode) int {
	switch {
	case a.dir && !b.dir:
		return -1
	case b.dir && !a.dir:
		return 1
	}
	return cmp.Or(cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)), cmp.Compare(a.name, b.name))
}

// codeRow is a row of the section: the selector when it has no node.
type codeRow struct {
	node  *codeNode
	depth int
	guide string // the tree's lines before the fold mark
	fold  string // foldOpen, foldClosed or, for a file at the top level, blank
}

// key names the row across reads.
func (r codeRow) key() string {
	if r.node == nil {
		return ""
	}
	return r.node.path
}

// codeRows returns the rows of the nodes shown: what a folded directory holds is
// left out.
func codeRows(nodes []*codeNode, open map[string]bool) []codeRow {
	var rows []codeRow
	var walk func(list []*codeNode, lead string, depth int)
	walk = func(list []*codeNode, lead string, depth int) {
		for i, n := range list {
			r := codeRow{node: n, depth: depth}
			var next string
			r.guide, next = treeGuides(lead, depth, i == len(list)-1)
			switch {
			case n.dir && open[n.path]:
				r.fold = foldOpen
			case n.dir:
				r.fold = foldClosed
			case depth == 0:
				r.fold = " "
			}
			rows = append(rows, r)
			if r.fold == foldOpen {
				walk(n.children, next, depth+1)
			}
		}
	}
	walk(nodes, "", 0)
	return rows
}

// codeFilesShown returns what is known of the files of the worktree chosen, nil
// before anything is.
func (m *Model) codeFilesShown() *codeFiles {
	l := m.list()
	if l == nil {
		return nil
	}
	return l.code.files[l.code.worktree]
}

// codeRowsShown returns the rows the section shows: nothing with no worktree to
// choose, and otherwise the selector and the tree.
func (m *Model) codeRowsShown() []codeRow {
	l := m.list()
	if l == nil || l.code.worktree == "" {
		return nil
	}
	rows := []codeRow{{}}
	if f := m.codeFilesShown(); f != nil {
		rows = append(rows, codeRows(f.tree, f.open)...)
	}
	return rows
}

// codeNote says what the section has in place of rows, or that it has not all of
// them; "" when it needs no saying.
func (m *Model) codeNote() string {
	l := m.list()
	f := m.codeFilesShown()
	switch {
	case l == nil:
		return ""
	case l.code.worktree == "" && m.worktreeStatuses() == nil:
		return "loading…"
	case l.code.worktree == "":
		return "no worktrees"
	case f != nil && f.failed:
		return "cannot read the files"
	case f == nil || !f.loaded:
		return "loading…"
	case f.truncated:
		return "too many files: these are the first"
	case len(f.tree) == 0:
		return "no files"
	}
	return ""
}

// codeWorktrees names the worktrees the selector goes through: those on disk.
func (m *Model) codeWorktrees() []string {
	var names []string
	for _, w := range m.worktreeStatuses() {
		if !w.Missing {
			names = append(names, w.Name)
		}
	}
	return names
}

// codeChanged keeps the worktree chosen, or goes back to the main checkout when it
// went, and the cursor on its row.
func (m *Model) codeChanged() {
	l := m.list()
	if l == nil || m.worktreeStatuses() == nil {
		return
	}
	names := m.codeWorktrees()
	switch {
	case slices.Contains(names, l.code.worktree):
	case slices.Contains(names, mainWorktree):
		l.code.worktree = mainWorktree
	case len(names) > 0:
		l.code.worktree = names[0]
	default:
		l.code.worktree = ""
	}
	m.keepCodeCursor()
}

// keepCodeCursor keeps the cursor on its row, or on the one that took its place;
// with none left it goes back to the agents.
func (m *Model) keepCodeCursor() {
	l, rows := m.list(), m.codeRowsShown()
	if len(rows) == 0 {
		l.code.row, l.code.cursor = 0, ""
		if l.section == sectionCode {
			l.section = sectionAgents
		}
		return
	}
	if i := slices.IndexFunc(rows, func(r codeRow) bool { return r.key() == l.code.cursor }); i >= 0 {
		l.code.row = i
		return
	}
	l.code.row = min(l.code.row, len(rows)-1)
	l.code.cursor = rows[l.code.row].key()
}

// moveToCode puts the cursor on the row i of the section.
func (m *Model) moveToCode(i int) {
	l, rows := m.list(), m.codeRowsShown()
	if i < 0 || i >= len(rows) {
		return
	}
	l.section, l.code.row, l.code.cursor = sectionCode, i, rows[i].key()
}

func (s *projectScreen) updateCode(name string) {
	switch name {
	case "left", "h":
		s.m.codeLeft()
	case "right", "l":
		s.m.codeRight()
	case "enter":
		if r, ok := s.m.codeCursorRow(); ok && r.node != nil && r.node.dir {
			s.m.toggleCodeDir(r.node.path)
		}
	}
}

func (m *Model) codeCursorRow() (codeRow, bool) {
	l, rows := m.list(), m.codeRowsShown()
	if l.code.row >= len(rows) {
		return codeRow{}, false
	}
	return rows[l.code.row], true
}

// codeLeft goes to the worktree before on the selector, folds the directory open
// under the cursor, and from any other row moves to its directory.
func (m *Model) codeLeft() {
	r, ok := m.codeCursorRow()
	switch {
	case !ok:
	case r.node == nil:
		m.chooseCodeWorktree(-1)
	case r.fold == foldOpen:
		m.toggleCodeDir(r.node.path)
	default:
		if parent := path.Dir(r.node.path); parent != "." {
			m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(o codeRow) bool { return o.key() == parent }))
		}
	}
}

// codeRight goes to the worktree after on the selector, and opens the directory
// folded under the cursor.
func (m *Model) codeRight() {
	r, ok := m.codeCursorRow()
	switch {
	case !ok:
	case r.node == nil:
		m.chooseCodeWorktree(1)
	case r.fold == foldClosed:
		m.toggleCodeDir(r.node.path)
	}
}

// chooseCodeWorktree goes by places through the worktrees, going round at the ends,
// and puts the cursor on the selector: what else is under it depends on the choice.
func (m *Model) chooseCodeWorktree(by int) {
	l, names := m.list(), m.codeWorktrees()
	i := slices.Index(names, l.code.worktree)
	if i < 0 || len(names) < 2 {
		return
	}
	l.code.worktree = names[(i+by+len(names))%len(names)]
	m.moveToCode(0)
	m.log.Info("code worktree chosen", "worktree", l.code.worktree)
}

// toggleCodeDir folds or unfolds the directory at dir. The cursor comes out of what
// it closes.
func (m *Model) toggleCodeDir(dir string) {
	l, f := m.list(), m.codeFilesShown()
	if f == nil {
		return
	}
	if f.open == nil {
		f.open = map[string]bool{}
	}
	if f.open[dir] {
		delete(f.open, dir)
		if strings.HasPrefix(l.code.cursor, dir+"/") {
			l.code.cursor = dir
		}
	} else {
		f.open[dir] = true
	}
	m.keepCodeCursor()
}

// codeSync reads the files of the worktree chosen when they have not been, or what
// they depend on changed. Other worktrees are read when they come to be chosen.
func (m *Model) codeSync() tea.Cmd {
	l := m.list()
	if l == nil || l.code.worktree == "" {
		return nil
	}
	if l.code.files == nil {
		l.code.files = map[string]*codeFiles{}
	}
	name := l.code.worktree
	f, ok := l.code.files[name]
	if !ok {
		f = &codeFiles{}
		l.code.files[name] = f
	}
	seen := m.diffSeen(uncommittedPrefix + name)
	if f.loading || (f.asked && f.seen == seen) {
		return nil
	}
	f.asked, f.loading, f.seen = true, true, seen
	return m.readCodeFiles(name)
}

// codeRefresh has the files of the worktree chosen read again, whatever the stream
// says: a change to what is ignored need not change the worktree's status.
func (m *Model) codeRefresh() {
	if f := m.codeFilesShown(); f != nil {
		f.asked = false
	}
}

// codeLoaded is the answer to reading a worktree's files.
type codeLoaded struct {
	projectID, worktree string
	paths               []string
	truncated           bool
	err                 error
}

// readCodeFiles asks the daemon for the files of the worktree called name.
func (m *Model) readCodeFiles(name string) tea.Cmd {
	following := m.following
	if following == nil || following.project == nil || m.client == nil {
		return nil
	}
	id := following.project.Id
	return func() tea.Msg {
		r, err := m.client.WorktreeFiles(m.ctx, name, "id:"+id, "")
		return codeLoaded{projectID: id, worktree: name, paths: r.Paths, truncated: r.Truncated, err: err}
	}
}

// codeLoaded takes the answer. An error keeps what was shown: the worktree may have
// gone, which the stream will say.
func (m *Model) codeLoaded(msg codeLoaded) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.code.files[msg.worktree] == nil {
		return
	}
	f := l.code.files[msg.worktree]
	f.loading = false
	f.failed = msg.err != nil
	if msg.err != nil {
		m.log.Info("files not read", "worktree", msg.worktree, "err", msg.err)
		return
	}
	f.tree, f.truncated, f.loaded = codeTree(msg.paths), msg.truncated, true
	m.log.Info("files read", "worktree", msg.worktree, "files", len(msg.paths), "truncated", msg.truncated)
	if l == m.list() {
		m.keepCodeCursor()
	}
}

// codeSelectorName is the name the selector shows, in a pane width wide.
func codeSelectorName(name string, width int) string {
	return ansi.Truncate(name, max(width-ansi.StringWidth("▸ <  > "), 1), "…")
}

// clickCode takes a click on the row i: on the selector's ends it goes through the
// worktrees, on a fold mark it only folds, and elsewhere on a row it moves the cursor
// there and folds a directory.
func (s *projectScreen) clickCode(i, col, width int) {
	m := s.m
	r := m.codeRowsShown()[i]
	pointer := ansi.StringWidth("▸ ")
	if r.node == nil {
		m.moveToCode(0)
		name := codeSelectorName(m.list().code.worktree, width)
		switch col {
		case pointer:
			m.chooseCodeWorktree(-1)
		case pointer + ansi.StringWidth("< "+name+" "):
			m.chooseCodeWorktree(1)
		}
		return
	}
	if r.node.dir {
		guide, _ := codeGuide(r, width)
		if col == pointer+ansi.StringWidth(guide) {
			m.toggleCodeDir(r.node.path)
			return
		}
	}
	m.moveToCode(i)
	if r.node.dir {
		m.toggleCodeDir(r.node.path)
	}
}

// codeGuide returns the guide drawn on r in a pane width wide, and the cells it
// leaves for the name and the end.
func codeGuide(r codeRow, width int) (guide string, room int) {
	base := width - ansi.StringWidth("▸ ")
	if r.fold != "" {
		base -= ansi.StringWidth(r.fold + " ")
	}
	return fitGuide(r.guide, r.depth, base)
}

// codeLine draws a row of the section, width wide.
func (m *Model) codeLine(r codeRow, cursor bool, width int) string {
	st := m.st
	pointer := "  "
	if cursor {
		pointer = st.Focused("▸ ")
	}
	if r.node == nil {
		name := codeSelectorName(m.list().code.worktree, width)
		if cursor {
			name = st.Name(name)
		}
		return pointer + st.Muted("<") + " " + name + " " + st.Muted(">")
	}
	end := " "
	guide, room := codeGuide(r, width)
	room -= style.Width(end)
	name := ansi.Truncate(r.node.name, max(room, 0), "…")
	if cursor {
		name = st.Name(name)
	}
	if r.fold != "" {
		guide += r.fold + " "
	}
	return pointer + st.Muted(guide) + name
}
