package tide

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// A diff is a tab of the agent pane, showing what the daemon read when the tab
// came to show or what it shows changed (docs/TIDE_WORKTREES.md). A file's diff
// is split in two, the committed file against the file now, whole. A worktree's
// shows only the changes it has committed, as a PR does.

const (
	fileDiffPrefix  = "diff:"
	committedPrefix = "pr:"
	fileDiffLabel   = "diff: "
	committedLabel  = "pr: "
	// uncommittedLabel is what a worktree's tab is called while it shows that.
	uncommittedLabel = "uncommitted: "
	// uncommittedPrefix names the view of a worktree's tab showing what is not
	// committed. It is no tab of its own: both views are the one tab.
	uncommittedPrefix = "uncommitted:"
	diffWheelLines    = 3
	diffTabStop       = "    "
	verifyKey         = "v"
	// jumpContext is how many lines stay above what a key goes to.
	jumpContext = 2
)

func fileDiffTab(worktree, file string) string {
	return fileDiffPrefix + worktree + rowSeparator + file
}

func committedTab(worktree string) string { return committedPrefix + worktree }

func isDiffTab(tab string) bool {
	return strings.HasPrefix(tab, fileDiffPrefix) || strings.HasPrefix(tab, committedPrefix)
}

// isTextTab reports whether the tab is read in the text view: a diff, a file or a
// doc.
func isTextTab(tab string) bool { return isDiffTab(tab) || isFileTab(tab) || isDocTab(tab) }

// diffTabOf returns the worktree a diff's tab is of, and its file if it is a
// file's.
func diffTabOf(tab string) (worktree, file string) {
	if id, ok := strings.CutPrefix(tab, docTabPrefix); ok {
		return "", id // of no worktree, and shown by line as a file is
	}
	if rest, ok := strings.CutPrefix(tab, uncommittedPrefix); ok {
		return rest, ""
	}
	if rest, ok := strings.CutPrefix(tab, fileDiffPrefix); ok {
		worktree, file, _ = strings.Cut(rest, rowSeparator)
		return worktree, file
	}
	if rest, ok := strings.CutPrefix(tab, fileTabPrefix); ok {
		worktree, file, _ = strings.Cut(rest, rowSeparator)
		return worktree, file
	}
	return strings.TrimPrefix(tab, committedPrefix), ""
}

// diffTitle is what the tab strip calls a diff's tab, which says what it shows now.
func (m *Model) diffTitle(tab string) string {
	worktree, file := diffTabOf(tab)
	switch {
	case isDocTab(tab):
		return m.docTitle(tab)
	case isFileTab(tab):
		if _, ok := m.tabEditor(tab); ok {
			return editTabLabel + worktree + "/" + tabLeaf(file)
		}
		return fileTabLabel + worktree + "/" + tabLeaf(file)
	case file != "":
		return fileDiffLabel + worktree + "/" + tabLeaf(file)
	case strings.HasPrefix(m.diffViewOf(tab), uncommittedPrefix):
		return uncommittedLabel + worktree
	}
	return committedLabel + worktree
}

// diffViewOf is the view tab shows now, which names what it holds: a worktree's
// tab shows what is not committed while there is any, unless Tab left that for the
// commits, and the commits once all is committed.
func (m *Model) diffViewOf(tab string) string {
	worktree, file := diffTabOf(tab)
	if file != "" || !m.hasUncommitted(worktree) {
		return tab
	}
	if l := m.list(); l != nil && l.showCommits[worktree] {
		return tab
	}
	return uncommittedPrefix + worktree
}

// hasUncommitted reports whether the worktree called name has files not committed.
func (m *Model) hasUncommitted(name string) bool {
	return slices.ContainsFunc(m.worktreeStatuses(), func(w api.WorktreeStatus) bool { return w.Name == name && len(w.Files) > 0 })
}

// shownDiff returns what the tab shown holds, nil before it is read.
func (m *Model) shownDiff() *diffView {
	l := m.list()
	if l == nil {
		return nil
	}
	return l.diffs[m.diffViewOf(l.active)]
}

// textShown reports whether the agent pane shows a diff or a file, read-only.
func (m *Model) textShown() bool {
	l := m.list()
	return l != nil && isTextTab(l.active) && !m.editShown()
}

