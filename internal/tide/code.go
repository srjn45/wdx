package tide

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The Code section lists the files of a worktree chosen on its first row, the
// selector, as a tree (docs/TIDE_CODE.md).

const (
	codeSelectorHint = "↑↓ select  ←→ worktree  / filter  Alt+arrows move  C-q menu"
	codeHint         = "↑↓ select  ←→ fold  Enter open  e edit  n/N new file/dir  r rename  d delete  / filter  C-q menu"
	mainWorktree     = "main"
)

// codeSection is what tide remembers of a project's Code section for the run.
type codeSection struct {
	worktree string                // the worktree chosen; "" until one is known
	row      int                   // the cursor's row: the selector is the first
	cursor   string                // what that row is, which it stays on as rows come and go
	files    map[string]*codeFiles // by worktree, each keeping its folds
	// awaiting is the worktree in which the cursor was put on what was just created
	// or renamed, which keeps it there until a read after the change shows the row.
	awaiting string
}

// codeFiles is what the section knows of a worktree's files.
type codeFiles struct {
	paths     []string
	emptyDirs []string // the directories holding no file, which paths cannot show
	read      int      // how many times they were read, to tell a match for these
	tree      []*codeNode
	open      map[string]bool // the directories unfolded: they start folded
	match     *codeMatch      // the files the filter last matched
	matching  codeMatchKey    // what a match is being found for, the zero value when none is
	loaded    bool
	failed    bool
	asked     bool // it was read for seen, or is being
	loading   bool
	seen      string
	// changes counts what tide created, renamed or deleted in the worktree, so that a
	// read asked before one is not taken for what is there after it.
	changes int
}

// codeNode is a file, or a directory with what is in it.
type codeNode struct {
	name     string
	path     string
	dir      bool
	children []*codeNode
}

