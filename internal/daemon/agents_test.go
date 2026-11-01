package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/telemetry"
	"github.com/srjn45/wdx/internal/waittest"
)

func (td *testDaemon) createAgent(t *testing.T, project, dir, name string) api.Agent {
	t.Helper()
	r, err := td.client.CreateAgentWithResponse(context.Background(), api.CreateAgent{Project: optional(project), Dir: dir, Name: optional(name)})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("create agent: %v %s", err, r.Body)
	}
	if r.JSON201.Result != api.ResultCreated || r.JSON201.Agent.State != api.AgentStateRunning {
		t.Fatalf("want a created running agent, got %+v", r.JSON201)
	}
	return r.JSON201.Agent
}

// createExitedAgent creates an agent whose run file ends it: it may be gone
// before the create returns, so the create's state says nothing.
func (td *testDaemon) createExitedAgent(t *testing.T, dir, name string) api.Agent {
	t.Helper()
	r, err := td.client.CreateAgentWithResponse(context.Background(), api.CreateAgent{Dir: dir, Name: optional(name)})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("create agent: %v %s", err, r.Body)
	}
	return td.waitAgentState(t, r.JSON201.Agent.Id, api.AgentStateExited)
}

func (td *testDaemon) agents(t *testing.T, params *api.ListAgentsParams) []api.Agent {
	t.Helper()
	r, err := td.client.ListAgentsWithResponse(context.Background(), params)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("list agents: %v %s", err, r.Body)
	}
	return r.JSON200.Agents
}

// sessions returns the session of each start of the daemon's test agents.
func (td *testDaemon) sessions(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(td.home, "test-launches"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func (td *testDaemon) waitAgentState(t *testing.T, id string, want api.AgentState) api.Agent {
	t.Helper()
	var got api.Agent
	waittest.For(t, "agent "+string(want), func() bool {
		r, err := td.client.GetAgentWithResponse(context.Background(), "id:"+id, nil)
		if err != nil || r.JSON200 == nil {
			return false
		}
		got = r.JSON200.Agent
		return got.State == want
	})
	return got
}

func TestCreateAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path

	a := td.createAgent(t, "", app, "helper")
	if a.ProjectName != "app" || a.Name != "helper" || a.Dir != app || a.Aca != "claude_code" || a.SessionId == "" || a.Pid == nil ||
		!strings.HasPrefix(a.Id, "agent-") || !strings.HasPrefix(a.TerminalId, "term-") {
		t.Fatalf("unexpected agent: %+v", a)
	}
	if got := td.sessions(t); !slices.Equal(got, []string{a.SessionId}) {
		t.Fatalf("sessions = %q, want %s", got, a.SessionId)
	}

	// Outside the project's worktrees it starts in the clone path.
	outside := td.createAgent(t, "app", t.TempDir(), "")
	if outside.Dir != app || outside.Name == "" {
		t.Fatalf("unexpected agent: %+v", outside)
	}

	r, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	r, err = td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app, Name: optional("a:b")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidName)
	r, err = td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: "relative"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath)
}

func TestAgentNamesAreSharedWithTerminals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	td.createAgent(t, "", app, "helper")
	td.createTerminal(t, "", app, "shell")

	c, err := td.client.CreateTerminalWithResponse(ctx, api.CreateTerminal{Dir: app, Name: optional("HELPER")})
	if err != nil {
		t.Fatal(err)
	}
	p := c.ApplicationproblemJSONDefault
	wantProblem(t, p, http.StatusConflict, api.ErrorCodeNameTaken)
	if _, leaked := (*p.Details)["terminal"]; leaked {
		t.Fatalf("an agent's row shown as a terminal: %v", *p.Details)
	}
	a, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app, Name: optional("shell")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, a.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeNameTaken)
}

func TestListAndGetAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	lib := td.create(t, "lib").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	b := td.createAgent(t, "", lib, "other")

	if got := td.agents(t, nil); len(got) != 2 || got[0].Id != a.Id || got[1].Id != b.Id {
		t.Fatalf("all: %+v", got)
	}
	if got := td.agents(t, &api.ListAgentsParams{Project: optional("lib")}); len(got) != 1 || got[0].Id != b.Id {
		t.Fatalf("by project: %+v", got)
	}
	if got := td.agents(t, &api.ListAgentsParams{Dir: optional(app)}); len(got) != 1 || got[0].Id != a.Id {
		t.Fatalf("by dir: %+v", got)
	}

	for _, tc := range []struct {
		ref    string
		params *api.GetAgentParams
	}{
		{"id:" + a.Id, nil},
		{"HELPER", &api.GetAgentParams{Project: optional("app")}},
		{"helper", &api.GetAgentParams{Dir: optional(app)}},
	} {
		r, err := td.client.GetAgentWithResponse(ctx, tc.ref, tc.params)
		if err != nil || r.JSON200 == nil || r.JSON200.Agent.Id != a.Id {
			t.Fatalf("get %s: %v %s", tc.ref, err, r.Body)
		}
	}
	r, err := td.client.GetAgentWithResponse(ctx, "id:"+a.Id, &api.GetAgentParams{Project: optional("lib")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	r, err = td.client.GetAgentWithResponse(ctx, "helper", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusBadRequest, api.ErrorCodeUsage)
}

// TestAgentsAreNotTerminals: the terminal API deals with shells alone. An
// agent's row answers as an unknown id does, and is left untouched.
func TestAgentsAreNotTerminals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	shell := td.createTerminal(t, "", app, "shell")

	if got := td.terminals(t, nil); len(got) != 1 || got[0].Id != shell.Id {
		t.Fatalf("list: %+v", got)
	}
	for _, ref := range []string{"id:" + a.TerminalId, "id:" + a.Id, "helper"} {
		project := optional("app")
		get, err := td.client.GetTerminalWithResponse(ctx, ref, &api.GetTerminalParams{Project: project})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, get.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
		st, err := td.client.StartTerminalWithResponse(ctx, ref, &api.StartTerminalParams{Project: project})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, st.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
		del, err := td.client.DeleteTerminalWithResponse(ctx, ref, &api.DeleteTerminalParams{Project: project, StopRunning: optional(true)})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, del.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
		h, err := td.client.GetTerminalHistoryWithResponse(ctx, ref, &api.GetTerminalHistoryParams{Project: project})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, h.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	}
	got := td.waitAgentState(t, a.Id, api.AgentStateRunning)
	if *got.Pid != *a.Pid {
		t.Fatalf("the agent was touched: %+v, was %+v", got, a)
	}
	if err := syscall.Kill(*a.Pid, 0); err != nil {
		t.Fatalf("the agent's process is gone: %v", err)
	}

	// Nor is a shell an agent.
	for _, ref := range []string{"id:" + shell.Id, "shell"} {
		r, err := td.client.GetAgentWithResponse(ctx, ref, &api.GetAgentParams{Project: optional("app")})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	}

	p, err := td.client.GetProjectWithResponse(ctx, "app")
	if err != nil || p.JSON200 == nil {
		t.Fatalf("get project: %v %s", err, p.Body)
	}
	project := p.JSON200.Project
	if project.Terminals == nil || len(*project.Terminals) != 1 || (*project.Terminals)[0].Id != shell.Id {
		t.Fatalf("project terminals: %+v", project.Terminals)
	}
	if project.Agents == nil || len(*project.Agents) != 1 || (*project.Agents)[0].Id != a.Id {
		t.Fatalf("project agents: %+v", project.Agents)
	}
}

func TestStartAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	if err := os.WriteFile(filepath.Join(app, "run"), []byte("rm run; exit 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := td.createExitedAgent(t, app, "helper")
	if a.ExitCode == nil || *a.ExitCode != 5 || a.Pid != nil {
		t.Fatalf("exited: %+v", a)
	}

	r, err := td.client.StartAgentWithResponse(ctx, "helper", &api.StartAgentParams{Project: optional("app")})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultStarted {
		t.Fatalf("start: %v %s", err, r.Body)
	}
	if s := r.JSON200.Agent; s.Id != a.Id || s.SessionId != a.SessionId || s.State != api.AgentStateRunning || s.ExitCode != nil {
		t.Fatalf("started: %+v", s)
	}
	if got := td.sessions(t); !slices.Equal(got, []string{a.SessionId, a.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}

	r, err = td.client.StartAgentWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := r.ApplicationproblemJSONDefault
	wantProblem(t, p, http.StatusConflict, api.ErrorCodeAgentRunning)
	if got := detail(t, p, "agent"); got["id"] != a.Id {
		t.Fatalf("details: %v", got)
	}
}

func TestStartAgentFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	dir := filepath.Join(app, "gone")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("exit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := td.createExitedAgent(t, dir, "helper")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	r, err := td.client.StartAgentWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := r.ApplicationproblemJSONDefault
	wantProblem(t, p, http.StatusInternalServerError, api.ErrorCodeStartFailed)
	if got := detail(t, p, "agent"); got["id"] != a.Id || got["state"] != string(api.AgentStateFailed) || got["error"] == nil {
		t.Fatalf("details: %v", got)
	}
}

func TestRemoveAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")

	r, err := td.client.DeleteAgentWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentRunning)

	r, err = td.client.DeleteAgentWithResponse(ctx, "helper", &api.DeleteAgentParams{Project: optional("app"), StopRunning: optional(true)})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultRemoved || r.JSON200.Agent.Id != a.Id {
		t.Fatalf("remove: %v %s", err, r.Body)
	}
	if runtest.Alive(*a.Pid) {
		t.Fatalf("want agent process %d gone", *a.Pid)
	}
	if got := td.agents(t, nil); len(got) != 0 {
		t.Fatalf("agents left: %+v", got)
	}
	wantNoTerminalDir(t, td.home, a.TerminalId)
	g, err := td.client.GetAgentWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, g.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}

func TestAgentHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")

	r, err := td.client.GetAgentHistoryWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil || r.JSON200 == nil || r.JSON200.History.Lines == nil {
		t.Fatalf("history: %v %s", err, r.Body)
	}
	r, err = td.client.GetAgentHistoryWithResponse(ctx, "id:"+a.Id, &api.GetAgentHistoryParams{Tail: optional(1), Count: optional(1)})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusBadRequest, api.ErrorCodeUsage)
	r, err = td.client.GetAgentHistoryWithResponse(ctx, "id:agent-nothing", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}

func TestCloseAndOpenProjectWithAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	shell := td.createTerminal(t, "", app, "shell")

	u, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed})
	if err != nil {
		t.Fatal(err)
	}
	p := u.ApplicationproblemJSONDefault
	wantProblem(t, p, http.StatusConflict, api.ErrorCodeProjectInUse)
	if live, _ := (*p.Details)["agents"].([]any); len(live) != 1 {
		t.Fatalf("want the live agent in details, got %v", *p.Details)
	}
	if live, _ := (*p.Details)["terminals"].([]any); len(live) != 1 {
		t.Fatalf("want only the live shell under terminals, got %v", *p.Details)
	}

	u, err = td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed, StopRunning: optional(true)})
	if err != nil || u.JSON200 == nil || u.JSON200.Result != api.ResultClosed {
		t.Fatalf("close: %v %s", err, u.Body)
	}
	if s := u.JSON200.StoppedAgents; s == nil || len(*s) != 1 || (*s)[0].Id != a.Id || (*s)[0].State != api.AgentStateStopped {
		t.Fatalf("stopped agents: %+v", s)
	}
	if s := u.JSON200.Stopped; s == nil || len(*s) != 1 || (*s)[0].Id != shell.Id {
		t.Fatalf("stopped terminals: %+v", s)
	}
	if runtest.Alive(*a.Pid) {
		t.Fatalf("want agent process %d gone", *a.Pid)
	}

	c, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, c.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectClosed)
	s, err := td.client.StartAgentWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, s.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectClosed)

	u, err = td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil || u.JSON200 == nil || u.JSON200.Result != api.ResultOpened {
		t.Fatalf("open: %v %s", err, u.Body)
	}
	if st := u.JSON200.StartedAgents; st == nil || len(*st) != 1 || (*st)[0].Id != a.Id || (*st)[0].State != api.AgentStateRunning {
		t.Fatalf("started agents: %+v", st)
	}
	if st := u.JSON200.Started; st == nil || len(*st) != 1 || (*st)[0].Id != shell.Id {
		t.Fatalf("started terminals: %+v", st)
	}
	if got := td.sessions(t); !slices.Equal(got, []string{a.SessionId, a.SessionId}) {
		t.Fatalf("sessions = %q, want the same one twice", got)
	}
}

func TestRemoveProjectWithAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")

	d, err := td.client.DeleteProjectWithResponse(ctx, "app", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, d.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectInUse)

	d, err = td.client.DeleteProjectWithResponse(ctx, "app", &api.DeleteProjectParams{StopRunning: optional(true)})
	if err != nil || d.JSON200 == nil || d.JSON200.Result != api.ResultRemoved {
		t.Fatalf("remove: %v %s", err, d.Body)
	}
	if s := d.JSON200.StoppedAgents; s == nil || len(*s) != 1 || (*s)[0].Id != a.Id {
		t.Fatalf("stopped agents: %+v", s)
	}
	if got := td.agents(t, nil); len(got) != 0 {
		t.Fatalf("agents left: %+v", got)
	}
	wantNoTerminalDir(t, td.home, a.TerminalId)
}

func TestAgentsOutliveDaemon(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	td.stop()
	if err := syscall.Kill(*a.Pid, 0); err != nil {
		t.Fatalf("want agent process %d running after the daemon stopped: %v", *a.Pid, err)
	}

	next := startOn(t, td.home, telemetry.Options{}, nil)
	list := next.agents(t, nil)
	if len(list) != 1 || list[0].Id != a.Id || list[0].State != api.AgentStateRunning || *list[0].Pid != *a.Pid {
		t.Fatalf("want %s adopted running, got %+v", a.Name, list)
	}
}

func TestRenameAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	parent := td.createAgent(t, "", app, "parent")
	td.createAgent(t, "", app, "other")
	byID := "id:" + parent.Id
	rename := func(name string) *api.UpdateAgentResponse {
		t.Helper()
		r, err := td.client.UpdateAgentWithResponse(ctx, byID, &api.UpdateAgentParams{}, api.UpdateAgent{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := rename(" elder ")
	if r.JSON200 == nil || r.JSON200.Result != api.ResultRenamed || r.JSON200.Agent.Name != "elder" || r.JSON200.Agent.Id != parent.Id {
		t.Fatalf("renamed: %s", r.Body)
	}
	wantProblem(t, rename("OTHER").ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeNameTaken)
	wantProblem(t, rename("no_way").ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidName)

	child, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app, Parent: &byID, Name: optional("child")})
	if err != nil || child.JSON201 == nil {
		t.Fatalf("child: %v", err)
	}
	if a := child.JSON201.Agent; a.ParentAgentName == nil || *a.ParentAgentName != "elder" {
		t.Fatalf("child: %+v", a)
	}
}

func TestChildAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	lib := td.create(t, "lib").Worktrees[0].Path
	parent := td.createAgent(t, "", app, "parent")

	child := func(body api.CreateAgent) (*api.CreateAgentResponse, error) {
		return td.client.CreateAgentWithResponse(ctx, body)
	}
	// An id needs no project, and the parent's project and dir win over dir.
	byID := "id:" + parent.Id
	r, err := child(api.CreateAgent{Dir: lib, Parent: &byID, Name: optional("by-id")})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("by id: %v %s", err, r.Body)
	}
	if a := r.JSON201.Agent; a.ProjectName != "app" || a.Dir != app || a.ParentAgentId == nil || *a.ParentAgentId != parent.Id {
		t.Fatalf("by id: %+v", a)
	}
	// A name is looked up in the project holding dir.
	byName := "parent"
	r, err = child(api.CreateAgent{Dir: app, Parent: &byName, Name: optional("by-name")})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("by name: %v %s", err, r.Body)
	}
	r, err = child(api.CreateAgent{Dir: lib, Parent: &byName})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	r, err = child(api.CreateAgent{Dir: app, Project: optional("lib"), Parent: &byID})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)

	// Lists nest; results stay flat.
	list := td.agents(t, &api.ListAgentsParams{Project: optional("app")})
	if len(list) != 1 || list[0].Id != parent.Id || list[0].Children == nil || len(*list[0].Children) != 2 ||
		(*list[0].Children)[0].Name != "by-id" || (*list[0].Children)[0].Children == nil {
		t.Fatalf("list: %+v", list)
	}
	got, err := td.client.GetAgentWithResponse(ctx, "id:"+parent.Id, nil)
	if err != nil || got.JSON200 == nil || got.JSON200.Agent.Children == nil || len(*got.JSON200.Agent.Children) != 2 {
		t.Fatalf("get: %v %s", err, got.Body)
	}
	p, err := td.client.GetProjectWithResponse(ctx, "app")
	if err != nil || p.JSON200 == nil || p.JSON200.Project.Agents == nil || len(*p.JSON200.Project.Agents) != 1 ||
		len(api.FlattenAgents(*p.JSON200.Project.Agents)) != 3 {
		t.Fatalf("project: %v %s", err, p.Body)
	}

	// A removed parent stays, as removed, and refuses what needs a live one.
	stop := true
	d, err := td.client.DeleteAgentWithResponse(ctx, "id:"+parent.Id, &api.DeleteAgentParams{StopRunning: &stop})
	if err != nil || d.JSON200 == nil {
		t.Fatalf("remove: %v %s", err, d.Body)
	}
	if a := d.JSON200.Agent; a.State != api.AgentStateRemoved || a.RemovedAt == nil {
		t.Fatalf("removed: %+v", a)
	}
	d, err = td.client.DeleteAgentWithResponse(ctx, "id:"+parent.Id, &api.DeleteAgentParams{StopRunning: &stop})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, d.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentRemoved)
	s, err := td.client.StartAgentWithResponse(ctx, "id:"+parent.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, s.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentRemoved)
	r, err = child(api.CreateAgent{Dir: app, Parent: &byID})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentRemoved)

	// It goes with its last child.
	for _, name := range []string{"by-id", "by-name"} {
		if d, err := td.client.DeleteAgentWithResponse(ctx, name, &api.DeleteAgentParams{Project: optional("app"), StopRunning: &stop}); err != nil || d.JSON200 == nil {
			t.Fatalf("remove %s: %v %s", name, err, d.Body)
		}
	}
	if list := td.agents(t, &api.ListAgentsParams{Project: optional("app")}); len(list) != 0 {
		t.Fatalf("list: %+v", list)
	}
}

func TestSendAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	const secret = "the-secret-prompt"

	r, err := td.client.SendAgentWithResponse(ctx, "id:"+a.Id, nil, api.AgentMessage{Prompt: secret, Instant: optional(true)})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultQueued || r.JSON200.Agent.Id != a.Id {
		t.Fatalf("want it queued, got %v %s", err, r.Body)
	}
	r, err = td.client.SendAgentWithResponse(ctx, "nope", &api.SendAgentParams{Dir: optional(app)}, api.AgentMessage{Prompt: secret})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	r, err = td.client.SendAgentWithResponse(ctx, "id:"+a.Id, nil, api.AgentMessage{})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusBadRequest, api.ErrorCodeUsage)
	r, err = td.client.SendAgentWithResponse(ctx, "id:"+a.Id, nil, api.AgentMessage{Prompt: strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusInternalServerError, api.ErrorCodeSendFailed)
	if sent := detail(t, r.ApplicationproblemJSONDefault, "agent"); sent["id"] != a.Id {
		t.Fatalf("want the agent in details, got %v", sent)
	}
	for _, line := range logLines(t, td.home) {
		if b, err := json.Marshal(line); err != nil || strings.Contains(string(b), secret) {
			t.Fatalf("the prompt is logged: %s, %v", b, err)
		}
	}
}

func TestSendAgentNotRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	if err := os.WriteFile(filepath.Join(app, "run"), []byte("exit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := td.createExitedAgent(t, app, "helper")
	r, err := td.client.SendAgentWithResponse(ctx, "id:"+a.Id, nil, api.AgentMessage{Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentNotRunning)
	if stopped := detail(t, r.ApplicationproblemJSONDefault, "agent"); stopped["id"] != a.Id {
		t.Fatalf("want the agent in details, got %v", stopped)
	}
}

// statusScript draws what Claude Code's parser reads, one screen for each file
// that appears, and ends when told. Started again, once "again" exists, it draws
// nothing.
const statusScript = `
if [ -f again ]; then exec sleep 1000; fi
rule=────────────────
stty raw -echo
wait_for() { until [ -f "$1" ]; do sleep 0.01; done; }
printf '\033[2J\033[H%s\r\n ❯ 1. Yes\r\n\r\n Esc to cancel · Tab to amend' 'Do you want to proceed?'
wait_for draft
printf '\033[2J\033[H%s\r\n❯ typed\r\n%s\r\n  ⏸ manual mode on · esc to interrupt' "$rule" "$rule"
wait_for banner
printf '\033[2J\033[H ▐▛███▛█   Claude Code v2.1.316\r\n▝▜██████▀  Haiku 5.5 · Claude Max\r\n%s\r\n❯ \r\n%s\r\n  ⏸ manual mode on · esc to interrupt' "$rule" "$rule"
wait_for idle
printf '\033[2J\033[H%s\r\n❯ \r\n%s\r\n  ⏸ manual mode on · ? for shortcuts · ← 1 agent' "$rule" "$rule"
wait_for end
exit 0
`

func activityOf(a api.Agent) string {
	if a.Activity == nil {
		return ""
	}
	return string(*a.Activity)
}

func modelOf(a api.Agent) string {
	if a.Model == nil {
		return ""
	}
	return *a.Model
}

func agentsIn(e event) []api.Agent {
	if e.project.Agents == nil {
		return nil
	}
	return *e.project.Agents
}

// waitActivity follows the project's stream until the agent named name shows the
// status: a change reaches every open stream.
func (td *testDaemon) waitActivity(t *testing.T, name, activity string, draft bool) {
	t.Helper()
	td.waitProject(t, "app", "agent "+name+" "+activity, func(e event) bool {
		for _, a := range agentsIn(e) {
			if a.Name == name {
				return activityOf(a) == activity && a.Draft != nil && *a.Draft == draft
			}
		}
		return false
	})
}

// TestAgentStatus: what the screen shows reaches the API and the project's stream,
// a daemon that restarts learns what changed while it was gone, and nothing shows
// once the process ends or of the last run when it starts again.
func TestAgentStatus(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	step := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(app, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(app, "run"), []byte(statusScript), 0o600); err != nil {
		t.Fatal(err)
	}
	a := td.createAgent(t, "", app, "helper")
	if a.Activity == nil || a.Draft == nil {
		t.Errorf("a running agent without a status: %+v", a)
	}
	td.waitActivity(t, "helper", "waiting", false)
	got := td.waitAgentState(t, a.Id, api.AgentStateRunning)
	if activityOf(got) != "waiting" || got.Draft == nil || *got.Draft {
		t.Errorf("get agent: %s %v", activityOf(got), got.Draft)
	}
	updated := got.UpdatedAt

	step("draft")
	td.waitActivity(t, "helper", "working", true)
	step("banner")
	td.waitActivity(t, "helper", "working", false)
	if got := td.waitAgentState(t, a.Id, api.AgentStateRunning); modelOf(got) != "Haiku 5.5" {
		t.Errorf("the banner read as model %q", modelOf(got))
	}

	// Changed with no daemon to tell, so that only the hello can say it.
	td.stop()
	step("idle")
	waittest.For(t, "the holder to read the change", func() bool {
		logs, _ := filepath.Glob(filepath.Join(identity.LogsDir(td.home), "*.log"))
		for _, name := range logs {
			b, _ := os.ReadFile(name)
			for line := range strings.Lines(string(b)) {
				if strings.Contains(line, "screen state changed") && strings.Contains(line, `"activity":"idle"`) {
					return true
				}
			}
		}
		return false
	})
	next := startOn(t, td.home, telemetry.Options{}, nil)
	next.waitActivity(t, "helper", "idle", false)
	got = next.waitAgentState(t, a.Id, api.AgentStateRunning)
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("updated_at moved from %s to %s", updated, got.UpdatedAt)
	}
	if modelOf(got) != "Haiku 5.5" {
		t.Errorf("a daemon starting again, on a screen that does not say it, learns model %q", modelOf(got))
	}

	step("end")
	got = next.waitAgentState(t, a.Id, api.AgentStateExited)
	if got.Activity != nil || got.Draft != nil || got.Model != nil {
		t.Errorf("an exited agent shows %s, %v, %s", activityOf(got), got.Draft, modelOf(got))
	}

	step("again")
	r, err := next.client.StartAgentWithResponse(context.Background(), "id:"+a.Id, nil)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("start agent: %v %s", err, r.Body)
	}
	if got := r.JSON200.Agent; activityOf(got) != "unknown" {
		t.Errorf("started again, it shows %s", activityOf(got))
	}
}

