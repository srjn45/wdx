package tide

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/runtest"
)

func archived(p api.Plan, state api.PlanState) api.Plan {
	at := time.UnixMilli(1).UTC()
	p.State, p.ArchivedAt = state, &at
	return p
}

// TestArchivedPlansHidden: an archived plan leaves the section and its count, a plan
// of plans' too, and the filter finds it, muted, saying so.
func TestArchivedPlansHidden(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("pawc")
	a, b := aPlan("doc-a", "auth-a", 1, 2), archived(aPlan("doc-b", "auth-b", 1, 1), api.PlanStateDone)
	auth := aParent("doc-auth", "auth", &a, &b)
	old := planIn(archived(aPlan("doc-old", "old", 1, 1), api.PlanStateCancelled), "/gone")
	failed := int64(1)
	old.TasksFailed = &failed
	setPlans(m, auth, a, b, old, aPlan("doc-sso", "sso", 1, 4))
	m.moveToPlan(1)
	var names []string
	for _, r := range m.planRows()[1:] { // after the selector
		names = append(names, r.plan.Name+r.place.folder)
	}
	if !slices.Equal(names, []string{"auth", "auth-a", "sso"}) {
		t.Errorf("rows %q", names)
	}
	wantText(t, m, "− Plans                   3")
	if m.folderNeedsYou("/gone") {
		t.Error("an archived plan says it needs you")
	}

	m.list().filter.text = "o"
	names = nil
	for _, r := range m.planRows()[1:] {
		names = append(names, r.plan.Name+r.place.folder)
	}
	if !slices.Equal(names, []string{"/gone", "old", "sso"}) {
		t.Errorf("filtered rows %q", names)
	}
	m.list().filter.text = "auth-b"
	if rows := m.planRows(); len(rows) != 3 || rows[2].plan.Name != "auth-b" {
		t.Errorf("the filter does not find an archived plan of a plan of plans: %+v", rows)
	}
	m.list().filter.text = "old"
	rows := m.planRows()
	if got := strings.TrimRight(planLine(m.st, rows[2], plansGoingOn, false, false, false, 40), " "); got != "    └ old cancelled, archived 1 task" {
		t.Errorf("an archived plan's row: %q", got)
	}
}

// lifecycleRequest is what the stub daemon of lifecycleTo was asked.
type lifecycleRequest struct{ path, name string }

// lifecycleTo points m's client at a daemon of its own, which archives, unarchives
// and copies, refusing a copy named taken, and sends on what it was asked.
func lifecycleTo(t *testing.T, m *Model) <-chan lifecycleRequest {
	t.Helper()
	socket := filepath.Join(runtest.Dir(t), "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	asked := make(chan lifecycleRequest, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/v1/plans/") {
			http.NotFound(w, r) // what else tide asks for is not this test's
			return
		}
		var body api.CopyPlan
		_ = json.NewDecoder(r.Body).Decode(&body)
		asked <- lifecycleRequest{path: r.URL.Path, name: body.Name}
		if body.Name == "taken" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.NewProblem(api.ErrorCodeNameTaken, `name "taken" is already used by plan taken`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/copies") {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.PlanResult{Result: api.ResultCreated, Plan: aPlan("doc-copy", body.Name, 1, 1)})
			return
		}
		_ = json.NewEncoder(w).Encode(api.PlanResult{Result: api.ResultArchived, Plan: aPlan("doc-sso", "sso", 1, 1)})
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	m.client = client.New(client.Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	return asked
}

// TestPlanTabArchives: a in a plan's tab archives a done or cancelled plan and
// unarchives an archived one, asking nothing; it does nothing to one going on.
func TestPlanTabArchives(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	asked := lifecycleTo(t, m)
	m.moveToPlan(4) // sso
	keys(m, "enter")
	running := aPlan("doc-sso", "sso", 2, 2)
	running.State = api.PlanStateRunning
	content := api.PlanContent{Plan: running, Revision: 2, Run: &api.PlanRun{State: api.PlanStateRunning, Branch: "plan/sso"},
		Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Tasks: &[]api.PlanTask{}}}
	loadPlan(m, "doc-sso", content)
	m.layout.setFocus(agent)
	if _, took := m.planKey(tea.KeyPressMsg{Code: 'a', Text: "a"}); took {
		t.Error("a takes a running plan")
	}

	content.Plan = aPlan("doc-sso", "sso", 2, 2)
	none := int64(0)
	content.Plan.State, content.Plan.TasksDone, content.Run.State = api.PlanStateCancelled, &none, api.PlanStateCancelled
	loadPlan(m, "doc-sso", content)
	wantText(t, m, "✗ cancelled", "a archive  y copy")
	keys(m, "a")
	if got := <-asked; got.path != "/v1/plans/id:doc-sso/archive" {
		t.Errorf("asked %+v", got)
	}
	if m.confirm != nil || m.statusNotice != "plan sso archived" {
		t.Errorf("archiving asked %+v, said %q", m.confirm, m.statusNotice)
	}

	content.Plan = archived(content.Plan, api.PlanStateCancelled)
	loadPlan(m, "doc-sso", content)
	wantText(t, m, "✗ cancelled archived", "a unarchive  y copy")
	keys(m, "a")
	if got := <-asked; got.path != "/v1/plans/id:doc-sso/unarchive" {
		t.Errorf("asked %+v", got)
	}
	if m.statusNotice != "plan sso unarchived" {
		t.Errorf("said %q", m.statusNotice)
	}
}

