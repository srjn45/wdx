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

func inFolderOf(d api.Doc, folder string) api.Doc { d.Folder = folder; return d }

func planIn(p api.Plan, folder string) api.Plan { p.Folder = folder; return p }

// folderModel is a model whose project has docs at the top and in folders, with
// the Docs section first and the cursor on its first row.
func folderModel(t *testing.T) *Model {
	t.Helper()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("dpawc")
	setDocs(m,
		inFolderOf(aDoc("doc-spec", "spec", api.DocStatusWip, 1), "/hub/initial"),
		inFolderOf(aDoc("doc-api", "api", api.DocStatusWip, 1), "/hub"),
		inFolderOf(aDoc("doc-zed", "zed", api.DocStatusWip, 1), "/"),
		inFolderOf(aDoc("doc-arch", "arch", api.DocStatusWip, 1), "/design"),
	)
	m.moveToDoc(0)
	m.list().viewRead = true
	return m
}

func docRowNames(m *Model) []string {
	var names []string
	for _, r := range m.docRows() {
		if r.place.folder != "" {
			names = append(names, r.place.folder+"/")
		} else {
			names = append(names, r.doc.Name)
		}
	}
	return names
}

// TestDocsInFolders: the Docs section is a tree, the folders first at each level,
// and what is at the top has no folder row.
func TestDocsInFolders(t *testing.T) {
	t.Parallel()
	m := folderModel(t)
	if got, want := docRowNames(m), []string{"/design/", "arch", "/hub/", "/hub/initial/", "spec", "api", "zed"}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
	lines := paneLines(m)
	for _, want := range []string{" ▸− design", "    └ arch", "  − hub", "    ├ − initial", "    │   └ spec", "    └ api", "    zed"} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimPrefix(lightSides(l), "│"), want) }) {
			t.Errorf("no line starting %q in:\n%s", want, strings.Join(lines, "\n"))
		}
	}

	flat := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	flat.sections = sectionOrder("dawc")
	setDocs(flat, aDoc("doc-a", "a", api.DocStatusWip, 1), inFolderOf(aDoc("doc-b", "b", api.DocStatusWip, 1), "/"))
	flat.moveToDoc(0)
	wantText(t, flat, "▸a wip", "  b wip")
}

// TestDocFolderFolds: ← and → fold a folder, ← on a doc folds its folder, Enter and
// a click fold too, and the folds are kept in the project's view.
func TestDocFolderFolds(t *testing.T) {
	t.Parallel()
	m := folderModel(t)
	m.moveToDoc(slices.Index(docRowNames(m), "spec"))
	keys(m, "left")
	if l := m.list(); l.docCursor != "folder:/hub/initial" || !l.docFolders["/hub/initial"] {
		t.Fatalf("← on a doc in a folder: cursor %q, folded %v", l.docCursor, l.docFolders)
	}
	if got := docRowNames(m); slices.Contains(got, "spec") {
		t.Errorf("a folded folder shows what it holds: %q", got)
	}
	wantText(t, m, "+ initial")
	keys(m, "right")
	if got := docRowNames(m); !slices.Contains(got, "spec") || m.list().docFolders["/hub/initial"] {
		t.Errorf("→ on a folded folder: rows %q, folded %v", got, m.list().docFolders)
	}
	m.moveToDoc(slices.Index(docRowNames(m), "/hub/"))
	keys(m, "enter")
	if got := docRowNames(m); !slices.Equal(got, []string{"/design/", "arch", "/hub/", "zed"}) {
		t.Errorf("Enter on a folder: rows %q", got)
	}
	if hint := (&projectScreen{m: m}).sectionHint(); hint != folderHint {
		t.Errorf("the hint on a folder: %q", hint)
	}
	keys(m, "left") // already folded: nothing changes
	if !m.list().docFolders["/hub"] {
		t.Error("← on a folded folder unfolded it")
	}
	m.moveToDoc(slices.Index(docRowNames(m), "zed"))
	keys(m, "left") // at the top: no folder to fold
	if l := m.list(); l.docCursor != "doc-zed" || len(l.docFolders) != 1 {
		t.Errorf("← on a doc at the top: cursor %q, folded %v", l.docCursor, l.docFolders)
	}

	_, v, ok := m.currentProjectView()
	if !ok || !slices.Equal(v.DocsFolded, []string{"/hub"}) {
		t.Fatalf("the view kept %v", v.DocsFolded)
	}
	again := folderModel(t)
	again.applyProjectView("proj-1", again.list(), v)
	if got := docRowNames(again); slices.Contains(got, "api") {
		t.Errorf("the folds were not restored: %q", got)
	}

	clicked := folderModel(t)
	clicked.moveToDoc(slices.Index(docRowNames(clicked), "zed"))
	clicked.openDoc(false)
	clicked.moveToDoc(slices.Index(docRowNames(clicked), "/design/"))
	clicked.openDoc(false)
	if got := docRowNames(clicked); slices.Contains(got, "arch") || clicked.list().active != "doc:doc-zed" {
		t.Errorf("Enter on a folder opened a tab or did not fold: rows %q, tab %q", got, clicked.list().active)
	}
}

