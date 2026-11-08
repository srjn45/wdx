package docsync

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/seal"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

const (
	alice = "usr-alice"
	bob   = "usr-bob"
	repo  = "github.com/acme/app"
)

type fixture struct {
	*Store
	ctx context.Context
	db  *sql.DB
	log *bytes.Buffer
	key []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.db")
	sqlitetest.Seed(t, path, hubdb.Open)
	d, err := hubdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for _, id := range []string{alice, bob} {
		if _, err := d.Exec("INSERT INTO users (id, username, created_at) VALUES (?, ?, 0)", id, id); err != nil {
			t.Fatal(err)
		}
	}
	key := make([]byte, seal.KeySize)
	_, _ = rand.Read(key)
	s, err := New(d, Options{MasterKey: key})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	ctx := logging.NewContext(context.Background(), slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &fixture{Store: s, ctx: ctx, db: d, log: &buf, key: key}
}

func gz(t *testing.T, text string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var when = time.UnixMilli(1_700_000_000_000).UTC()

func meta(name string) Metadata {
	return Metadata{RepoKey: repo, Kind: "doc", Name: name, Folder: "/", Format: "md", Labels: []string{"hub"}, Status: "wip", CreatedAt: when, UpdatedAt: when}
}

func revision(t *testing.T, seq int64, id string, n int, name, text string) Change {
	t.Helper()
	return Change{Seq: seq, DocID: id, Type: TypeRevision, Metadata: meta(name), Revision: n, Content: gz(t, text)}
}

// apply applies changes as user's node and returns their errors.
func (f *fixture) apply(t *testing.T, user string, changes ...Change) []error {
	t.Helper()
	results, err := f.Apply(f.ctx, By{UserID: user, NodeID: "node-1", Address: "192.0.2.1"}, changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(changes) {
		t.Fatalf("%d results for %d changes", len(results), len(changes))
	}
	errs := make([]error, len(results))
	for i, r := range results {
		if r.Seq != changes[i].Seq {
			t.Fatalf("result %d is of seq %d, not %d", i, r.Seq, changes[i].Seq)
		}
		errs[i] = r.Err
	}
	return errs
}

func wantApplied(t *testing.T, what string, errs []error) {
	t.Helper()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%s: change %d refused: %v", what, i, err)
		}
	}
}

func wantRefused(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// actions are the audit chain's actions of docs, in order.
func (f *fixture) actions(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query("SELECT action FROM audit WHERE action LIKE 'doc.%' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return list
}

func (f *fixture) wantContent(t *testing.T, user, id string, n int, want string) {
	t.Helper()
	got, err := f.Content(f.ctx, user, id, n)
	if err != nil || string(got) != want {
		t.Fatalf("content of %s revision %d: %q, %v; want %q", id, n, got, err, want)
	}
}

func TestEachChangeAppliedOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	renamed := meta("RENAMED")
	renamed.Status, renamed.UpdatedAt = "review", when.Add(time.Minute)
	batch := []Change{
		revision(t, 1, "doc-1", 1, "PLAN", "first"),
		revision(t, 2, "doc-1", 2, "PLAN", "second"),
		{Seq: 3, DocID: "doc-1", Type: TypeMetadata, Metadata: renamed},
	}
	wantApplied(t, "the batch", f.apply(t, alice, batch...))
	want := []string{ActionCreated, ActionRevised, ActionChanged}
	if got := f.actions(t); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("audit %v, want %v", got, want)
	}
	// The answer was lost: the node sends the batch again.
	wantApplied(t, "the batch again", f.apply(t, alice, batch...))
	if got := f.actions(t); len(got) != len(want) {
		t.Fatalf("the batch again wrote %v", got[len(want):])
	}
	if n := f.count(t, "SELECT COUNT(*) FROM doc_revisions WHERE doc_id = 'doc-1'"); n != 2 {
		t.Fatalf("%d revisions stored", n)
	}
	d, err := f.Doc(f.ctx, alice, "doc-1")
	if err != nil || d.Name != "RENAMED" || d.Status != "review" || d.Revision != 2 || !d.UpdatedAt.Equal(renamed.UpdatedAt) || len(d.Labels) != 1 {
		t.Fatalf("doc %+v, %v", d, err)
	}
	f.wantContent(t, alice, "doc-1", 1, "first")
	f.wantContent(t, alice, "doc-1", 2, "second")

	removed := Change{Seq: 4, DocID: "doc-1", Type: TypeRemoved}
	wantApplied(t, "removed", f.apply(t, alice, removed))
	wantApplied(t, "removed again", f.apply(t, alice, removed))
	if got := f.actions(t); len(got) != 4 || got[3] != ActionRemoved {
		t.Fatalf("audit %v", got)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM doc_revisions WHERE doc_id = 'doc-1'"); n != 0 {
		t.Fatalf("%d revisions left", n)
	}
	_, err = f.Doc(f.ctx, alice, "doc-1")
	wantRefused(t, "the doc removed", err, ErrNotFound)
	wantRefused(t, "metadata of a doc removed", f.apply(t, alice, Change{Seq: 5, DocID: "doc-1", Type: TypeMetadata, Metadata: renamed})[0], ErrNotFound)
}

func TestRevisionGap(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	wantRefused(t, "a new doc at revision 2", f.apply(t, alice, revision(t, 1, "doc-1", 2, "PLAN", "x"))[0], ErrRevisionGap)
	errs := f.apply(t, alice, revision(t, 2, "doc-1", 1, "PLAN", "x"), revision(t, 3, "doc-1", 3, "PLAN", "y"))
	wantApplied(t, "revision 1", errs[:1])
	wantRefused(t, "revision 3 after 1", errs[1], ErrRevisionGap)
	if d, err := f.Doc(f.ctx, alice, "doc-1"); err != nil || d.Revision != 1 {
		t.Fatalf("doc %+v, %v", d, err)
	}
}

func TestDocNameTaken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other := revision(t, 3, "doc-3", 1, "plan", "z")
	other.Metadata.RepoKey = "github.com/acme/other"
	errs := f.apply(t, alice,
		revision(t, 1, "doc-1", 1, "PLAN", "x"),
		revision(t, 2, "doc-2", 1, "plan", "y"), // another node's, of the name whatever its case
		other,
		revision(t, 4, "doc-4", 1, "NOTES", "w"),
	)
	wantRefused(t, "a second doc of the name", errs[1], ErrNameTaken)
	wantApplied(t, "the others", []error{errs[0], errs[2], errs[3]})
	if n := f.count(t, "SELECT COUNT(*) FROM docs WHERE id = 'doc-2'"); n != 0 {
		t.Fatal("the doc refused was stored")
	}
	// Bob's docs are his own: the name is free for him.
	wantApplied(t, "bob's doc of the name", f.apply(t, bob, revision(t, 1, "doc-5", 1, "PLAN", "v")))

	renamed := meta("Plan")
	wantRefused(t, "renamed to a name taken", f.apply(t, alice, Change{Seq: 5, DocID: "doc-4", Type: TypeMetadata, Metadata: renamed})[0], ErrNameTaken)
	// The doc moved to the other repository takes the name there.
	moved := meta("NOTES")
	moved.RepoKey = "github.com/acme/other"
	wantApplied(t, "moved", f.apply(t, alice, Change{Seq: 6, DocID: "doc-4", Type: TypeMetadata, Metadata: moved}))
	if d, err := f.Doc(f.ctx, alice, "doc-4"); err != nil || d.RepoKey != moved.RepoKey {
		t.Fatalf("doc %+v, %v", d, err)
	}
}

