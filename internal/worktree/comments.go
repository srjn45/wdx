package worktree

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/logging"
)

// A comment's state, side and view, as the table has them.
const (
	CommentDraft    = "draft"
	CommentSent     = "sent"
	SideNew         = "new"
	SideOld         = "old"
	ViewUncommitted = "uncommitted"
	ViewCommitted   = "committed"
)

// ErrEmptyComment is a comment with nothing in it.
var ErrEmptyComment = errors.New("a comment needs some text")

// ErrNoComments is a review with nothing to send.
var ErrNoComments = errors.New("the worktree has no comments to send")

// Comment is what a human said of a line of a diff. It is sent with the line as it
// was seen, so that the agent can find it after the line numbers moved.
type Comment struct {
	ID        string
	Worktree  string // the checkout's path
	File      string // relative to it
	Line      int
	Side      string // SideNew, or SideOld for a line removed
	View      string // ViewUncommitted or ViewCommitted
	Quote     string
	ContentID string // the file's content as it was seen
	Body      string
	State     string
	CreatedAt time.Time
	// Changed says the file differs from what was seen; set when a checkout is read.
	Changed bool
}

// Comments keeps the comments.
type Comments struct {
	db *sql.DB
}

// NewComments returns the comments kept in db.
func NewComments(db *sql.DB) *Comments { return &Comments{db: db} }

const commentColumns = `c.id, c.worktree, c.file, c.line, c.side, c.view, c.quote, c.content_id, c.body,
	CASE WHEN m.state = 'delivered' THEN 'delivered' ELSE c.state END, c.created_at`

const commentsFrom = " FROM review_comments c LEFT JOIN messages m ON m.id = c.message_id "

func scanComments(rows *sql.Rows) ([]Comment, error) {
	defer rows.Close()
	var list []Comment
	for rows.Next() {
		var c Comment
		var created int64
		if err := rows.Scan(&c.ID, &c.Worktree, &c.File, &c.Line, &c.Side, &c.View, &c.Quote, &c.ContentID, &c.Body, &c.State, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = time.UnixMilli(created).UTC()
		list = append(list, c)
	}
	return list, rows.Err()
}

// Drafts returns the project's comments not sent yet, by file and line.
func (s *Comments) Drafts(ctx context.Context, projectID string) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+commentColumns+commentsFrom+"WHERE c.project_id = ? AND c.state = ? ORDER BY c.file, c.line, c.created_at", projectID, CommentDraft)
	if err != nil {
		return nil, err
	}
	return scanComments(rows)
}

// List returns the comments of the checkout at path, in every state, by file and
// line.
func (s *Comments) List(ctx context.Context, projectID, path string) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+commentColumns+commentsFrom+"WHERE c.project_id = ? AND c.worktree = ? ORDER BY c.file, c.line, c.created_at", projectID, path)
	if err != nil {
		return nil, err
	}
	return scanComments(rows)
}

// Add saves a new draft of c, in the project.
func (s *Comments) Add(ctx context.Context, projectID string, c Comment) (Comment, error) {
	c.Body = strings.TrimSpace(c.Body)
	if c.Body == "" {
		return Comment{}, ErrEmptyComment
	}
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return Comment{}, err
	}
	c.ID, c.State, c.CreatedAt = "cmt-"+hex.EncodeToString(raw), CommentDraft, time.Now().UTC()
	// Not db.Exec, which logs what it is given: a comment can hold secrets.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO review_comments (id, project_id, worktree, file, line, side, view, quote, content_id, body, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, projectID, c.Worktree, c.File, c.Line, c.Side, c.View, c.Quote, c.ContentID, c.Body, c.State, c.CreatedAt.UnixMilli())
	if err != nil {
		return Comment{}, err
	}
	logging.FromContext(ctx).Info("comment added", "project_id", projectID, "comment_id", c.ID, "file", c.File, "line", c.Line)
	return c, nil
}

// Edit changes the text of a comment not sent yet.
func (s *Comments) Edit(ctx context.Context, projectID, id, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return ErrEmptyComment
	}
	// Not db.Exec, which logs what it is given: a comment can hold secrets.
	res, err := s.db.ExecContext(ctx, "UPDATE review_comments SET body = ? WHERE id = ? AND project_id = ? AND state = ?", body, id, projectID, CommentDraft)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("%w: comment %s", ErrNotFound, id)
	}
	logging.FromContext(ctx).Info("comment edited", "comment_id", id)
	return nil
}