// otherACA is one no holder knows: it reads no screen and says nothing, as a
// holder too old to does.
type otherACA struct{ *acatest.ACA }

func (otherACA) ID() string { return "other" }

func TestAgentStatusOfAHolderThatSaysNothing(t *testing.T) {
	t.Parallel()
	home := runtest.Dir(t)
	td := startOn(t, home, telemetry.Options{}, func(o *Options) { o.ACA = otherACA{&acatest.ACA{}} })
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	if activityOf(a) != "unknown" || a.Draft == nil || *a.Draft || a.Model != nil {
		t.Errorf("got %s, %v, %s", activityOf(a), a.Draft, modelOf(a))
	}
}

// TestAgentQueued: a message held behind a draft shows until it is delivered.
func TestAgentQueued(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	if err := os.WriteFile(filepath.Join(app, "run"), []byte(statusScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "draft"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := td.createAgent(t, "", app, "helper")
	td.waitActivity(t, "helper", "working", true)
	r, err := td.client.SendAgentWithResponse(context.Background(), "id:"+a.Id, nil, api.SendAgentJSONRequestBody{Prompt: "hello"})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("send: %v %s", err, r.Body)
	}
	queued := func(n int) func(event) bool {
		return func(e event) bool {
			list := agentsIn(e)
			return len(list) == 1 && list[0].Queued != nil && *list[0].Queued == n
		}
	}
	td.waitProject(t, "app", "the message queued", queued(1))
	for _, step := range []string{"banner", "idle"} {
		if err := os.WriteFile(filepath.Join(app, step), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	td.waitProject(t, "app", "the message delivered", queued(0))
}

func TestAgentTranscript(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const secret = "the-secret-in-a-conversation"
	conversation := filepath.Join(t.TempDir(), "conversation")
	if err := os.WriteFile(conversation, []byte("hello\n"+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) { o.ACA = &acatest.ACA{Conversation: conversation} })
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")

	r, err := td.client.GetAgentTranscriptWithResponse(ctx, "id:"+a.Id, nil)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("transcript: %v %s", err, r.Body)
	}
	got := r.JSON200.Transcript
	if want := filepath.Join(identity.AgentDir(td.home, a.Id), aca.TranscriptFile); got.Path != want || got.From != 0 || got.Next != 2 || !slices.Equal(got.Lines, []string{"hello", secret}) {
		t.Errorf("transcript = %+v", got)
	}
	r, err = td.client.GetAgentTranscriptWithResponse(ctx, "id:"+a.Id, &api.GetAgentTranscriptParams{From: optional(int64(1)), Count: optional(1)})
	if err != nil || r.JSON200 == nil || !slices.Equal(r.JSON200.Transcript.Lines, []string{secret}) {
		t.Errorf("from 1: %v %s", err, r.Body)
	}
	r, err = td.client.GetAgentTranscriptWithResponse(ctx, "id:"+a.Id, &api.GetAgentTranscriptParams{Tail: optional(1), Count: optional(1)})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusBadRequest, api.ErrorCodeUsage)
	r, err = td.client.GetAgentTranscriptWithResponse(ctx, "id:agent-nothing", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)

	logs, err := filepath.Glob(filepath.Join(identity.LogsDir(td.home), "*.log"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("logs: %v %v", logs, err)
	}
	for _, name := range logs {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) {
			t.Errorf("the conversation is in %s", name)
		}
	}
}

// An agent's history reaches the logs no more than a terminal's does.
func TestAgentHistoryIsNotLogged(t *testing.T) {
	t.Parallel()
	const secret = "the-secret-on-a-screen"
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	// The test terminals keep no history: it is written as a holder would.
	dir := filepath.Join(identity.TerminalDir(td.home, a.TerminalId), "history")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0000000000.log"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := td.client.GetAgentHistoryWithResponse(context.Background(), "id:"+a.Id, nil)
	if err != nil || r.JSON200 == nil || !slices.Equal(r.JSON200.History.Lines, []string{secret}) {
		t.Fatalf("history: %v %s", err, r.Body)
	}
	logged := func() string {
		logs, err := filepath.Glob(filepath.Join(identity.LogsDir(td.home), "*.log"))
		if err != nil {
			t.Fatal(err)
		}
		var all strings.Builder
		for _, name := range logs {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			all.Write(data)
		}
		return all.String()
	}
	waittest.For(t, "the history's response to be logged", func() bool {
		for line := range strings.Lines(logged()) {
			if strings.Contains(line, "request returned") && strings.Contains(line, a.Id+"/history") {
				return true
			}
		}
		return false
	})
	if strings.Contains(logged(), secret) {
		t.Error("the screen's text is in the log")
	}
}

// The backup comes up to date when an agent stops to wait and when it ends,
// with nobody asking for it.
func TestAgentBackupFollowsTheAgent(t *testing.T) {
	t.Parallel()
	conversation := filepath.Join(t.TempDir(), "conversation")
	write := func(text string) {
		if err := os.WriteFile(conversation, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("waiting\n")
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) { o.ACA = &acatest.ACA{Conversation: conversation} })
	app := td.create(t, "app").Worktrees[0].Path
	if err := os.WriteFile(filepath.Join(app, "run"), []byte(statusScript), 0o600); err != nil {
		t.Fatal(err)
	}
	a := td.createAgent(t, "", app, "helper")
	file := filepath.Join(identity.AgentDir(td.home, a.Id), aca.TranscriptFile)
	has := func(want string) func() bool {
		return func() bool {
			data, err := os.ReadFile(file)
			return err == nil && string(data) == want
		}
	}
	td.waitActivity(t, "helper", "waiting", false)
	waittest.For(t, "the backup as it waits", has("waiting\n"))
	write("later\n")
	if err := syscall.Kill(*td.waitAgentState(t, a.Id, api.AgentStateRunning).Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waittest.For(t, "the backup at the agent's end", has("later\n"))
}

func TestRemovingAProjectMovesItsAgentsBackups(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	if _, err := os.Stat(identity.AgentDir(td.home, a.Id)); err != nil {
		t.Fatal(err)
	}
	stop := true
	r, err := td.client.DeleteProjectWithResponse(context.Background(), "app", &api.DeleteProjectParams{StopRunning: &stop})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v %s", err, r.Body)
	}
	if _, err := os.Stat(identity.AgentDir(td.home, a.Id)); !os.IsNotExist(err) {
		t.Errorf("the backup stayed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(identity.TrashAgentsDir(td.home), a.Id, "removed.json")); err != nil {
		t.Errorf("not in the trash: %v", err)
	}
}

// TestAgentCurrentDirFollowsTheBackup: an agent's ACA says it moved, into a worktree
// wdx never heard of, and the API and the stream say so. Its dir stays.
func TestAgentCurrentDirFollowsTheBackup(t *testing.T) {
	t.Parallel()
	said := filepath.Join(t.TempDir(), "dir")
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) { o.ACA = &acatest.ACA{Dir: said} })
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	a := td.createAgent(t, "api", dir, "mover")
	if a.CurrentDir != nil {
		t.Errorf("before it said: %q", *a.CurrentDir)
	}
	work := filepath.Join(t.TempDir(), "work")
	if err := os.WriteFile(said, []byte(work+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	td.daemon.backups.Request(a.TerminalId)
	waittest.For(t, "the agent to say it moved", func() bool {
		r, err := td.client.GetAgentWithResponse(context.Background(), "id:"+a.Id, &api.GetAgentParams{})
		return err == nil && r.JSON200 != nil && r.JSON200.Agent.CurrentDir != nil && *r.JSON200.Agent.CurrentDir == work && r.JSON200.Agent.Dir == dir
	})
}

// TestMessageOutlivesItsHolder kills an agent's holder with a message still on its way,
// as a test agent's holder holds them: starting the agent, in the same conversation,
// sends it again.
func TestMessageOutlivesItsHolder(t *testing.T) {
	t.Parallel()
	td := start(t)
	ctx := context.Background()
	app := td.create(t, "app").Worktrees[0].Path
	a := td.createAgent(t, "", app, "helper")
	if r, err := td.client.SendAgentWithResponse(ctx, "id:"+a.Id, nil, api.SendAgentJSONRequestBody{Prompt: "still wanted"}); err != nil || r.JSON200 == nil {
		t.Fatalf("send: %v, %s", err, r.Body)
	}
	project := "app"
	messages := func() []api.Message {
		t.Helper()
		r, err := td.client.ListMessagesWithResponse(ctx, &api.ListMessagesParams{Project: &project})
		if err != nil || r.JSON200 == nil {
			t.Fatalf("list: %v, %s", err, r.Body)
		}
		return r.JSON200.Messages
	}
	if list := messages(); len(list) != 1 || list[0].Text != "still wanted" || list[0].State != api.MessageStatePending || list[0].Attempts != 1 {
		t.Fatalf("before: %+v", list)
	}
	row, err := td.daemon.agents.Store().Get(ctx, a.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(row.Terminal.HolderPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	td.waitAgentState(t, a.Id, api.AgentStateLost)
	if list := messages(); len(list) != 1 || list[0].State != api.MessageStatePending {
		t.Fatalf("with the holder gone: %+v", list)
	}
	if r, err := td.client.StartAgentWithResponse(ctx, "id:"+a.Id, nil); err != nil || r.JSON200 == nil {
		t.Fatalf("start: %v, %s", err, r.Body)
	}
	td.waitAgentState(t, a.Id, api.AgentStateRunning)
	waittest.For(t, "the message to be sent again", func() bool {
		list := messages()
		return len(list) == 1 && list[0].State == api.MessageStatePending && list[0].Attempts == 2
	})
}

// TestRemovingAnAgentGivesItsReviewBack: what was written is not lost with it.
func TestRemovingAnAgentGivesItsReviewBack(t *testing.T) {
	t.Parallel()
	td := start(t)
	ctx := context.Background()
	project := "app"
	app := td.create(t, project).Worktrees[0].Path
	if err := os.WriteFile(filepath.Join(app, "file"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := td.createAgent(t, "", app, "helper")
	if r, err := td.client.CreateWorktreeCommentWithResponse(ctx, "main", &api.CreateWorktreeCommentParams{Project: &project}, api.WorktreeCommentCreate{
		File: "file", Line: 1, Side: api.WorktreeCommentCreateSideNew, View: api.WorktreeCommentCreateViewUncommitted, Body: "keep me",
	}); err != nil || r.JSON201 == nil {
		t.Fatalf("comment: %v, %s", err, r.Body)
	}
	if r, err := td.client.SendWorktreeReviewWithResponse(ctx, "main", &api.SendWorktreeReviewParams{Project: &project}, api.WorktreeReview{Agent: "id:" + a.Id}); err != nil || r.JSON200 == nil {
		t.Fatalf("review: %v, %s", err, r.Body)
	}
	stateOf := func() api.WorktreeCommentState {
		t.Helper()
		r, err := td.client.ListWorktreeCommentsWithResponse(ctx, "main", &api.ListWorktreeCommentsParams{Project: &project})
		if err != nil || r.JSON200 == nil || len(r.JSON200.Comments) != 1 {
			t.Fatalf("comments: %v, %s", err, r.Body)
		}
		return r.JSON200.Comments[0].State
	}
	if got := stateOf(); got != api.WorktreeCommentStateSent {
		t.Fatalf("sent: %s", got)
	}
	if r, err := td.client.DeleteAgentWithResponse(ctx, "id:"+a.Id, &api.DeleteAgentParams{StopRunning: optional(true)}); err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v, %s", err, r.Body)
	}
	if got := stateOf(); got != api.WorktreeCommentStateDraft {
		t.Errorf("after: %s", got)
	}
}
