package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/waittest"
)

// newAgent creates an agent named name in the project dir is in.
func newAgent(t *testing.T, home, dir, name string) api.Agent {
	t.Helper()
	v, exit := wdxJSONWith(t, home, in(dir), "agent", "create", "--name", name)
	if exit != api.ExitOK || v.Result != "created" || v.Agent == nil || v.Agent.State != api.AgentStateRunning {
		t.Fatalf("create agent %s: exit %d, %+v", name, exit, v)
	}
	return *v.Agent
}

// newExitedAgent creates an agent whose run file ends it: it may be gone
// before the create returns, so the create's state says nothing.
func newExitedAgent(t *testing.T, home, dir, name string) api.Agent {
	t.Helper()
	v, exit := wdxJSONWith(t, home, in(dir), "agent", "create", "--name", name)
	if exit != api.ExitOK || v.Result != "created" || v.Agent == nil {
		t.Fatalf("create agent %s: exit %d, %+v", name, exit, v)
	}
	return waitAgent(t, home, v.Agent.Id, api.AgentStateExited)
}

func launches(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(launchesPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func waitAgent(t *testing.T, home, id string, want api.AgentState) api.Agent {
	t.Helper()
	var got api.Agent
	waittest.For(t, "agent "+string(want), func() bool {
		v, exit := wdxJSON(t, home, "agent", "get", "--id", id)
		if exit != api.ExitOK {
			return false
		}
		got = *v.Agent
		return got.State == want
	})
	return got
}

func TestAgentCreate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newProject(t, home, "lib")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "create")
	if exit != api.ExitOK || v.Agent.ProjectName != "app" || v.Agent.Dir != app || v.Agent.Pid == nil || v.Agent.Name == "" ||
		v.Agent.Aca != "claude_code" || v.Agent.SessionId == "" {
		t.Fatalf("unexpected create: exit %d, %+v", exit, v.Agent)
	}
	if got := launches(t, home); !slices.Equal(got, []string{v.Agent.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "lib", "--name", "helper")
	if exit != api.ExitOK || v.Agent.ProjectName != "lib" || v.Agent.Dir == app || v.Agent.Name != "helper" {
		t.Fatalf("want helper in lib's clone path, got exit %d, %+v", exit, v.Agent)
	}

	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "lib", "--name", "HELPER")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "choose another --name")
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-name", "lib", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "lib", "--name", "a:b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "create")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")

	out := wdxWith(t, home, in(app), "agent", "create", "--name", "texty")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "created agent texty in app\n  dir  "+app+"\n  aca  claude_code\n  id   agent-") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentCreateInClosedProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "app")
	wantError(t, v, exit, api.ErrorCodeProjectClosed, api.ExitRejected)
	wantHint(t, v, "project open")
}