// codeTree makes the tree of paths, whose directories are what the paths say, and of
// the empty directories: directories first, then files, each by name.
func codeTree(paths, emptyDirs []string) []*codeNode {
	root := &codeNode{dir: true}
	dirs := map[string]*codeNode{"": root}
	// dirOf returns the node of the directory p is in, making those not made yet.
	dirOf := func(p string) *codeNode {
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
		return parent
	}
	for _, p := range paths {
		parent := dirOf(p)
		parent.children = append(parent.children, &codeNode{name: path.Base(p), path: p})
	}
	for _, d := range emptyDirs {
		if _, ok := dirs[d]; !ok {
			parent := dirOf(d)
			dirs[d] = &codeNode{name: path.Base(d), path: d, dir: true}
			parent.children = append(parent.children, dirs[d])
		}
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
	match bool   // it matches the filter
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
	f := m.codeFilesShown()
	switch {
	case f == nil:
	case l.filter.words() == nil:
		rows = append(rows, codeRows(f.tree, f.open)...)
	case f.match != nil:
		for _, r := range codeRows(f.match.tree, f.match.open) {
			r.match = !r.node.dir
			rows = append(rows, r)
		}
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
	case l.filter.words() != nil && f.match == nil:
		return "filtering…"
	case l.filter.words() != nil && len(f.match.tree) == 0:
		return "no match"
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
	if l.code.awaiting != "" && l.code.awaiting == l.code.worktree {
		return
	}
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

func (s *projectScreen) updateCode(name string) tea.Cmd {
	switch name {
	case "left", "h":
		s.m.codeLeft()
	case "right", "l":
		s.m.codeRight()
	case "enter":
		r, ok := s.m.codeCursorRow()
		switch {
		case !ok || r.node == nil:
		case r.node.dir:
			s.m.toggleCodeDir(r.node.path)
		default:
			s.m.openCodeFile(r.node.path)
		}
	case editFileKey:
		if r, ok := s.m.codeCursorRow(); ok && r.node != nil && !r.node.dir {
			s.m.openCodeFile(r.node.path)
			return s.m.askEditor(s.m.list().active)
		}
	case newFileKey, newDirKey:
		s.m.newCodeEntry(name == newDirKey)
	case renameKey:
		s.m.renameCodeEntry()
	case deleteFileKey:
		s.m.deleteCodeEntry()
	}
	return nil
}

// openCodeFile opens the tab of the file at p of the worktree chosen, or goes to it.
// The focus stays in the list, to go on through the tree, unless the list is zoomed
// over the tab.
func (m *Model) openCodeFile(p string) {
	l := m.list()
	l.openTab(fileTab(l.code.worktree, p))
	if m.layout.zoomed != none {
		m.layout.setFocus(agent)
		m.layout.zoomed = agent
	}
	m.changed("file")
	m.log.Info("file opened", "worktree", l.code.worktree, "path", p)
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
	case r.fold == foldOpen && !m.filtering():
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
	if f == nil || m.filtering() {
		return // what matches stays open
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
	var shown *fileList
	if f.loaded {
		shown = &fileList{paths: f.paths, emptyDirs: f.emptyDirs}
	}
	return m.readCodeFiles(name, shown, f.changes)
}

// codeRefresh has the files of the worktree chosen read again, whatever the stream
// says: a change to what is ignored need not change the worktree's status.
func (m *Model) codeRefresh() {
	if f := m.codeFilesShown(); f != nil {
		f.asked = false
	}
	if l := m.list(); l != nil && isFileTab(l.active) && l.diffs[l.active] != nil {
		l.diffs[l.active].asked = false
	}
}

// fileList is what the daemon lists of a worktree's files.
type fileList struct {
	paths, emptyDirs []string
}

// codeLoaded is the answer to reading a worktree's files, with their tree built off
// the UI loop: it takes long in a large repository. same is a list like the one
// shown, which has nothing to build. changes is how many changes tide had made when
// it was asked.
type codeLoaded struct {
	projectID, worktree string
	paths, emptyDirs    []string
	tree                []*codeNode
	same                bool
	changes             int
	err                 error
}

// newCodeLoaded is the answer of the list got, compared with the one shown, if any.
func newCodeLoaded(projectID, worktree string, got fileList, shown *fileList) codeLoaded {
	msg := codeLoaded{projectID: projectID, worktree: worktree, paths: got.paths, emptyDirs: got.emptyDirs}
	if shown != nil && slices.Equal(shown.paths, got.paths) && slices.Equal(shown.emptyDirs, got.emptyDirs) {
		msg.same = true
		return msg
	}
	msg.tree = codeTree(got.paths, got.emptyDirs)
	return msg
}

// readCodeFiles asks the daemon for the files of the worktree called name. The
// lists it compares are never changed in place, only replaced.
func (m *Model) readCodeFiles(name string, shown *fileList, changes int) tea.Cmd {
	following := m.following
	if following == nil || following.project == nil || m.client == nil {
		return nil
	}
	id := following.project.Id
	return func() tea.Msg {
		r, err := m.client.WorktreeFiles(m.ctx, name, "id:"+id, "")
		if err != nil {
			return codeLoaded{projectID: id, worktree: name, changes: changes, err: err}
		}
		msg := newCodeLoaded(id, name, fileList{paths: r.Paths, emptyDirs: r.EmptyDirs}, shown)
		msg.changes = changes
		return msg
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
	if msg.changes != f.changes {
		// Asked before tide changed an entry, it may not show the change; the change
		// asked for another read, which codeSync now starts.
		m.log.Info("files read before a change, dropped", "worktree", msg.worktree)
		return
	}
	f.failed = msg.err != nil
	if msg.err != nil {
		m.log.Info("files not read", "worktree", msg.worktree, "err", msg.err)
		return
	}
	if msg.same && f.loaded {
		m.log.Info("files read", "worktree", msg.worktree, "files", len(msg.paths), "changed", false)
		return
	}
	f.paths, f.emptyDirs, f.tree, f.loaded = msg.paths, msg.emptyDirs, msg.tree, true
	f.read++
	m.log.Info("files read", "worktree", msg.worktree, "files", len(msg.paths), "changed", true)
	if l.code.awaiting == msg.worktree {
		l.code.awaiting = ""
	}
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
// there and folds a directory or opens a file.
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
		return
	}
	m.openCodeFile(r.node.path)
}

// codeRowActive reports whether the tab shown is the row's file; a folded directory
// carries the mark of what it hides.
func (m *Model) codeRowActive(r codeRow) bool {
	l := m.list()
	if r.node == nil || !isFileTab(l.active) {
		return false
	}
	worktree, file := diffTabOf(l.active)
	if worktree != l.code.worktree {
		return false
	}
	return file == r.node.path || (r.fold == foldClosed && strings.HasPrefix(file, r.node.path+"/"))
}

// codeMark is how the tree shows a changed file: git's letter, and whether the
// change is committed or not yet.
type codeMark struct {
	letter    string
	committed bool
	count     int // a directory's changed files, at any depth
}

// treeMarks are the marks of a worktree's tree, by path: a file and a directory can
// have the same path, one deleted in a commit and the other made since.
type treeMarks struct {
	files, dirs map[string]codeMark
}

// codeMarks returns the marks of the chosen worktree's changed files and of the
// directories they are in. A file with changes of both kinds has the mark of those
// not committed, the newer; a directory has the weightiest of those below it.
func (m *Model) codeMarks() treeMarks {
	l := m.list()
	if l == nil {
		return treeMarks{}
	}
	for _, w := range m.worktreeStatuses() {
		if w.Name != l.code.worktree {
			continue
		}
		marks := treeMarks{files: make(map[string]codeMark, len(w.Files)+len(w.CommittedFiles)), dirs: map[string]codeMark{}}
		for _, f := range w.CommittedFiles {
			marks.files[f.Path] = codeMark{letter: string(f.Status), committed: true}
		}
		for _, f := range w.Files {
			marks.files[f.Path] = codeMark{letter: string(f.Status)}
		}
		for p, mark := range marks.files {
			for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
				d := marks.dirs[dir]
				if d.letter == "" || mark.weight() > d.weight() {
					d.letter, d.committed = mark.letter, mark.committed
				}
				d.count++
				marks.dirs[dir] = d
			}
		}
		return marks
	}
	return treeMarks{}
}

// weight orders marks for the directory holding them: what is not committed comes
// before what is, and among those a deletion or a conflict, then an addition, then
// a change.
func (c codeMark) weight() int {
	if c.committed {
		return 0
	}
	switch api.WorktreeFileStatus(c.letter) {
	case api.WorktreeFileStatusD, api.WorktreeFileStatusU:
		return 3
	case api.WorktreeFileStatusA, api.WorktreeFileStatusEmpty:
		return 2
	}
	return 1
}

// paint styles text, a file's name or letter, in the colour of the mark: what is
// committed in one colour, what is not by what changed.
func (c codeMark) paint(st style.Style, text string) string {
	switch {
	case c.letter == "":
		return text
	case c.committed:
		return st.Committed(text)
	}
	switch api.WorktreeFileStatus(c.letter) {
	case api.WorktreeFileStatusA, api.WorktreeFileStatusEmpty:
		return st.Added(text)
	case api.WorktreeFileStatusD, api.WorktreeFileStatusU:
		return st.Removed(text)
	}
	return st.Warning(text)
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

// codeLine draws a row of the section, width wide: a changed file with its letter
// from marks, a directory holding changes with their count.
func (m *Model) codeLine(r codeRow, marks treeMarks, cursor bool, width int) string {
	st := m.st
	pointer := "  "
	if cursor {
		pointer = st.Focused("▸ ")
	}
	if r.node == nil {
		name := st.Name(codeSelectorName(m.list().code.worktree, width))
		return pointer + st.Muted("<") + " " + name + " " + st.Muted(">")
	}
	active := m.codeRowActive(r)
	end := " "
	if active {
		end = " " + st.Focused("◂") + " "
	}
	mark, label := marks.files[r.node.path], ""
	if r.node.dir {
		mark = marks.dirs[r.node.path]
	}
	switch {
	case mark.count > 0:
		label = " " + mark.paint(st, strconv.Itoa(mark.count))
	case mark.letter != "":
		label = " " + mark.paint(st, mark.letter)
	}
	guide, room := codeGuide(r, width)
	room -= style.Width(end) + style.Width(label)
	name := ansi.Truncate(r.node.name, max(room, 0), "…")
	gap := strings.Repeat(" ", max(room-ansi.StringWidth(name), 0))
	name = mark.paint(st, name)
	if cursor || active {
		name = st.Name(name)
	}
	if r.fold != "" {
		guide += r.fold + " "
	}
	row := pointer + st.Muted(guide) + name + gap + label + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}