// diffView is what a diff's tab holds, for the run.
type diffView struct {
	seen        string // what the worktree was when last asked for: asked again when it differs
	asked       bool
	loading     bool
	err         string
	uncommitted bool // the view of what is not committed, of a worktree's tab
	source      bool // the view of a file as it is, not of changes
	doc         bool // the view of a doc, a source tide never edits
	editing     bool // its tab showed the file's editor when last looked

	message string // why there is nothing to show, instead of the rows
	heading string // the line above the rows
	split   []splitRow
	unified []string
	lineOf  []string      // what a file holds, by line, when source
	colors  [][]colorSpan // the colours of lineOf by line, once worked out
	reading int           // which different text of the file lineOf is, for what colours belong to
	cutOff  bool          // the daemon left out the rest

	width     int // of what lines was drawn for, 0 before
	lines     []string
	rows      []int                 // the row of the source each line shows
	numbers   []int                 // the file's line number each line shows; 0 on a line wrapped or of no number
	changes   []int                 // the lines changes start on, which [ and ] go to
	files     []int                 // the lines files start on, which { and } go to
	text      []string              // what each row of the source says, which a search reads
	starts    []int                 // the line each row starts on
	file      string                // the file of a file's diff
	anchors   []lineAnchor          // the line of a file each line shows; line 0 when none
	commentAt []string              // the comment each line is, or is on
	comments  []api.WorktreeComment // those on this view, as the lines were worked out for
	placed    int                   // how many of them found their line
	spans     []fileSpan            // where each file of a worktree's patch starts
	marks     map[string]bool       // the files verified, as the lines were worked out for
	content   string                // the id of the file's content in a file's patch
	top       int                   // the rows above the lines in the last draw
	verified  string                // what the bar says of how much is verified

	cursor int  // the current line
	offset int  // the first line shown
	height int  // how many lines the last draw had room for
	jump   bool // the first draw goes to the first change

	prompt  string // what a key opened, ":" or "/", while it is typed; "" when none is
	typed   string // what was typed of it
	search  *regexp.Regexp
	query   string // what search was made from, for the bar
	notice  string // what the last key found, until the next
	keyHelp bool   // ? shows the keys in place of the lines
}

// fileSpan is a file of a worktree's patch, from the line it starts on.
type fileSpan struct {
	start    int
	path, id string
}

// diffLoaded is the answer to reading a diff.
type diffLoaded struct {
	projectID, tab string
	file           *api.WorktreeFileDiff
	committed      *api.WorktreeDiff
	uncommitted    *api.WorktreeDiff
	content        *api.WorktreeFileContent
	doc            *api.DocContent
	err            error
}

// diffSeen is what the tab's diff depends on, from what the stream says: a file's
// status, size and time, or a worktree's commits.
func (m *Model) diffSeen(tab string) string {
	if isDocTab(tab) {
		return m.docSeen(tab)
	}
	worktree, file := diffTabOf(tab)
	i := slices.IndexFunc(m.worktreeStatuses(), func(w api.WorktreeStatus) bool { return w.Name == worktree })
	if i < 0 {
		return ""
	}
	w := m.worktreeStatuses()[i]
	head := ""
	if w.Head != nil {
		head = *w.Head
	}
	if strings.HasPrefix(tab, uncommittedPrefix) {
		return fmt.Sprint(head, " ", w.Files)
	}
	if file == "" {
		return fmt.Sprint(head, " ", number(w.Ahead), " ", number(w.Added), " ", number(w.Removed))
	}
	at := slices.IndexFunc(w.Files, func(f api.WorktreeFile) bool { return f.Path == file })
	if at < 0 {
		return head
	}
	f := w.Files[at]
	when := ""
	if f.ModifiedAt != nil {
		when = f.ModifiedAt.String()
	}
	return fmt.Sprint(head, " ", f.Status, " ", f.Size, " ", when)
}