func TestAgentList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	lib := newProjectDir(t, home, "lib")
	a := newAgent(t, home, app, "helper")
	newAgent(t, home, lib, "other")
	newTerminal(t, home, app, "shell")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "list")
	if exit != api.ExitOK || len(v.Agents) != 1 || v.Agents[0].Id != a.Id {
		t.Fatalf("unexpected list: exit %d, %+v", exit, v.Agents)
	}
	v, exit = wdxJSON(t, home, "agent", "list", "--all")
	if exit != api.ExitOK || len(v.Agents) != 2 {
		t.Fatalf("unexpected list: exit %d, %+v", exit, v.Agents)
	}
	v, exit = wdxJSON(t, home, "agent", "list", "--project-name", "lib")
	if exit != api.ExitOK || len(v.Agents) != 1 || v.Agents[0].Name != "other" {
		t.Fatalf("unexpected list: exit %d, %+v", exit, v.Agents)
	}
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id, or --all")

	out := wdxWith(t, home, in(app), "agent", "list")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "NAME") || !strings.Contains(out.stdout, "helper") || !strings.Contains(out.stdout, "claude_code") || strings.Contains(out.stdout, "shell") {
		t.Fatalf("unexpected text: %+v", out)
	}
	empty := newProjectDir(t, home, "empty")
	if out := wdxWith(t, home, in(empty), "agent", "list"); out.exit != api.ExitOK || out.stdout != "no agents\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "get", "--name", "HELPER")
	if exit != api.ExitOK || v.Agent.Id != a.Id {
		t.Fatalf("unexpected get: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "agent", "get", "--id", a.Id)
	if exit != api.ExitOK || v.Agent.Id != a.Id {
		t.Fatalf("unexpected get: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "get", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "get", "--id", "agent-nothing")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "get", "--name", "nope", "--project-name", "app")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

	out := wdxWith(t, home, in(app), "agent", "get", "--name", "helper")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "agent helper in app\n") || !strings.Contains(out.stdout, a.SessionId) {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentStart(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	if err := os.WriteFile(filepath.Join(app, "run"), []byte("rm run; exit 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newExitedAgent(t, home, app, "helper")
	if a.ExitCode == nil || *a.ExitCode != 5 {
		t.Fatalf("exited: %+v", a)
	}

	v, exit := wdxJSONWith(t, home, in(app), "agent", "start", "--name", "helper")
	if exit != api.ExitOK || v.Result != "started" || v.Agent.State != api.AgentStateRunning || v.Agent.SessionId != a.SessionId {
		t.Fatalf("want helper started, got exit %d, %+v", exit, v)
	}
	if got := launches(t, home); !slices.Equal(got, []string{a.SessionId, a.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	v, exit = wdxJSON(t, home, "agent", "start", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeAgentRunning, api.ExitRejected)
	wantHint(t, v, "agent attach")
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "start", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the agent's project, or pass --project-name or --project-id")

	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	v, exit = wdxJSON(t, home, "agent", "start", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeProjectClosed, api.ExitRejected)
}

func TestAgentStartFails(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	dir := filepath.Join(app, "gone")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("exit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newExitedAgent(t, home, dir, "helper")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	v, exit := wdxJSON(t, home, "agent", "start", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeStartFailed, api.ExitSystem)
	wantHint(t, v, "agent start")
}

func TestAgentRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "helper", "--cancel")
	wantError(t, v, exit, api.ErrorCodeAgentRunning, api.ExitRejected)
	wantHint(t, v, "pass --stop-running to stop it")
	out := wdxWith(t, home, in(app), "agent", "remove", "--name", "helper")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "hint: pass --stop-running or --cancel") {
		t.Fatalf("unexpected text error: %+v", out)
	}

	v, exit = wdxJSON(t, home, "agent", "remove", "--id", a.Id, "--project-name", "app", "--stop-running")
	if exit != api.ExitOK || v.Result != "removed" || v.Agent.Id != a.Id {
		t.Fatalf("want helper removed, got exit %d, %+v", exit, v)
	}
	if syscall.Kill(*a.Pid, 0) == nil {
		t.Fatalf("want process %d gone", *a.Pid)
	}
	v, exit = wdxJSON(t, home, "agent", "remove", "--id", a.Id, "--cancel")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

	// One that is not running needs no answer.
	newAgent(t, home, app, "idle")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if out := wdxWith(t, home, in(app), "agent", "remove", "--name", "idle"); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "removed agent idle from app\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentRemoveAnswers(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newAgent(t, home, app, "helper")

	out := wdxWith(t, home, func(env *Env) { in(app)(env); answering(t, "n")(env) }, "agent", "remove", "--name", "helper")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "stopping an agent interrupts what it is doing") {
		t.Fatalf("unexpected answer no: %+v", out)
	}
	out = wdxWith(t, home, func(env *Env) { in(app)(env); answering(t, "y")(env) }, "agent", "remove", "--name", "helper")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "removed agent helper from app\n") {
		t.Fatalf("unexpected answer yes: %+v", out)
	}
}

// TestTerminalCommandsDoNotSeeAgents: wdx terminal deals with shells alone.
func TestTerminalCommandsDoNotSeeAgents(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, _ := wdxJSONWith(t, home, in(app), "terminal", "list")
	if len(v.Terminals) != 0 {
		t.Fatalf("terminals: %+v", v.Terminals)
	}
	for _, args := range [][]string{
		{"terminal", "get", "--id", a.TerminalId},
		{"terminal", "start", "--id", a.TerminalId},
		{"terminal", "remove", "--id", a.TerminalId, "--stop-running"},
		{"terminal", "history", "--id", a.TerminalId},
		{"terminal", "attach", "--id", a.TerminalId},
		{"terminal", "get", "--name", "helper", "--project-name", "app"},
		{"terminal", "start", "--name", "helper", "--project-name", "app"},
		{"terminal", "remove", "--name", "helper", "--project-name", "app", "--stop-running"},
		{"terminal", "attach", "--name", "helper", "--project-name", "app"},
	} {
		v, exit := wdxJSON(t, home, args...)
		wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	}
	if got := waitAgent(t, home, a.Id, api.AgentStateRunning); *got.Pid != *a.Pid {
		t.Fatalf("the agent was touched: %+v, was %+v", got, a)
	}
}

func TestProjectGetAgents(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	v, _ := wdxJSON(t, home, "project", "get", "--name", "app")
	if v.Project.Agents == nil || len(*v.Project.Agents) != 1 || (*v.Project.Agents)[0].Id != a.Id {
		t.Fatalf("project agents: %+v", v.Project.Agents)
	}
	if v.Project.Terminals == nil || len(*v.Project.Terminals) != 0 {
		t.Fatalf("project terminals: %+v", v.Project.Terminals)
	}
	out := wdx(t, home, "project", "get", "--name", "app")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "agents:") || !strings.Contains(out.stdout, "helper") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestProjectCloseAndRemoveWithAgents(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeProjectInUse, api.ExitRejected)
	v, exit = wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if exit != api.ExitOK || v.Result != "closed" || len(v.StoppedAgents) != 1 || v.StoppedAgents[0].Id != a.Id || v.StoppedAgents[0].State != api.AgentStateStopped {
		t.Fatalf("want app closed with helper stopped, got exit %d, %+v", exit, v)
	}
	if syscall.Kill(*a.Pid, 0) == nil {
		t.Fatalf("want process %d gone", *a.Pid)
	}
	v, exit = wdxJSON(t, home, "project", "open", "--local", app)
	if exit != api.ExitOK || v.Result != "opened" || len(v.StartedAgents) != 1 || v.StartedAgents[0].State != api.AgentStateRunning {
		t.Fatalf("want app opened with helper started, got exit %d, %+v", exit, v)
	}
	if got := launches(t, home); !slices.Equal(got, []string{a.SessionId, a.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	out := wdx(t, home, "project", "remove", "--name", "app", "--stop-running")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "\n  stopped agent helper ("+a.Id+")\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
	if v, _ := wdxJSON(t, home, "agent", "list", "--all"); len(v.Agents) != 0 {
		t.Fatalf("agents left: %+v", v.Agents)
	}
}

func TestCompleteAgents(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"agent", "start", "--name", ""}, "helper\trunning, " + a.Id},
		{[]string{"agent", "remove", "--id", ""}, a.Id + "\thelper in app"},
	} {
		out := wdxWith(t, home, in(app), append([]string{"__complete"}, tc.args...)...)
		if !strings.Contains(out.stdout, tc.want+"\n") {
			t.Errorf("%v: got %q, want %q", tc.args, out.stdout, tc.want)
		}
	}
}

func TestAgentAttachNotRunning(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newAgent(t, home, app, "helper")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "attach", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeAgentNotRunning, api.ExitRejected)
	wantHint(t, v, "agent start")
}

func TestAgentAttachNotFound(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "attach", "--name", "web")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

// agentAttach runs wdx agent attach in a terminal of its own, on an agent
// that runs a shell to type in.
func agentAttach(t *testing.T, home, dir string, args ...string) *attachRun {
	t.Helper()
	return startInTerminal(t, home, dir, 80, 24, append([]string{"agent", "attach"}, args...))
}

// typableAgent creates an agent whose AI coding agent is a shell to type in.
func typableAgent(t *testing.T, home, dir, name string) api.Agent {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("PS1='$ ' exec /bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return newAgent(t, home, dir, name)
}

func TestAgentAttach(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	agent := typableAgent(t, home, app, "helper")

	a := agentAttach(t, home, app, "--name", "helper", "--json")
	a.waitScreen("C-\\ ? for keys")
	if !a.altScreen() || !strings.Contains(a.text(), "helper · app") {
		t.Fatalf("want the view full-window with its status line, got:\n%s", a.text())
	}
	a.input("echo hel''lo\r")
	a.waitScreen("hello")
	a.input("\x1c")
	a.waitScreen("d detach")
	a.input("d")
	v, exit := a.wait()
	if exit != api.ExitOK || v.Result != "detached" || v.Agent == nil || v.Agent.Id != agent.Id || v.Agent.State != api.AgentStateRunning || v.Terminal != nil {
		t.Fatalf("want detached with the agent running, got exit %d, %+v", exit, v)
	}

	// Detached, it runs on, and shows the screen it left.
	b := agentAttach(t, home, app, "--id", agent.Id, "--json")
	b.waitScreen("hello")
	b.input("exit 3\r")
	v, exit = b.wait()
	if exit != api.ExitOK || v.Result != "exited" || v.Agent.State != api.AgentStateExited || v.Agent.ExitCode == nil || *v.Agent.ExitCode != 3 {
		t.Fatalf("want exited with code 3, got exit %d, %+v", exit, v)
	}
}

func TestAgentAttachText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	agent := typableAgent(t, home, app, "helper")
	a := agentAttach(t, home, app, "--name", "helper")
	a.waitScreen("for keys")
	a.input("\x1c")
	a.waitScreen("d detach")
	a.input("d")
	out := a.waitText()
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "detached from agent helper in app\n") || !strings.Contains(out.stdout, agent.Id) || strings.Contains(out.stdout, agent.TerminalId) {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentCreateChild(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	lib := newProjectDir(t, home, "lib")
	parent := newAgent(t, home, app, "parent")

	v, exit := wdxJSONWith(t, home, in(lib), "agent", "create", "--parent-id", parent.Id, "--name", "by-id")
	if exit != api.ExitOK || v.Agent.ProjectName != "app" || v.Agent.Dir != app || v.Agent.ParentAgentId == nil || *v.Agent.ParentAgentId != parent.Id {
		t.Fatalf("by id: exit %d, %+v", exit, v.Agent)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "PARENT", "--name", "by-name")
	if exit != api.ExitOK || v.Agent.ParentAgentId == nil || *v.Agent.ParentAgentId != parent.Id {
		t.Fatalf("by name: exit %d, %+v", exit, v.Agent)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "by-name", "--name", "deep")
	if exit != api.ExitOK {
		t.Fatalf("deep: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-id", "agent-none")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(lib), "agent", "create", "--parent-name", "parent")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-id", parent.Id, "--parent-name", "parent")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)

	// The list nests the children, in JSON and as a tree in text.
	v, exit = wdxJSONWith(t, home, in(app), "agent", "list")
	if exit != api.ExitOK || len(v.Agents) != 1 || v.Agents[0].Children == nil || len(*v.Agents[0].Children) != 2 {
		t.Fatalf("list: exit %d, %+v", exit, v.Agents)
	}
	out := wdxWith(t, home, in(app), "agent", "list")
	for _, want := range []string{"\nparent ", "\n├ by-id ", "\n└ by-name ", "\n  └ deep "} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("list: no %q in:\n%s", want, out.stdout)
		}
	}
	out = wdxWith(t, home, in(app), "agent", "get", "--name", "parent")
	if !strings.Contains(out.stdout, "by-id, by-name") {
		t.Errorf("get: %s", out.stdout)
	}
	out = wdxWith(t, home, in(app), "agent", "create", "--parent-id", parent.Id, "--name", "text")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "parent "+parent.Id) {
		t.Errorf("create: %+v", out)
	}
	out = wdxWith(t, home, in(app), "project", "get", "--name", "app")
	if !strings.Contains(out.stdout, "└ deep") {
		t.Errorf("project get: %s", out.stdout)
	}
}

