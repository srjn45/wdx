// Package docsync applies the changes of docs that nodes push to the hub, and
// keeps the docs sealed at rest: each doc's revisions under a data key of its
// own, which the master key seals. Every change is applied once, by the doc's
// id and revision, in a transaction of its own with its row of the audit
// chain, so that a batch sent again after a lost answer does nothing twice.
// Every doc belongs to the user whose node pushed it, and nothing of another
// user's is ever read or changed (the wdx doc HUB_DOCS_PUSH).
package docsync

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/seal"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// MaxContent is the most a revision's text may unpack to: the node's limit.
const MaxContent = 2 << 20

// The refusals of a change. Their messages say what the node did wrong.
var (
	// ErrNotFound is a doc the hub does not have, or another user's.
	ErrNotFound = errors.New("no such doc")
	// ErrRevisionGap is a revision that is not the doc's next: a bug in the
	// node, which sends every revision in order.
	ErrRevisionGap = errors.New("the revision is not the doc's next")
	ErrTooLarge    = errors.New("the content unpacks to more than a doc can hold")
	// ErrNameTaken is a name another of the user's docs of its kind in the
	// repository has.
	ErrNameTaken = errors.New("another doc of its kind in the repository has that name")
	// ErrRevisionConflict is a revision stored already with another text:
	// another node wrote it.
	ErrRevisionConflict = errors.New("the revision is stored with another text")
)

// InvalidError is a change that lacks what its kind needs, or holds what
// cannot be used.
type InvalidError struct {
	Field  string
	Reason string
}

func (e *InvalidError) Error() string { return e.Field + " " + e.Reason }

// The audit actions of the changes.
const (
	ActionCreated = "doc.created"
	ActionRevised = "doc.revised"
	ActionChanged = "doc.changed"
	ActionRemoved = "doc.removed"
)

// Type is what a change does.
type Type string

const (
	TypeRevision Type = "revision" // a new revision, the first making the doc
	TypeMetadata Type = "metadata" // its name, folder, labels, status...
	TypeRemoved  Type = "removed"
)

// Metadata is what a doc is, without its content.
type Metadata struct {
	RepoKey   string // the project's repository key
	Kind      string // doc or plan
	Name      string
	Folder    string
	Format    string
	Labels    []string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Change is one change of a doc as a node pushes it.
type Change struct {
	Seq   int64 // the node's number for it, echoed in its result
	DocID string
	Type  Type
	// Metadata is the doc's as the node has it now; empty for removed.
	Metadata Metadata
	// Revision and Content are a revision's number and its text gzipped.
	Revision int
	Content  []byte
	// ContentSHA256 is the sha256 of the text, in lowercase hex, as the node
	// has it; empty from a node older than it.
	ContentSHA256 string
}

// Result is how a change was answered: applied when Err is nil, refused with
// Err otherwise.
type Result struct {
	Seq int64
	Err error
}

// By is who pushes: the user, through one of their nodes, from an address.
type By struct {
	UserID  string
	NodeID  string
	Address string
}

// Doc is a doc as the hub keeps it, without its content.
type Doc struct {
	ID string
	Metadata
	Revision int // the latest stored
}

// Options configure a Store.
type Options struct {
	// MasterKey seals the docs' data keys: seal.KeySize bytes.
	MasterKey []byte
	// Now is the store's clock; time.Now when nil.
	Now func() time.Time
}

// Store keeps the docs in the hub's database.
type Store struct {
	db  *sql.DB
	key []byte
	now func() time.Time
}

// New returns the store of the docs in db, the hub's database.
func New(db *sql.DB, opts Options) (*Store, error) {
	if len(opts.MasterKey) != seal.KeySize {
		return nil, fmt.Errorf("the master key is %d bytes, not %d", len(opts.MasterKey), seal.KeySize)
	}
	s := &Store{db: db, key: opts.MasterKey, now: opts.Now}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Apply applies each change in order, each in a transaction of its own, and
// returns one result per change. It fails only when the database does: the
// changes applied by then stay, and applying them again does nothing.
func (s *Store) Apply(ctx context.Context, by By, changes []Change) ([]Result, error) {
	log := logging.FromContext(ctx)
	results := make([]Result, 0, len(changes))
	applied := 0
	for _, c := range changes {
		err := s.apply(ctx, by, c)
		if err != nil && !refusal(err) {
			return nil, err
		}
		if err != nil {
			log.Info("doc change refused", "seq", c.Seq, "doc_id", c.DocID, "change", c.Type, "reason", err.Error())
		} else {
			applied++
		}
		results = append(results, Result{Seq: c.Seq, Err: err})
	}
	log.Info("doc changes applied", "user_id", by.UserID, "node_id", by.NodeID, "changes", len(changes), "applied", applied, "refused", len(changes)-applied)
	return results, nil
}

// refusal reports whether err refuses a change, rather than failing the
// database.
func refusal(err error) bool {
	if _, ok := errors.AsType[*InvalidError](err); ok {
		return true
	}
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrRevisionGap) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrNameTaken) || errors.Is(err, ErrRevisionConflict)
}

