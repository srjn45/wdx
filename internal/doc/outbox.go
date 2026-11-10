package doc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// ChangeKind is what a change waiting for the hub is (the wdx doc HUB_DOCS_PUSH).
type ChangeKind string

const (
	ChangeRevision ChangeKind = "revision" // a doc created, or its content updated
	ChangeMetadata ChangeKind = "metadata" // its status, labels, folder or name changed
	ChangeRemoved  ChangeKind = "removed"
)

// ErrNoRemote is returned when a project to sync has no git remote, whose key the
// hub files its docs under.
var ErrNoRemote = errors.New("project has no git remote")

// enqueue writes the change of the doc with id to the hub's outbox in tx, so that
// the change and its row commit together, when the doc's project is synced and the
// row is a doc rather than a plan; revision is the one a ChangeRevision sends. A
// new row of a doc clears its failed ones, which are then tried again.
func enqueue(ctx context.Context, tx *sql.Tx, id string, change ChangeKind, revision int, now int64) error {
	var rev any
	if change == ChangeRevision {
		rev = revision
	}
	n, err := sqlite.Exec(ctx, tx, `
		INSERT INTO hub_outbox (project_id, doc_id, change, revision, created_at)
		SELECT d.project_id, d.id, ?, ?, ? FROM docs d JOIN projects p ON p.id = d.project_id
		WHERE d.id = ? AND p.hub_sync = 1 AND d.kind = 'doc'`, change, rev, now, id)
	if err != nil || n == 0 {
		return err
	}
	cleared, err := sqlite.Exec(ctx, tx, "UPDATE hub_outbox SET failed = NULL WHERE doc_id = ? AND failed IS NOT NULL", id)
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("doc change queued for the hub", "doc_id", id, "change", change, "revision", revision, "failed_cleared", cleared)
	return nil
}

// Outbox is the changes of synced projects' docs waiting for the hub, which the doc
// store writes and the node's link to the hub sends.
type Outbox struct {
	db *sql.DB
}

// NewOutbox returns the Outbox of database.
func NewOutbox(database *sql.DB) *Outbox {
	return &Outbox{db: database}
}

// Outgoing is a change to send to the hub, read when it is sent.
type Outgoing struct {
	Seq       int64
	Change    ChangeKind
	DocID     string
	ProjectID string
	RepoKey   string // the project's origin_key as it is now
	Kind      Kind
	Revision  int    // the revision a ChangeRevision sends
	Content   []byte // that revision's content, gzipped as stored
	// ContentSHA256 is the sha256 of that revision's text, in lowercase hex.
	ContentSHA256 string
	Doc           Doc // the doc's metadata as it is now; empty for ChangeRemoved
}

// SyncStatus is how far a project's docs are on the hub.
type SyncStatus struct {
	Sync         bool
	Pending      int // the changes waiting, failed ones aside
	Failed       []FailedDoc
	LastPushedAt time.Time // zero if never
	RepoKey      string    // empty with no git remote
}

// FailedDoc is a doc the hub refused a change of, which holds up its later ones.
type FailedDoc struct {
	DocID string
	Name  string
	Code  string
}

// SyncOn syncs the project with projectID, every revision of each of its docs
// queued in order, the metadata going with them. It returns false when it was
// synced already; ErrNoRemote when the project has no git remote.
func (o *Outbox) SyncOn(ctx context.Context, projectID string) (bool, error) {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sync bool
	var key sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT hub_sync, origin_key FROM projects WHERE id = ?", projectID).Scan(&sync, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: project %s", ErrNotFound, projectID)
	}
	if err != nil {
		return false, err
	}
	log := logging.FromContext(ctx)
	if !key.Valid {
		log.Info("hub sync refused: no git remote", "project_id", projectID)
		return false, ErrNoRemote
	}
	if sync {
		log.Info("hub sync on already", "project_id", projectID)
		return false, nil
	}
	if _, err := sqlite.Exec(ctx, tx, "UPDATE projects SET hub_sync = 1 WHERE id = ?", projectID); err != nil {
		return false, err
	}
	queued, err := enqueueAll(ctx, tx, projectID, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	log.Info("hub sync on", "project_id", projectID, "queued", queued)
	return true, nil
}