// TestDocFolderActive: a folded folder holding the doc shown carries its mark.
func TestDocFolderActive(t *testing.T) {
	t.Parallel()
	m := folderModel(t)
	m.moveToDoc(slices.Index(docRowNames(m), "arch"))
	m.openDoc(false)
	rows := m.docRows()
	if i := slices.IndexFunc(rows, func(r docRow) bool { return r.place.folder == "/design" }); m.docRowShows(rows[i], m.list().active) {
		t.Error("an open folder holding the doc shown is marked, as well as the doc")
	}
	keys(m, "left")
	rows = m.docRows()
	i := slices.IndexFunc(rows, func(r docRow) bool { return r.place.folder == "/design" })
	if !m.docRowShows(rows[i], m.list().active) {
		t.Error("a folded folder holding the doc shown is not marked")
	}
	if j := slices.IndexFunc(rows, func(r docRow) bool { return r.place.folder == "/hub" }); m.docRowShows(rows[j], m.list().active) {
		t.Error("an open folder not holding it is marked")
	}

	m.moveToDoc(slices.Index(docRowNames(m), "spec"))
	m.openDoc(false)
	m.moveToDoc(slices.Index(docRowNames(m), "/hub/"))
	keys(m, "left")
	rows = m.docRows()
	if i := slices.IndexFunc(rows, func(r docRow) bool { return r.place.folder == "/hub" }); !m.docRowShows(rows[i], m.list().active) {
		t.Error("a folded folder holding the doc shown in a folder under it is not marked")
	}
}

// TestDocFolderFilter: the filter matches docs, not folders, and shows each match
// under its folders unfolded.
func TestDocFolderFilter(t *testing.T) {
	t.Parallel()
	m := folderModel(t)
	m.list().docFolders = map[string]bool{"/hub": true}
	m.list().filter.text = "spec"
	if got := docRowNames(m); !slices.Equal(got, []string{"/hub/", "/hub/initial/", "spec"}) {
		t.Errorf("filtered rows %q", got)
	}
	m.list().filter.text = "hub"
	if got := docRowNames(m); len(got) != 0 {
		t.Errorf("the filter matched a folder: %q", got)
	}
}