func (s *Store) apply(ctx context.Context, by By, c Change) error {
	if c.DocID == "" {
		return &InvalidError{Field: "doc_id", Reason: "is empty"}
	}
	switch c.Type {
	case TypeRevision:
		if err := checkMetadata(c.Metadata); err != nil {
			return err
		}
		if c.Revision < 1 {
			return &InvalidError{Field: "revision", Reason: "must be 1 or more"}
		}
		if c.Content == nil {
			return &InvalidError{Field: "content", Reason: "is missing"}
		}
		return s.inTx(ctx, func(tx *sql.Tx) error { return s.revise(ctx, tx, by, c) })
	case TypeMetadata:
		if err := checkMetadata(c.Metadata); err != nil {
			return err
		}
		return s.inTx(ctx, func(tx *sql.Tx) error { return s.change(ctx, tx, by, c) })
	case TypeRemoved:
		return s.inTx(ctx, func(tx *sql.Tx) error { return s.remove(ctx, tx, by, c) })
	}
	return &InvalidError{Field: "change", Reason: "is not revision, metadata or removed"}
}

func checkMetadata(m Metadata) error {
	switch {
	case m.RepoKey == "":
		return &InvalidError{Field: "repo_key", Reason: "is missing"}
	case m.Name == "":
		return &InvalidError{Field: "name", Reason: "is missing"}
	case m.Kind != "doc" && m.Kind != "plan":
		return &InvalidError{Field: "kind", Reason: "is not doc or plan"}
	case m.Status == "":
		return &InvalidError{Field: "status", Reason: "is missing"}
	case m.CreatedAt.IsZero() || m.UpdatedAt.IsZero():
		return &InvalidError{Field: "created_at", Reason: "and updated_at are needed"}
	}
	return nil
}

// stored is a doc's row as the store reads it in a change.
type stored struct {
	Doc
	sealedKey []byte
}

// doc reads the user's doc id in tx; found is false when the user has none
// such.
func (s *Store) doc(ctx context.Context, q sqlite.Querier, userID, id string) (d stored, found bool, err error) {
	var labels string
	var created, updated int64
	err = q.QueryRowContext(ctx, "SELECT repo_key, kind, name, folder, format, labels, status, revision, data_key, created_at, updated_at FROM docs WHERE id = ? AND owner_id = ?", id, userID).
		Scan(&d.RepoKey, &d.Kind, &d.Name, &d.Folder, &d.Format, &labels, &d.Status, &d.Revision, &d.sealedKey, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return stored{}, false, nil
	}
	if err != nil {
		return stored{}, false, err
	}
	if err := json.Unmarshal([]byte(labels), &d.Labels); err != nil {
		return stored{}, false, err
	}
	d.ID, d.CreatedAt, d.UpdatedAt = id, time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return d, true, nil
}

// othersDoc reports whether id is a doc of a user other than userID. It is
// the one query not scoped to the user, since a doc's id is global: it says
// only whether the id is taken, so that such a change is refused as not
// found, as for a doc that does not exist, and nothing of the doc is read.
func othersDoc(ctx context.Context, tx *sql.Tx, userID, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM docs WHERE id = ? AND owner_id != ?", id, userID).Scan(&n)
	return n > 0, err
}

// nameTaken reports whether another of the user's docs of m's kind in m's
// repository is named m's name, whatever its case.
func nameTaken(ctx context.Context, tx *sql.Tx, userID, id string, m Metadata) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM docs WHERE owner_id = ? AND repo_key = ? AND kind = ? AND name = ? AND id != ?", userID, m.RepoKey, m.Kind, m.Name, id).Scan(&n)
	return n > 0, err
}