func TestDocTooLarge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	errs := f.apply(t, alice,
		revision(t, 1, "doc-1", 1, "BIG", strings.Repeat("a", MaxContent+1)),
		revision(t, 2, "doc-2", 1, "FULL", strings.Repeat("a", MaxContent)),
		Change{Seq: 3, DocID: "doc-3", Type: TypeRevision, Metadata: meta("BROKEN"), Revision: 1, Content: []byte("not gzip")},
	)
	wantRefused(t, "over the limit", errs[0], ErrTooLarge)
	wantApplied(t, "at the limit", errs[1:2])
	if _, ok := errors.AsType[*InvalidError](errs[2]); !ok {
		t.Fatalf("content not gzip: %v", errs[2])
	}
	if n := f.count(t, "SELECT COUNT(*) FROM docs"); n != 1 {
		t.Fatalf("%d docs stored", n)
	}
}

func TestOtherUsersDocNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	wantApplied(t, "alice's doc", f.apply(t, alice, revision(t, 1, "doc-1", 1, "PLAN", "alice's")))
	errs := f.apply(t, bob,
		revision(t, 1, "doc-1", 1, "PLAN", "bob's"),
		revision(t, 2, "doc-1", 2, "PLAN", "bob's"),
		Change{Seq: 3, DocID: "doc-1", Type: TypeMetadata, Metadata: meta("MINE")},
		Change{Seq: 4, DocID: "doc-1", Type: TypeRemoved},
	)
	for i, err := range errs {
		wantRefused(t, "bob's change "+string(changeTypes[i]), err, ErrNotFound)
	}
	if d, err := f.Doc(f.ctx, alice, "doc-1"); err != nil || d.Name != "PLAN" || d.Revision != 1 {
		t.Fatalf("alice's doc %+v, %v", d, err)
	}
	f.wantContent(t, alice, "doc-1", 1, "alice's")
	if _, err := f.Doc(f.ctx, bob, "doc-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alice's doc read as bob: %v", err)
	}
	if _, err := f.Content(f.ctx, bob, "doc-1", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alice's content read as bob: %v", err)
	}
	if got := f.actions(t); len(got) != 1 {
		t.Fatalf("audit %v", got)
	}
}