// TestPlansInFolders: a plan of plans keeps its plans under it, in the folder they
// share; a folded folder says when a plan in it needs the person.
func TestPlansInFolders(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("pdawc")
	a, b := planIn(aPlan("doc-a", "auth-a", 1, 2), "/hub"), planIn(aPlan("doc-b", "auth-b", 1, 1), "/hub")
	auth := planIn(aParent("doc-auth", "auth", &a, &b), "/hub")
	failed := int64(1)
	sso := planIn(aPlan("doc-sso", "sso", 1, 4), "/later")
	sso.TasksFailed = &failed
	setPlans(m, auth, a, b, sso, aPlan("doc-top", "top", 1, 1))
	m.moveToPlan(1)
	m.list().viewRead = true
	var got []string
	for _, r := range m.planRows()[1:] { // after the selector
		got = append(got, treeLead(r.place)+r.place.name()+r.plan.Name)
	}
	if want := []string{"− hub", "  └ auth", "      ├ auth-a", "      └ auth-b", "− later", "  └ sso", "  top"}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
	wantPlanLines(t, m, "− hub", "  └ − auth draft 2 plans", "      ├ auth-a draft 2 tasks", "      └ auth-b draft 1 task",
		"− later", "  └ sso 4 tasks needs you", "  top draft 1 task")

	m.moveToPlan(5)
	keys(m, "left")
	wantText(t, m, "+ later needs you")
	if m.list().planCursor != "folder:/later" {
		t.Errorf("the cursor after folding: %q", m.list().planCursor)
	}
	if _, ok := m.cursorPlan(); ok {
		t.Error("a folder's row is taken for a plan")
	}
	m.moveToPlan(slices.IndexFunc(m.planRows(), func(r planRow) bool { return r.plan.Name == "auth-b" }))
	keys(m, "left") // on a plan of a plan of plans: folds that plan of plans, as before
	if !m.list().plansFolded["doc-auth"] || m.list().planFolders["/hub"] {
		t.Errorf("← on auth-b: plans folded %v, folders folded %v", m.list().plansFolded, m.list().planFolders)
	}
	keys(m, "left") // on the folded plan of plans: folds its folder
	if !m.list().planFolders["/hub"] || m.list().planCursor != "folder:/hub" {
		t.Errorf("← on a folded plan of plans: folders folded %v, cursor %q", m.list().planFolders, m.list().planCursor)
	}
	m.clickPlan(1, 2, 40)
	if m.list().planFolders["/hub"] {
		t.Error("a click on a folded folder left it folded")
	}
	_, v, _ := m.currentProjectView()
	if !slices.Equal(v.PlansFolded, []string{"/later"}) {
		t.Errorf("the view kept %v", v.PlansFolded)
	}
	m.list().filter.text = "sso"
	if rows := m.planRows(); len(rows) != 3 || rows[2].plan.Name != "sso" {
		t.Errorf("the filter does not show a match in a folded folder: %+v", rows)
	}
}

// TestPlanChildrenNested: the plans of a plan of plans are a level of the tree
// under it, wherever it is, with the lines of the rows that follow it, the last
// shown taking the end branch.
func TestPlanChildrenNested(t *testing.T) {
	t.Parallel()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("pdawc")
	one, two, smoke := planIn(aPlan("doc-c1", "can-1", 1, 1), "/hub/deploy"), planIn(aPlan("doc-c2", "can-2", 1, 1), "/hub/deploy"), planIn(aPlan("doc-sm", "smoke", 1, 1), "/hub/deploy")
	rollout := planIn(aParent("doc-roll", "roll", &one, &two, &smoke), "/hub/deploy")
	a := planIn(aPlan("doc-a", "auth-a", 1, 2), "/hub")
	auth := planIn(aParent("doc-auth", "auth", &a), "/hub")
	setPlans(m, rollout, one, two, smoke, planIn(aPlan("doc-dn", "notes", 1, 1), "/hub/deploy"),
		auth, a, planIn(aPlan("doc-hn", "hub-notes", 1, 1), "/hub"))
	m.moveToPlan(0)
	// The deepest rows have no room for their state, which gives way first.
	all := []string{
		"− hub",
		"  ├ − deploy",
		"  │   ├ − roll draft 3 plans",
		"  │   │   ├ can-1 1 task",
		"  │   │   ├ can-2 1 task",
		"  │   │   └ smoke 1 task",
		"  │   └ notes draft 1 task",
		"  ├ − auth draft 1 plan",
		"  │   └ auth-a draft 2 tasks",
		"  └ hub-notes draft 1 task",
	}
	wantPlanLines(t, m, all...)

	m.moveToPlan(slices.IndexFunc(m.planRows(), func(r planRow) bool { return r.plan.Name == "roll" }))
	keys(m, "left")
	wantPlanLines(t, m, slices.Concat(all[:2], []string{"  │   ├ + roll draft 3 plans"}, all[6:])...)
	keys(m, "right")
	wantPlanLines(t, m, all...)

	m.list().filter.text = "can"
	wantPlanLines(t, m, "− hub", "  └ − deploy", "      └ − roll draft 3 plans", "          ├ can-1 1 task", "          └ can-2 1 task")
}