// revise stores revision c.Revision of the doc when it is the next, making
// the doc at its first, and updates its metadata; a revision stored already
// with the same text, or with no hash, is acknowledged and nothing is
// written, and one stored with another text is refused.
func (s *Store) revise(ctx context.Context, tx *sql.Tx, by By, c Change) error {
	log := logging.FromContext(ctx)
	d, found, err := s.doc(ctx, tx, by.UserID, c.DocID)
	if err != nil {
		return err
	}
	if !found {
		if others, err := othersDoc(ctx, tx, by.UserID, c.DocID); err != nil || others {
			return orRefuse(err, ErrNotFound)
		}
	}
	size, sum, err := unpacked(c.Content)
	if err != nil {
		return err
	}
	if c.ContentSHA256 != "" && c.ContentSHA256 != sum {
		return &InvalidError{Field: "content_sha256", Reason: "is not the sha256 of the content"}
	}
	if found && c.Revision <= d.Revision {
		var stored sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT content_sha256 FROM doc_revisions WHERE doc_id = ? AND revision = ?", c.DocID, c.Revision).Scan(&stored); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if stored.Valid && stored.String != sum {
			return fmt.Errorf("%w: revision %d", ErrRevisionConflict, c.Revision)
		}
		log.Info("doc change acknowledged", "doc_id", c.DocID, "revision", c.Revision, "reason", "stored already", "hashed", stored.Valid)
		return nil
	}
	if c.Revision != d.Revision+1 {
		return fmt.Errorf("%w: revision %d after %d", ErrRevisionGap, c.Revision, d.Revision)
	}
	if taken, err := nameTaken(ctx, tx, by.UserID, c.DocID, c.Metadata); err != nil || taken {
		return orRefuse(err, ErrNameTaken)
	}
	var dataKey []byte
	now := s.now()
	action := ActionRevised
	if found {
		if dataKey, err = seal.Open(s.key, d.sealedKey, []byte(c.DocID)); err != nil {
			return fmt.Errorf("doc %s: data key: %w", c.DocID, err)
		}
		if err := s.update(ctx, tx, by.UserID, c.DocID, c.Metadata, c.Revision); err != nil {
			return err
		}
	} else {
		action = ActionCreated
		dataKey = make([]byte, seal.KeySize)
		_, _ = rand.Read(dataKey) // never fails
		sealedKey, err := seal.Seal(s.key, dataKey, []byte(c.DocID))
		if err != nil {
			return err
		}
		m := c.Metadata
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO docs (id, owner_id, repo_key, kind, name, folder, format, labels, status, revision, data_key, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			c.DocID, by.UserID, m.RepoKey, m.Kind, m.Name, m.Folder, m.Format, labelsJSON(m.Labels), m.Status, c.Revision, secret(sealedKey), ms(m.CreatedAt), ms(m.UpdatedAt)); err != nil {
			return err
		}
	}
	sealed, err := seal.Seal(dataKey, c.Content, revisionData(c.DocID, c.Revision))
	if err != nil {
		return err
	}
	if _, err := sqlite.Exec(ctx, tx, "INSERT INTO doc_revisions (doc_id, revision, content, size, content_sha256, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		c.DocID, c.Revision, secret(sealed), size, sum, ms(now)); err != nil {
		return err
	}
	log.Info("doc revision stored", "doc_id", c.DocID, "revision", c.Revision, "size", size, "created", !found)
	return s.record(ctx, tx, by, action, c.DocID, c.Revision)
}

