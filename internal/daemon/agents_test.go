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

	"github.com/srjn45/wdx/internal/api"
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
