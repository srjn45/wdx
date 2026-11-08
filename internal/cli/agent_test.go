package cli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/waittest"
)

// newAgent creates an agent named name in the project dir is in.
func newAgent(t *testing.T, home, dir, name string) api.Agent {
	t.Helper()
	v, exit := wdxJSONWith(t, home, in(dir), "agent", "create", "--name", name)
	if exit != clikit.ExitOK || v.Result != "created" || v.Agent == nil || v.Agent.State != api.AgentStateRunning {
		t.Fatalf("create agent %s: exit %d, %+v", name, exit, v)
	}
	return *v.Agent
}

// newExitedAgent creates an agent whose run file ends it: it may be gone
// before the create returns, so the create's state says nothing.
func newExitedAgent(t *testing.T, home, dir, name string) api.Agent {
	t.Helper()
	v, exit := wdxJSONWith(t, home, in(dir), "agent", "create", "--name", name)
	if exit != clikit.ExitOK || v.Result != "created" || v.Agent == nil {
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
		if exit != clikit.ExitOK {
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
	if exit != clikit.ExitOK || v.Agent.ProjectName != "app" || v.Agent.Dir != app || v.Agent.Pid == nil || v.Agent.Name == "" ||
		v.Agent.Aca != "claude_code" || v.Agent.SessionId == "" {
		t.Fatalf("unexpected create: exit %d, %+v", exit, v.Agent)
	}
	if got := launches(t, home); !slices.Equal(got, []string{v.Agent.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "lib", "--name", "helper")
	if exit != clikit.ExitOK || v.Agent.ProjectName != "lib" || v.Agent.Dir == app || v.Agent.Name != "helper" {
		t.Fatalf("want helper in lib's clone path, got exit %d, %+v", exit, v.Agent)
	}

	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "lib", "--name", "HELPER")
	wantError(t, v, exit, api.ErrorCodeNameTaken, clikit.ExitRejected)
	wantHint(t, v, "choose another --name")
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "create", "--project-name", "lib", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeNameTaken, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "lib", "--name", "a:b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "create")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")

	out := wdxWith(t, home, in(app), "agent", "create", "--name", "texty")
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "created agent texty in app\n  dir  "+app+"\n  aca  claude_code\n  id   agent-") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentCreateModelAndPermissions(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--name", "default")
	if exit != clikit.ExitOK || v.Agent.RequestedModel != nil || v.Agent.Permissions != api.AgentPermissionsFull {
		t.Fatalf("by default: exit %d, model %v, permissions %q", exit, v.Agent.RequestedModel, v.Agent.Permissions)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--name", "asked", "--model", "sonnet", "--permissions", "ask")
	if exit != clikit.ExitOK || v.Agent.RequestedModel == nil || *v.Agent.RequestedModel != "sonnet" || v.Agent.Permissions != api.AgentPermissionsAsk {
		t.Fatalf("asked: exit %d, model %v, permissions %q", exit, v.Agent.RequestedModel, v.Agent.Permissions)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "get", "--name", "asked")
	if exit != clikit.ExitOK || v.Agent.RequestedModel == nil || *v.Agent.RequestedModel != "sonnet" || v.Agent.Permissions != api.AgentPermissionsAsk {
		t.Fatalf("read back: exit %d, model %v, permissions %q", exit, v.Agent.RequestedModel, v.Agent.Permissions)
	}
	out := wdxWith(t, home, in(app), "agent", "get", "--name", "asked")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "  model       asked for sonnet\n  permissions ask\n") {
		t.Fatalf("unexpected text: %+v", out)
	}

	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--model", "opus 4")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--model", "")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	for _, mode := range []string{"some", ""} {
		v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--permissions", mode)
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	}
}

func TestAgentModels(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "agent", "models")
	if exit != clikit.ExitOK || v.Aca != "claude_code" || len(v.Models) != 4 || v.Models[0].Id != "fable" || v.Models[0].Default {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	out := wdx(t, home, "agent", "models")
	if out.exit != clikit.ExitOK || out.stdout != "models of claude_code\nID      NAME\nfable   Fable\nopus    Opus\nsonnet  Sonnet\nhaiku   Haiku\nwith no --model, your own setting for it\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
	if got := complete(t, home, "agent", "create", "--model", ""); !slices.Equal(got, []string{"fable\tFable", "opus\tOpus", "sonnet\tSonnet", "haiku\tHaiku"}) {
		t.Errorf("--model completes %q", got)
	}
}

func TestAgentCreateInClosedProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--project-name", "app")
	wantError(t, v, exit, api.ErrorCodeProjectClosed, clikit.ExitRejected)
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
	if exit != clikit.ExitOK || len(v.Agents) != 1 || v.Agents[0].Id != a.Id {
		t.Fatalf("unexpected list: exit %d, %+v", exit, v.Agents)
	}
	v, exit = wdxJSON(t, home, "agent", "list", "--all")
	if exit != clikit.ExitOK || len(v.Agents) != 2 {
		t.Fatalf("unexpected list: exit %d, %+v", exit, v.Agents)
	}
	v, exit = wdxJSON(t, home, "agent", "list", "--project-name", "lib")
	if exit != clikit.ExitOK || len(v.Agents) != 1 || v.Agents[0].Name != "other" {
		t.Fatalf("unexpected list: exit %d, %+v", exit, v.Agents)
	}
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id, or --all")

	out := wdxWith(t, home, in(app), "agent", "list")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "NAME") || !strings.Contains(out.stdout, "helper") || !strings.Contains(out.stdout, "claude_code") || strings.Contains(out.stdout, "shell") {
		t.Fatalf("unexpected text: %+v", out)
	}
	empty := newProjectDir(t, home, "empty")
	if out := wdxWith(t, home, in(empty), "agent", "list"); out.exit != clikit.ExitOK || out.stdout != "no agents\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "get", "--name", "HELPER")
	if exit != clikit.ExitOK || v.Agent.Id != a.Id {
		t.Fatalf("unexpected get: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "agent", "get", "--id", a.Id)
	if exit != clikit.ExitOK || v.Agent.Id != a.Id {
		t.Fatalf("unexpected get: exit %d, %+v", exit, v)
	}
	if v.Agent.Activity == nil || *v.Agent.Activity != api.AgentActivityUnknown || v.Agent.Draft == nil || *v.Agent.Draft {
		t.Fatalf("unexpected status: %+v", v.Agent)
	}
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "get", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "get", "--id", "agent-nothing")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "get", "--name", "nope", "--project-name", "app")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)

	out := wdxWith(t, home, in(app), "agent", "get", "--name", "helper")
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "agent helper in app\n") || !strings.Contains(out.stdout, a.SessionId) ||
		!strings.Contains(out.stdout, "activity") || !strings.Contains(out.stdout, "unknown") || strings.Contains(out.stdout, "draft") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

// TestAgentStatusText: a draft shows only when there is one, and an agent not
// running has no status to show.
func TestAgentStatusText(t *testing.T) {
	t.Parallel()
	waiting, yes, two, model := api.AgentActivityWaiting, true, 2, "Haiku 5.5"
	var out bytes.Buffer
	agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: api.Agent{Name: "helper", State: api.AgentStateRunning, Activity: &waiting, Draft: &yes, Queued: &two, Model: &model}}}.text(&out, style.New(false))
	if !regexp.MustCompile(`activity\s+waiting\n`).MatchString(out.String()) || !regexp.MustCompile(`model\s+Haiku 5\.5\n`).MatchString(out.String()) || !regexp.MustCompile(`draft\s+yes\n`).MatchString(out.String()) || !regexp.MustCompile(`queued\s+2\n`).MatchString(out.String()) {
		t.Errorf("running: %s", out.String())
	}
	if strings.Contains(out.String(), "prompt") || strings.Contains(out.String(), "question") || strings.Contains(out.String(), "options") {
		t.Errorf("without a prompt read, its fields show: %s", out.String())
	}
	out.Reset()
	title, detail, selected := "Bash command", "touch a.txt\nls", 1
	prompt := &api.AgentPrompt{Kind: api.AgentPromptKindYesNo, Title: &title, Question: "Do you want to proceed?", Detail: &detail, Options: []string{"Yes", "No"}, Selected: &selected}
	agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: api.Agent{Name: "helper", State: api.AgentStateRunning, Activity: &waiting, Prompt: prompt}}}.text(&out, style.New(false))
	if !strings.Contains(out.String(), "  activity    yes/no\n  prompt      yes/no · Bash command\n  question    Do you want to proceed?\n  detail      touch a.txt\n              ls\n  options       Yes\n              ❯ No\n") {
		t.Errorf("a yes/no prompt: %s", out.String())
	}
	out.Reset()
	decision := &api.AgentPrompt{Kind: api.AgentPromptKindDecision, Question: "Which fruits do you like?", Options: []string{"Apple", "Pear"}, Multi: true, Ticked: []int{0}}
	agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: api.Agent{Name: "helper", State: api.AgentStateRunning, Activity: &waiting, Prompt: decision}}}.text(&out, style.New(false))
	if !strings.Contains(out.String(), "  activity    decision\n  prompt      decision\n  question    Which fruits do you like?\n  options       [✔] Apple\n                [ ] Pear\n") || strings.Contains(out.String(), "detail") {
		t.Errorf("a decision: %s", out.String())
	}
	out.Reset()
	agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: api.Agent{Name: "helper", State: api.AgentStateExited}}}.text(&out, style.New(false))
	if strings.Contains(out.String(), "activity") || strings.Contains(out.String(), "draft") || strings.Contains(out.String(), "model") {
		t.Errorf("exited: %s", out.String())
	}
	out.Reset()
	agentList{AgentList: api.AgentList{Agents: []api.Agent{{Name: "helper", State: api.AgentStateRunning, Activity: &waiting, Model: &model, Aca: "claude_code",
		Context: &api.AgentContext{Tokens: 1_500_000}}}}}.text(&out, style.New(false))
	if !regexp.MustCompile(`NAME\s+STATE\s+ACTIVITY\s+MODEL\s+CONTEXT\s+ACA`).MatchString(out.String()) || !regexp.MustCompile(`helper\s+running\s+waiting\s+Haiku 5\.5\s+1\.5M\s+claude_code`).MatchString(out.String()) {
		t.Errorf("list: %s", out.String())
	}
	out.Reset()
	agentList{AgentList: api.AgentList{Agents: []api.Agent{{Name: "helper", State: api.AgentStateRunning, Activity: &waiting, Prompt: prompt, Aca: "claude_code"},
		{Name: "picker", State: api.AgentStateRunning, Activity: &waiting, Prompt: decision, Aca: "claude_code"}}}}.text(&out, style.New(false))
	if !regexp.MustCompile(`helper\s+running\s+yes/no\s+claude_code`).MatchString(out.String()) || !regexp.MustCompile(`picker\s+running\s+decision\s+claude_code`).MatchString(out.String()) {
		t.Errorf("list with prompts: %s", out.String())
	}
}

