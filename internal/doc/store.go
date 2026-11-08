package doc

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Store keeps docs of one kind and their revisions, and a read-only copy of each
// one's latest revision under its directory. It never reads or changes a row of
// another kind.
type Store struct {
	db      *sql.DB
	kind    Kind
	copies  copies
	changed func() // called after every change; see OnChange
}

// NewStore returns a Store of docs backed by database, keeping their copies in dir.
func NewStore(database *sql.DB, dir string) *Store {
	return NewKindStore(database, dir, KindDoc)
}

// NewKindStore returns a Store of the kind backed by database, keeping their copies
// in dir.
func NewKindStore(database *sql.DB, dir string, kind Kind) *Store {
	return &Store{db: database, kind: kind, copies: copies{dir: dir}, changed: func() {}}
}

// OnChange makes the store call fn after every change it writes, before it is
// used: a doc created, changed, renamed or removed.
func (s *Store) OnChange(fn func()) { s.changed = fn }

// New is what a doc is created with. An empty Status is StatusWIP, an empty Folder
// TopFolder. Children are the ids of rows of the store's kind it lists, as a plan
// of plans does: their ParentID becomes its id, and their folder its folder.
type New struct {
	Name     string
	Folder   string
	Format   string
	Labels   []string
	Status   Status
	Content  []byte
	Children []string
}