func TestAgentRemoveParent(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	parent := newAgent(t, home, app, "parent")
	if v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "parent", "--name", "child"); exit != api.ExitOK {
		t.Fatalf("create: exit %d, %+v", exit, v)
	}

	out := wdxWith(t, home, in(app), "agent", "remove", "--name", "parent", "--stop-running")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "until its children are gone") {
		t.Fatalf("remove: %+v", out)
	}
	got, exit := wdxJSON(t, home, "agent", "get", "--id", parent.Id)
	if exit != api.ExitOK || got.Agent.State != api.AgentStateRemoved || got.Agent.RemovedAt == nil {
		t.Fatalf("get: exit %d, %+v", exit, got.Agent)
	}
	out = wdxWith(t, home, in(app), "agent", "get", "--name", "parent")
	if !strings.Contains(out.stdout, "removed") {
		t.Fatalf("get: %+v", out)
	}

	for _, args := range [][]string{
		{"agent", "start", "--name", "parent"},
		{"agent", "remove", "--name", "parent", "--stop-running"},
		{"agent", "create", "--parent-name", "parent"},
	} {
		v, exit := wdxJSONWith(t, home, in(app), args...)
		wantError(t, v, exit, api.ErrorCodeAgentRemoved, api.ExitRejected)
		wantHint(t, v, "it goes when its last child is removed")
	}
	v, exit := wdxJSONWith(t, home, in(app), "agent", "attach", "--name", "parent")
	wantError(t, v, exit, api.ErrorCodeAgentNotRunning, api.ExitRejected)

	// A parent cannot be a tombstone: completions leave it out.
	out = wdxWith(t, home, in(app), "__complete", "agent", "create", "--parent-name", "")
	if strings.Contains(out.stdout, "parent\t") || !strings.Contains(out.stdout, "child\t") {
		t.Fatalf("completions: %q", out.stdout)
	}

	if v, exit := wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "child", "--stop-running"); exit != api.ExitOK {
		t.Fatalf("remove child: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "list")
	if exit != api.ExitOK || len(v.Agents) != 0 {
		t.Fatalf("list: exit %d, %+v", exit, v.Agents)
	}
}
