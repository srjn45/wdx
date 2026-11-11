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
		b.WriteString("  - id: " + id + "\n    aca: claude\n    model: opus\n    prompt: p\n")
	}
	return b.String()
}

func (f fixture) create(t *testing.T, name, content string) Plan {
	t.Helper()
	s, err := f.store.Create(context.Background(), f.project, name, "", []byte(content))
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
	content := "goal: g\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n  - id: b\n    aca: claude\n    model: opus\n    after: [a]\n    prompt: p\n  - id: c\n    aca: claude\n    model: opus\n    after: [a, b]\n    prompt: p\n"
	s, err := f.store.Create(ctx, f.project, "auth", "", []byte(content))
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
	if _, err := f.store.Create(ctx, f.project, "AUTH", "", []byte(tasks("a"))); !errors.As(err, &taken) || taken.Plan.ID != p.ID {
		t.Errorf("a name taken: %v", err)
	}
	if got := problems(t, func() error { _, err := f.store.Create(ctx, f.project, "x", "", []byte("goal: g\n")); return err }()); len(got) != 1 {
		t.Errorf("not a plan: %v", got)
	}
	if list, err := f.store.List(ctx, f.project); err != nil || len(list) != 1 {
		t.Errorf("a refused plan was kept: %+v, %v", list, err)
	}
}

// TestPRSetOnTheParent: pr is set on a plan of plans for the plans it lists, so a
// plan listed that sets it is refused, whether it is listed or set later.
func TestPRSetOnTheParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.create(t, "auth-a", "goal: g\npr: true\ntasks:\n  - id: x\n    aca: claude\n    model: opus\n    prompt: p\n")
	b := f.create(t, "auth-b", tasks("y"))
	got := problems(t, func() error {
		_, err := f.store.Create(ctx, f.project, "auth", "", []byte("goal: g\npr: true\nplans:\n  - id: auth-b\n  - id: auth-a\n"))
		return err
	}())
	if want := []string{"line 5: auth-a sets pr itself: set pr on the plan of plans"}; !reflect.DeepEqual(got, want) {
		t.Errorf("listing a plan that sets pr: %q", got)
	}

	f.create(t, "auth", "goal: g\npr: true\nplans:\n  - id: auth-b\n")
	got = problems(t, func() error {
		_, err := f.store.Update(ctx, b.ID, 1, []byte("goal: g\npr: false\ntasks:\n  - id: y\n    aca: claude\n    model: opus\n    prompt: p\n"))
		return err
	}())
	if want := []string{"line 2: auth-b is one of the plans of auth, so it cannot set pr: set pr on the plan of plans"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a listed plan setting pr: %q", got)
	}
}