// TestPlanTabCopies: y opens a popup with the first name free, a refusal stays there
// to correct, and the copy's tab opens; on a plan of plans it says its plans not
// done go with it.
func TestPlanTabCopies(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	asked := lifecycleTo(t, m)
	setPlans(m, append(m.plans(), aPlan("doc-sso-2", "sso-2", 1, 1))...)
	m.moveToPlan(slices.IndexFunc(m.planRows(), func(r planRow) bool { return r.plan.Name == "sso" }))
	keys(m, "enter")
	loadPlan(m, "doc-sso", api.PlanContent{Plan: aPlan("doc-sso", "sso", 2, 4), Revision: 2,
		Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Tasks: &[]api.PlanTask{}}})
	wantText(t, m, "r run  y copy")
	m.layout.setFocus(agent)
	keys(m, "y")
	f, ok := m.openTop().(*fieldScreen)
	if !ok || f.heading != "Copy plan" || f.value != "sso-3" {
		t.Fatalf("the popup: %+v", m.openTop())
	}
	keys(m, "backspace", "backspace", "backspace", "backspace", "backspace", "taken", "enter")
	if got := <-asked; got.path != "/v1/plans/id:doc-sso/copies" || got.name != "taken" {
		t.Errorf("asked %+v", got)
	}
	if f, ok := m.openTop().(*fieldScreen); !ok || !strings.Contains(f.failure, "already used") || f.busy {
		t.Fatalf("a refused name: %+v", m.openTop())
	}
	keys(m, "backspace", "backspace", "backspace", "backspace", "backspace", "again", "enter")
	if got := <-asked; got.name != "again" {
		t.Errorf("asked %+v", got)
	}
	if m.openTop() != nil || m.list().active != planTab("doc-copy") || m.statusNotice != "plan again created from sso" {
		t.Errorf("once copied: popup %+v, tab %q, said %q", m.openTop(), m.list().active, m.statusNotice)
	}

	if got := m.freeCopyName("sso-2"); got != "sso-3" {
		t.Errorf("the name for a copy of a copy: %q", got)
	}
	m.layout.setFocus(project)
	m.moveToPlan(1) // auth, a plan of plans
	keys(m, "enter")
	auth := m.plans()[0]
	loadPlan(m, "doc-auth", api.PlanContent{Plan: auth, Revision: 1, Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Plans: &[]api.PlanChild{}}})
	m.layout.setFocus(agent)
	keys(m, "y")
	if f, ok := m.openTop().(*fieldScreen); !ok || f.value != "auth-2" || !strings.Contains(f.prompt, "with its plans not done") {
		t.Errorf("y on a plan of plans: popup %+v", m.openTop())
	}
}