// TestAgentPromptKeptOutOfTheLog: what the log says of a response holding an agent
// has the prompt's kind and how many options it has, never its text.
func TestAgentPromptKeptOutOfTheLog(t *testing.T) {
	t.Parallel()
	waiting, title, selected := api.AgentActivityWaiting, "Bash command", 0
	prompt := &api.AgentPrompt{Kind: api.AgentPromptKindYesNo, Title: &title, Question: "Do you want to proceed?", Options: []string{"Yes", "No"}, Selected: &selected}
	a := api.Agent{Name: "helper", State: api.AgentStateRunning, Activity: &waiting, Prompt: prompt}
	for name, logged := range map[string]slog.Value{
		"get":     agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: a}}.LogValue(),
		"list":    agentList{AgentList: api.AgentList{Agents: []api.Agent{a}}}.LogValue(),
		"result":  agentResult(api.AgentResult{Result: api.ResultStarted, Agent: a}).LogValue(),
		"project": projectDetail(api.ProjectEnvelope{Project: api.Project{Name: "app", Agents: &[]api.Agent{a}}}).LogValue(),
	} {
		b, err := json.Marshal(logged.Any())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Do you want") || strings.Contains(string(b), `"Yes"`) || !strings.Contains(string(b), `"prompt":{"kind":"yes_no","options":2}`) {
			t.Errorf("%s logs %s", name, b)
		}
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
	if exit != clikit.ExitOK || v.Result != "started" || v.Agent.State != api.AgentStateRunning || v.Agent.SessionId != a.SessionId {
		t.Fatalf("want helper started, got exit %d, %+v", exit, v)
	}
	if got := launches(t, home); !slices.Equal(got, []string{a.SessionId, a.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	v, exit = wdxJSON(t, home, "agent", "start", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeAgentRunning, clikit.ExitRejected)
	wantHint(t, v, "agent attach")
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "agent", "start", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the agent's project, or pass --project-name or --project-id")

	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	v, exit = wdxJSON(t, home, "agent", "start", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeProjectClosed, clikit.ExitRejected)
}

func TestAgentRename(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	newAgent(t, home, app, "other")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "rename", "--name", "helper", "--new-name", "aide")
	if exit != clikit.ExitOK || v.Result != "renamed" || v.Agent.Name != "aide" || v.Agent.Id != a.Id || v.Agent.SessionId != a.SessionId {
		t.Fatalf("want aide, got exit %d, %+v", exit, v)
	}
	v, exit = wdxJSON(t, home, "agent", "rename", "--id", a.Id, "--new-name", "Other")
	wantError(t, v, exit, api.ErrorCodeNameTaken, clikit.ExitRejected)
	wantHint(t, v, "choose another --new-name")
	v, exit = wdxJSON(t, home, "agent", "rename", "--id", a.Id, "--new-name", "no_way")
	wantError(t, v, exit, api.ErrorCodeInvalidName, clikit.ExitRejected)
	wantHint(t, v, "choose another --new-name")
	v, exit = wdxJSONWith(t, home, in(app), "agent", "rename", "--name", "helper", "--new-name", "again")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "rename", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)

	if out := wdx(t, home, "agent", "rename", "--id", a.Id, "--new-name", "aide-2"); out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "renamed agent aide-2 in app\n") {
		t.Fatalf("unexpected text: %+v", out)
	}

	wdxJSONWith(t, home, in(app), "agent", "create", "--parent-id", a.Id, "--name", "child")
	wdxJSON(t, home, "agent", "remove", "--id", a.Id, "--stop-running")
	v, exit = wdxJSON(t, home, "agent", "rename", "--id", a.Id, "--new-name", "ghost")
	wantError(t, v, exit, api.ErrorCodeAgentRemoved, clikit.ExitRejected)
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
	wantError(t, v, exit, api.ErrorCodeStartFailed, clikit.ExitSystem)
	wantHint(t, v, "agent start")
}

func TestAgentRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "helper", "--cancel")
	wantError(t, v, exit, api.ErrorCodeAgentRunning, clikit.ExitRejected)
	wantHint(t, v, "pass --stop-running to stop it")
	out := wdxWith(t, home, in(app), "agent", "remove", "--name", "helper")
	if out.exit != clikit.ExitRejected || !strings.Contains(out.stderr, "hint: pass --stop-running or --cancel") {
		t.Fatalf("unexpected text error: %+v", out)
	}

	v, exit = wdxJSON(t, home, "agent", "remove", "--id", a.Id, "--project-name", "app", "--stop-running")
	if exit != clikit.ExitOK || v.Result != "removed" || v.Agent.Id != a.Id {
		t.Fatalf("want helper removed, got exit %d, %+v", exit, v)
	}
	if runtest.Alive(*a.Pid) {
		t.Fatalf("want process %d gone", *a.Pid)
	}
	v, exit = wdxJSON(t, home, "agent", "remove", "--id", a.Id, "--cancel")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)

	// One that is not running needs no answer.
	newAgent(t, home, app, "idle")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if out := wdxWith(t, home, in(app), "agent", "remove", "--name", "idle"); out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "removed agent idle from app\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestAgentRemoveAnswers(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newAgent(t, home, app, "helper")

	out := wdxWith(t, home, func(env *Env) { in(app)(env); answering(t, "n")(env) }, "agent", "remove", "--name", "helper")
	if out.exit != clikit.ExitRejected || !strings.Contains(out.stderr, "stopping an agent interrupts what it is doing") {
		t.Fatalf("unexpected answer no: %+v", out)
	}
	out = wdxWith(t, home, func(env *Env) { in(app)(env); answering(t, "y")(env) }, "agent", "remove", "--name", "helper")
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "removed agent helper from app\n") {
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
		{"terminal", "send", "--id", a.TerminalId, "--prompt", "hello"},
		{"terminal", "get", "--name", "helper", "--project-name", "app"},
		{"terminal", "start", "--name", "helper", "--project-name", "app"},
		{"terminal", "remove", "--name", "helper", "--project-name", "app", "--stop-running"},
		{"terminal", "attach", "--name", "helper", "--project-name", "app"},
	} {
		v, exit := wdxJSON(t, home, args...)
		wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
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
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "agents:") || !strings.Contains(out.stdout, "helper") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestProjectCloseAndRemoveWithAgents(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeProjectInUse, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	if exit != clikit.ExitOK || v.Result != "closed" || len(v.StoppedAgents) != 1 || v.StoppedAgents[0].Id != a.Id || v.StoppedAgents[0].State != api.AgentStateStopped {
		t.Fatalf("want app closed with helper stopped, got exit %d, %+v", exit, v)
	}
	if runtest.Alive(*a.Pid) {
		t.Fatalf("want process %d gone", *a.Pid)
	}
	v, exit = wdxJSON(t, home, "project", "open", "--local", app)
	if exit != clikit.ExitOK || v.Result != "opened" || len(v.StartedAgents) != 1 || v.StartedAgents[0].State != api.AgentStateRunning {
		t.Fatalf("want app opened with helper started, got exit %d, %+v", exit, v)
	}
	if got := launches(t, home); !slices.Equal(got, []string{a.SessionId, a.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	out := wdx(t, home, "project", "remove", "--name", "app", "--stop-running")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "\n  stopped agent helper ("+a.Id+")\n") {
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
	wantError(t, v, exit, api.ErrorCodeAgentNotRunning, clikit.ExitRejected)
	wantHint(t, v, "agent start")
}

func TestAgentAttachNotFound(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "attach", "--name", "web")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
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
	if exit != clikit.ExitOK || v.Result != "detached" || v.Agent == nil || v.Agent.Id != agent.Id || v.Agent.State != api.AgentStateRunning || v.Terminal != nil {
		t.Fatalf("want detached with the agent running, got exit %d, %+v", exit, v)
	}

	// Detached, it runs on, and shows the screen it left.
	b := agentAttach(t, home, app, "--id", agent.Id, "--json")
	b.waitScreen("hello")
	b.input("exit 3\r")
	v, exit = b.wait()
	if exit != clikit.ExitOK || v.Result != "exited" || v.Agent.State != api.AgentStateExited || v.Agent.ExitCode == nil || *v.Agent.ExitCode != 3 {
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
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "detached from agent helper in app\n") || !strings.Contains(out.stdout, agent.Id) || strings.Contains(out.stdout, agent.TerminalId) {
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
	if exit != clikit.ExitOK || v.Agent.ProjectName != "app" || v.Agent.Dir != app || v.Agent.ParentAgentId == nil || *v.Agent.ParentAgentId != parent.Id {
		t.Fatalf("by id: exit %d, %+v", exit, v.Agent)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "PARENT", "--name", "by-name")
	if exit != clikit.ExitOK || v.Agent.ParentAgentId == nil || *v.Agent.ParentAgentId != parent.Id {
		t.Fatalf("by name: exit %d, %+v", exit, v.Agent)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "by-name", "--name", "deep")
	if exit != clikit.ExitOK {
		t.Fatalf("deep: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-id", "agent-none")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(lib), "agent", "create", "--parent-name", "parent")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--parent-id", parent.Id, "--parent-name", "parent")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)

	// The list nests the children, in JSON and as a tree in text.
	v, exit = wdxJSONWith(t, home, in(app), "agent", "list")
	if exit != clikit.ExitOK || len(v.Agents) != 1 || v.Agents[0].Children == nil || len(*v.Agents[0].Children) != 2 {
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
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "parent "+parent.Id) {
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
	if v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "parent", "--name", "child"); exit != clikit.ExitOK {
		t.Fatalf("create: exit %d, %+v", exit, v)
	}

	out := wdxWith(t, home, in(app), "agent", "remove", "--name", "parent", "--stop-running")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "until its children are gone") {
		t.Fatalf("remove: %+v", out)
	}
	got, exit := wdxJSON(t, home, "agent", "get", "--id", parent.Id)
	if exit != clikit.ExitOK || got.Agent.State != api.AgentStateRemoved || got.Agent.RemovedAt == nil {
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
		wantError(t, v, exit, api.ErrorCodeAgentRemoved, clikit.ExitRejected)
		wantHint(t, v, "it goes when its last child is removed")
	}
	v, exit := wdxJSONWith(t, home, in(app), "agent", "attach", "--name", "parent")
	wantError(t, v, exit, api.ErrorCodeAgentNotRunning, clikit.ExitRejected)

	// A parent cannot be a tombstone: completions leave it out.
	out = wdxWith(t, home, in(app), "__complete", "agent", "create", "--parent-name", "")
	if strings.Contains(out.stdout, "parent\t") || !strings.Contains(out.stdout, "child\t") {
		t.Fatalf("completions: %q", out.stdout)
	}

	if v, exit := wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "child", "--stop-running"); exit != clikit.ExitOK {
		t.Fatalf("remove child: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "list")
	if exit != clikit.ExitOK || len(v.Agents) != 0 {
		t.Fatalf("list: exit %d, %+v", exit, v.Agents)
	}
}

func TestAgentCreateWorktree(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := gittest.Repo(t, "app")
	if v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app"); exit != clikit.ExitOK {
		t.Fatalf("open: exit %d, %+v", exit, v)
	}
	path := filepath.Join(repo, ".worktrees", "fix")

	// The default is the checkout you are in, main for the clone.
	parent := newAgent(t, home, repo, "parent")
	if parent.Worktree == nil || *parent.Worktree != "main" {
		t.Fatalf("worktree %v", parent.Worktree)
	}

	// A new name creates it; a child with no flag stays in its parent's.
	v, exit := wdxJSONWith(t, home, in(repo), "agent", "create", "--name", "in-fix", "--parent-name", "parent", "--worktree", "fix")
	if exit != clikit.ExitOK || v.Agent.Dir != path || *v.Agent.Worktree != "fix" || v.WorktreeCreated == nil || !*v.WorktreeCreated {
		t.Fatalf("new: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(repo), "agent", "create", "--name", "kid", "--parent-name", "in-fix")
	if exit != clikit.ExitOK || v.Agent.Dir != path || *v.Agent.Worktree != "fix" || v.WorktreeCreated != nil {
		t.Fatalf("child: exit %d, %+v", exit, v)
	}
	// Run inside it, with no flag, an agent stays there.
	v, exit = wdxJSONWith(t, home, in(path), "agent", "create", "--name", "here")
	if exit != clikit.ExitOK || *v.Agent.Worktree != "fix" {
		t.Fatalf("here: exit %d, %+v", exit, v)
	}
	// main is the clone, from anywhere in the project.
	v, exit = wdxJSONWith(t, home, in(path), "agent", "create", "--name", "back", "--worktree", "main")
	if exit != clikit.ExitOK || v.Agent.Dir != repo || *v.Agent.Worktree != "main" {
		t.Fatalf("main: exit %d, %+v", exit, v)
	}

	// Text says what happened.
	out := wdxWith(t, home, in(repo), "agent", "create", "--name", "txt", "--worktree", "text-one")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "text-one (new)") {
		t.Fatalf("text: %+v", out)
	}
	out = wdxWith(t, home, in(repo), "agent", "get", "--name", "txt")
	if !strings.Contains(out.stdout, "worktree    text-one") {
		t.Fatalf("get: %+v", out)
	}

	gittest.Git(t, repo, "branch", "taken")
	v, exit = wdxJSONWith(t, home, in(repo), "agent", "create", "--worktree", "taken")
	wantError(t, v, exit, api.ErrorCodeBranchExists, clikit.ExitRejected)
	wantHint(t, v, "a new worktree gets a new branch")
	v, exit = wdxJSONWith(t, home, in(repo), "agent", "create", "--worktree", "a/b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, clikit.ExitRejected)
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSONWith(t, home, in(repo), "agent", "create", "--worktree", "plain")
	wantError(t, v, exit, api.ErrorCodePathExists, clikit.ExitRejected)

	// Removing the agent leaves the worktree.
	if v, exit := wdxJSONWith(t, home, in(repo), "agent", "remove", "--name", "here", "--stop-running"); exit != clikit.ExitOK {
		t.Fatalf("remove: exit %d, %+v", exit, v)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestAgentCreateWorktreeNoCommit: a repository with nothing committed has no
// HEAD to start a worktree from.
func TestAgentCreateWorktreeNoCommit(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--worktree", "fix")
	wantError(t, v, exit, api.ErrorCodeWorktreeFailed, clikit.ExitRejected)
}

// claudeAtRest is a run file for the test ACA: a program drawing Claude Code's
// idle screen, which the holder goes by, and recording its input in got.
const claudeAtRest = `stty raw -echo
printf '────────────\r\n❯ \r\n────────────\r\n  ⏸ manual mode on · ? for shortcuts · ← 1 agent'
exec cat > got
`

func TestAgentSend(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	if err := os.WriteFile(filepath.Join(app, "run"), []byte(claudeAtRest), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newAgent(t, home, app, "helper")
	const secret = "the-secret-prompt"

	v, exit := wdxJSONWith(t, home, in(app), "agent", "send", "--name", "helper", "--prompt", secret)
	if exit != clikit.ExitOK || v.Result != "queued" || v.Agent.Id != a.Id {
		t.Fatalf("want it queued, got exit %d, %+v", exit, v)
	}
	out := wdx(t, home, "agent", "send", "--id", a.Id, "--instant", "--prompt=line one\nline two")
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "queued agent helper in app\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
	// Its parser and its way of delivering, not typing. The second is instant, so is
	// steered, with Ctrl+X Ctrl+S, and may go first if it came while the first was
	// still queued.
	first, second := "\x1b[200~"+secret+"\x1b[201~\r", "\x1b[200~line one\nline two\x1b[201~\r\x18\x13"
	got := filepath.Join(app, "got")
	var read []byte
	if !eventually(t, func() bool {
		read, _ = os.ReadFile(got)
		return string(read) == first+second || string(read) == second+first
	}) {
		t.Fatalf("the agent read %q", read)
	}

	v, exit = wdxJSONWith(t, home, in(app), "agent", "send", "--name", "nope", "--prompt", secret)
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "send", "--id", a.Id, "--prompt", "")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	v, exit = wdxJSON(t, home, "agent", "send", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	v, exit = wdxJSON(t, home, "agent", "send", "--id", a.Id, "--prompt", strings.Repeat("x", 1<<20))
	wantError(t, v, exit, api.ErrorCodeSendFailed, clikit.ExitSystem)

	files, err := filepath.Glob(filepath.Join(home, "logs", "*.log"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no logs: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if logged := string(b); strings.Contains(logged, secret) || strings.Contains(logged, "line one") {
			t.Fatalf("a prompt is logged in %s", f)
		}
	}
}

func TestAgentSendNotRunning(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	if err := os.WriteFile(filepath.Join(app, "run"), []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newExitedAgent(t, home, app, "helper")
	v, exit := wdxJSON(t, home, "agent", "send", "--id", a.Id, "--prompt", "hello")
	wantError(t, v, exit, api.ErrorCodeAgentNotRunning, clikit.ExitRejected)
	wantHint(t, v, "start it with wdx agent start, or open its project")
}

func TestAgentSendRemoved(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	parent := newAgent(t, home, app, "parent")
	if v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--parent-name", "parent", "--name", "child"); exit != clikit.ExitOK {
		t.Fatalf("create: exit %d, %+v", exit, v)
	}
	if v, exit := wdxJSON(t, home, "agent", "remove", "--id", parent.Id, "--stop-running"); exit != clikit.ExitOK {
		t.Fatalf("remove: exit %d, %+v", exit, v)
	}
	v, exit := wdxJSON(t, home, "agent", "send", "--id", parent.Id, "--prompt", "hello")
	wantError(t, v, exit, api.ErrorCodeAgentRemoved, clikit.ExitRejected)
}

func TestAgentTranscript(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	file := filepath.Join(identity.AgentDir(home, a.Id), aca.TranscriptFile)
	if err := os.WriteFile(file, []byte("# Session s\n\nhello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, exit := wdxJSONWith(t, home, in(app), "agent", "transcript", "--name", "helper")
	if exit != clikit.ExitOK || v.Result != "" || v.Transcript.Path != file || v.Transcript.Next != 3 || !slices.Equal(v.Transcript.Lines, []string{"# Session s", "", "hello"}) {
		t.Fatalf("want every line, got exit %d, %+v", exit, v.Transcript)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "transcript", "--name", "helper", "--tail", "1")
	if exit != clikit.ExitOK || v.Transcript.From != 2 || !slices.Equal(v.Transcript.Lines, []string{"hello"}) {
		t.Fatalf("want the last line, got exit %d, %+v", exit, v.Transcript)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "transcript", "--name", "helper", "--from", "2", "--count", "1")
	if exit != clikit.ExitOK || v.Transcript.From != 2 || !slices.Equal(v.Transcript.Lines, []string{"hello"}) {
		t.Fatalf("want line 2, got exit %d, %+v", exit, v.Transcript)
	}
	for _, flags := range [][]string{{"--tail", "2", "--from", "3"}, {"--tail", "2", "--count", "3"}, {"--path", "--tail", "2"}, {"--tail", "0"}} {
		v, exit = wdxJSONWith(t, home, in(app), append([]string{"agent", "transcript", "--name", "helper"}, flags...)...)
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "transcript", "--name", "nobody")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)

	out := wdxWith(t, home, in(app), "agent", "transcript", "--name", "helper", "--tail", "1")
	if out.exit != clikit.ExitOK || out.stdout != "hello\n" {
		t.Fatalf("text: %+v", out)
	}
	out = wdxWith(t, home, in(app), "agent", "transcript", "--name", "helper", "--path")
	if out.exit != clikit.ExitOK || out.stdout != file+"\n" {
		t.Fatalf("path: %+v", out)
	}
}

func TestAgentCreateWithAPrompt(t *testing.T) {
	t.Parallel()
	const secret = "the-secret-task"
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--name", "empty", "--prompt", " ")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	wantHint(t, v, "agent create --help")

	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--name", "worker", "--prompt", secret)
	if exit != clikit.ExitOK || v.Result != "created" {
		t.Fatalf("create: %d, %+v", exit, v)
	}
	out := wdxWith(t, home, in(app), "message", "list")
	if !strings.Contains(out.stdout, secret) || !strings.Contains(out.stdout, "worker") {
		t.Errorf("its message: %+v", out)
	}
	logs, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("logs: %v, %v", logs, err)
	}
	for _, name := range logs {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) {
			t.Errorf("the prompt is in %s", name)
		}
	}
}

func TestAgentRestart(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")

	v, exit := wdxJSONWith(t, home, in(app), "agent", "restart", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeAgentBusy, clikit.ExitRejected)
	wantHint(t, v, "--force")

	v, exit = wdxJSONWith(t, home, in(app), "agent", "restart", "--name", "helper", "--force")
	if exit != clikit.ExitOK || v.Result != "restarted" || v.Agent.Id != a.Id || v.Agent.State != api.AgentStateRunning || v.Agent.SessionId == a.SessionId {
		t.Fatalf("want helper restarted, got exit %d, %+v", exit, v)
	}
	if got := launches(t, home); !slices.Equal(got, []string{a.SessionId, v.Agent.SessionId}) {
		t.Fatalf("sessions = %q", got)
	}
	v, exit = wdxJSON(t, home, "agent", "restart", "--id", "agent-nothing")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "restart")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)

	out := wdxWith(t, home, in(app), "agent", "restart", "--name", "helper", "--force")
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "restarted agent helper") {
		t.Fatalf("text: %+v", out)
	}

	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")
	v, exit = wdxJSON(t, home, "agent", "restart", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeProjectClosed, clikit.ExitRejected)
}

// An agent that is not running needs no --force, and one that does not start says so.
func TestAgentRestartOfOneThatEnded(t *testing.T) {
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
	v, exit := wdxJSON(t, home, "agent", "restart", "--id", a.Id)
	if exit != clikit.ExitOK || v.Result != "restarted" || v.Agent.SessionId == a.SessionId {
		t.Fatalf("want it restarted, got exit %d, %+v", exit, v)
	}
	waitAgent(t, home, a.Id, api.AgentStateExited)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSON(t, home, "agent", "restart", "--id", a.Id)
	wantError(t, v, exit, api.ErrorCodeStartFailed, clikit.ExitSystem)
}

func TestAgentRestartOfATombstone(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	parent := newAgent(t, home, app, "parent")
	if v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--name", "child", "--parent-name", "parent"); exit != clikit.ExitOK {
		t.Fatalf("child: %d, %+v", exit, v)
	}
	if v, exit := wdxJSON(t, home, "agent", "remove", "--id", parent.Id, "--stop-running"); exit != clikit.ExitOK {
		t.Fatalf("remove: %d, %+v", exit, v)
	}
	v, exit := wdxJSON(t, home, "agent", "restart", "--id", parent.Id, "--force")
	wantError(t, v, exit, api.ErrorCodeAgentRemoved, clikit.ExitRejected)
}

func TestAgentTranscriptBrief(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	v, exit := wdxJSON(t, home, "agent", "transcript", "--id", a.Id, "--brief")
	if exit != clikit.ExitOK || len(v.Transcript.Lines) != 0 {
		t.Fatalf("nothing said yet: exit %d, %+v", exit, v.Transcript)
	}
	file := filepath.Join(identity.AgentDir(home, a.Id), aca.TranscriptFile)
	text := "# Session s\n\n## User · 2026-11-01T13:09:49Z\n\nthe request\n\n" +
		"## Assistant · 2026-11-01T13:09:50Z\n\n### Tool: Bash\n\n```\n{\"command\":\"ls\"}\n```\n\n### Result\n\n```\nthe output\n```\n\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxJSON(t, home, "agent", "transcript", "--id", a.Id, "--brief")
	if exit != clikit.ExitOK || v.Result != "" || v.Transcript.Path != file || v.Transcript.From != 2 || v.Transcript.Next != 20 || !slices.Contains(v.Transcript.Lines, "the request") || !slices.Contains(v.Transcript.Lines, "- Bash ls (line 8)") || slices.Contains(v.Transcript.Lines, "the output") {
		t.Fatalf("exit %d, %+v", exit, v.Transcript)
	}
	for _, flags := range [][]string{{"--tail", "2"}, {"--from", "3"}, {"--count", "3"}, {"--path"}} {
		v, exit = wdxJSON(t, home, append([]string{"agent", "transcript", "--id", a.Id, "--brief"}, flags...)...)
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
	}
	v, exit = wdxJSON(t, home, "agent", "transcript", "--id", "agent-nothing", "--brief")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	out := wdxWith(t, home, in(app), "agent", "transcript", "--id", a.Id, "--brief")
	if out.exit != clikit.ExitOK || !strings.HasPrefix(out.stdout, "This is the conversation so far in brief") || !strings.Contains(out.stdout, "the request\n") {
		t.Fatalf("text: %+v", out)
	}
}

// TestAgentContextText: agent get shows how full the context is out of its window,
// the tokens alone with no window, and nothing before the agent's ACA has told.
func TestAgentContextText(t *testing.T) {
	t.Parallel()
	window := int64(200_000)
	for _, tc := range []struct {
		context *api.AgentContext
		want    string
	}{
		{&api.AgentContext{Tokens: 70_086, Window: &window}, "context 70k of 200k (35%)"},
		{&api.AgentContext{Tokens: 70_086}, "context 70k"},
		{nil, ""},
	} {
		var out bytes.Buffer
		agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: api.Agent{Name: "helper", State: api.AgentStateExited, Context: tc.context}}}.text(&out, style.New(false))
		var got string
		for line := range strings.Lines(out.String()) {
			if strings.HasPrefix(strings.TrimSpace(line), "context") {
				got = strings.Join(strings.Fields(line), " ")
			}
		}
		if got != tc.want {
			t.Errorf("%s: %q in %s", tc.want, got, out.String())
		}
	}
}

