package plan

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
)

type fixture struct {
	db      *sql.DB
	store   *Store
	project string
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
	if _, err := d.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-wdx', 'wdx', 'open', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	return fixture{db: d, store: NewStore(d, filepath.Join(t.TempDir(), "plans")), project: "proj-wdx"}
}

func tasks(ids ...string) string {
	var b strings.Builder
	b.WriteString("goal: g\ntasks:\n")
	for _, id := range ids {
		b.WriteString("  - id: " + id + "\n    prompt: p\n")
	}
	return b.String()
}

func (f fixture) create(t *testing.T, name, content string) Plan {
	t.Helper()
	s, err := f.store.Create(context.Background(), f.project, name, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return s.Plan
}

func (f fixture) get(t *testing.T, name string) Plan {
	t.Helper()
	p, err := f.store.GetByName(context.Background(), f.project, name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// problems returns the messages of err's problems, or fails if it is not an
// *InvalidError.
func problems(t *testing.T, err error) []string {
	t.Helper()
	invalid, ok := errors.AsType[*InvalidError](err)
	if !ok {
		t.Fatalf("%v, want an invalid plan", err)
	}
	var list []string
	for _, p := range invalid.Problems {
		list = append(list, p.String())
	}
	return list
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	content := "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n  - id: c\n    after: [a, b]\n    prompt: p\n"
	s, err := f.store.Create(ctx, f.project, "auth", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	p := s.Plan
	if p.Name != "auth" || p.State != StateDraft || p.Tasks != 3 || p.Plans != nil || p.Parent != "" || p.Revision != 1 || p.ProjectName != "wdx" {
		t.Errorf("created %+v", p)
	}
	if want := []Dropped{{Entry: "c", After: "a", Via: "b"}}; !reflect.DeepEqual(s.Dropped, want) {
		t.Errorf("dropped %+v", s.Dropped)
	}
	stored, r, def, err := f.store.Content(ctx, p.ID, 0)
	if err != nil || r.Number != 1 || strings.Contains(string(stored), "after: [a, b]") || !reflect.DeepEqual(def.Tasks[2].After, []string{"b"}) {
		t.Errorf("stored %q, %+v, %+v, %v", stored, r, def, err)
	}
	if _, _, _, err := f.store.Content(ctx, p.ID, 2); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "plan auth has no revision 2") {
		t.Errorf("a revision it does not have: %v", err)
	}

	var taken *NameTakenError
	if _, err := f.store.Create(ctx, f.project, "AUTH", []byte(tasks("a"))); !errors.As(err, &taken) || taken.Plan.ID != p.ID {
		t.Errorf("a name taken: %v", err)
	}
	if got := problems(t, func() error { _, err := f.store.Create(ctx, f.project, "x", []byte("goal: g\n")); return err }()); len(got) != 1 {
		t.Errorf("not a plan: %v", got)
	}
	if list, err := f.store.List(ctx, f.project); err != nil || len(list) != 1 {
		t.Errorf("a refused plan was kept: %+v, %v", list, err)
	}
}

func TestPlansOfPlans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a, b := f.create(t, "auth-a", tasks("x")), f.create(t, "auth-b", tasks("y"))
	parent := f.create(t, "auth", "goal: g\nplans:\n  - id: AUTH-B\n    after: [auth-a]\n  - id: auth-a\n")
	if parent.Tasks != 0 || !reflect.DeepEqual(parent.Plans, []string{"auth-a", "AUTH-B"}) {
		t.Errorf("the parent: %+v", parent)
	}
	for _, name := range []string{a.Name, b.Name} {
		if got := f.get(t, name); got.Parent != "auth" {
			t.Errorf("%s's parent: %q", name, got.Parent)
		}
	}
	list, err := f.store.List(ctx, f.project)
	if err != nil || len(list) != 3 || list[1].Parent != "auth" || list[2].Parent != "auth" {
		t.Errorf("listed %+v, %v", list, err)
	}

	f.create(t, "other", tasks("z"))
	got := problems(t, func() error {
		_, err := f.store.Create(ctx, f.project, "big", []byte("goal: g\nplans:\n  - id: auth-a\n  - id: auth\n  - id: nowhere\n  - id: big\n  - id: other\n"))
		return err
	}())
	want := []string{
		"line 3: auth-a is already one of the plans of auth: a plan has one parent",
		"line 4: auth lists plans itself: plans of plans have one level",
		"line 5: the project has no plan nowhere",
		"line 6: a plan cannot list itself",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if f.get(t, "other").Parent != "" {
		t.Error("a refused parent took a child")
	}
	got = problems(t, func() error {
		_, err := f.store.Update(ctx, a.ID, 1, []byte("goal: g\nplans:\n  - id: other\n"))
		return err
	}())
	if want := []string{"line 3: auth-a is one of the plans of auth, so it cannot list plans: plans of plans have one level"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a child listing plans: %q", got)
	}

	if _, err := f.store.Update(ctx, parent.ID, 1, []byte("goal: g\nplans:\n  - id: auth-a\n  - id: other\n")); err != nil {
		t.Fatal(err)
	}
	if f.get(t, "auth-b").Parent != "" || f.get(t, "auth-a").Parent != "auth" || f.get(t, "other").Parent != "auth" {
		t.Error("the parent's children did not follow its new revision")
	}

	var inUse *InUseError
	if _, err := f.store.Rename(ctx, a.ID, "auth-one"); !errors.As(err, &inUse) || inUse.Parent.ID != parent.ID || inUse.Plan.ID != a.ID {
		t.Errorf("renaming a child: %v", err)
	}
	if _, err := f.store.Remove(ctx, a.ID); !errors.As(err, &inUse) {
		t.Errorf("removing a child: %v", err)
	}
	if got, err := f.store.Rename(ctx, b.ID, "auth-two"); err != nil || got.Name != "auth-two" {
		t.Errorf("renaming a plan no one lists: %+v, %v", got, err)
	}
	if got, err := f.store.Rename(ctx, parent.ID, "auth-all"); err != nil || f.get(t, "auth-a").Parent != "auth-all" {
		t.Errorf("renaming the parent: %+v, %v", got, err)
	}
	if _, err := f.store.Remove(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if f.get(t, "auth-a").Parent != "" || f.get(t, "other").Parent != "" {
		t.Error("removing the parent kept its children's parent")
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a"))
	if got, err := f.store.List(ctx, f.project); err != nil || got[0].Tasks != 1 {
		t.Fatalf("listed %+v, %v", got, err)
	}

	s, err := f.store.Update(ctx, p.ID, 1, []byte(tasks("a", "b")))
	if err != nil || !s.Changed || s.Plan.Revision != 2 || s.Plan.Tasks != 2 || s.Dropped == nil {
		t.Errorf("updated %+v, %v", s, err)
	}
	if got, err := f.store.List(ctx, f.project); err != nil || got[0].Tasks != 2 {
		t.Errorf("listed after the update, from what was kept of revision 1: %+v, %v", got, err)
	}
	if s, err := f.store.Update(ctx, p.ID, 2, []byte(tasks("a", "b"))); err != nil || s.Changed || s.Plan.Revision != 2 {
		t.Errorf("the same content: %+v, %v", s, err)
	}
	var changed *ChangedError
	if _, err := f.store.Update(ctx, p.ID, 1, []byte(tasks("c"))); !errors.As(err, &changed) || changed.Plan.Revision != 2 || changed.Base != 1 {
		t.Errorf("a stale base: %v", err)
	}
	problems(t, func() error { _, err := f.store.Update(ctx, p.ID, 2, []byte("tasks: []\n")); return err }())
	if revisions, err := f.store.Revisions(ctx, p.ID); err != nil || len(revisions) != 2 {
		t.Errorf("revisions %+v, %v", revisions, err)
	}
	if _, err := f.store.Update(ctx, "doc-nowhere", 1, []byte(tasks("a"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such plan: %v", err)
	}
}

func TestRemoveForgets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a := f.create(t, "a", tasks("x"))
	f.create(t, "b", tasks("x"))
	if _, err := f.store.Remove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, kept := f.store.summaries[a.ID]; kept || len(f.store.summaries) != 1 {
		t.Errorf("kept after its removal: %+v", f.store.summaries)
	}
	f.store.RemoveProjectCopies(ctx, f.project, "wdx")
	if len(f.store.summaries) != 0 {
		t.Errorf("kept after its project's removal: %+v", f.store.summaries)
	}
}
