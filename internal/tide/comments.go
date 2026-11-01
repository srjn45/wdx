package tide

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// Comments are written on a diff's lines and sent together as a review, so that an
// agent does not start acting on the first (docs/TIDE_WORKTREES.md). They are kept by
// the daemon until then: no agent may be running.

const (
	commentKey  = "c"
	editKey     = "e"
	deleteKey   = "d"
	reviewKey   = "s"
	commentMark = "✎"
)

// lineAnchor is a line of a file a line of a diff shows, which a comment can be on.
type lineAnchor struct {
	file  string
	line  int
	side  api.WorktreeCommentSide
	quote string
	id    string // the file's content as seen
}

// commentBox asks for a comment's text.
type commentBox struct {
	worktree string
	view     api.WorktreeCommentCreateView
	anchor   lineAnchor
	editing  string // the comment being edited, "" for a new one
	text     string
	sending  bool
	err      string
}

// commentAnswered is the answer to saving or deleting a comment.
type commentAnswered struct{ err error }

// countOf says how many comments n is.
func countOf(n int) string {
	if n == 1 {
		return "1 comment"
	}
	return fmt.Sprintf("%d comments", n)
}

// viewComments are the comments of the worktree on the view shown: one written on the
// other diff has line numbers of its own.
func (m *Model) viewComments(view string) []api.WorktreeComment {
	worktree, file := diffTabOf(view)
	kind := api.WorktreeCommentViewCommitted
	if file != "" || strings.HasPrefix(view, uncommittedPrefix) {
		kind = api.WorktreeCommentViewUncommitted
	}
	var list []api.WorktreeComment
	for _, c := range m.draftComments(worktree) {
		if c.View == kind && (file == "" || c.File == file) {
			list = append(list, c)
		}
	}
	return list
}

// draftComments are the worktree's comments not sent yet.
func (m *Model) draftComments(name string) []api.WorktreeComment {
	i := slices.IndexFunc(m.worktreeStatuses(), func(w api.WorktreeStatus) bool { return w.Name == name })
	if i < 0 {
		return nil
	}
	return m.worktreeStatuses()[i].Comments
}

// addComments adds the lines of the comments on a file's line below it, and has the
// line at anchorLine know the first of them, for e and d.
func (v *diffView) addComments(anchorLine int, a lineAnchor, row, width int, st style.Style) {
	for _, c := range v.comments {
		if c.File != a.file || c.Line != a.line || c.Side != a.side {
			continue
		}
		v.placed++
		if v.commentAt[anchorLine] == "" {
			v.commentAt[anchorLine] = c.Id
		}
		const indent = "    "
		for i, piece := range wrapText(c.Body, max(width-len(indent)-2, 1)) {
			lead := indent + "  "
			if i == 0 {
				lead = indent + st.Warning(commentMark) + " "
			}
			v.add(lead+piece, row, 0)
			v.commentAt[len(v.commentAt)-1] = c.Id
		}
		if c.Changed {
			v.add(indent+"  "+st.Muted("file changed"), row, 0)
			v.commentAt[len(v.commentAt)-1] = c.Id
		}
	}
}

// commentOf returns the comment the current line is, or is on.
func (v *diffView) commentOf() (api.WorktreeComment, bool) {
	if v.cursor >= len(v.commentAt) || v.commentAt[v.cursor] == "" {
		return api.WorktreeComment{}, false
	}
	i := slices.IndexFunc(v.comments, func(c api.WorktreeComment) bool { return c.Id == v.commentAt[v.cursor] })
	if i < 0 {
		return api.WorktreeComment{}, false
	}
	return v.comments[i], true
}

// anchorOf returns the line of a file the current line shows.
func (v *diffView) anchorOf() (lineAnchor, bool) {
	if v.cursor >= len(v.anchors) || v.anchors[v.cursor].line == 0 {
		return lineAnchor{}, false
	}
	return v.anchors[v.cursor], true
}

// commentBarKeys are what the bar says of comments on the view shown.
func (m *Model) commentBarKeys(tab string, v *diffView) []barKey {
	var keys []barKey
	if _, ok := v.anchorOf(); ok {
		keys = append(keys, barKey{commentKey, "comment", 7})
	}
	if _, ok := v.commentOf(); ok {
		keys = append(keys, barKey{editKey, "edit", 6}, barKey{deleteKey, "delete", 6})
	}
	worktree, _ := diffTabOf(tab)
	if n := len(m.draftComments(worktree)); n > 0 {
		keys = append(keys, barKey{reviewKey, fmt.Sprintf("send (%d)", n), 8})
	}
	return keys
}