// TestAgentContextColors: agent get and agent list color the size by the
// thresholds, from each one up.
func TestAgentContextColors(t *testing.T) {
	t.Parallel()
	st, thresholds := style.New(true), contextThresholds{warning: 1000, critical: 2000}
	for _, tc := range []struct {
		tokens int64
		color  func(string) string
	}{
		{999, st.ContextFine}, {1000, st.ContextWarning}, {1999, st.ContextWarning}, {2000, st.ContextCritical}, {2001, st.ContextCritical},
	} {
		a := api.Agent{Name: "helper", State: api.AgentStateExited, Context: &api.AgentContext{Tokens: tc.tokens}}
		var out bytes.Buffer
		agentDetail{AgentEnvelope: api.AgentEnvelope{Agent: a}, thresholds: thresholds}.text(&out, st)
		if want := tc.color(api.TokenCount(tc.tokens)) + "\n"; !strings.Contains(out.String(), want) {
			t.Errorf("get %d: want %q in %q", tc.tokens, want, out.String())
		}
		out.Reset()
		agentList{AgentList: api.AgentList{Agents: []api.Agent{a}}, thresholds: thresholds}.text(&out, st)
		if want := tc.color(api.TokenCount(tc.tokens)) + " "; !strings.Contains(out.String(), want) {
			t.Errorf("list %d: want %q in %q", tc.tokens, want, out.String())
		}
	}
}