// SyncOff stops syncing the project with projectID, its changes waiting deleted;
// what reached the hub stays there. It returns false when it was not synced.
func (o *Outbox) SyncOff(ctx context.Context, projectID string) (bool, error) {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sync bool
	err = tx.QueryRowContext(ctx, "SELECT hub_sync FROM projects WHERE id = ?", projectID).Scan(&sync)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: project %s", ErrNotFound, projectID)
	}
	if err != nil {
		return false, err
	}
	log := logging.FromContext(ctx)
	if !sync {
		log.Info("hub sync off already", "project_id", projectID)
		return false, nil
	}
	if _, err := sqlite.Exec(ctx, tx, "UPDATE projects SET hub_sync = 0 WHERE id = ?", projectID); err != nil {
		return false, err
	}
	deleted, err := sqlite.Exec(ctx, tx, "DELETE FROM hub_outbox WHERE project_id = ?", projectID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	log.Info("hub sync off", "project_id", projectID, "deleted", deleted)
	return true, nil
}

// EnqueueAll queues every revision of every doc of each synced project, as SyncOn
// does for one, for a hub that may have none of them. What was waiting goes, but
// the removals: a revision queued earlier would reach the hub before the ones it
// follows, while the docs' every revision is queued after.
func (o *Outbox) EnqueueAll(ctx context.Context) error {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	replaced, err := sqlite.Exec(ctx, tx, "DELETE FROM hub_outbox WHERE change <> 'removed'")
	if err != nil {
		return err
	}
	queued, err := enqueueAll(ctx, tx, "", time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("hub outbox filled", "queued", queued, "replaced", replaced)
	return nil
}

// enqueueAll queues every revision of each doc of the synced project with
// projectID, or of every synced project when it is empty, a doc's revisions in
// order. A revision row carries the doc's metadata, so none of those is queued.
func enqueueAll(ctx context.Context, tx *sql.Tx, projectID string, now int64) (int64, error) {
	return sqlite.Exec(ctx, tx, `
		INSERT INTO hub_outbox (project_id, doc_id, change, revision, created_at)
		SELECT d.project_id, d.id, 'revision', r.revision, ?
		FROM doc_revisions r
		JOIN docs d ON d.id = r.doc_id
		JOIN projects p ON p.id = d.project_id
		WHERE p.hub_sync = 1 AND d.kind = 'doc' AND (?2 = '' OR p.id = ?2)
		ORDER BY d.created_at, d.id, r.revision`, now, projectID)
}

// Clear deletes every change waiting, as one meant for a hub logged out of must
// never reach another.
func (o *Outbox) Clear(ctx context.Context) error {
	n, err := sqlite.Exec(ctx, o.db, "DELETE FROM hub_outbox")
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("hub outbox cleared", "deleted", n)
	return nil
}

// Next returns up to limit of the oldest changes waiting, leaving out every change
// of a doc the hub refused one of, so that none reaches it out of order. Before,
// it deletes what need not be sent: the changes of a doc since removed but its
// removed one, and a metadata change followed by another of its doc, as each is
// sent with the doc's metadata as it is then.
func (o *Outbox) Next(ctx context.Context, limit int) ([]Outgoing, error) {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	gone, err := sqlite.Exec(ctx, tx, `
		DELETE FROM hub_outbox WHERE change <> 'removed'
		AND NOT EXISTS (SELECT 1 FROM docs WHERE docs.id = hub_outbox.doc_id)`)
	if err != nil {
		return nil, err
	}
	merged, err := sqlite.Exec(ctx, tx, `
		DELETE FROM hub_outbox WHERE change = 'metadata' AND failed IS NULL
		AND (SELECT n.change FROM hub_outbox n WHERE n.doc_id = hub_outbox.doc_id AND n.seq > hub_outbox.seq
			AND n.failed IS NULL ORDER BY n.seq LIMIT 1) = 'metadata'`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT o.seq, o.change, o.doc_id, o.project_id, coalesce(p.origin_key, ''), coalesce(o.revision, 0), r.content,
			d.id IS NOT NULL, coalesce(d.kind, 'doc'), coalesce(d.name, ''), coalesce(d.folder, ''), coalesce(d.format, ''),
			coalesce(d.labels, '[]'), coalesce(d.status, ''), coalesce(d.revision, 0), coalesce(cr.size, 0),
			coalesce(d.created_at, 0), coalesce(d.updated_at, 0), p.name
		FROM hub_outbox o
		JOIN projects p ON p.id = o.project_id
		LEFT JOIN docs d ON d.id = o.doc_id
		LEFT JOIN doc_revisions r ON o.change = 'revision' AND r.doc_id = o.doc_id AND r.revision = o.revision
		LEFT JOIN doc_revisions cr ON cr.doc_id = d.id AND cr.revision = d.revision
		WHERE o.failed IS NULL
		AND o.doc_id NOT IN (SELECT doc_id FROM hub_outbox WHERE failed IS NOT NULL)
		ORDER BY o.seq LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var list []Outgoing
	for rows.Next() {
		var c Outgoing
		var found bool
		var labels, project string
		var created, updated int64
		if err := rows.Scan(&c.Seq, &c.Change, &c.DocID, &c.ProjectID, &c.RepoKey, &c.Revision, &c.Content,
			&found, &c.Kind, &c.Doc.Name, &c.Doc.Folder, &c.Doc.Format, &labels, &c.Doc.Status, &c.Doc.Revision, &c.Doc.Size,
			&created, &updated, &project); err != nil {
			rows.Close()
			return nil, err
		}
		if found {
			if err := json.Unmarshal([]byte(labels), &c.Doc.Labels); err != nil {
				rows.Close()
				return nil, fmt.Errorf("labels of doc %s: %w", c.DocID, err)
			}
			c.Doc.ID, c.Doc.ProjectID, c.Doc.ProjectName = c.DocID, c.ProjectID, project
			c.Doc.CreatedAt, c.Doc.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
		}
		if c.Content != nil {
			text, err := unpack(c.Content)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("revision %d of doc %s: %w", c.Revision, c.DocID, err)
			}
			sum := sha256.Sum256(text)
			c.ContentSHA256 = hex.EncodeToString(sum[:])
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Debug("hub outbox read", "changes", len(list), "gone", gone, "merged", merged)
	return list, nil
}

// Done deletes the changes with seqs, which the hub applied, and marks their
// projects pushed at at.
func (o *Outbox) Done(ctx context.Context, seqs []int64, at time.Time) error {
	if len(seqs) == 0 {
		return nil
	}
	list, err := json.Marshal(seqs)
	if err != nil {
		return err
	}
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := sqlite.Exec(ctx, tx, `UPDATE projects SET hub_pushed_at = ?
		WHERE id IN (SELECT project_id FROM hub_outbox WHERE seq IN (SELECT value FROM json_each(?)))`, at.UnixMilli(), string(list)); err != nil {
		return err
	}
	n, err := sqlite.Exec(ctx, tx, "DELETE FROM hub_outbox WHERE seq IN (SELECT value FROM json_each(?))", string(list))
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("hub changes done", "changes", len(seqs), "deleted", n)
	return nil
}

// Fail marks the change with seq refused by the hub with code, which holds up its
// doc's later changes until a new one of it.
func (o *Outbox) Fail(ctx context.Context, seq int64, code string) error {
	n, err := sqlite.Exec(ctx, o.db, "UPDATE hub_outbox SET failed = ? WHERE seq = ?", code, seq)
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("hub change failed", "seq", seq, "code", code, "found", n == 1)
	return nil
}

// Status returns how far the docs of the project with projectID are on the hub.
// Its two reads are one transaction, so that a change the pusher marks failed
// between them is not counted both pending and failed.
func (o *Outbox) Status(ctx context.Context, projectID string) (SyncStatus, error) {
	tx, err := o.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SyncStatus{}, err
	}
	defer tx.Rollback()
	var st SyncStatus
	var pushed sql.NullInt64
	var key sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT p.hub_sync, p.hub_pushed_at, p.origin_key,
			(SELECT count(*) FROM hub_outbox o WHERE o.project_id = p.id AND o.failed IS NULL)
		FROM projects p WHERE p.id = ?`, projectID).Scan(&st.Sync, &pushed, &key, &st.Pending)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncStatus{}, fmt.Errorf("%w: project %s", ErrNotFound, projectID)
	}
	if err != nil {
		return SyncStatus{}, err
	}
	st.RepoKey = key.String
	if pushed.Valid {
		st.LastPushedAt = time.UnixMilli(pushed.Int64).UTC()
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT o.doc_id, coalesce(d.name, ''), o.failed FROM hub_outbox o
		LEFT JOIN docs d ON d.id = o.doc_id
		WHERE o.project_id = ? AND o.failed IS NOT NULL
		AND o.seq = (SELECT min(f.seq) FROM hub_outbox f WHERE f.doc_id = o.doc_id AND f.failed IS NOT NULL)
		ORDER BY o.seq`, projectID)
	if err != nil {
		return SyncStatus{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var f FailedDoc
		if err := rows.Scan(&f.DocID, &f.Name, &f.Code); err != nil {
			return SyncStatus{}, err
		}
		st.Failed = append(st.Failed, f)
	}
	return st, rows.Err()
}