func number(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

// diffSync reads the diff of the tab shown when it has not been, or what it
// depends on changed, keeping what is shown and its place until the answer. A tab
// not shown is read when it comes to show: it may never.
func (m *Model) diffSync() tea.Cmd {
	l := m.list()
	if l == nil {
		return nil
	}
	came := l.active != l.lastActive
	l.lastActive = l.active
	if !isTextTab(l.active) {
		return nil
	}
	if l.diffs == nil {
		l.diffs = map[string]*diffView{}
	}
	view := m.diffViewOf(l.active)
	v, ok := l.diffs[view]
	if !ok {
		_, file := diffTabOf(view)
		v = &diffView{jump: true, uncommitted: strings.HasPrefix(view, uncommittedPrefix), source: isFileTab(view) || isDocTab(view), doc: isDocTab(view), file: file}
		l.diffs[view] = v
	}
	editing := m.editShown()
	if (came || (v.editing && !editing)) && v.source && !v.doc {
		v.asked = false // a gitignored file, or one edited by another or in its editor, says nothing in the stream
	}
	v.editing = editing
	seen := m.diffSeen(view)
	if v.loading || (v.asked && v.seen == seen) {
		return nil
	}
	v.asked, v.loading, v.seen = true, true, seen
	return m.readDiff(view)
}

// readDiff asks the daemon for the diff of tab.
func (m *Model) readDiff(tab string) tea.Cmd {
	f := m.following
	if f == nil || f.project == nil || m.client == nil {
		return nil
	}
	id := f.project.Id
	ref := "id:" + id
	worktree, file := diffTabOf(tab)
	return func() tea.Msg {
		msg := diffLoaded{projectID: id, tab: tab}
		switch {
		case isDocTab(tab):
			r, err := m.client.GetDoc(m.ctx, "id:"+file, ref, "", 0)
			msg.doc, msg.err = &r, err
		case isFileTab(tab):
			r, err := m.client.WorktreeFileContent(m.ctx, worktree, file, ref, "")
			msg.content, msg.err = &r, err
		case file != "":
			r, err := m.client.WorktreeFileDiff(m.ctx, worktree, file, ref, "")
			msg.file, msg.err = &r, err
		case strings.HasPrefix(tab, uncommittedPrefix):
			r, err := m.client.WorktreeUncommittedDiff(m.ctx, worktree, ref, "")
			msg.uncommitted, msg.err = &r, err
		default:
			r, err := m.client.WorktreeDiff(m.ctx, worktree, ref, "")
			msg.committed, msg.err = &r, err
		}
		return msg
	}
}

func (m *Model) diffLoaded(msg diffLoaded) tea.Cmd {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.diffs[msg.tab] == nil {
		return nil
	}
	v := l.diffs[msg.tab]
	v.loading = false
	if msg.err != nil {
		m.log.Info("diff not read", "tab", msg.tab, "err", msg.err)
		if !v.setSourceError(msg.err) {
			v.err = msg.err.Error()
		}
		return nil
	}
	v.err, v.message, v.heading, v.cutOff = "", "", "", false
	old := v.lineOf
	v.split, v.unified, v.lineOf = nil, nil, nil
	v.width = 0 // what is drawn is to be worked out again
	switch {
	case msg.content != nil:
		v.setSource(*msg.content)
		return m.sourceRead(msg.projectID, msg.tab, v, old)
	case msg.doc != nil:
		v.setDoc(*msg.doc)
		return m.sourceRead(msg.projectID, msg.tab, v, old)
	case msg.file != nil:
		v.setFile(*msg.file)
	case msg.uncommitted != nil:
		v.setUncommitted(*msg.uncommitted)
	default:
		v.setCommitted(*msg.committed)
	}
	return nil
}

func (v *diffView) setFile(d api.WorktreeFileDiff) {
	v.cutOff = d.Truncated
	switch {
	case d.Binary:
		v.message = "a binary file: only said to differ"
	case d.Patch == "":
		v.message = "no uncommitted changes"
	default:
		v.split = splitPatch(d.Patch)
	}
	if h := indexLine.FindStringSubmatch(d.Patch); h != nil {
		v.content = h[3]
	}
}

func (v *diffView) setUncommitted(d api.WorktreeDiff) {
	v.cutOff = d.Truncated
	if d.Patch == "" {
		v.message = "no uncommitted changes"
		return
	}
	v.unified = strings.Split(strings.TrimRight(d.Patch, "\n"), "\n")
	files := 0
	for _, line := range v.unified {
		if strings.HasPrefix(line, "diff --git ") {
			files++
		}
	}
	v.heading = "not committed · 1 file"
	if files != 1 {
		v.heading = fmt.Sprintf("not committed · %d files", files)
	}
}

func (v *diffView) setCommitted(d api.WorktreeDiff) {
	v.cutOff = d.Truncated
	base := ""
	if d.Base != nil {
		base = *d.Base
	}
	switch {
	case base == "":
		v.message = "nothing to compare with: no origin/HEAD, main or master"
		return
	case d.MergeBase == nil:
		v.message = "no history in common with " + base
		return
	case d.Patch == "":
		v.message = "nothing ahead of " + base
		return
	}
	mergeBase := *d.MergeBase
	if len(mergeBase) > 7 {
		mergeBase = mergeBase[:7]
	}
	v.heading = "base: " + base + " · merge-base " + mergeBase
	if d.Ahead != nil {
		v.heading += fmt.Sprintf(" · %d commits", *d.Ahead)
		if *d.Ahead == 1 {
			v.heading = strings.TrimSuffix(v.heading, "s")
		}
	}
	v.unified = strings.Split(strings.TrimRight(d.Patch, "\n"), "\n")
}

// splitRow is a row of the split view: a line of the committed file, of the file
// now, or the two where one took the other's place. A number of 0 is no line.
type splitRow struct {
	leftNo, rightNo int
	left, right     string
	changed         bool
}

// indexLine is a patch's line naming the file's content before and after.
var indexLine = regexp.MustCompile(`(?m)^(index )([0-9a-f]+)\.\.([0-9a-f]+)`)

// patchPath returns the file of a patch's "diff --git a/x b/x" line. git quotes a
// path it cannot show, which is not one a mark can name. With renames off the two
// paths are the same, which tells where they part, whatever spaces they have.
func patchPath(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "diff --git a/")
	if !ok || (len(rest)-len(" b/"))%2 != 0 {
		return "", false
	}
	path := rest[:(len(rest)-len(" b/"))/2]
	return path, rest == path+" b/"+path
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// splitPatch lays a patch out in two columns, lined up: removed lines beside the
// lines added in their place.
func splitPatch(patch string) []splitRow {
	var rows []splitRow
	var removed, added []splitRow
	flush := func() {
		for i := range max(len(removed), len(added)) {
			r := splitRow{changed: true}
			if i < len(removed) {
				r.leftNo, r.left = removed[i].leftNo, removed[i].left
			}
			if i < len(added) {
				r.rightNo, r.right = added[i].rightNo, added[i].right
			}
			rows = append(rows, r)
		}
		removed, added = nil, nil
	}
	left, right := 0, 0
	inHunk := false
	for line := range strings.SplitSeq(patch, "\n") {
		if h := hunkHeader.FindStringSubmatch(line); h != nil {
			flush()
			left, _ = strconv.Atoi(h[1])
			right, _ = strconv.Atoi(h[2])
			inHunk = true
			continue
		}
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			inHunk = false
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		text := line[1:]
		switch line[0] {
		case '-':
			removed = append(removed, splitRow{leftNo: left, left: text})
			left++
		case '+':
			added = append(added, splitRow{rightNo: right, right: text})
			right++
		case ' ':
			flush()
			rows = append(rows, splitRow{leftNo: left, rightNo: right, left: text, right: text})
			left++
			right++
		}
	}
	flush()
	return rows
}

// shownText is a line of a file as it can be drawn: what is not text is a program's
// to write, and must not reach the terminal.
func shownText(s string) string {
	s = strings.ReplaceAll(s, "\t", diffTabStop)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return '·'
		}
		return r
	}, s)
}