var changeTypes = []Type{TypeRevision, TypeRevision, TypeMetadata, TypeRemoved}

func TestSealedAtRest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const text = "the plan nobody may read"
	wantApplied(t, "two docs", f.apply(t, alice, revision(t, 1, "doc-1", 1, "PLAN", text), revision(t, 2, "doc-2", 1, "NOTES", "notes")))
	var dataKey, content []byte
	if err := f.db.QueryRow("SELECT d.data_key, r.content FROM docs d JOIN doc_revisions r ON r.doc_id = d.id WHERE d.id = 'doc-1'").Scan(&dataKey, &content); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, gz(t, text)) || bytes.Contains(content, []byte(text)) {
		t.Fatal("the content is stored as it came")
	}
	f.wantContent(t, alice, "doc-1", 1, text)

	other := make([]byte, seal.KeySize)
	_, _ = rand.Read(other)
	withOther, err := New(f.db, Options{MasterKey: other})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withOther.Content(f.ctx, alice, "doc-1", 1); !errors.Is(err, seal.ErrOpen) {
		t.Fatalf("opened with another master key: %v", err)
	}
	// doc-1's data key does not open as doc-2's.
	if _, err := f.db.Exec("UPDATE docs SET data_key = ? WHERE id = 'doc-2'", dataKey); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Content(f.ctx, alice, "doc-2", 1); !errors.Is(err, seal.ErrOpen) {
		t.Fatalf("a data key copied to another doc: %v", err)
	}
	// Under the same data key, as sealed for doc-2, doc-1's revision does
	// not open as doc-2's, nor as another revision of doc-1.
	plainKey, err := seal.Open(f.key, dataKey, []byte("doc-1"))
	if err != nil {
		t.Fatal(err)
	}
	resealed, err := seal.Seal(f.key, plainKey, []byte("doc-2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE docs SET data_key = ? WHERE id = 'doc-2'", resealed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE doc_revisions SET content = ? WHERE doc_id = 'doc-2'", content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Content(f.ctx, alice, "doc-2", 1); !errors.Is(err, seal.ErrOpen) {
		t.Fatalf("a revision copied to another doc: %v", err)
	}
	wantApplied(t, "revision 2", f.apply(t, alice, revision(t, 3, "doc-1", 2, "PLAN", "second")))
	if _, err := f.db.Exec("UPDATE doc_revisions SET content = ? WHERE doc_id = 'doc-1' AND revision = 2", content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Content(f.ctx, alice, "doc-1", 2); !errors.Is(err, seal.ErrOpen) {
		t.Fatalf("a revision copied to another of its doc: %v", err)
	}
}

func TestContentNeverLogged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const text = "a line nobody may read in a log"
	c := revision(t, 1, "doc-1", 1, "PLAN", text)
	wantApplied(t, "the doc", f.apply(t, alice, c, revision(t, 2, "doc-1", 2, "PLAN", text+" again")))
	if _, err := f.Content(f.ctx, alice, "doc-1", 2); err != nil {
		t.Fatal(err)
	}
	logged := f.log.String()
	if !strings.Contains(logged, "doc revision stored") {
		t.Fatalf("nothing logged: %s", logged)
	}
	for _, s := range []string{text, base64.StdEncoding.EncodeToString(c.Content)} {
		if strings.Contains(logged, s) {
			t.Fatalf("the content logged: %s", logged)
		}
	}
}
