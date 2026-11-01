package tide

import (
	"fmt"
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
	diffWheelLines  = 3
	diffTabStop     = "    "
	// jumpContext is how many lines stay above a change gone to.
	jumpContext = 2
	diffHint    = "↑↓ PgUp PgDn scroll  n p change  C-q - close  Alt+← back  C-q menu"
)

func fileDiffTab(worktree, file string) string {
	return fileDiffPrefix + worktree + rowSeparator + file
}

func committedTab(worktree string) string { return committedPrefix + worktree }

func isDiffTab(tab string) bool {
	return strings.HasPrefix(tab, fileDiffPrefix) || strings.HasPrefix(tab, committedPrefix)
}

// diffTabOf returns the worktree a diff's tab is of, and its file if it is a
// file's.
func diffTabOf(tab string) (worktree, file string) {
	if rest, ok := strings.CutPrefix(tab, fileDiffPrefix); ok {
		worktree, file, _ = strings.Cut(rest, rowSeparator)
		return worktree, file
	}
	return strings.TrimPrefix(tab, committedPrefix), ""
}

// diffTitle is what the tab strip calls a diff's tab.
func diffTitle(tab string) string {
	worktree, file := diffTabOf(tab)
	if file != "" {
		return fileDiffLabel + worktree + "/" + tabLeaf(file)
	}
	return committedLabel + worktree
}

// diffShown reports whether the agent pane shows a diff.
func (m *Model) diffShown() bool {
	l := m.list()
	return l != nil && isDiffTab(l.active)
}

// diffView is what a diff's tab holds, for the run.
type diffView struct {
	seen    string // what the worktree was when last asked for: asked again when it differs
	asked   bool
	loading bool
	err     string

	message string // why there is nothing to show, instead of the rows
	heading string // the line above the rows
	split   []splitRow
	unified []string
	cutOff  bool // the daemon left out the rest

	width   int // of what lines was drawn for, 0 before
	lines   []string
	changes []int // the lines changes start on, which n and p go to
	offset  int
	height  int  // of the last draw
	jump    bool // the first draw goes to the first change
}

// diffLoaded is the answer to reading a diff.
type diffLoaded struct {
	projectID, tab string
	file           *api.WorktreeFileDiff
	committed      *api.WorktreeDiff
	err            error
}

// diffSeen is what the tab's diff depends on, from what the stream says: a file's
// status, size and time, or a worktree's commits.
func (m *Model) diffSeen(tab string) string {
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
	if l == nil || !isDiffTab(l.active) {
		return nil
	}
	if l.diffs == nil {
		l.diffs = map[string]*diffView{}
	}
	v, ok := l.diffs[l.active]
	if !ok {
		v = &diffView{jump: true}
		l.diffs[l.active] = v
	}
	seen := m.diffSeen(l.active)
	if v.loading || (v.asked && v.seen == seen) {
		return nil
	}
	v.asked, v.loading, v.seen = true, true, seen
	return m.readDiff(l.active)
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
		if file != "" {
			r, err := m.client.WorktreeFileDiff(m.ctx, worktree, file, ref, "")
			msg.file, msg.err = &r, err
		} else {
			r, err := m.client.WorktreeDiff(m.ctx, worktree, ref, "")
			msg.committed, msg.err = &r, err
		}
		return msg
	}
}

func (m *Model) diffLoaded(msg diffLoaded) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.diffs[msg.tab] == nil {
		return
	}
	v := l.diffs[msg.tab]
	v.loading = false
	if msg.err != nil {
		m.log.Info("diff not read", "tab", msg.tab, "err", msg.err)
		v.err = msg.err.Error()
		return
	}
	v.err, v.message, v.heading, v.cutOff = "", "", "", false
	v.split, v.unified = nil, nil
	v.width = 0 // what is drawn is to be worked out again
	if msg.file != nil {
		v.setFile(*msg.file)
	} else {
		v.setCommitted(*msg.committed)
	}
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
	v.lines, v.changes = nil, nil
	switch {
	case v.split != nil:
		v.prepareSplit(width, st)
	case v.unified != nil:
		v.prepareUnified(width, st)
	}
	if v.cutOff {
		v.lines = append(v.lines, st.Warning("too long: the rest is left out"))
	}
	if v.lines == nil {
		v.lines = []string{}
	}
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
	cell := func(no int, content, sign string, paint func(string) string, wrapped []string, i int) string {
		number, mark := strings.Repeat(" ", digits), " "
		if i == 0 && no > 0 {
			number, mark = fmt.Sprintf("%*d", digits, no), sign
		}
		piece := ""
		if i < len(wrapped) {
			piece = wrapped[i]
		}
		pad := strings.Repeat(" ", max(text-ansi.StringWidth(piece), 0))
		return st.Muted(number) + " " + paint(mark+" "+piece) + pad
	}
	inChange := false
	for _, r := range v.split {
		if r.changed && !inChange {
			v.changes = append(v.changes, len(v.lines))
		}
		inChange = r.changed
		lefts, rights := wrapText(r.left, text), wrapText(r.right, text)
		same := func(s string) string { return s }
		removed, added := same, same
		leftSign, rightSign := " ", " "
		if r.changed {
			removed, added, leftSign, rightSign = st.Removed, st.Added, "-", "+"
		}
		for i := range max(len(lefts), len(rights)) {
			v.lines = append(v.lines, cell(r.leftNo, r.left, leftSign, removed, lefts, i)+st.Muted("│")+cell(r.rightNo, r.right, rightSign, added, rights, i))
		}
	}
}