// askComment opens the box for a comment on the current line.
func (m *Model) askComment(tab string, v *diffView) {
	a, ok := v.anchorOf()
	if !ok {
		v.notice = "not on a line of a file"
		return
	}
	worktree, _ := diffTabOf(tab)
	view := api.WorktreeCommentCreateViewCommitted
	if v.uncommitted || v.split != nil {
		view = api.WorktreeCommentCreateViewUncommitted
	}
	m.comment = &commentBox{worktree: worktree, view: view, anchor: a}
	m.log.Info("comment asked", "worktree", worktree, "file", a.file, "line", a.line)
}

// askEdit opens the box on the comment of the current line.
func (m *Model) askEdit(tab string, v *diffView) {
	c, ok := v.commentOf()
	if !ok {
		return
	}
	worktree, _ := diffTabOf(tab)
	m.comment = &commentBox{worktree: worktree, anchor: lineAnchor{file: c.File, line: c.Line, side: c.Side, quote: c.Quote}, editing: c.Id, text: c.Body}
}

// deleteComment deletes the comment of the current line.
func (m *Model) deleteComment(tab string, v *diffView) tea.Cmd {
	c, ok := v.commentOf()
	f := m.following
	if !ok || f == nil || f.project == nil {
		return nil
	}
	worktree, _ := diffTabOf(tab)
	ref := "id:" + f.project.Id
	return func() tea.Msg { return commentAnswered{m.client.DeleteWorktreeComment(m.ctx, worktree, c.Id, ref)} }
}

// commentBoxKey takes a key while the box is open.
func (m *Model) commentBoxKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.comment
	if c.sending {
		return nil
	}
	switch name := msg.String(); {
	case name == "esc":
		m.comment = nil
	case name == "enter":
		text := strings.TrimSpace(c.text)
		f := m.following
		if text == "" || f == nil || f.project == nil {
			return nil
		}
		c.sending, c.err = true, ""
		ref := "id:" + f.project.Id
		return func() tea.Msg {
			if c.editing != "" {
				return commentAnswered{m.client.EditWorktreeComment(m.ctx, c.worktree, c.editing, ref, text)}
			}
			quote := c.anchor.quote
			return commentAnswered{m.client.AddWorktreeComment(m.ctx, c.worktree, ref, api.WorktreeCommentCreate{
				File: c.anchor.file, Line: c.anchor.line, Side: api.WorktreeCommentCreateSide(c.anchor.side), View: c.view,
				Quote: &quote, Body: text, ContentId: &c.anchor.id,
			})}
		}
	case name == "backspace" && c.text != "":
		r := []rune(c.text)
		c.text = string(r[:len(r)-1])
	case typed(msg):
		c.text += msg.Text
	}
	return nil
}

// commentAnsweredMsg closes the box, or leaves it open with the text and why.
func (m *Model) commentAnsweredMsg(msg commentAnswered) {
	if msg.err != nil {
		m.log.Info("comment not saved", "err", msg.err)
		if m.comment != nil {
			m.comment.sending, m.comment.err = false, msg.err.Error()
			return
		}
		m.statusNotice = msg.err.Error()
		return
	}
	m.comment = nil
}

// commentPopup is what the box shows.
func (m *Model) commentPopup() popup {
	c, st := m.comment, m.st
	title := "Comment"
	if c.editing != "" {
		title = "Edit comment"
	}
	body := []string{fmt.Sprintf("%s, line %d:", c.anchor.file, c.anchor.line)}
	if c.anchor.quote != "" {
		body = append(body, st.Muted(shownText(c.anchor.quote)))
	}
	body = append(body, "", "> "+c.text+"_")
	if c.sending {
		body = append(body, "", st.Muted("saving…"))
	}
	if c.err != "" {
		body = append(body, "", st.Failure(c.err))
	}
	return popup{title: title, body: body, footer: st.Command("Enter") + " save  " + st.Command("Esc") + " cancel"}
}

// askReview opens the box to send the worktree's comments, or says why it cannot.
func (m *Model) askReview(tab string, v *diffView) {
	worktree, _ := diffTabOf(tab)
	n := len(m.draftComments(worktree))
	if n == 0 {
		return
	}
	agents, chosen := m.agentsToAsk(worktree)
	if len(agents) == 0 {
		v.notice = "no agent is running in this project: the comments stay"
		return
	}
	m.commit = &commitBox{worktree: worktree, agents: agents, chosen: chosen, review: n}
	m.log.Info("review asked", "worktree", worktree, "comments", n)
}