// prepare works out the lines for width, once per width and content.
func (v *diffView) prepare(width int, st style.Style) {
	if v.width == width && v.lines != nil {
		return
	}
	v.width = width
	// The current line stays on its row of the source: a comment above adds lines.
	row := -1
	if v.cursor < len(v.rows) && v.cursor >= 0 {
		row = v.rows[v.cursor]
	}
	v.lines, v.rows, v.numbers, v.changes, v.files, v.text, v.starts, v.spans = nil, nil, nil, nil, nil, nil, nil, nil
	v.anchors, v.commentAt, v.placed = nil, nil, 0
	defer func() {
		if row >= 0 && row < len(v.starts) {
			v.cursor = v.starts[row]
		}
	}()
	switch {
	case v.lineOf != nil:
		v.prepareSource(width, st)
	case v.split != nil:
		v.prepareSplit(width, st)
	case v.unified != nil:
		v.prepareUnified(width, st)
	}
	if v.cutOff {
		v.add(st.Warning("too long: the rest is left out"), len(v.text)-1, 0)
	}
	if v.lines == nil {
		v.lines = []string{}
	}
	v.cursor = max(min(v.cursor, len(v.lines)-1), 0)
}

// add appends a line showing row of the source.
func (v *diffView) add(line string, row, number int) {
	v.lines = append(v.lines, line)
	v.rows = append(v.rows, row)
	v.numbers = append(v.numbers, number)
	v.anchors = append(v.anchors, lineAnchor{})
	v.commentAt = append(v.commentAt, "")
}

// paint styles a piece of a line, with what the search finds standing out.
func (v *diffView) paint(piece string, paint func(string) string, st style.Style) string {
	return v.paintRanges(piece, func(from, to int) string { return paint(piece[from:to]) }, st)
}

// paintRanges is paint for what paints by where in piece the text is.
func (v *diffView) paintRanges(piece string, paint func(from, to int) string, st style.Style) string {
	if v.search == nil {
		return paint(0, len(piece))
	}
	var b strings.Builder
	at := 0
	for _, m := range v.search.FindAllStringIndex(piece, -1) {
		b.WriteString(paint(at, m[0]))
		b.WriteString(st.Match(piece[m[0]:m[1]]))
		at = m[1]
	}
	b.WriteString(paint(at, len(piece)))
	return b.String()
}

// prepareSplit lays the rows out in two columns; a long line wraps, and its side
// waits for it, so that the two stay lined up.
func (v *diffView) prepareSplit(width int, st style.Style) {
	last := 0
	for _, r := range v.split {
		last = max(last, r.leftNo, r.rightNo)
	}
	digits := len(strconv.Itoa(last))
	side := (width - 1) / 2
	text := max(side-digits-3, 1) // the number, a space, the sign, a space
	cell := func(no int, sign string, paint func(string) string, wrapped []string, i int) string {
		number, mark := strings.Repeat(" ", digits), " "
		if i == 0 && no > 0 {
			number, mark = fmt.Sprintf("%*d", digits, no), sign
		}
		piece := ""
		if i < len(wrapped) {
			piece = wrapped[i]
		}
		pad := strings.Repeat(" ", max(text-ansi.StringWidth(piece), 0))
		return st.Muted(number) + " " + paint(mark+" ") + v.paint(piece, paint, st) + pad
	}
	inChange := false
	for row, r := range v.split {
		if r.changed && !inChange {
			v.changes = append(v.changes, len(v.lines))
		}
		inChange = r.changed
		v.text = append(v.text, shownText(r.left)+"\n"+shownText(r.right))
		v.starts = append(v.starts, len(v.lines))
		lefts, rights := wrapText(r.left, text), wrapText(r.right, text)
		same := func(s string) string { return s }
		removed, added := same, same
		leftSign, rightSign := " ", " "
		if r.changed {
			removed, added, leftSign, rightSign = st.Removed, st.Added, "-", "+"
		}
		number := r.rightNo
		if number == 0 {
			number = r.leftNo
		}
		for i := range max(len(lefts), len(rights), 1) {
			line := cell(r.leftNo, leftSign, removed, lefts, i) + st.Muted("│") + cell(r.rightNo, rightSign, added, rights, i)
			shown := 0
			if i == 0 {
				shown = number
			}
			v.add(line, row, shown)
		}
		first := v.starts[row]
		a := lineAnchor{file: v.file, line: r.rightNo, side: api.WorktreeCommentSideNew, quote: r.right, id: v.content}
		if r.rightNo == 0 {
			a.line, a.side, a.quote = r.leftNo, api.WorktreeCommentSideOld, r.left
		}
		for i := first; i < len(v.anchors); i++ {
			v.anchors[i] = a
		}
		v.addComments(first, a, row, width, st)
		if a.side == api.WorktreeCommentSideNew && r.changed && r.leftNo > 0 {
			v.addComments(first, lineAnchor{file: v.file, line: r.leftNo, side: api.WorktreeCommentSideOld}, row, width, st)
		}
	}
}

