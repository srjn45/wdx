package plan

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/gittest"
)

// family is the children of a plan of plans: a, then b, then c after a and b,
// every one a draft.
func family() []ChildRun {
	return []ChildRun{
		{Plan: Plan{Name: "a", State: StateDraft}},
		{Plan: Plan{Name: "b", State: StateDraft}, After: []string{"a"}},
		{Plan: Plan{Name: "c", State: StateDraft}, After: []string{"a", "b"}},
	}
}

// child returns children with the one called name in state, landed or not.
func child(children []ChildRun, name, state string, landed bool) []ChildRun {
	children = append([]ChildRun(nil), children...)
	for i := range children {
		if children[i].Plan.Name == name {
			children[i].Plan.State, children[i].Landed = state, landed
		}
	}
	return children
}

func TestNextParent(t *testing.T) {
	t.Parallel()
	start := func(name string) Action { return Action{Kind: ActionStartPlan, Plan: name} }
	cancel := func(name string) Action { return Action{Kind: ActionCancelPlan, Plan: name} }
	finish, closing := Action{Kind: ActionFinish}, Action{Kind: ActionClose}
	running := Run{State: StateRunning}
	stacked := Run{State: StateRunning, Definition: Definition{Stacked: true}}
	aLanded := child(family(), "a", StateDone, true)
	allDone := child(child(aLanded, "b", StateDone, true), "c", StateDone, false)
	failed := func(children []ChildRun, name string) []ChildRun {
		children = append([]ChildRun(nil), children...)
		for i := range children {
			if children[i].Plan.Name == name {
				children[i].StartFailed = true
			}
		}
		return children
	}

	tests := []struct {
		name     string
		run      Run
		closed   bool
		children []ChildRun
		want     []Action
	}{
		{"what waits for nothing starts", running, false, family(), []Action{start("a")}},
		{"nothing starts while a runs", running, false, child(family(), "a", StateRunning, false), nil},
		{"a done but not landed holds b", running, false, child(family(), "a", StateDone, false), nil},
		{"a landed starts b, not c", running, false, aLanded, []Action{start("b")}},
		{"b landed starts c", running, false, child(aLanded, "b", StateDone, true), []Action{start("c")}},
		{"one before not landed holds c", running, false, child(aLanded, "b", StateDone, false), nil},
		{"a start that failed waits for the person", running, false, failed(aLanded, "b"), nil},
		{"nothing starts in a closed project", running, true, aLanded, nil},
		{"nothing starts while paused", Run{State: StatePaused}, false, aLanded, nil},
		{"every one done finishes, landed or not", running, false, allDone, []Action{finish}},
		{"paused, every one done finishes too", Run{State: StatePaused}, false, allDone, []Action{finish}},
		{"cancelled stops those going on, then closes", Run{State: StateCancelled}, false,
			child(child(aLanded, "b", StateRunning, false), "c", StatePaused, false), []Action{cancel("b"), cancel("c"), closing}},
		{"cancelled and closed does nothing", Run{State: StateCancelled, FinishedAt: time.UnixMilli(1)}, false, aLanded, nil},
		{"done does nothing", Run{State: StateDone}, false, allDone, nil},
		{"stacked, a done but not landed starts b on a", stacked, false, child(family(), "a", StateDone, false),
			[]Action{{Kind: ActionStartPlan, Plan: "b", On: "a"}}},
		{"stacked, a landed starts b from the default branch", stacked, false, aLanded, []Action{start("b")}},
		{"stacked, c after a and b waits for both to land", stacked, false,
			child(child(aLanded, "b", StateDone, false), "a", StateDone, false), nil},
		{"stacked, c after a and b starts once both landed", stacked, false, child(aLanded, "b", StateDone, true), []Action{start("c")}},
		{"stacked, a start that failed waits for the person", stacked, false, failed(child(family(), "a", StateDone, false), "b"), nil},
	}
	for _, tt := range tests {
		got := NextParent(ParentFacts{Run: tt.run, ProjectOpen: !tt.closed, Children: tt.children})
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestChildWaiting(t *testing.T) {
	t.Parallel()
	running := Run{State: StateRunning}
	waiting := func(parent Run, children []ChildRun, name string) bool {
		for _, c := range children {
			if c.Plan.Name == name {
				return c.Waiting(parent, children)
			}
		}
		t.Fatalf("no %s", name)
		return false
	}
	aDone := child(family(), "a", StateDone, false)
	for _, tt := range []struct {
		name     string
		parent   Run
		children []ChildRun
		child    string
		want     bool
	}{
		{"the one before it done, not landed", running, aDone, "b", true},
		{"paused, the same", Run{State: StatePaused}, aDone, "b", true},
		{"the one before it landed", running, child(family(), "a", StateDone, true), "b", false},
		{"one before it not done", running, aDone, "c", false},
		{"the one before it running", running, child(family(), "a", StateRunning, false), "b", false},
		{"waiting for nothing", running, family(), "a", false},
		{"its parent cancelled", Run{State: StateCancelled}, aDone, "b", false},
		{"its parent a draft", Run{State: StateDraft}, aDone, "b", false},
		{"started", running, child(aDone, "b", StateRunning, false), "b", false},
		{"stacked, the one before it done, not landed", Run{State: StateRunning, Definition: Definition{Stacked: true}}, aDone, "b", false},
		{"stacked, after two, one not landed", Run{State: StateRunning, Definition: Definition{Stacked: true}},
			child(child(family(), "a", StateDone, true), "b", StateDone, false), "c", true},
	} {
		if got := waiting(tt.parent, tt.children, tt.child); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
	failed := family()
	failed[0].StartFailed = true
	if !failed[0].Waiting(running, failed) {
		t.Error("a start that failed does not wait for the person")
	}
}

// finishRun runs the plan p, of the one task a, to its end.
func (f fixture) finishRun(t *testing.T, p Plan) {
	t.Helper()
	ctx := context.Background()
	f.agent(t, "agent-"+p.Name)
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/"+p.Name, 0, ""); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return f.store.HoldSlot(ctx, p.ID, "a", 1) },
		func() error { return f.store.StartTask(ctx, f.db, p.ID, "a", "agent-"+p.Name) },
		func() error { _, err := f.store.TaskDone(ctx, p.ID, "a", "s"); return err },
		func() error { return f.store.Landed(ctx, p.ID, "a") },
		func() error { return f.store.Finish(ctx, p.ID) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAParentsRun: a plan of plans' run has no tasks; it counts its plans done,
// moves on as each of them changes, finishes, and is archived with those over.
func TestAParentsRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a := f.create(t, "a", tasks("a"))
	f.create(t, "b", tasks("a"))
	big := f.create(t, "big", "goal: g\nplans:\n  - id: a\n  - id: b\n    after: [a]\n")
	run, err := f.store.Start(ctx, big.ID, "main", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StateRunning || len(run.Tasks) != 0 || run.Branch != "" || len(run.Definition.Plans) != 2 {
		t.Errorf("the run: %+v", run)
	}
	last := f.get(t, "big").UpdatedAt
	f.finishRun(t, a)
	got := f.get(t, "big")
	if got.PlansDone != 1 || got.State != StateRunning || got.TasksDone != 0 {
		t.Errorf("one of its plans done: %+v", got)
	}
	if !got.UpdatedAt.After(last) {
		t.Errorf("its plan's run did not move it on: %v, was %v", got.UpdatedAt, last)
	}
	if list, err := f.store.List(ctx, a.ProjectID); err != nil || list[2].Name != "big" || list[2].PlansDone != 1 {
		t.Errorf("listed: %+v, %v", list, err)
	}

	if err := f.store.Cancel(ctx, big.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(ctx, big.ID); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.store.Archive(ctx, big.ID); !changed || err != nil {
		t.Fatalf("archived: %v, %v", changed, err)
	}
	if f.get(t, "a").ArchivedAt.IsZero() || !f.get(t, "b").ArchivedAt.IsZero() {
		t.Errorf("archived with it: a %v, b, a draft, %v", f.get(t, "a").ArchivedAt, f.get(t, "b").ArchivedAt)
	}
	if changed, err := f.store.Unarchive(ctx, big.ID); !changed || err != nil {
		t.Fatalf("unarchived: %v, %v", changed, err)
	}
	if !f.get(t, "a").ArchivedAt.IsZero() {
		t.Error("its plan stayed archived")
	}
}

// TestAParentFinishes: Finish takes a plan of plans, which has no task to wait for.
func TestAParentFinishes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.create(t, "a", tasks("a"))
	big := f.create(t, "big", "goal: g\nplans:\n  - id: a\n")
	if _, err := f.store.Start(ctx, big.ID, "main", "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Finish(ctx, big.ID); err != nil {
		t.Fatal(err)
	}
	if p := f.get(t, "big"); p.State != StateDone {
		t.Errorf("finished: %+v", p)
	}
}

// TestLanded: a plan's work is in the default branch when its branch's tip is, or
// its branch is gone.
func TestLanded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := gittest.Repo(t, "app")
	gittest.Git(t, repo, "branch", "-M", "main")
	gittest.Git(t, repo, "switch", "-q", "-c", "plan/sso")
	gittest.Git(t, repo, "commit", "-q", "--allow-empty", "-m", "sso")
	gittest.Git(t, repo, "switch", "-q", "main")
	for _, tt := range []struct {
		name string
		do   []string
		want bool
	}{
		{"not merged", nil, false},
		{"merged", []string{"merge", "-q", "--ff-only", "plan/sso"}, true},
		{"gone", []string{"branch", "-q", "-D", "plan/sso"}, true},
	} {
		if tt.do != nil {
			gittest.Git(t, repo, tt.do...)
		}
		if got, err := landed(ctx, repo, "sso", "main"); err != nil || got != tt.want {
			t.Errorf("%s: %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
}
