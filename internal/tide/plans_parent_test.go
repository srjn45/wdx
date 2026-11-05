package tide

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/runtest"
)

// runningParent is a model whose plan of plans auth runs, auth-a done and auth-b
// waiting for the person, with auth's tab open on its definition and the focus in
// it.
func runningParent(t *testing.T) *Model {
	t.Helper()
	m := plansModel(t)
	done, one := int64(1), int64(2)
	a, b := m.plans()[1], m.plans()[2]
	a.State, a.TasksDone = api.PlanStateDone, &one
	auth := m.plans()[0]
	auth.State, auth.PlansDone, auth.PlansWaiting = api.PlanStateRunning, &done, &[]string{"auth-b"}
	setPlans(m, auth, a, b, m.plans()[3])
	m.moveToPlan(0)
	keys(m, "enter")
	tasks := int64(2)
	loadPlan(m, "doc-auth", api.PlanContent{Plan: auth, Revision: 1, Run: &api.PlanRun{State: api.PlanStateRunning},
		Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Plans: &[]api.PlanChild{
			{Id: "auth-b", State: api.PlanStateDraft, Tasks: 1, After: []string{"auth-a"}, Waiting: true},
			{Id: "auth-a", State: api.PlanStateDone, Tasks: tasks, TasksDone: &one},
		}}})
	m.layout.setFocus(agent)
	return m
}

// TestPlanOfPlansShown: a running plan of plans says how many of its plans are
// done, and that it needs you while one of them waits for you, as that one does;
// its tab says each plan's state, a waiting one what r does.
func TestPlanOfPlansShown(t *testing.T) {
	t.Parallel()
	m := runningParent(t)
	rows := m.planRows()
	if !rows[0].needsYou || !rows[1].needsYou || rows[2].needsYou {
		t.Errorf("needs you: auth %v, auth-b %v, auth-a %v", rows[0].needsYou, rows[1].needsYou, rows[2].needsYou)
	}
	if got := strings.TrimRight(planLine(m.st, rows[0], false, false, false, 60), " "); got != "  − auth running 1/2 plans needs you" {
		t.Errorf("the parent's row: %q", got)
	}
	wantText(t, m, "running, 1/2 plans done: p pauses it, c cancels it", "waiting for you · 1 task", "done · 2/2")
	m.values.cursor = slices.IndexFunc(m.planTabRows(), func(r valueRow) bool { return r.key == "auth-b" })
	wantText(t, m, "r starts it, once the plans before it are in the default branch")

	waiting := m.plans()[0]
	waiting.PlansWaiting = &[]string{}
	setPlans(m, waiting, m.plans()[1], m.plans()[2], m.plans()[3])
	if rows := m.planRows(); rows[0].needsYou || rows[1].needsYou {
		t.Errorf("none waiting: auth %v, auth-b %v", rows[0].needsYou, rows[1].needsYou)
	}
}

// runAsked is what the stub daemon of runTo was asked to run.
type runAsked struct{ path string }

// runTo points m's client at a daemon of its own that runs plans, refusing one
// whose id is refused, and sends on what it was asked.
func runTo(t *testing.T, m *Model, refused string) <-chan runAsked {
	t.Helper()
	socket := filepath.Join(runtest.Dir(t), "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	asked := make(chan runAsked, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/run") {
			http.NotFound(w, r) // what else tide asks for is not this test's
			return
		}
		asked <- runAsked{path: r.URL.Path}
		if strings.Contains(r.URL.Path, refused) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.NewProblem(api.ErrorCodePlanNotLanded, "plan auth-b waits for the work of auth-a, not in main"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.PlanResult{Result: api.ResultStarted, Plan: aPlan("doc-b", "auth-b", 1, 1)})
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	m.client = client.New(client.Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	return asked
}

// TestPlanOfPlansAnswered: r on a plan waiting for you in its parent's tab starts
// it, or says why not; on one not waiting it does nothing, and a plan a plan of
// plans lists does not run from its own tab.
func TestPlanOfPlansAnswered(t *testing.T) {
	t.Parallel()
	m := runningParent(t)
	asked := runTo(t, m, "nowhere")
	rows := m.planTabRows()
	m.values.cursor = slices.IndexFunc(rows, func(r valueRow) bool { return r.key == "auth-a" })
	if _, took := m.planKey(keyMsgs("r")[0]); took {
		t.Error("r takes a plan that does not wait")
	}
	m.values.cursor = slices.IndexFunc(rows, func(r valueRow) bool { return r.key == "auth-b" })
	keys(m, "r")
	if got := <-asked; got.path != "/v1/plans/id:doc-b/run" {
		t.Errorf("asked %+v", got)
	}
	if m.confirm != nil || m.statusNotice != "plan auth-b started" {
		t.Errorf("the answer asked %+v, said %q", m.confirm, m.statusNotice)
	}

	m = runningParent(t)
	asked = runTo(t, m, "doc-b")
	m.values.cursor = slices.IndexFunc(m.planTabRows(), func(r valueRow) bool { return r.key == "auth-b" })
	keys(m, "r")
	<-asked
	if !strings.Contains(m.statusNotice, "waits for the work of auth-a") {
		t.Errorf("a refused answer said %q", m.statusNotice)
	}

	m.layout.setFocus(project)
	m.moveToPlan(1) // auth-b
	keys(m, "enter")
	b := m.plans()[2]
	loadPlan(m, "doc-b", api.PlanContent{Plan: b, Revision: 1, Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Tasks: &[]api.PlanTask{}}})
	m.layout.setFocus(agent)
	wantText(t, m, "draft: it runs with auth, y copies it")
	if _, took := m.planKey(keyMsgs("r")[0]); took {
		t.Error("r runs a plan a plan of plans lists from its own tab")
	}
}

// TestPlanOfPlansRunAsked: r on a draft plan of plans asks first, saying how its
// plans run.
func TestPlanOfPlansRunAsked(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	m.moveToPlan(0)
	keys(m, "enter")
	loadPlan(m, "doc-auth", api.PlanContent{Plan: m.plans()[0], Revision: 1, Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Plans: &[]api.PlanChild{}}})
	m.layout.setFocus(agent)
	wantText(t, m, "draft: r runs it, y copies it")
	keys(m, "r")
	if m.confirm == nil || m.confirm.r.kind != runPlanKind {
		t.Fatalf("r on a draft plan of plans: %+v", m.confirm)
	}
	wantText(t, m, "Each plan it lists runs from the default branch once the plans")
}