func wrapText(s string, width int) []string {
	if s == "" {
		return nil
	}
	return strings.Split(ansi.Hardwrap(shownText(s), width, true), "\n")
}

// prepareUnified shows a commit's patch as git writes it.
func (v *diffView) prepareUnified(width int, st style.Style) {
	inHunk := false
	left, right := 0, 0
	for row, line := range v.unified {
		paint := func(s string) string { return s }
		var a lineAnchor
		switch {
		case strings.HasPrefix(line, "diff --git "):
			v.files = append(v.files, len(v.lines))
			paint = st.Name
			inHunk = false
			if path, ok := patchPath(line); ok {
				v.spans = append(v.spans, fileSpan{start: len(v.lines), path: path})
				if v.marks[path] {
					line = "✓ " + line
				}
			}
		case hunkHeader.MatchString(line):
			v.changes = append(v.changes, len(v.lines))
			paint = st.Muted
			inHunk = true
			h := hunkHeader.FindStringSubmatch(line)
			left, _ = strconv.Atoi(h[1])
			right, _ = strconv.Atoi(h[2])
		case inHunk:
			a = v.hunkAnchor(line, &left, &right)
			if strings.HasPrefix(line, "+") {
				paint = st.Added
			} else if strings.HasPrefix(line, "-") {
				paint = st.Removed
			}
		case indexLine.MatchString(line):
			paint = st.Muted
			h := indexLine.FindStringSubmatch(line)
			if n := len(v.spans); n > 0 && v.spans[n-1].id == "" {
				v.spans[n-1].id = h[3]
			}
			// The full ids are for the marks; seven say as much to a reader.
			line = "index " + shortID(h[2]) + ".." + shortID(h[3]) + line[len(h[0]):]
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "new file "), strings.HasPrefix(line, "deleted file "):
			paint = st.Muted
		}
		v.text = append(v.text, shownText(line))
		v.starts = append(v.starts, len(v.lines))
		pieces := wrapText(line, width)
		if len(pieces) == 0 {
			pieces = []string{""}
		}
		first := len(v.lines)
		for _, piece := range pieces {
			v.add(v.paint(piece, paint, st), row, 0)
			v.anchors[len(v.anchors)-1] = a
		}
		if a.line > 0 {
			v.addComments(first, a, row, width, st)
		}
	}
}

