package doc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

// synced adds a project with a git remote and syncs it to the hub.
func (f fixture) synced(t *testing.T, name string) string {
	t.Helper()
	p := f.remoteProject(t, name)
	if changed, err := NewOutbox(f.db).SyncOn(context.Background(), p); err != nil || !changed {
		t.Fatalf("sync on: %v, changed %v", err, changed)
	}
	return p
}

func (f fixture) remoteProject(t *testing.T, name string) string {
	t.Helper()
	id := "proj-" + name
	if _, err := f.db.Exec(`INSERT INTO projects (id, name, origin_url, origin_key, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'open', 0, 0)`, id, name, "https://github.com/o/"+name, "github.com/o/"+name); err != nil {
		t.Fatal(err)
	}
	return id
}

// outbox returns the outbox's rows in order, each as "change doc_id revision",
// with " failed:<code>" when the hub refused it.
func (f fixture) outbox(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query("SELECT change, doc_id, revision, failed FROM hub_outbox ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var change, doc string
		var revision sql.NullInt64
		var failed sql.NullString
		if err := rows.Scan(&change, &doc, &revision, &failed); err != nil {
			t.Fatal(err)
		}
		row := fmt.Sprintf("%s %s %d", change, doc, revision.Int64)
		if failed.Valid {
			row += " failed:" + failed.String
		}
		list = append(list, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return list
}

func (f fixture) wantOutbox(t *testing.T, what string, want ...string) {
	t.Helper()
	if got := f.outbox(t); !slices.Equal(got, want) {
		t.Errorf("%s: outbox\n%q\nwant\n%q", what, got, want)
	}
}

func next(t *testing.T, o *Outbox, limit int) []Outgoing {
	t.Helper()
	list, err := o.Next(context.Background(), limit)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func seqs(list []Outgoing) []int64 {
	var out []int64
	for _, c := range list {
		out = append(out, c.Seq)
	}
	return out
}

func TestStoreWritesOutbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.synced(t, "wdx")

	d := f.create(t, p, New{Name: "auth", Content: []byte("one")})
	f.wantOutbox(t, "create", "revision "+d.ID+" 1")

	if _, err := f.store.Update(ctx, d.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateWithChildren(ctx, d.ID, 2, []byte("three"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Update(ctx, d.ID, 3, []byte("three")); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "updates, the last with the same content",
		"revision "+d.ID+" 1", "revision "+d.ID+" 2", "revision "+d.ID+" 3")

	if _, _, err := f.store.Set(ctx, d.ID, Change{Status: StatusApproved}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.Set(ctx, d.ID, Change{Status: StatusApproved}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Rename(ctx, d.ID, "auth-rfc"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Remove(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "set twice, renamed, removed",
		"revision "+d.ID+" 1", "revision "+d.ID+" 2", "revision "+d.ID+" 3",
		"metadata "+d.ID+" 0", "metadata "+d.ID+" 0", "removed "+d.ID+" 0")
}

func TestStoreWritesOutboxForMovedFamily(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.synced(t, "wdx")
	parent := f.create(t, p, New{Name: "parent"})
	child := f.create(t, p, New{Name: "child"})
	if _, err := f.db.Exec("UPDATE docs SET parent_id = ? WHERE id = ?", parent.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("DELETE FROM hub_outbox"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.store.Move(ctx, child.ID, "/rfcs"); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "a move", "metadata "+parent.ID+" 0", "metadata "+child.ID+" 0")
}

func TestStoreWritesNoOutbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)

	notSynced := f.remoteProject(t, "app")
	d := f.create(t, notSynced, New{Name: "auth"})
	if _, err := f.store.Update(ctx, d.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Rename(ctx, d.ID, "auth2"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Remove(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "a project not synced")

	p := f.synced(t, "wdx")
	plans := NewKindStore(f.db, f.dir, KindPlan)
	plan, err := plans.Create(ctx, p, New{Name: "release"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Update(ctx, plan.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plans.Set(ctx, plan.ID, Change{Status: StatusApproved}); err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Remove(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "a plan")

	d = f.create(t, p, New{Name: "auth"})
	if _, err := f.db.Exec("DELETE FROM hub_outbox"); err != nil {
		t.Fatal(err)
	}
	// The row is written before the children, a missing one of which fails it.
	if _, err := f.store.UpdateWithChildren(ctx, d.ID, 1, []byte("two"), []string{"doc-missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing child: %v", err)
	}
	f.wantOutbox(t, "a change rolled back")
}

func TestSyncOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.remoteProject(t, "wdx")
	a := f.create(t, p, New{Name: "a", Content: []byte("1")})
	b := f.create(t, p, New{Name: "b", Content: []byte("1")})
	for i, content := range []string{"2", "3"} {
		if _, err := f.store.Update(ctx, a.ID, i+1, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	plans := NewKindStore(f.db, f.dir, KindPlan)
	if _, err := plans.Create(ctx, p, New{Name: "release"}); err != nil {
		t.Fatal(err)
	}
	other := f.remoteProject(t, "other")
	f.create(t, other, New{Name: "c"})
	f.wantOutbox(t, "before")

	changed, err := o.SyncOn(ctx, p)
	if err != nil || !changed {
		t.Fatalf("sync on: %v, changed %v", err, changed)
	}
	f.wantOutbox(t, "every revision of every doc, in order",
		"revision "+a.ID+" 1", "revision "+a.ID+" 2", "revision "+a.ID+" 3", "revision "+b.ID+" 1")
	st, err := o.Status(ctx, p)
	if err != nil || !st.Sync || st.Pending != 4 || st.RepoKey != "github.com/o/wdx" || !st.LastPushedAt.IsZero() {
		t.Errorf("status: %+v, %v", st, err)
	}

	if changed, err := o.SyncOn(ctx, p); err != nil || changed {
		t.Errorf("on again: %v, changed %v", err, changed)
	}
	if got := len(f.outbox(t)); got != 4 {
		t.Errorf("on again queued again: %d rows", got)
	}

	noRemote := f.project(t, "local")
	f.create(t, noRemote, New{Name: "d"})
	if _, err := o.SyncOn(ctx, noRemote); !errors.Is(err, ErrNoRemote) {
		t.Errorf("no remote: %v", err)
	}
	if st, err := o.Status(ctx, noRemote); err != nil || st.Sync || st.RepoKey != "" {
		t.Errorf("a project with no remote: %+v, %v", st, err)
	}
	if _, err := o.SyncOn(ctx, "proj-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing project: %v", err)
	}
}

func TestSyncOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	other := f.synced(t, "other")
	f.create(t, p, New{Name: "a"})
	kept := f.create(t, other, New{Name: "b"})

	if changed, err := o.SyncOff(ctx, p); err != nil || !changed {
		t.Fatalf("sync off: %v, changed %v", err, changed)
	}
	f.wantOutbox(t, "off", "revision "+kept.ID+" 1")
	d := f.create(t, p, New{Name: "c"})
	if _, err := f.store.Rename(ctx, d.ID, "c2"); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "changes once off", "revision "+kept.ID+" 1")
	if changed, err := o.SyncOff(ctx, p); err != nil || changed {
		t.Errorf("off again: %v, changed %v", err, changed)
	}
	if _, err := o.SyncOff(ctx, "proj-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing project: %v", err)
	}
}

func TestOutboxNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	d := f.create(t, p, New{Name: "auth", Format: "md", Labels: []string{"rfc"}, Content: []byte("# Auth\n")})
	if _, _, err := f.store.Set(ctx, d.ID, Change{Status: StatusReview}); err != nil {
		t.Fatal(err)
	}

	list := next(t, o, 10)
	if len(list) != 2 {
		t.Fatalf("got %d changes, want 2", len(list))
	}
	rev, meta := list[0], list[1]
	if rev.Change != ChangeRevision || rev.DocID != d.ID || rev.ProjectID != p || rev.RepoKey != "github.com/o/wdx" || rev.Kind != KindDoc || rev.Revision != 1 {
		t.Errorf("revision: %+v", rev)
	}
	if content, err := unpack(rev.Content); err != nil || string(content) != "# Auth\n" {
		t.Errorf("content, gzipped: %q, %v", content, err)
	}
	if rev.Doc.Name != "auth" || rev.Doc.Format != "md" || !slices.Equal(rev.Doc.Labels, []string{"rfc"}) || rev.Doc.Status != StatusReview || rev.Doc.Revision != 1 {
		t.Errorf("a revision's metadata is the doc's now: %+v", rev.Doc)
	}
	if meta.Change != ChangeMetadata || meta.Content != nil || meta.Doc.Status != StatusReview {
		t.Errorf("metadata: %+v", meta)
	}
	if got := next(t, o, 1); len(got) != 1 || got[0].Seq != rev.Seq {
		t.Errorf("one at most: %+v", got)
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()
	if err := o.Done(ctx, seqs(list), at); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "done")
	if st, err := o.Status(ctx, p); err != nil || !st.LastPushedAt.Equal(at) || st.Pending != 0 {
		t.Errorf("status once done: %+v, %v", st, err)
	}
}

func TestOutboxNextMergesMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	a := f.create(t, p, New{Name: "a"})
	b := f.create(t, p, New{Name: "b"})
	if err := o.Done(ctx, seqs(next(t, o, 10)), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { _, _, err := f.store.Set(ctx, a.ID, Change{Status: StatusReview}); return err },
		func() error { _, _, err := f.store.Set(ctx, b.ID, Change{Status: StatusReview}); return err },
		func() error { _, err := f.store.Rename(ctx, a.ID, "a2"); return err },
		func() error { _, err := f.store.Update(ctx, a.ID, 1, []byte("two")); return err },
		func() error { _, _, err := f.store.Set(ctx, a.ID, Change{Status: StatusApproved}); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, c := range next(t, o, 10) {
		got = append(got, fmt.Sprintf("%s %s %s", c.Change, c.DocID, c.Doc.Name))
	}
	want := []string{"metadata " + b.ID + " b", "metadata " + a.ID + " a2", "revision " + a.ID + " a2", "metadata " + a.ID + " a2"}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%q\nwant the first two of a as one\n%q", got, want)
	}
}

func TestOutboxNextRemovedDoc(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	d := f.create(t, p, New{Name: "auth"})
	if _, err := f.store.Update(ctx, d.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Remove(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	list := next(t, o, 10)
	if len(list) != 1 || list[0].Change != ChangeRemoved || list[0].DocID != d.ID || list[0].Doc.ID != "" || list[0].RepoKey != "github.com/o/wdx" {
		t.Errorf("a removed doc's changes: %+v, want its removed one only", list)
	}
	f.wantOutbox(t, "the others deleted", "removed "+d.ID+" 0")
}

func TestOutboxFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	held := f.create(t, p, New{Name: "auth"})
	other := f.create(t, p, New{Name: "design"})
	if _, err := f.store.Update(ctx, held.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}

	first := next(t, o, 1)
	if len(first) != 1 || first[0].DocID != held.ID {
		t.Fatalf("first: %+v", first)
	}
	if err := o.Fail(ctx, first[0].Seq, "doc_name_taken"); err != nil {
		t.Fatal(err)
	}
	list := next(t, o, 10)
	if len(list) != 1 || list[0].DocID != other.ID {
		t.Errorf("with auth refused: %+v, want design's change only", list)
	}
	st, err := o.Status(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 2 || !slices.Equal(st.Failed, []FailedDoc{{DocID: held.ID, Name: "auth", Code: "doc_name_taken"}}) {
		t.Errorf("status: %+v", st)
	}
	if err := o.Done(ctx, seqs(list), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := next(t, o, 10); len(got) != 0 {
		t.Errorf("auth still held: %+v", got)
	}

	if _, err := f.store.Rename(ctx, held.ID, "auth-rfc"); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "renamed", "revision "+held.ID+" 1", "revision "+held.ID+" 2", "metadata "+held.ID+" 0")
	var got []int
	for _, c := range next(t, o, 10) {
		got = append(got, c.Revision)
	}
	if !slices.Equal(got, []int{1, 2, 0}) {
		t.Errorf("tried again in order: revisions %v", got)
	}
	if st, err := o.Status(ctx, p); err != nil || st.Pending != 3 || len(st.Failed) != 0 {
		t.Errorf("status once renamed: %+v, %v", st, err)
	}
}

func TestOutboxProjectRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	other := f.synced(t, "other")
	f.create(t, p, New{Name: "a"})
	kept := f.create(t, other, New{Name: "b"})
	if _, err := f.db.Exec("DELETE FROM projects WHERE id = ?", p); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "a project removed", "revision "+kept.ID+" 1")
	if _, err := o.Status(ctx, p); !errors.Is(err, ErrNotFound) {
		t.Errorf("status of a removed project: %v", err)
	}
}

func TestOutboxClearAndEnqueueAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	o := NewOutbox(f.db)
	p := f.synced(t, "wdx")
	a := f.create(t, p, New{Name: "a"})
	if _, err := f.store.Update(ctx, a.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}
	f.create(t, f.remoteProject(t, "other"), New{Name: "b"})
	if err := o.Done(ctx, seqs(next(t, o, 1)), time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := o.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "cleared")
	if err := o.EnqueueAll(ctx); err != nil {
		t.Fatal(err)
	}
	f.wantOutbox(t, "every synced project's docs", "revision "+a.ID+" 1", "revision "+a.ID+" 2")
}