// Remove deletes a comment not sent yet.
func (s *Comments) Remove(ctx context.Context, projectID, id string) error {
	n, err := db.Exec(ctx, s.db, "DELETE FROM review_comments WHERE id = ? AND project_id = ? AND state = ?", id, projectID, CommentDraft)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: comment %s", ErrNotFound, id)
	}
	logging.FromContext(ctx).Info("comment removed", "comment_id", id)
	return nil
}

// RemoveWorktree deletes the comments of a checkout git no longer lists.
func (s *Comments) RemoveWorktree(ctx context.Context, projectID, path string) error {
	_, err := db.Exec(ctx, s.db, "DELETE FROM review_comments WHERE project_id = ? AND worktree = ?", projectID, path)
	return err
}

// MarkSent records that the comments went to the agent.
func (s *Comments) MarkSent(ctx context.Context, projectID string, comments []Comment, agentID, messageID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	for _, c := range comments {
		if _, err := db.Exec(ctx, tx, "UPDATE review_comments SET state = ?, agent_id = ?, sent_at = ?, message_id = ? WHERE id = ? AND project_id = ?", CommentSent, agentID, now, messageID, c.ID, projectID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("review sent", "project_id", projectID, "agent_id", agentID, "comments", len(comments))
	return nil
}

// AddComment saves a comment on a line of the file at path in the checkout called
// name, with the file's content as seen, or as it is now when that is "".
func AddComment(ctx context.Context, store *Comments, projectID string, co Checkouts, name string, c Comment) (Comment, error) {
	s, err := findFile(ctx, co, name, c.File)
	if err != nil {
		return Comment{}, err
	}
	if c.ContentID == "" {
		if id, known, err := contentOf(ctx, s.Path, c.File); err == nil && known {
			c.ContentID = id
		}
	}
	if strings.Trim(c.ContentID, "0") == "" {
		c.ContentID = contentDeleted
	}
	c.Worktree = s.Path
	return store.Add(ctx, projectID, c)
}

// applyComments gives s its drafts, each saying whether its file changed since it
// was seen. A file that cannot be compared is not known to have changed.
func applyComments(ctx context.Context, s *Status, drafts []Comment) {
	s.Comments = nil
	current := map[string]string{}
	for _, c := range drafts {
		if c.Worktree != s.Path {
			continue
		}
		id, seen := current[c.File]
		if !seen {
			var known bool
			var err error
			if id, known, err = contentOf(ctx, s.Path, c.File); err != nil || !known {
				id = c.ContentID
			}
			current[c.File] = id
		}
		c.Changed = id != c.ContentID
		s.Comments = append(s.Comments, c)
	}
}

// ReviewMessage is what an agent gets for a review: where the worktree is, as the
// agent may be working elsewhere, every comment by file, with the line as it was
// seen, and the note first if there is one.
func ReviewMessage(name, path, note string, comments []Comment) string {
	var b strings.Builder
	what := "1 comment"
	if len(comments) != 1 {
		what = fmt.Sprintf("%d comments", len(comments))
	}
	fmt.Fprintf(&b, "Review of %s (%s), %s. Please address them.\n", name, path, what)
	if note = strings.TrimSpace(note); note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	sorted := slices.Clone(comments)
	slices.SortStableFunc(sorted, func(a, b Comment) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	file := ""
	for _, c := range sorted {
		if c.File != file {
			file = c.File
			fmt.Fprintf(&b, "\n%s\n", file)
		}
		place := fmt.Sprintf("line %d", c.Line)
		if c.Side == SideOld {
			place = fmt.Sprintf("removed line %d", c.Line)
		}
		fmt.Fprintf(&b, "  %s (%s):\n", place, c.View)
		if c.Quote != "" {
			fmt.Fprintf(&b, "  > %s\n", c.Quote)
		}
		fmt.Fprintf(&b, "  %s\n", c.Body)
	}
	return b.String()
}