// hunkAnchor is the line of the file a line of a hunk shows, moving the numbers on.
func (v *diffView) hunkAnchor(line string, left, right *int) lineAnchor {
	if line == "" || len(v.spans) == 0 {
		return lineAnchor{}
	}
	span := v.spans[len(v.spans)-1]
	a := lineAnchor{file: span.path, quote: line[1:], id: span.id}
	switch line[0] {
	case '+':
		a.line, a.side = *right, api.WorktreeCommentSideNew
		*right++
	case '-':
		a.line, a.side = *left, api.WorktreeCommentSideOld
		*left++
	case ' ':
		a.line, a.side = *right, api.WorktreeCommentSideNew
		*left++
		*right++
	default:
		return lineAnchor{} // \ No newline at end of file
	}
	return a
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// currentFile returns the file the current line is of, and the content seen of it.
func (v *diffView) currentFile(tab string) (path, id string, ok bool) {
	if _, file := diffTabOf(tab); file != "" {
		return file, v.content, true
	}
	for _, span := range slices.Backward(v.spans) {
		if span.start <= v.cursor {
			return span.path, span.id, true
		}
	}
	if len(v.spans) > 0 {
		return v.spans[0].path, v.spans[0].id, true
	}
	return "", "", false
}

// barKey is what the bar says of a key.
type barKey struct {
	keys, label string
	keep        int // the higher, the longer it stays when the bar is short of room
}

// barKeys are the keys that can act on the tab now, in the order the bar shows
// them. file says whether it is a file's diff.
func (v *diffView) barKeys(file bool, extra []barKey) []barKey {
	if len(v.lines) == 0 {
		return append([]barKey{{"C-q -", "close", 1}}, extra...)
	}
	keys := []barKey{{"↑↓", "scroll", 1}}
	if v.source && !v.doc {
		keys = append(keys, barKey{editFileKey, "edit", 2})
	} else if !v.source {
		keys = append(keys, barKey{"[ ]", "change", 5})
	}
	if file {
		keys = append(keys, barKey{":", "line", 3})
	} else {
		keys = append(keys, barKey{"{ }", "file", 3})
	}
	keys = append(keys, barKey{"/", "search", 4})
	if v.search != nil {
		keys = append(keys, barKey{"n N", "match", 6})
	}
	return append(append(keys, extra...), barKey{"?", "keys", 9})
}

// worktreeBarKeys are what a worktree's tab adds to the bar: the other view while
// there are two, and committing while it shows what is not.
func (m *Model) worktreeBarKeys(tab string) []barKey {
	worktree, file := diffTabOf(tab)
	var keys []barKey
	if v := m.shownDiff(); v != nil && (len(v.split) > 0 || len(v.unified) > 0) {
		label := "verified"
		if path, _, ok := v.currentFile(tab); ok && m.verifiedIn(worktree)[path] {
			label = "unverified"
		}
		keys = append(keys, barKey{verifyKey, label, 7})
		keys = append(keys, m.commentBarKeys(tab, v)...)
	}
	if file != "" {
		return keys
	}
	if m.hasUncommitted(worktree) {
		if strings.HasPrefix(m.diffViewOf(tab), uncommittedPrefix) {
			return append(keys, barKey{"Tab", "commits", 7}, barKey{commitKey, "commit", 8})
		}
		keys = append(keys, barKey{"Tab", "uncommitted", 7})
	}
	if key, ok := m.landBarKey(worktree); ok {
		keys = append(keys, key)
	}
	return keys
}

// allKeys are what ? shows.
func (v *diffView) allKeys(file bool, extra []barKey) []barKey {
	if v.doc {
		return docKeys
	}
	if v.source {
		return sourceKeys
	}
	keys := []barKey{
		{"↑ ↓", "the line above or below", 0}, {"PgUp PgDn", "a page up or down", 0}, {"b Space", "a page up or down", 0},
		{"g G", "the first or last line", 0}, {"[ ]", "the change before or after", 0},
	}
	if file {
		keys = append(keys, barKey{": and a number", "go to that line of the file", 0})
	} else {
		keys = append(keys, barKey{"{ }", "the file before or after", 0})
	}
	for _, k := range extra {
		keys = append(keys, barKey{k.keys, "shows or does: " + k.label, 0})
	}
	return append(keys,
		barKey{commentKey, "comment on the current line", 0}, barKey{"e d", "edit or delete the comment on it", 0},
		barKey{reviewKey, "send the comments to an agent, together", 0},
		barKey{verifyKey, "mark the file verified, or not; an edit takes the mark away", 0},
		barKey{"/ and text", "search; capitals make it exact", 0}, barKey{"n N", "the next or last match", 0},
		barKey{"Esc", "end the search", 0}, barKey{"C-q -", "close the tab", 0}, barKey{"Alt+←", "the tab before", 0})
}

// bar is the bar of shortcuts at the bottom of the tab: what can act on it now and,
// at its right end, where the current line is. What does not fit goes, the least
// needed first.
func (v *diffView) bar(file bool, extra []barKey, width int, st style.Style) string {
	if v.prompt != "" {
		return v.prompt + v.typed + "_" + st.Muted("  Enter go  Esc cancel")
	}
	place := v.notice
	if place == "" {
		place = v.position(file)
		if v.verified != "" {
			place = v.verified + " · " + place
		}
	}
	keys := v.barKeys(file, extra)
	used := func(keys []barKey, place string) int {
		n := 1 + ansi.StringWidth(place)
		for _, k := range keys {
			n += ansi.StringWidth(k.keys) + 1 + ansi.StringWidth(k.label) + 2
		}
		return n
	}
	for used(keys, place) > width && place != "" && len(keys) > 3 {
		keys = dropLeast(keys)
	}
	if used(keys, place) > width && v.notice != "" {
		// What the last key found, as why the editor did not start, is cut short
		// rather than lost.
		place = ansi.Truncate(place, max(width-used(keys, "")-1, 0), "…")
	}
	if used(keys, place) > width {
		place = ""
	}
	for used(keys, place) > width && len(keys) > 1 {
		keys = dropLeast(keys)
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, st.Command(k.keys)+" "+st.Muted(k.label))
	}
	line := " " + strings.Join(parts, "  ")
	if place != "" {
		line += strings.Repeat(" ", max(width-style.Width(line)-ansi.StringWidth(place), 1)) + st.Muted(place)
	}
	return line
}

func dropLeast(keys []barKey) []barKey {
	least := 0
	for i, k := range keys {
		if k.keep < keys[least].keep {
			least = i
		}
	}
	return slices.Delete(slices.Clone(keys), least, least+1)
}

// position says where the current line is.
func (v *diffView) position(file bool) string {
	if len(v.lines) == 0 {
		return ""
	}
	if v.source {
		return fmt.Sprintf("line %d of %d", v.lineNumber(), len(v.lineOf))
	}
	if file {
		return ofAll("change", v.changes, v.cursor) + " · " + fmt.Sprintf("line %d of %d", v.lineNumber(), v.lastNumber())
	}
	return ofAll("change", v.changes, v.cursor) + " · " + ofAll("file", v.files, v.cursor)
}

// ofAll says which of the starts the line is at or past.
func ofAll(what string, starts []int, line int) string {
	n := 0
	for _, s := range starts {
		if s <= line {
			n++
		}
	}
	return fmt.Sprintf("%s %d of %d", what, n, len(starts))
}

// lineNumber is the number of the file's line the current line is part of.
func (v *diffView) lineNumber() int {
	for i := v.cursor; i >= 0; i-- {
		if v.numbers[i] > 0 {
			return v.numbers[i]
		}
	}
	return 0
}

func (v *diffView) lastNumber() int {
	last := 0
	for _, r := range v.split {
		last = max(last, r.rightNo)
	}
	if last == 0 {
		for _, r := range v.split {
			last = max(last, r.leftNo)
		}
	}
	return last
}