func TestCheckACAsAndModels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	calls := map[string]int{}
	boom := errors.New("boom")
	f.store.SetModels(func(_ context.Context, aca string) ([]string, error) {
		calls[aca]++
		switch aca {
		case "claude":
			return []string{"opus", "sonnet"}, nil
		case "codex":
			return []string{"gpt-5"}, nil
		case "cursor":
			return nil, &ACAError{Err: errors.New("cursor is not installed")}
		case "broken":
			return nil, boom
		default:
			return nil, &ACAError{Err: errors.New("unknown AI coding agent " + aca)}
		}
	})

	p := f.create(t, "valid", "goal: g\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n  - id: b\n    aca: claude\n    model: sonnet\n    prompt: p\n  - id: c\n    aca: codex\n    model: gpt-5\n    prompt: p\n")
	if want := map[string]int{"claude": 1, "codex": 1}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %+v, want %+v", calls, want)
	}

	clear(calls)
	bad := "goal: g\ntasks:\n" +
		"  - id: a\n    aca: nope\n    model: x\n    prompt: p\n" +
		"  - id: b\n    aca: cursor\n    model: x\n    prompt: p\n" +
		"  - id: c\n    aca: cursor\n    model: y\n    prompt: p\n" +
		"  - id: d\n    aca: claude\n    model: haiku\n    prompt: p\n"
	got := problems(t, func() error {
		_, err := f.store.Create(ctx, f.project, "bad", "", []byte(bad))
		return err
	}())
	want := []string{
		"line 4: unknown AI coding agent nope",
		"line 8: cursor is not installed",
		"line 12: cursor is not installed",
		`line 17: model "haiku" is not one claude offers (opus, sonnet)`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if wantCalls := map[string]int{"nope": 1, "cursor": 1, "claude": 1}; !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls %+v, want %+v", calls, wantCalls)
	}

	got = problems(t, func() error {
		_, err := f.store.Update(ctx, p.ID, 1, []byte("goal: g\ntasks:\n  - id: a\n    aca: claude\n    model: haiku\n    prompt: p\n"))
		return err
	}())
	if want := []string{`line 5: model "haiku" is not one claude offers (opus, sonnet)`}; !reflect.DeepEqual(got, want) {
		t.Errorf("update got %q, want %q", got, want)
	}

	if _, err := f.store.Create(ctx, f.project, "broken", "", []byte("goal: g\ntasks:\n  - id: a\n    aca: broken\n    model: x\n    prompt: p\n")); !errors.Is(err, boom) {
		t.Errorf("broken models error: %v, want %v", err, boom)
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
		_, err := f.store.Create(ctx, f.project, "big", "", []byte("goal: g\nplans:\n  - id: auth-a\n  - id: auth\n  - id: nowhere\n  - id: big\n  - id: other\n"))
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

// TestFolders: a plan of plans and the plans it lists share one folder, whichever
// of them moves, and each answer names the others moved.
func TestFolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	folderOf := func(name string) string { t.Helper(); return f.get(t, name).Folder }
	if s, err := f.store.Create(ctx, f.project, "a", "/x", []byte(tasks("t"))); err != nil || s.Plan.Folder != "/x" || len(s.Moved) != 0 {
		t.Fatalf("created in a folder: %+v, %v", s, err)
	}
	f.create(t, "b", tasks("t"))
	f.create(t, "c", tasks("t"))
	if _, err := f.store.Create(ctx, f.project, "bad", "a.b", []byte(tasks("t"))); err == nil {
		t.Error("created in a folder with a dot")
	}

	s, err := f.store.Create(ctx, f.project, "big", "Hub", []byte("goal: g\nplans:\n  - id: a\n  - id: b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Plan.Folder != "/hub" || !reflect.DeepEqual(s.Moved, []string{"a", "b"}) || folderOf("a") != "/hub" || folderOf("b") != "/hub" {
		t.Errorf("a plan of plans created in /hub: %+v, moved %q; a in %s, b in %s", s.Plan, s.Moved, folderOf("a"), folderOf("b"))
	}

	p, changed, moved, err := f.store.Move(ctx, f.get(t, "a").ID, "/y")
	if err != nil || !changed || p.Folder != "/y" || !reflect.DeepEqual(moved, []string{"b", "big"}) {
		t.Errorf("moving a plan it lists: %+v, changed %v, moved %q, %v", p, changed, moved, err)
	}
	if _, changed, moved, err := f.store.Move(ctx, f.get(t, "big").ID, "/y/"); err != nil || changed || moved == nil || len(moved) != 0 {
		t.Errorf("moving to its folder: changed %v, moved %#v, %v", changed, moved, err)
	}

	big := f.get(t, "big")
	s, err = f.store.Update(ctx, big.ID, big.Revision, []byte("goal: g\nplans:\n  - id: a\n  - id: c\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Moved, []string{"c"}) || folderOf("c") != "/y" || folderOf("b") != "/y" {
		t.Errorf("an update listing c: moved %q; c in %s, b let go in %s", s.Moved, folderOf("c"), folderOf("b"))
	}
	big = f.get(t, "big")
	if s, err := f.store.Update(ctx, big.ID, big.Revision, []byte("goal: g\nplans:\n  - id: a\n  - id: c\n")); err != nil || s.Changed || len(s.Moved) != 0 {
		t.Errorf("an update changing nothing: %+v, %v", s, err)
	}
	// Plans from before folders may be apart; content that does not change moves
	// none of them, and says so.
	if _, err := f.db.Exec("UPDATE docs SET folder = '/q' WHERE id = ?", f.get(t, "c").ID); err != nil {
		t.Fatal(err)
	}
	if s, err := f.store.Update(ctx, big.ID, big.Revision, []byte("goal: g\nplans:\n  - id: a\n  - id: c\n")); err != nil || len(s.Moved) != 0 || folderOf("c") != "/q" {
		t.Errorf("an update changing nothing: moved %q, c in %s, %v", s.Moved, folderOf("c"), err)
	}

	f.create(t, "d", tasks("t"))
	if _, _, _, err := f.store.Move(ctx, f.get(t, "d").ID, "/q"); err != nil {
		t.Fatal(err)
	}
	if s, err := f.store.Create(ctx, f.project, "other", "Q/", []byte("goal: g\nplans:\n  - id: d\n")); err != nil || len(s.Moved) != 0 {
		t.Errorf("a plan of plans created in the folder of the plan it lists, spelled otherwise: moved %q, %v", s.Moved, err)
	}
}