func wrapText(s string, width int) []string {
	if s == "" {
		return nil
	}
	return strings.Split(ansi.Hardwrap(shownText(s), width, true), "\n")
}

// prepareUnified shows a commit's patch as git writes it, each file's start being
// what n and p go to.
func (v *diffView) prepareUnified(width int, st style.Style) {
	for _, line := range v.unified {
		paint := func(s string) string { return s }
		switch {
		case strings.HasPrefix(line, "diff --git "):
			v.changes = append(v.changes, len(v.lines))
			paint = st.Name
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file "), strings.HasPrefix(line, "deleted file "), strings.HasPrefix(line, "@@"):
			paint = st.Muted
		case strings.HasPrefix(line, "+"):
			paint = st.Added
		case strings.HasPrefix(line, "-"):
			paint = st.Removed
		}
		for _, piece := range wrapText(line, width) {
			v.lines = append(v.lines, paint(piece))
		}
		if line == "" {
			v.lines = append(v.lines, "")
		}
	}
}

// drawDiff draws the diff of the tab shown in area, or why there is none.
func (m *Model) drawDiff(buf uv.Screen, area uv.Rectangle, st style.Style) {
	l := m.list()
	v := l.diffs[l.active]
	var top []string
	if v != nil && v.err != "" {
		top = append(top, st.Failure(wrap("the diff could not be read: "+v.err, area.Dx())))
	}
	switch {
	case v == nil || (v.loading && v.lines == nil && v.split == nil && v.unified == nil && v.message == ""):
		top = append(top, st.Muted("loading…"))
	case v.message != "":
		top = append(top, st.Muted(v.message))
	}
	if v == nil || v.split == nil && v.unified == nil {
		uv.NewStyledString(strings.Join(top, "\n")).Draw(buf, area)
		return
	}
	if v.heading != "" {
		top = append(top, st.Muted(ansi.Truncate(v.heading, area.Dx(), "…")))
	}
	if v.split != nil {
		side := (area.Dx() - 1) / 2
		top = append(top, st.Heading(ansi.Truncate(padded(" HEAD", side), side, ""))+st.Muted("│")+st.Heading(ansi.Truncate(" uncommitted", area.Dx()-side-1, "")))
	}
	v.prepare(area.Dx(), st)
	body := max(area.Dy()-len(top), 1)
	v.height = body
	if v.jump {
		v.jump = false
		if len(v.changes) > 0 {
			v.offset = v.changes[0] - jumpContext
		}
	}
	v.offset = max(min(v.offset, len(v.lines)-body), 0)
	shown := append(top, v.lines[v.offset:min(v.offset+body, len(v.lines))]...)
	uv.NewStyledString(strings.Join(shown, "\n")).Draw(buf, area)
}

func padded(s string, width int) string {
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

// diffKey takes a key while the agent pane shows a diff.
func (m *Model) diffKey(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	l := m.list()
	v := l.diffs[l.active]
	if !ok || v == nil {
		return nil
	}
	page := max(v.height-1, 1)
	switch name := key.String(); {
	case slices.Contains(upKeys, name):
		v.scroll(-1)
	case slices.Contains(downKeys, name):
		v.scroll(1)
	case name == "pgup":
		v.scroll(-page)
	case name == "pgdown", name == "space", name == " ":
		v.scroll(page)
	case name == "home", name == "g":
		v.offset = 0
	case name == "end", name == "G":
		v.offset = len(v.lines)
	case name == "n":
		v.nextChange()
	case name == "p":
		v.previousChange()
	}
	return nil
}

func (v *diffView) scroll(by int) { v.offset = max(min(v.offset+by, len(v.lines)-1), 0) }

// nextChange goes to the first change below the top of what is shown, past the
// lines it keeps above the one it goes to.
func (v *diffView) nextChange() {
	for _, c := range v.changes {
		if c-jumpContext > v.offset {
			v.offset = c - jumpContext
			return
		}
	}
}

func (v *diffView) previousChange() {
	for _, c := range slices.Backward(v.changes) {
		if c-jumpContext < v.offset {
			v.offset = max(c-jumpContext, 0)
			return
		}
	}
}

// diffWheel scrolls the diff shown.
func (m *Model) diffWheel(msg tea.MouseWheelMsg) {
	l := m.list()
	v := l.diffs[l.active]
	if v == nil {
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		v.scroll(-diffWheelLines)
	case tea.MouseWheelDown:
		v.scroll(diffWheelLines)
	}
}