// drawDiff draws the diff of the tab shown in area, with its bar at the bottom, or
// why there is none.
func (m *Model) drawDiff(buf uv.Screen, area uv.Rectangle, st style.Style) {
	l := m.list()
	v := m.shownDiff()
	var top []string
	if v != nil && v.err != "" {
		what := "the diff"
		if v.doc {
			what = "the doc"
		} else if v.source {
			what = "the file"
		}
		top = append(top, st.Failure(wrap(what+" could not be read: "+v.err, area.Dx())))
	}
	switch {
	case v == nil || (v.loading && v.lines == nil && !v.hasText() && v.message == ""):
		top = append(top, st.Muted("loading…"))
	case v.message != "":
		top = append(top, st.Muted(v.message))
	}
	if v == nil {
		uv.NewStyledString(strings.Join(top, "\n")).Draw(buf, area)
		return
	}
	_, file := diffTabOf(l.active)
	isFile := file != ""
	width := max(area.Dx()-1, 1) // the first column says which line is current
	worktree, _ := diffTabOf(l.active)
	if marks := m.verifiedIn(worktree); !maps.Equal(marks, v.marks) {
		v.marks = marks
		v.width = 0 // a file's heading says it
	}
	if comments := m.viewComments(m.diffViewOf(l.active)); !v.source && !reflect.DeepEqual(comments, v.comments) {
		v.comments = comments
		v.width = 0 // they show under their lines
	}
	v.verified = ""
	if !isFile {
		if i := slices.IndexFunc(m.worktreeStatuses(), func(w api.WorktreeStatus) bool { return w.Name == worktree }); i >= 0 {
			v.verified = verifiedProgress(m.worktreeStatuses()[i])
		}
	}
	if v.hasText() {
		v.prepare(width, st)
		if v.jump {
			v.jump = false
			if len(v.changes) > 0 {
				v.goTo(v.changes[0])
			}
		}
	} else {
		v.lines = nil
	}
	var extra []barKey
	if !v.source {
		extra = m.worktreeBarKeys(l.active) // after the lines: it says of the current file
	}
	content := area
	if area.Dy() >= 3 { // too small for it, the lines come first
		content.Max.Y--
		uv.NewStyledString(v.bar(isFile, extra, area.Dx(), st)).Draw(buf, uv.Rect(area.Min.X, content.Max.Y, area.Dx(), 1))
	}
	if v.keyHelp {
		lines := []string{st.Heading(" Keys") + st.Muted("  Esc or ? closes"), ""}
		for _, k := range v.allKeys(isFile, extra) {
			lines = append(lines, " "+st.Command(padded(k.keys, 16))+" "+st.Muted(k.label))
		}
		uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, content)
		return
	}
	if !v.hasText() {
		uv.NewStyledString(strings.Join(top, "\n")).Draw(buf, content)
		return
	}
	if v.heading != "" {
		top = append(top, st.Muted(ansi.Truncate(v.heading, area.Dx(), "…")))
	}
	if v.split != nil {
		if v.marks[file] {
			top = append(top, st.Success("✓ verified"))
		}
		side := (width - 1) / 2
		top = append(top, " "+st.Heading(ansi.Truncate(padded(" HEAD", side), side, ""))+st.Muted("│")+st.Heading(ansi.Truncate(" uncommitted", width-side-1, "")))
	}
	if n := len(v.comments) - v.placed; n > 0 {
		top = append(top, st.Muted(countOf(n)+" on lines not shown here: s still sends them"))
	}
	body := max(content.Dy()-len(top), 1)
	v.height, v.top = body, len(top)
	v.offset = max(min(v.offset, len(v.lines)-body), 0)
	v.offset = max(min(v.offset, v.cursor), v.cursor-body+1)
	shown := top
	for i := v.offset; i < min(v.offset+body, len(v.lines)); i++ {
		if i == v.cursor {
			shown = append(shown, st.Focused("▸")+st.ActiveRow(v.lines[i]))
		} else {
			shown = append(shown, " "+v.lines[i])
		}
	}
	uv.NewStyledString(strings.Join(shown, "\n")).Draw(buf, content)
}

