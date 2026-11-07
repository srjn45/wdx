package tide

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
)

func inState(p api.Plan, state api.PlanState) api.Plan { p.State = state; return p }

// plansViewModel is a model whose project has the plan of plans auth, running,
// with auth-a done and auth-b a draft under it, the draft sso, old done in the
// folder /over with a task failed, gone cancelled and arch done and archived, with
// the Plans section first and the cursor on the selector.
func plansViewModel(t *testing.T) *Model {
	t.Helper()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("pawc")
	a, b := inState(aPlan("doc-a", "auth-a", 1, 2), api.PlanStateDone), aPlan("doc-b", "auth-b", 1, 1)
	auth := inState(aParent("doc-auth", "auth", &a, &b), api.PlanStateRunning)
	old := planIn(inState(aPlan("doc-old", "old", 1, 1), api.PlanStateDone), "/over")
	failed := int64(1)
	old.TasksFailed = &failed
	setPlans(m, auth, a, b, aPlan("doc-sso", "sso", 1, 4), old,
		inState(aPlan("doc-gone", "gone", 1, 1), api.PlanStateCancelled), archived(aPlan("doc-arch", "arch", 1, 1), api.PlanStateDone))
	m.moveToPlan(0)
	return m
}

// planRowNames are the rows of the Plans section: > for the selector, a folder's
// path, a plan's name.
func planRowNames(m *Model) []string {
	var names []string
	for _, r := range m.planRows() {
		switch {
		case r.selector:
			names = append(names, ">")
		case r.place.folder != "":
			names = append(names, r.place.folder)
		default:
			names = append(names, r.plan.Name)
		}
	}
	return names
}

// TestPlansViewSelector: the selector goes through the views with ← → and h l, going
// round, each showing its plans, a plan of plans with its own whatever their state,
// the folders with nothing in the view hidden, its count and needs you.
func TestPlansViewSelector(t *testing.T) {
	t.Parallel()
	m := plansViewModel(t)
	l := m.list()
	wantText(t, m, "< Draft & in progress >", "− Plans                     4")
	if got := planRowNames(m); !slices.Equal(got, []string{">", "auth", "auth-a", "auth-b", "sso"}) {
		t.Errorf("Draft & in progress: %q", got)
	}
	if !strings.Contains(statusRow(m), "←→ view") {
		t.Errorf("hint on the selector %q", statusRow(m))
	}
	if m.folderNeedsYou("/over") {
		t.Error("a folder needs you for a plan not in the view")
	}
	keys(m, "enter")
	if isPlanTab(l.active) {
		t.Errorf("Enter on the selector opened %q", l.active)
	}

	keys(m, "right")
	wantText(t, m, "< Completed >", "− Plans                     2")
	if got := planRowNames(m); !slices.Equal(got, []string{">", "/over", "old", "gone"}) {
		t.Errorf("Completed: %q", got)
	}
	if !m.folderNeedsYou("/over") {
		t.Error("a folder of the view does not need you for its plan")
	}
	keys(m, "l") // round to the first
	wantText(t, m, "< All >", "− Plans                     6")
	if got := planRowNames(m); !slices.Equal(got, []string{">", "/over", "old", "auth", "auth-a", "auth-b", "sso", "gone"}) {
		t.Errorf("All: %q", got)
	}
	keys(m, "h") // round to the last
	if l.plansView != plansCompleted {
		t.Errorf("h from All: %q", l.plansView)
	}
	keys(m, "left")
	if l.plansView != plansGoingOn || l.planCursor != planSelectorKey {
		t.Errorf("← from Completed: %q, cursor %q", l.plansView, l.planCursor)
	}

	// None in the view: the selector alone, and a line saying so.
	setPlans(m, inState(aPlan("doc-old", "old", 1, 1), api.PlanStateDone))
	wantText(t, m, "< Draft & in progress >", "no plans in this view", "− Plans                     0")
}

// TestPlansViewClick: a click on < goes to the view before, on > to the one after.
func TestPlansViewClick(t *testing.T) {
	t.Parallel()
	m := plansViewModel(t)
	l := m.list()
	keys(m, "down") // off the selector, which a click puts the cursor back on
	selector := 2   // the frame, the heading
	m.Update(clickAt(1+2+ansi.StringWidth("< Draft & in progress "), selector))
	if l.plansView != plansCompleted || l.planCursor != planSelectorKey {
		t.Errorf("a click on >: %q, cursor %q", l.plansView, l.planCursor)
	}
	m.Update(clickAt(1+2, selector))
	m.Update(clickAt(1+2, selector))
	if l.plansView != plansAll {
		t.Errorf("two clicks on <: %q", l.plansView)
	}
	m.Update(clickAt(1+2+2, selector)) // the name
	if l.plansView != plansAll {
		t.Errorf("a click on the name: %q", l.plansView)
	}
}

// TestPlansViewFilter: / finds plans in every view, archived ones too.
func TestPlansViewFilter(t *testing.T) {
	t.Parallel()
	m := plansViewModel(t)
	l := m.list()
	l.filter.text = "o"
	if got := planRowNames(m); !slices.Equal(got, []string{">", "/over", "old", "sso", "gone"}) {
		t.Errorf("Draft & in progress filtered: %q", got)
	}
	l.filter.text = ""
	keys(m, "right") // Completed
	l.filter.text = "a"
	if got := planRowNames(m); !slices.Equal(got, []string{">", "auth", "auth-a", "auth-b", "arch"}) {
		t.Errorf("Completed filtered: %q", got)
	}
}

// TestPlansViewKept: the choice is kept in the project's view as plans_view, none
// for Draft & in progress, and read back, a key missing or unknown meaning Draft &
// in progress.
func TestPlansViewKept(t *testing.T) {
	t.Parallel()
	m := plansViewModel(t)
	l := m.list()
	if _, v, _ := m.currentProjectView(); v.PlansView != "" {
		t.Errorf("Draft & in progress kept as %q", v.PlansView)
	}
	keys(m, "right")
	if _, v, _ := m.currentProjectView(); v.PlansView != "completed" {
		t.Errorf("Completed kept as %q", v.PlansView)
	}
	keys(m, "right")
	if _, v, _ := m.currentProjectView(); v.PlansView != "all" {
		t.Errorf("All kept as %q", v.PlansView)
	}
	for key, want := range map[string]plansView{"completed": plansCompleted, "all": plansAll, "": plansGoingOn, "later": plansGoingOn} {
		m.applyProjectView("proj-demo", l, projectView{PlansView: key})
		if l.plansView != want {
			t.Errorf("%q read back as %q", key, l.plansView)
		}
	}
}

// TestPlansViewCursor: the cursor on a plan that leaves the view, as it finishes,
// goes to the nearest row.
func TestPlansViewCursor(t *testing.T) {
	t.Parallel()
	m := plansViewModel(t)
	l := m.list()
	keys(m, "down", "down", "down", "down") // sso
	if l.planCursor != "doc-sso" {
		t.Fatalf("on %q", l.planCursor)
	}
	plans := slices.Clone(m.plans())
	for i := range plans {
		if plans[i].Name == "sso" {
			plans[i].State = api.PlanStateDone
		}
	}
	setPlans(m, plans...)
	if l.section != sectionPlans || l.planCursor != "doc-b" {
		t.Errorf("sso done: the cursor on %v %q", l.section, l.planCursor)
	}
}
