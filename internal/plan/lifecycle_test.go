package plan

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// done runs the plan named name, of the one task a, to its end.
func (f fixture) done(t *testing.T, name string) Plan {
	t.Helper()
	ctx := context.Background()
	p := f.create(t, name, tasks("a"))
	f.agent(t, "agent-"+name)
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/"+name, 0); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return f.store.HoldSlot(ctx, p.ID, "a", 1) },
		func() error { return f.store.StartTask(ctx, f.db, p.ID, "a", "agent-"+name) },
		func() error { _, err := f.store.TaskDone(ctx, p.ID, "a", "s"); return err },
		func() error { return f.store.Landed(ctx, p.ID, "a") },
		func() error { return f.store.Finish(ctx, p.ID) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	return f.get(t, name)
}

func TestCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	draft := f.create(t, "draft", tasks("a", "b"))
	if _, _, _, err := f.store.Move(ctx, draft.ID, "/hub"); err != nil {
		t.Fatal(err)
	}
	s, err := f.store.Copy(ctx, draft.ID, "draft-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Plan; c.ID == draft.ID || c.State != StateDraft || c.Revision != 1 || c.Tasks != 2 || c.Folder != "/hub" {
		t.Errorf("a draft's copy: %+v", c)
	}
	content, _, _, err := f.store.Content(ctx, s.Plan.ID, 0)
	if err != nil || string(content) != tasks("a", "b") {
		t.Errorf("the copy's content: %q, %v", content, err)
	}

	done := f.done(t, "done")
	s, err = f.store.Copy(ctx, done.ID, "done-2", new("/later"))
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Plan; c.State != StateDraft || c.Folder != "/later" || c.TasksDone != 0 {
		t.Errorf("a done plan's copy: %+v", c)
	}
	if _, err := f.store.Archive(ctx, done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Copy(ctx, done.ID, "done-3", nil); err != nil {
		t.Errorf("an archived plan's copy: %v", err)
	}

	child := f.create(t, "child", tasks("a"))
	parent := f.create(t, "parent", "goal: g\nplans:\n  - id: child\n")
	s, err = f.store.Copy(ctx, child.ID, "child-2", nil)
	if err != nil || s.Plan.Parent != "" {
		t.Errorf("a child's copy has a parent: %+v, %v", s.Plan, err)
	}
	if got := problems(t, func() error { _, err := f.store.Copy(ctx, parent.ID, "parent-2", nil); return err }()); len(got) != 1 {
		t.Errorf("a plan of plans' copy: %+v", got)
	}
	var taken *NameTakenError
	if _, err := f.store.Copy(ctx, draft.ID, "DONE", nil); !errors.As(err, &taken) {
		t.Errorf("a copy given a name taken: %v", err)
	}
	if _, err := f.store.Copy(ctx, "doc-nowhere", "x", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("a copy of no plan: %v", err)
	}
}

func TestArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	notFinished := func(err error, state string) {
		t.Helper()
		var e *NotFinishedError
		if !errors.As(err, &e) || e.Plan.State != state {
			t.Errorf("want not finished, %s: %v", state, err)
		}
	}
	draft := f.create(t, "draft", tasks("a"))
	_, err := f.store.Archive(ctx, draft.ID)
	notFinished(err, StateDraft)
	_, err = f.store.Unarchive(ctx, draft.ID)
	notFinished(err, StateDraft)

	running := f.create(t, "running", tasks("a"))
	if _, err := f.store.Start(ctx, running.ID, "main", "plan/running", 0); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Archive(ctx, running.ID)
	notFinished(err, StateRunning)
	if err := f.store.Cancel(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Archive(ctx, running.ID)
	notFinished(err, StateCancelled) // its agents are still being stopped
	if err := f.store.Close(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if archived, err := f.store.Archive(ctx, running.ID); err != nil || !archived {
		t.Errorf("a cancelled plan archived: %v, %v", archived, err)
	}

	done := f.done(t, "done")
	if changed, err := f.store.Unarchive(ctx, done.ID); err != nil || changed {
		t.Errorf("a plan not archived unarchived: %v, %v", changed, err)
	}
	before := done.UpdatedAt
	if changed, err := f.store.Archive(ctx, done.ID); err != nil || !changed {
		t.Fatalf("a done plan archived: %v, %v", changed, err)
	}
	p := f.get(t, "done")
	if p.ArchivedAt.IsZero() || p.State != StateDone || !p.UpdatedAt.After(before) {
		t.Errorf("archived: %+v", p)
	}
	if changed, err := f.store.Archive(ctx, done.ID); err != nil || changed {
		t.Errorf("archived twice: %v, %v", changed, err)
	}
	list, err := f.store.List(ctx, f.project)
	if err != nil {
		t.Fatal(err)
	}
	var archived []string
	for _, p := range list {
		if !p.ArchivedAt.IsZero() {
			archived = append(archived, p.Name)
		}
	}
	if !slices.Equal(archived, []string{"done", "running"}) {
		t.Errorf("listed as archived: %q", archived)
	}
	if changed, err := f.store.Unarchive(ctx, done.ID); err != nil || !changed || !f.get(t, "done").ArchivedAt.IsZero() {
		t.Errorf("unarchived: %v, %v", changed, err)
	}
	if changed, err := f.store.Unarchive(ctx, done.ID); err != nil || changed {
		t.Errorf("unarchived twice: %v, %v", changed, err)
	}
	if _, err := f.store.Archive(ctx, "doc-nowhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("archiving no plan: %v", err)
	}
}

// TestSlotUses: a plan shows its run's slots, with the task in each, and SlotAt
// finds the plan of a slot's path, until the slot is dropped.
func TestSlotUses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "sso", tasks("a", "b"))
	if got := f.get(t, "sso").Slots; len(got) != 0 {
		t.Errorf("a draft's slots: %+v", got)
	}
	f.agent(t, "agent-a")
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/sso", 0); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return f.store.AddSlot(ctx, p.ID, 1, "/r/.worktrees/sso-1") },
		func() error { return f.store.AddSlot(ctx, p.ID, 2, "/r/.worktrees/sso-2") },
		func() error { return f.store.HoldSlot(ctx, p.ID, "a", 1) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	want := []SlotUse{{Number: 1, Path: "/r/.worktrees/sso-1", Task: "a"}, {Number: 2, Path: "/r/.worktrees/sso-2"}}
	if got := f.get(t, "sso").Slots; !slices.Equal(got, want) {
		t.Errorf("slots %+v, want %+v", got, want)
	}
	if list, err := f.store.List(ctx, f.project); err != nil || len(list) != 1 || !slices.Equal(list[0].Slots, want) {
		t.Errorf("listed slots: %+v, %v", list, err)
	}
	run, _, err := f.store.Run(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, "sso").AsOf(run).Slots; !slices.Equal(got, want) {
		t.Errorf("slots as of the run: %+v", got)
	}
	if got, ok, err := f.store.SlotAt(ctx, "/r/.worktrees/sso-2"); err != nil || !ok || got.ID != p.ID {
		t.Errorf("the plan of a slot: %+v, %v, %v", got, ok, err)
	}
	if _, ok, err := f.store.SlotAt(ctx, "/r/.worktrees/other"); err != nil || ok {
		t.Errorf("a worktree no plan has: %v, %v", ok, err)
	}
	if err := f.store.DropSlot(ctx, p.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.store.SlotAt(ctx, "/r/.worktrees/sso-2"); err != nil || ok {
		t.Errorf("a slot dropped: %v, %v", ok, err)
	}
}