func padded(s string, width int) string {
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

// goTo makes line current, with what comes before it in view.
func (v *diffView) goTo(line int) {
	v.cursor = max(min(line, len(v.lines)-1), 0)
	v.offset = max(v.cursor-jumpContext, 0)
}

// moveBy moves the current line; the view follows it when it is drawn.
func (v *diffView) moveBy(by int) {
	v.cursor = max(min(v.cursor+by, len(v.lines)-1), 0)
}

// next goes to the first of starts past the current line.
func (v *diffView) next(starts []int) {
	if i := slices.IndexFunc(starts, func(s int) bool { return s > v.cursor }); i >= 0 {
		v.goTo(starts[i])
	}
}

// previous goes to the last of starts before the current line.
func (v *diffView) previous(starts []int) {
	for _, s := range slices.Backward(starts) {
		if s < v.cursor {
			v.goTo(s)
			return
		}
	}
}

// goToNumber goes to the line of the file numbered n, or the nearest there is.
func (v *diffView) goToNumber(n int) {
	best := -1
	for i, no := range v.numbers {
		if no == 0 {
			continue
		}
		best = i
		if no >= n {
			break
		}
	}
	if best >= 0 {
		v.goTo(best)
	}
}

// setSearch searches for query: a query with capitals is exact, one without is not.
func (v *diffView) setSearch(query string) {
	pattern := regexp.QuoteMeta(query)
	if strings.ToLower(query) == query {
		pattern = "(?i)" + pattern
	}
	v.search, v.query = regexp.MustCompile(pattern), query
	v.width = 0 // the lines are worked out again, for the matches to stand out
}

// clearSearch ends the search.
func (v *diffView) clearSearch() {
	v.search, v.query = nil, ""
	v.width = 0
}

// nextMatch goes to the next row the search finds, going round at the ends; step
// is 1 or -1. from says to count from the current row itself, as a new search does.
func (v *diffView) nextMatch(step int, from bool) {
	if v.search == nil || len(v.text) == 0 {
		return
	}
	n := len(v.text)
	at := 0
	if v.cursor < len(v.rows) {
		at = v.rows[v.cursor]
	}
	if !from {
		at += step
	}
	for range n {
		row := ((at % n) + n) % n
		if v.search.MatchString(v.text[row]) {
			v.goTo(v.starts[row])
			return
		}
		at += step
	}
	v.notice = "no match: " + v.query
}

// diffKey takes a key while the agent pane shows a diff.
func (m *Model) diffKey(msg tea.Msg) tea.Cmd {
	l := m.list()
	v := m.shownDiff()
	if v == nil {
		return nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		if v.prompt != "" {
			v.typed += strings.Join(strings.Fields(paste.Content), " ")
		}
		return nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	_, file := diffTabOf(l.active)
	v.notice = ""
	if v.prompt != "" {
		v.promptKey(key)
		return nil
	}
	if v.keyHelp {
		if name := key.String(); name == "esc" || name == "?" {
			v.keyHelp = false
		}
		return nil
	}
	if v.source && key.String() == editFileKey {
		if v.doc {
			return nil // tide never changes a doc
		}
		return m.askEditor(l.active)
	}
	if v.source && slices.Contains(diffOnlyKeys, key.String()) {
		return nil
	}
	page := max(v.height-1, 1)
	switch name := key.String(); {
	case slices.Contains(upKeys, name):
		v.moveBy(-1)
	case slices.Contains(downKeys, name):
		v.moveBy(1)
	case name == "pgup", name == "b":
		v.moveBy(-page)
	case name == "pgdown", name == "space", name == " ":
		v.moveBy(page)
	case name == "home", name == "g":
		v.cursor = 0
	case name == "end", name == "G":
		v.cursor = max(len(v.lines)-1, 0)
	case name == "[":
		v.previous(v.changes)
	case name == "]":
		v.next(v.changes)
	case name == "{" && file == "":
		v.previous(v.files)
	case name == "}" && file == "":
		v.next(v.files)
	case name == ":" && file != "":
		v.prompt, v.typed = ":", ""
	case name == "/":
		v.prompt, v.typed = "/", ""
	case name == "n":
		v.nextMatch(1, false)
	case name == "N":
		v.nextMatch(-1, false)
	case name == "?":
		v.keyHelp = true
	case name == "esc":
		v.clearSearch()
	case name == "tab":
		m.flipWorktreeView(l.active)
	case name == commitKey:
		m.askCommit(l.active, v)
	case name == landKey:
		return m.askLand(l.active, v)
	case name == commentKey:
		m.askComment(l.active, v)
	case name == editKey:
		m.askEdit(l.active, v)
	case name == deleteKey:
		return m.deleteComment(l.active, v)
	case name == reviewKey:
		m.askReview(l.active, v)
	case name == verifyKey:
		if path, id, ok := v.currentFile(l.active); ok && (len(v.split) > 0 || len(v.unified) > 0) {
			worktree, _ := diffTabOf(l.active)
			return m.sendVerify(worktree, path, id)
		}
	}
	return nil
}

// promptKey takes a key while : or / is typed.
func (v *diffView) promptKey(key tea.KeyPressMsg) {
	switch name := key.String(); {
	case name == "esc":
		v.prompt = ""
	case name == "enter":
		v.finishPrompt()
	case name == "backspace" && v.typed != "":
		r := []rune(v.typed)
		v.typed = string(r[:len(r)-1])
	case typed(key):
		v.typed += key.Text
	}
}

// finishPrompt does what was typed.
func (v *diffView) finishPrompt() {
	prompt, text := v.prompt, strings.TrimSpace(v.typed)
	v.prompt, v.typed = "", ""
	switch {
	case text == "":
	case prompt == ":":
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 {
			v.notice = "not a line number: " + text
			return
		}
		v.goToNumber(n)
	default:
		v.setSearch(text)
		v.nextMatch(1, true)
	}
}

// diffWheel scrolls the diff shown, the current line staying in view.
func (m *Model) diffWheel(msg tea.MouseWheelMsg) {
	v := m.shownDiff()
	if v == nil {
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		v.offset = max(v.offset-diffWheelLines, 0)
	case tea.MouseWheelDown:
		v.offset = max(min(v.offset+diffWheelLines, len(v.lines)-v.height), 0)
	}
	v.cursor = max(min(v.cursor, v.offset+max(v.height, 1)-1), v.offset)
}

// diffClick makes the line clicked current; row counts from the pane's top.
func (m *Model) diffClick(row int) {
	v := m.shownDiff()
	if v == nil || v.keyHelp {
		return
	}
	line := v.offset + row - v.top
	if line >= v.offset && line < min(v.offset+v.height, len(v.lines)) {
		v.cursor = line
	}
}