// change updates the doc's metadata; the same metadata again is acknowledged
// and nothing is written.
func (s *Store) change(ctx context.Context, tx *sql.Tx, by By, c Change) error {
	log := logging.FromContext(ctx)
	d, found, err := s.doc(ctx, tx, by.UserID, c.DocID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if sameMetadata(d.Metadata, c.Metadata) {
		log.Info("doc change acknowledged", "doc_id", c.DocID, "reason", "unchanged")
		return nil
	}
	if taken, err := nameTaken(ctx, tx, by.UserID, c.DocID, c.Metadata); err != nil || taken {
		return orRefuse(err, ErrNameTaken)
	}
	if err := s.update(ctx, tx, by.UserID, c.DocID, c.Metadata, d.Revision); err != nil {
		return err
	}
	log.Info("doc metadata changed", "doc_id", c.DocID, "moved", d.RepoKey != c.Metadata.RepoKey)
	return s.record(ctx, tx, by, ActionChanged, c.DocID, d.Revision)
}

// remove deletes the doc and its revisions; a doc gone already is
// acknowledged.
func (s *Store) remove(ctx context.Context, tx *sql.Tx, by By, c Change) error {
	log := logging.FromContext(ctx)
	d, found, err := s.doc(ctx, tx, by.UserID, c.DocID)
	if err != nil {
		return err
	}
	if !found {
		if others, err := othersDoc(ctx, tx, by.UserID, c.DocID); err != nil || others {
			return orRefuse(err, ErrNotFound)
		}
		log.Info("doc change acknowledged", "doc_id", c.DocID, "reason", "gone already")
		return nil
	}
	if _, err := sqlite.Exec(ctx, tx, "DELETE FROM docs WHERE id = ? AND owner_id = ?", c.DocID, by.UserID); err != nil {
		return err
	}
	log.Info("doc removed", "doc_id", c.DocID, "revisions", d.Revision)
	return s.record(ctx, tx, by, ActionRemoved, c.DocID, d.Revision)
}

func (s *Store) update(ctx context.Context, tx *sql.Tx, userID, id string, m Metadata, revision int) error {
	_, err := sqlite.Exec(ctx, tx, "UPDATE docs SET repo_key = ?, kind = ?, name = ?, folder = ?, format = ?, labels = ?, status = ?, revision = ?, created_at = ?, updated_at = ? WHERE id = ? AND owner_id = ?",
		m.RepoKey, m.Kind, m.Name, m.Folder, m.Format, labelsJSON(m.Labels), m.Status, revision, ms(m.CreatedAt), ms(m.UpdatedAt), id, userID)
	return err
}

func sameMetadata(a, b Metadata) bool {
	return a.RepoKey == b.RepoKey && a.Kind == b.Kind && a.Name == b.Name && a.Folder == b.Folder && a.Format == b.Format &&
		slices.Equal(a.Labels, b.Labels) && a.Status == b.Status && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

// unpacked is the length of the text gzipped holds and its sha256 in hex,
// refused with ErrTooLarge past MaxContent; it reads no further than that.
func unpacked(gzipped []byte) (int, string, error) {
	r, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		return 0, "", &InvalidError{Field: "content", Reason: "is not gzip"}
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, MaxContent+1))
	if err != nil {
		return 0, "", &InvalidError{Field: "content", Reason: "is not gzip"}
	}
	if n > MaxContent {
		return 0, "", ErrTooLarge
	}
	return int(n), hex.EncodeToString(h.Sum(nil)), nil
}

func unpack(gzipped []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(r, MaxContent+1))
}

// revisionData is the additional data a revision is sealed with: its doc's
// id and its number, so that a sealed revision copied to another doc or
// revision does not open.
func revisionData(id string, revision int) []byte {
	return []byte(id + "\x00" + strconv.Itoa(revision))
}

// Doc returns the user's doc id, or ErrNotFound.
func (s *Store) Doc(ctx context.Context, userID, id string) (Doc, error) {
	d, found, err := s.doc(ctx, s.db, userID, id)
	if err != nil {
		return Doc{}, err
	}
	if !found {
		return Doc{}, ErrNotFound
	}
	return d.Doc, nil
}

// Content returns the text of revision of the user's doc id, opened with the
// doc's data key, or ErrNotFound.
func (s *Store) Content(ctx context.Context, userID, id string, revision int) ([]byte, error) {
	var sealedKey, sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT d.data_key, r.content FROM docs d JOIN doc_revisions r ON r.doc_id = d.id WHERE d.id = ? AND d.owner_id = ? AND r.revision = ?", id, userID, revision).
		Scan(&sealedKey, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dataKey, err := seal.Open(s.key, sealedKey, []byte(id))
	if err != nil {
		return nil, fmt.Errorf("doc %s: data key: %w", id, err)
	}
	gzipped, err := seal.Open(dataKey, sealed, revisionData(id, revision))
	if err != nil {
		return nil, fmt.Errorf("doc %s revision %d: %w", id, revision, err)
	}
	return unpack(gzipped)
}

// orRefuse is err when the query failed, refusal otherwise.
func orRefuse(err, refusal error) error {
	if err != nil {
		return err
	}
	return refusal
}

func labelsJSON(labels []string) string {
	if labels == nil {
		return "[]"
	}
	b, _ := json.Marshal(labels) // a []string always marshals
	return string(b)
}

// secret passes sealed bytes to the database, logged as their length.
func secret(v []byte) sqlite.Secret[[]byte] { return sqlite.Secret[[]byte]{V: v} }

func ms(t time.Time) int64 { return t.UnixMilli() }

// inTx runs fn in a transaction, committed when fn returns nil.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// record appends the change to the audit chain in tx: the doc's id and
// revision, never its content.
func (s *Store) record(ctx context.Context, tx *sql.Tx, by By, action, id string, revision int) error {
	return audit.Append(ctx, tx, audit.Entry{
		At: s.now(), UserID: by.UserID, NodeID: by.NodeID, Actor: audit.ActorUser, Action: action, Address: by.Address,
		Details: map[string]any{"doc_id": id, "revision": revision},
	})
}
