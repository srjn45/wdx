package doc

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
)

type fixture struct {
	db      *sql.DB
	store   *Store
	changes *int
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dbtest.Seed(t, path)
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	changes := 0
	s := NewStore(d)
	s.OnChange(func() { changes++ })
	return fixture{db: d, store: s, changes: &changes}
}

func (f fixture) project(t *testing.T, name string) string {
	t.Helper()
	id := "proj-" + name
	if _, err := f.db.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES (?, ?, 'open', 0, 0)`, id, name); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) create(t *testing.T, projectID string, n New) Doc {
	t.Helper()
	d, err := f.store.Create(context.Background(), projectID, n)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f fixture) content(t *testing.T, id string, revision int) string {
	t.Helper()
	content, _, err := f.store.Content(context.Background(), id, revision)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	d := f.create(t, p, New{Name: "auth_rfc", Format: "md", Labels: []string{"rfc", "auth", "rfc"}, Content: []byte("# Auth\n")})

	if !strings.HasPrefix(d.ID, "doc-") || d.ProjectID != p || d.Name != "auth_rfc" || d.Format != "md" {
		t.Errorf("doc: %+v", d)
	}
	if !slices.Equal(d.Labels, []string{"auth", "rfc"}) {
		t.Errorf("labels: got %q, want them sorted, each once", d.Labels)
	}
	if d.Status != StatusWIP || d.Revision != 1 || d.Size != len("# Auth\n") {
		t.Errorf("status %s, revision %d, size %d", d.Status, d.Revision, d.Size)
	}
	if got := f.content(t, d.ID, 0); got != "# Auth\n" {
		t.Errorf("content: %q", got)
	}
	byName, err := f.store.GetByName(ctx, p, "AUTH_RFC")
	if err != nil || byName.ID != d.ID {
		t.Errorf("by name in another case: %+v, %v", byName, err)
	}
	if *f.changes != 1 {
		t.Errorf("changes: %d, want 1", *f.changes)
	}

	empty := f.create(t, p, New{Name: "notes", Status: StatusApproved})
	if empty.Status != StatusApproved || empty.Size != 0 || empty.Format != "" || empty.Labels == nil {
		t.Errorf("an empty doc: %+v", empty)
	}
}

func TestCreateNameTaken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project(t, "wdx")
	first := f.create(t, p, New{Name: "auth"})

	_, err := f.store.Create(context.Background(), p, New{Name: "Auth"})
	var taken *NameTakenError
	if !errors.As(err, &taken) || taken.Doc.ID != first.ID {
		t.Fatalf("got %v, want a *NameTakenError naming %s", err, first.ID)
	}
	other := f.project(t, "other")
	f.create(t, other, New{Name: "auth"})
}

func TestCreateRefuses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project(t, "wdx")
	tooMany := make([]string, MaxLabels+1)
	for i := range tooMany {
		tooMany[i] = strings.Repeat("l", i+1)
	}
	for _, c := range []struct {
		name string
		new  New
		what string // the InvalidError's What, or "name" for an *InvalidNameError
	}{
		{"blank name", New{}, "name"},
		{"space in name", New{Name: "auth rfc"}, "name"},
		{"dot in name", New{Name: "auth.md"}, "name"},
		{"long name", New{Name: strings.Repeat("a", 101)}, "name"},
		{"dot in format", New{Name: "a", Format: "tar.gz"}, "format"},
		{"long format", New{Name: "a", Format: "abcdefghijk"}, "format"},
		{"capital in label", New{Name: "a", Labels: []string{"Auth"}}, "label"},
		{"space in label", New{Name: "a", Labels: []string{"a b"}}, "label"},
		{"too many labels", New{Name: "a", Labels: tooMany}, "labels"},
		{"unknown status", New{Name: "a", Status: "done"}, "status"},
		{"not UTF-8", New{Name: "a", Content: []byte{0xff, 0xfe}}, "content"},
		{"NUL", New{Name: "a", Content: []byte("a\x00b")}, "content"},
		{"too large", New{Name: "a", Content: make([]byte, MaxContent+1)}, "content"},
	} {
		_, err := f.store.Create(context.Background(), p, c.new)
		var invalidName *InvalidNameError
		var invalid *InvalidError
		switch {
		case c.what == "name" && errors.As(err, &invalidName):
		case c.what != "name" && errors.As(err, &invalid) && invalid.What == c.what:
		default:
			t.Errorf("%s: got %v, want it refused for its %s", c.name, err, c.what)
		}
	}
	if list, _ := f.store.List(context.Background(), p, nil, ""); len(list) != 0 || *f.changes != 0 {
		t.Errorf("refused docs left %d docs and %d changes", len(list), *f.changes)
	}
	f.create(t, p, New{Name: "a", Content: []byte(strings.Repeat("a", MaxContent))})
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	d := f.create(t, p, New{Name: "auth", Status: StatusApproved, Labels: []string{"rfc"}, Content: []byte("one")})

	up, err := f.store.Update(ctx, d.ID, 1, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if !up.Changed || up.Previous != StatusApproved || up.Doc.Revision != 2 || up.Doc.Status != StatusWIP || up.Doc.Size != 3 {
		t.Errorf("update: %+v", up)
	}
	if !slices.Equal(up.Doc.Labels, []string{"rfc"}) {
		t.Errorf("labels: %q", up.Doc.Labels)
	}
	if got := f.content(t, d.ID, 0); got != "two" {
		t.Errorf("latest: %q", got)
	}
	if got := f.content(t, d.ID, 1); got != "one" {
		t.Errorf("revision 1: %q", got)
	}

	// A client that read revision 1 writes after another wrote revision 2.
	_, err = f.store.Update(ctx, d.ID, 1, []byte("stale"))
	var changed *ChangedError
	if !errors.As(err, &changed) || changed.Base != 1 || changed.Latest != 2 {
		t.Fatalf("got %v, want a *ChangedError from 1 to 2", err)
	}
	if got := f.content(t, d.ID, 0); got != "two" {
		t.Errorf("a stale update wrote: %q", got)
	}

	if _, _, err := f.store.Set(ctx, d.ID, Change{Status: StatusReview}); err != nil {
		t.Fatal(err)
	}
	before := *f.changes
	same, err := f.store.Update(ctx, d.ID, 2, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if same.Changed || same.Doc.Revision != 2 || same.Doc.Status != StatusReview || *f.changes != before {
		t.Errorf("the same content: %+v, %d changes", same, *f.changes-before)
	}

	revisions, err := f.store.Revisions(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].Number != 2 || revisions[1].Number != 1 || revisions[1].Size != 3 {
		t.Errorf("revisions: %+v", revisions)
	}

	var invalid *InvalidError
	if _, err := f.store.Update(ctx, d.ID, 2, []byte{0}); !errors.As(err, &invalid) {
		t.Errorf("binary content: %v", err)
	}
	if _, err := f.store.Update(ctx, "doc-missing", 1, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing doc: %v", err)
	}
}

func TestContentMissingRevision(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	d := f.create(t, f.project(t, "wdx"), New{Name: "auth"})
	if _, _, err := f.store.Content(context.Background(), d.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("revision 2 of 1: %v", err)
	}
	if _, err := f.store.Revisions(context.Background(), "doc-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revisions of a missing doc: %v", err)
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	f.create(t, p, New{Name: "zeta", Labels: []string{"auth", "rfc"}})
	f.create(t, p, New{Name: "Alpha", Labels: []string{"auth"}, Status: StatusApproved})
	f.create(t, p, New{Name: "beta"})
	f.create(t, f.project(t, "other"), New{Name: "gamma", Labels: []string{"auth"}})

	names := func(labels []string, status Status) []string {
		list, err := f.store.List(ctx, p, labels, status)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range list {
			out = append(out, d.Name)
		}
		return out
	}
	for _, c := range []struct {
		labels []string
		status Status
		want   []string
	}{
		{nil, "", []string{"Alpha", "beta", "zeta"}},
		{[]string{"auth"}, "", []string{"Alpha", "zeta"}},
		{[]string{"auth", "rfc"}, "", []string{"zeta"}},
		{[]string{"nope"}, "", nil},
		{nil, StatusApproved, []string{"Alpha"}},
		{[]string{"rfc"}, StatusApproved, nil},
	} {
		if got := names(c.labels, c.status); !slices.Equal(got, c.want) {
			t.Errorf("labels %q, status %q: got %q, want %q", c.labels, c.status, got, c.want)
		}
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	d := f.create(t, f.project(t, "wdx"), New{Name: "auth", Labels: []string{"draft", "rfc"}, Content: []byte("text")})

	got, changed, err := f.store.Set(ctx, d.ID, Change{Status: StatusApproved, AddLabels: []string{"auth"}, RemoveLabels: []string{"draft", "absent"}})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || got.Status != StatusApproved || !slices.Equal(got.Labels, []string{"auth", "rfc"}) || got.Revision != 1 {
		t.Errorf("set: %+v, changed %v", got, changed)
	}

	before := *f.changes
	if _, changed, err := f.store.Set(ctx, d.ID, Change{Status: StatusApproved, AddLabels: []string{"rfc"}}); err != nil || changed || *f.changes != before {
		t.Errorf("setting what it has: changed %v, %v, %d changes", changed, err, *f.changes-before)
	}

	var invalid *InvalidError
	if _, _, err := f.store.Set(ctx, d.ID, Change{Status: "done"}); !errors.As(err, &invalid) {
		t.Errorf("an unknown status: %v", err)
	}
	if _, _, err := f.store.Set(ctx, d.ID, Change{AddLabels: []string{"RFC"}}); !errors.As(err, &invalid) {
		t.Errorf("a capital: %v", err)
	}
	many := make([]string, MaxLabels-1)
	for i := range many {
		many[i] = strings.Repeat("m", i+1)
	}
	if _, _, err := f.store.Set(ctx, d.ID, Change{AddLabels: many}); !errors.As(err, &invalid) || invalid.What != "labels" {
		t.Errorf("past %d labels: %v", MaxLabels, err)
	}
	if _, _, err := f.store.Set(ctx, "doc-missing", Change{Status: StatusWIP}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing doc: %v", err)
	}
}

func TestRename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	d := f.create(t, p, New{Name: "auth"})
	other := f.create(t, p, New{Name: "design"})

	got, err := f.store.Rename(ctx, d.ID, "Auth")
	if err != nil || got.Name != "Auth" {
		t.Errorf("its own name in another case: %+v, %v", got, err)
	}
	var taken *NameTakenError
	if _, err := f.store.Rename(ctx, d.ID, "DESIGN"); !errors.As(err, &taken) || taken.Doc.ID != other.ID {
		t.Errorf("another doc's name: %v", err)
	}
	var invalid *InvalidNameError
	if _, err := f.store.Rename(ctx, d.ID, "a/b"); !errors.As(err, &invalid) {
		t.Errorf("a slash: %v", err)
	}
	if got, err := f.store.Rename(ctx, d.ID, "auth-rfc"); err != nil || got.Name != "auth-rfc" {
		t.Errorf("rename: %+v, %v", got, err)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	d := f.create(t, p, New{Name: "auth", Content: []byte("one")})
	if _, err := f.store.Update(ctx, d.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}

	removed, err := f.store.Remove(ctx, d.ID)
	if err != nil || removed.Name != "auth" {
		t.Fatalf("remove: %+v, %v", removed, err)
	}
	if _, err := f.store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after remove: %v", err)
	}
	if n := f.revisionRows(t); n != 0 {
		t.Errorf("%d revisions left", n)
	}
	if _, err := f.store.Remove(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing it again: %v", err)
	}

	f.create(t, p, New{Name: "kept"})
	if _, err := f.db.Exec("DELETE FROM projects WHERE id = ?", p); err != nil {
		t.Fatal(err)
	}
	if n := f.revisionRows(t); n != 0 {
		t.Errorf("removing the project left %d revisions", n)
	}
}

func (f fixture) revisionRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT count(*) FROM doc_revisions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