// moveRequest is what the stub daemon of movesTo was asked.
type moveRequest struct {
	path   string
	folder string
}

// movesTo points m's client at a daemon of its own, which takes a doc's or a plan's
// new folder, refusing a.b, and sends on what it was asked.
func movesTo(t *testing.T, m *Model) <-chan moveRequest {
	t.Helper()
	socket := filepath.Join(runtest.Dir(t), "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	asked := make(chan moveRequest, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.NotFound(w, r) // what else tide asks for is not this test's
			return
		}
		var body struct {
			Folder string `json:"folder"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		asked <- moveRequest{path: r.URL.Path, folder: body.Folder}
		if body.Folder == "a.b" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(api.NewProblem(api.ErrorCodeInvalidArgument, `invalid folder: "a.b" can only have letters, numbers, _, - and /`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/plans/") {
			_ = json.NewEncoder(w).Encode(api.PlanResult{Result: api.ResultUpdated, Plan: aPlan("doc-sso", "sso", 1, 1)})
			return
		}
		_ = json.NewEncoder(w).Encode(api.DocResult{Result: api.ResultUpdated, Doc: aDoc("doc-api", "api", api.DocStatusWip, 1)})
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	m.client = client.New(client.Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	return asked
}

// TestMoveDoc: m on a doc opens a popup with its folder to edit, which sends the
// folder typed; a refusal stays in the popup, to correct.
func TestMoveDoc(t *testing.T) {
	t.Parallel()
	m := folderModel(t)
	asked := movesTo(t, m)
	m.moveToDoc(slices.Index(docRowNames(m), "/hub/"))
	keys(m, "m")
	if m.openTop() != nil {
		t.Fatal("m on a folder opened a popup")
	}
	m.moveToDoc(slices.Index(docRowNames(m), "api"))
	keys(m, "m")
	f, ok := m.openTop().(*fieldScreen)
	if !ok || f.heading != "Move doc" || f.value != "/hub" {
		t.Fatalf("the popup: %+v", m.openTop())
	}
	keys(m, "backspace", "backspace", "backspace", "backspace", "a", ".", "b", "enter")
	if got := <-asked; got.path != "/v1/docs/id:doc-api" || got.folder != "a.b" {
		t.Errorf("asked %+v", got)
	}
	if f, ok := m.openTop().(*fieldScreen); !ok || !strings.Contains(f.failure, "invalid folder") || f.busy {
		t.Fatalf("a refused folder: %+v", m.openTop())
	}
	keys(m, "backspace", "backspace", "x", "enter")
	if got := <-asked; got.folder != "ax" {
		t.Errorf("asked %+v", got)
	}
	if m.openTop() != nil {
		t.Error("the popup stayed open once moved")
	}
}

// TestMovePlan: m on a plan says which plans move with it.
func TestMovePlan(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	asked := movesTo(t, m)
	keys(m, "m")
	f, ok := m.openTop().(*fieldScreen)
	if !ok || f.heading != "Move plan" || f.prompt != "Folder of auth, which moves its 2 plans with it:" {
		t.Fatalf("the popup on a plan of plans: %+v", m.openTop())
	}
	keys(m, "esc")
	m.moveToPlan(2)
	keys(m, "m")
	if f, ok := m.openTop().(*fieldScreen); !ok || f.prompt != "Folder of auth-b, which moves auth and its plans with it:" {
		t.Fatalf("the popup on a plan it lists: %+v", m.openTop())
	}
	keys(m, "esc")
	m.moveToPlan(4)
	keys(m, "m")
	if f, ok := m.openTop().(*fieldScreen); !ok || f.prompt != "Folder of sso:" {
		t.Fatalf("the popup on a plan: %+v", m.openTop())
	}
	keys(m, "h", "u", "b", "enter")
	if got := <-asked; got.path != "/v1/plans/id:doc-sso" || got.folder != "hub" {
		t.Errorf("asked %+v", got)
	}
}