// Create adds a doc to the project with projectID, its content as revision 1. A
// name the project has returns a *NameTakenError.
func (s *Store) Create(ctx context.Context, projectID string, n New) (Doc, error) {
	if n.Status == "" {
		n.Status = StatusWIP
	}
	labels, err := cleanLabels(n.Labels)
	if err != nil {
		return Doc{}, err
	}
	folder, err := CleanFolder(n.Folder)
	if err != nil {
		return Doc{}, err
	}
	for _, err := range []error{s.kind.checkName(n.Name), checkFormat(n.Format), checkStatus(n.Status), checkContent(n.Content)} {
		if err != nil {
			return Doc{}, err
		}
	}
	id, err := sqlite.NewID("doc")
	if err != nil {
		return Doc{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Doc{}, err
	}
	defer tx.Rollback()
	if err := s.checkFree(ctx, tx, projectID, n.Name, ""); err != nil {
		return Doc{}, err
	}
	now := time.Now().UnixMilli()
	if _, err := sqlite.Exec(ctx, tx, `
		INSERT INTO docs (id, project_id, kind, name, folder, format, labels, status, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, id, projectID, s.kind, s.kind.stored(n.Name), folder, n.Format, labelsJSON(labels), n.Status, now, now); err != nil {
		return Doc{}, err
	}
	if err := insertRevision(ctx, tx, id, 1, n.Content, now); err != nil {
		return Doc{}, err
	}
	if err := s.setChildren(ctx, tx, id, folder, n.Children, now); err != nil {
		return Doc{}, err
	}
	if err := tx.Commit(); err != nil {
		return Doc{}, err
	}
	logging.FromContext(ctx).Info("doc created", "doc_id", id, "kind", s.kind, "name", n.Name, "project_id", projectID, "folder", folder, "status", n.Status, "size", len(n.Content))
	s.changed()
	d, err := s.Get(ctx, id)
	if err != nil {
		return Doc{}, err
	}
	s.copies.write(ctx, d, n.Content, time.UnixMilli(now))
	return d, nil
}

// Get returns the doc with id.
func (s *Store) Get(ctx context.Context, id string) (Doc, error) {
	return s.one(ctx, s.db, "d.id = ?", id)
}

// GetByName returns the doc named name, ignoring case, in the project with
// projectID.
func (s *Store) GetByName(ctx context.Context, projectID, name string) (Doc, error) {
	return s.one(ctx, s.db, "d.project_id = ? AND d.name = ?", projectID, s.kind.stored(name))
}

// List returns the docs of the project with projectID by folder, then name: those with every
// one of labels, and in status unless it is empty. A label no doc could have is an
// *InvalidError rather than matching none, so that a capital is not taken for a
// label nothing has.
func (s *Store) List(ctx context.Context, projectID string, labels []string, status Status) ([]Doc, error) {
	labels, err := cleanLabels(labels)
	if err != nil {
		return nil, err
	}
	if status != "" {
		if err := checkStatus(status); err != nil {
			return nil, err
		}
	}
	return s.query(ctx, s.db, `d.project_id = ?
		AND (?2 = '' OR d.status = ?2)
		AND NOT EXISTS (SELECT 1 FROM json_each(?3) w WHERE w.value NOT IN (SELECT value FROM json_each(d.labels)))`,
		projectID, status, labelsJSON(labels))
}

// Content returns the text of the doc with id at revision, the latest when it is
// 0, and that revision.
func (s *Store) Content(ctx context.Context, id string, revision int) ([]byte, Revision, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, Revision{}, err
	}
	if revision == 0 {
		revision = d.Revision
	}
	var packed []byte
	var created int64
	r := Revision{Number: revision}
	err = s.db.QueryRowContext(ctx, "SELECT content, size, created_at FROM doc_revisions WHERE doc_id = ? AND revision = ?", id, revision).Scan(&packed, &r.Size, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Revision{}, fmt.Errorf("%w: %s %s has no revision %d, its latest is %d", ErrNotFound, s.kind, d.Name, revision, d.Revision)
	}
	if err != nil {
		return nil, Revision{}, err
	}
	content, err := unpack(packed)
	if err != nil {
		return nil, Revision{}, fmt.Errorf("revision %d of doc %s: %w", revision, d.Name, err)
	}
	r.CreatedAt = time.UnixMilli(created).UTC()
	return content, r, nil
}

// Revisions returns the revisions of the doc with id, the latest first.
func (s *Store) Revisions(ctx context.Context, id string) ([]Revision, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT revision, size, created_at FROM doc_revisions WHERE doc_id = ? ORDER BY revision DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Revision
	for rows.Next() {
		var r Revision
		var created int64
		if err := rows.Scan(&r.Number, &r.Size, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = time.UnixMilli(created).UTC()
		list = append(list, r)
	}
	return list, rows.Err()
}

// Updated is what Update did.
type Updated struct {
	Doc      Doc
	Previous Status // the status before; a new revision sets StatusWIP
	Changed  bool   // false when the content was the latest revision's
}

// Update makes content the doc's next revision, if base is still its latest:
// otherwise it returns a *ChangedError, and the caller reads the doc again. A new
// revision sets the status to StatusWIP, as what was agreed is not the new text.
// Content equal to the latest revision's changes nothing.
func (s *Store) Update(ctx context.Context, id string, base int, content []byte) (Updated, error) {
	return s.update(ctx, id, base, content, nil, false)
}

// UpdateWithChildren is Update for a row that lists others, as New's Children: the
// rows in children are its children from the new revision on, in its folder, and
// those it listed before and no longer does are no one's, keeping their folder. Content equal to the latest revision's
// changes nothing, its children included, as they come from the content.
func (s *Store) UpdateWithChildren(ctx context.Context, id string, base int, content []byte, children []string) (Updated, error) {
	return s.update(ctx, id, base, content, children, true)
}

func (s *Store) update(ctx context.Context, id string, base int, content []byte, children []string, withChildren bool) (Updated, error) {
	if err := checkContent(content); err != nil {
		return Updated{}, err
	}
	log := logging.FromContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Updated{}, err
	}
	defer tx.Rollback()
	d, err := s.one(ctx, tx, "d.id = ?", id)
	if err != nil {
		return Updated{}, err
	}
	if d.Revision != base {
		log.Info("doc changed since its base", "doc_id", id, "name", d.Name, "base", base, "revision", d.Revision)
		return Updated{}, &ChangedError{Doc: d, Base: base}
	}
	var packed []byte
	if err := tx.QueryRowContext(ctx, "SELECT content FROM doc_revisions WHERE doc_id = ? AND revision = ?", id, d.Revision).Scan(&packed); err != nil {
		return Updated{}, err
	}
	latest, err := unpack(packed)
	if err != nil {
		return Updated{}, fmt.Errorf("revision %d of doc %s: %w", d.Revision, d.Name, err)
	}
	if bytes.Equal(latest, content) {
		log.Info("doc unchanged", "doc_id", id, "name", d.Name, "revision", d.Revision)
		return Updated{Doc: d, Previous: d.Status}, nil
	}
	next, now := d.Revision+1, time.Now().UnixMilli()
	if _, err := sqlite.Exec(ctx, tx, "UPDATE docs SET revision = ?, status = ?, updated_at = ? WHERE id = ?", next, StatusWIP, now, id); err != nil {
		return Updated{}, err
	}
	if err := insertRevision(ctx, tx, id, next, content, now); err != nil {
		return Updated{}, err
	}
	if withChildren {
		if _, err := sqlite.Exec(ctx, tx, "UPDATE docs SET parent_id = NULL WHERE parent_id = ?", id); err != nil {
			return Updated{}, err
		}
		if err := s.setChildren(ctx, tx, id, d.Folder, children, now); err != nil {
			return Updated{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Updated{}, err
	}
	log.Info("doc updated", "doc_id", id, "kind", s.kind, "name", d.Name, "project_id", d.ProjectID, "revision", next, "size", len(content), "previous_status", d.Status)
	s.changed()
	updated, err := s.Get(ctx, id)
	if err != nil {
		return Updated{}, err
	}
	s.copies.write(ctx, updated, content, time.UnixMilli(now))
	return Updated{Doc: updated, Previous: d.Status, Changed: true}, nil
}

// Change is what Set changes: the folder unless it is nil, the status unless it is
// empty, and labels added and removed. Removing a label the doc does not have is no
// error; adding and removing the same one is.
type Change struct {
	Folder       *string
	Status       Status
	AddLabels    []string
	RemoveLabels []string
}

// Set changes the doc with id as c says, never its content: it needs no base, as
// two such changes cannot lose text. It returns the doc, and whether anything
// changed. A folder changes for the doc's whole family, as Move says.
func (s *Store) Set(ctx context.Context, id string, c Change) (Doc, bool, error) {
	d, changed, _, err := s.set(ctx, id, c)
	return d, changed, err
}

// Move puts the doc with id in folder, with its family: the row that lists it and
// every row that one lists, or every row it lists itself, as a plan of plans and
// its plans share one folder. It returns the doc, whether its folder changed, and
// the names of the others whose folder did.
func (s *Store) Move(ctx context.Context, id, folder string) (Doc, bool, []string, error) {
	return s.set(ctx, id, Change{Folder: &folder})
}

func (s *Store) set(ctx context.Context, id string, c Change) (Doc, bool, []string, error) {
	if c.Status != "" {
		if err := checkStatus(c.Status); err != nil {
			return Doc{}, false, nil, err
		}
	}
	if _, err := cleanLabels(c.AddLabels); err != nil {
		return Doc{}, false, nil, err
	}
	for _, l := range c.AddLabels {
		if slices.Contains(c.RemoveLabels, l) {
			return Doc{}, false, nil, &InvalidError{What: "labels", Reason: fmt.Sprintf("%q is both added and removed", l)}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Doc{}, false, nil, err
	}
	defer tx.Rollback()
	d, err := s.one(ctx, tx, "d.id = ?", id)
	if err != nil {
		return Doc{}, false, nil, err
	}
	folder := d.Folder
	if c.Folder != nil {
		if folder, err = CleanFolder(*c.Folder); err != nil {
			return Doc{}, false, nil, err
		}
	}
	status := d.Status
	if c.Status != "" {
		status = c.Status
	}
	kept := slices.DeleteFunc(slices.Clone(d.Labels), func(l string) bool { return slices.Contains(c.RemoveLabels, l) })
	labels, err := cleanLabels(append(kept, c.AddLabels...))
	if err != nil {
		return Doc{}, false, nil, err
	}
	log := logging.FromContext(ctx)
	if folder == d.Folder && status == d.Status && slices.Equal(labels, d.Labels) {
		log.Info("doc unchanged", "doc_id", id, "name", d.Name)
		return d, false, nil, nil
	}
	now := time.Now().UnixMilli()
	var moved []string
	if folder != d.Folder {
		if moved, err = s.moveFamily(ctx, tx, d, folder, now); err != nil {
			return Doc{}, false, nil, err
		}
	}
	if _, err := sqlite.Exec(ctx, tx, "UPDATE docs SET folder = ?, status = ?, labels = ?, updated_at = ? WHERE id = ?", folder, status, labelsJSON(labels), now, id); err != nil {
		return Doc{}, false, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Doc{}, false, nil, err
	}
	log.Info("doc set", "doc_id", id, "name", d.Name, "folder", folder, "status", status, "labels", labels, "moved", len(moved))
	s.changed()
	d, err = s.Get(ctx, id)
	return d, true, moved, err
}

// moveFamily puts the rest of d's family in folder: the row listing d and the
// others it lists, or the rows d lists. It returns the names of those it moved.
func (s *Store) moveFamily(ctx context.Context, tx *sql.Tx, d Doc, folder string, now int64) ([]string, error) {
	root := d.ID
	if d.ParentID != "" {
		root = d.ParentID
	}
	family, err := s.query(ctx, tx, "(d.id = ? OR d.parent_id = ?) AND d.id <> ? AND d.folder <> ?", root, root, d.ID, folder)
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, f := range family {
		if _, err := sqlite.Exec(ctx, tx, "UPDATE docs SET folder = ?, updated_at = ? WHERE id = ?", folder, now, f.ID); err != nil {
			return nil, err
		}
		moved = append(moved, f.Name)
	}
	return moved, nil
}

// Rename gives the doc with id the name name. A name another doc in its project
// has returns a *NameTakenError; the doc's own name in another case is a rename.
func (s *Store) Rename(ctx context.Context, id, name string) (Doc, error) {
	if err := s.kind.checkName(name); err != nil {
		return Doc{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Doc{}, err
	}
	defer tx.Rollback()
	d, err := s.one(ctx, tx, "d.id = ?", id)
	if err != nil {
		return Doc{}, err
	}
	if err := s.checkFree(ctx, tx, d.ProjectID, name, id); err != nil {
		return Doc{}, err
	}
	if _, err := sqlite.Exec(ctx, tx, "UPDATE docs SET name = ?, updated_at = ? WHERE id = ?", s.kind.stored(name), time.Now().UnixMilli(), id); err != nil {
		return Doc{}, err
	}
	if err := tx.Commit(); err != nil {
		return Doc{}, err
	}
	logging.FromContext(ctx).Info("doc renamed", "doc_id", id, "kind", s.kind, "name", name, "old_name", d.Name)
	s.copies.rename(ctx, d, name)
	s.changed()
	return s.Get(ctx, id)
}

// Remove deletes the doc with id and its revisions, and returns what it was.
func (s *Store) Remove(ctx context.Context, id string) (Doc, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return Doc{}, err
	}
	n, err := sqlite.Exec(ctx, s.db, "DELETE FROM docs WHERE id = ?", id)
	if err != nil {
		return Doc{}, err
	}
	if n == 0 {
		return Doc{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	logging.FromContext(ctx).Info("doc removed", "doc_id", id, "kind", s.kind, "name", d.Name, "project_id", d.ProjectID, "revisions", d.Revision)
	s.copies.remove(ctx, d)
	s.changed()
	return d, nil
}

// RemoveProjectCopies deletes the copies of the project named projectName, once
// the project is removed: its docs' rows went with its own.
func (s *Store) RemoveProjectCopies(ctx context.Context, projectName string) {
	s.copies.removeProject(ctx, projectName)
}

// CatchUpCopies writes every copy that is missing or older than its doc's latest
// revision, as one a crash kept from being written; the daemon calls it on start.
// It never deletes one: were the database lost or replaced, the copies are what is
// left of the docs.
func (s *Store) CatchUpCopies(ctx context.Context) error {
	docs, err := s.query(ctx, s.db, "1")
	if err != nil {
		return err
	}
	written := 0
	for _, d := range docs {
		content, r, err := s.Content(ctx, d.ID, d.Revision)
		if err != nil {
			logging.FromContext(ctx).Error("doc copy not caught up", "doc_id", d.ID, "err", err)
			continue
		}
		if current(s.copies.path(d.ProjectName, d.Name, d.Format), r.CreatedAt) {
			continue
		}
		s.copies.write(ctx, d, content, r.CreatedAt)
		written++
	}
	logging.FromContext(ctx).Info("doc copies caught up", "docs", len(docs), "written", written)
	return nil
}

// setChildren makes the rows in children the children of the one with id, in its
// folder; a child moved there is updated at now. A child that is not there is an
// error: the caller checked them.
func (s *Store) setChildren(ctx context.Context, tx *sql.Tx, id, folder string, children []string, now int64) error {
	for _, child := range children {
		n, err := sqlite.Exec(ctx, tx, `UPDATE docs SET parent_id = ?, folder = ?,
			updated_at = CASE WHEN folder = ? THEN updated_at ELSE ? END WHERE id = ?`, id, folder, folder, now, child)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: child %s of %s", ErrNotFound, child, id)
		}
	}
	return nil
}

// checkFree returns a *NameTakenError if a doc of the project other than self is
// named name. Only the store's kind need be looked at: the stored names of two
// kinds never match.
func (s *Store) checkFree(ctx context.Context, q queryer, projectID, name, self string) error {
	other, err := s.one(ctx, q, "d.project_id = ? AND d.name = ? AND d.id <> ?", projectID, s.kind.stored(name), self)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("name taken", "name", name, "doc_id", other.ID)
	return &NameTakenError{Name: name, Doc: other}
}

// queryer reads rows: the database, or a transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) one(ctx context.Context, q queryer, where string, args ...any) (Doc, error) {
	list, err := s.query(ctx, q, where, args...)
	if err != nil {
		return Doc{}, err
	}
	if len(list) == 0 {
		return Doc{}, ErrNotFound
	}
	return list[0], nil
}

// query returns the store's kind of docs that match where. The kind's ? comes last,
// so that a where numbering its own (?2) keeps its numbers.
func (s *Store) query(ctx context.Context, q queryer, where string, args ...any) ([]Doc, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT d.id, d.project_id, p.name, d.name, d.folder, d.format, d.labels, d.status, d.revision, r.size, d.created_at, d.updated_at, coalesce(d.parent_id, '')
		FROM docs d
		JOIN projects p ON p.id = d.project_id
		JOIN doc_revisions r ON r.doc_id = d.id AND r.revision = d.revision
		WHERE (`+where+`) AND d.kind = ? ORDER BY d.folder, d.name`, append(args, s.kind)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Doc
	for rows.Next() {
		var d Doc
		var labels string
		var created, updated int64
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ProjectName, &d.Name, &d.Folder, &d.Format, &labels, &d.Status, &d.Revision, &d.Size, &created, &updated, &d.ParentID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(labels), &d.Labels); err != nil {
			return nil, fmt.Errorf("labels of doc %s: %w", d.ID, err)
		}
		d.Name = s.kind.shown(d.Name)
		d.CreatedAt, d.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
		list = append(list, d)
	}
	return list, rows.Err()
}

// insertRevision stores content as revision of the doc with id. Not sqlite.Exec, which
// logs its arguments: the content can be large.
func insertRevision(ctx context.Context, tx *sql.Tx, id string, revision int, content []byte, now int64) error {
	packed, err := pack(content)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO doc_revisions (doc_id, revision, content, size, created_at) VALUES (?, ?, ?, ?, ?)", id, revision, packed, len(content), now)
	return err
}

func labelsJSON(labels []string) string {
	if len(labels) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(labels) // a []string always marshals
	return string(b)
}

func pack(content []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(content); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unpack(packed []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(r, MaxContent+1))
}
