package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/telemetry"
)

// testShell is the shell of every test terminal: it stays alive until it
// is hung up on.
const testShell = "#!/bin/sh\nexec sleep 60\n"

// removeTerminals removes every terminal, stopping the live ones, so no
// holder outlives the test; it does nothing once the daemon is stopped.
func (td *testDaemon) removeTerminals() {
	ctx := context.Background()
	l, err := td.client.ListTerminalsWithResponse(ctx, nil)
	if err != nil || l.JSON200 == nil {
		return
	}
	for _, term := range l.JSON200.Terminals {
		_, _ = td.client.DeleteTerminalWithResponse(ctx, "id:"+term.Id, &api.DeleteTerminalParams{StopRunning: optional(true)})
	}
}

// createTerminal creates a terminal in dir, of project when it is not "".
func (td *testDaemon) createTerminal(t *testing.T, project, dir, name string) api.Terminal {
	t.Helper()
	r, err := td.client.CreateTerminalWithResponse(context.Background(), api.CreateTerminal{Project: optional(project), Dir: dir, Name: optional(name)})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("create terminal: %v %s", err, r.Body)
	}
	if r.JSON201.Result != api.ResultCreated || r.JSON201.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want a created running terminal, got %+v", r.JSON201)
	}
	return r.JSON201.Terminal
}

func (td *testDaemon) terminals(t *testing.T, params *api.ListTerminalsParams) []api.Terminal {
	t.Helper()
	r, err := td.client.ListTerminalsWithResponse(context.Background(), params)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("list terminals: %v %s", err, r.Body)
	}
	return r.JSON200.Terminals
}

func detail(t *testing.T, p *api.Problem, key string) map[string]any {
	t.Helper()
	if p.Details == nil {
		t.Fatalf("want details.%s, got none", key)
	}
	v, ok := (*p.Details)[key].(map[string]any)
	if !ok {
		t.Fatalf("want details.%s, got %v", key, *p.Details)
	}
	return v
}

func TestCreateTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	root := td.create(t, "app").Worktrees[0].Path
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without a project, the dir names it and the shell starts there.
	byDir := td.createTerminal(t, "", sub, "")
	if byDir.ProjectName != "app" || byDir.Dir != sub || byDir.Pid == nil || byDir.Name == "" || byDir.StartedAt == nil || byDir.ExitCode != nil {
		t.Fatalf("unexpected terminal %+v", byDir)
	}
	// With one, a dir outside it gives way to its clone path.
	byProject := td.createTerminal(t, "app", t.TempDir(), "build")
	if byProject.Dir != root || byProject.Name != "build" {
		t.Fatalf("want build in %s, got %+v", root, byProject)
	}

	for _, tc := range []struct {
		body   api.CreateTerminal
		status int
		code   api.ErrorCode
	}{
		{api.CreateTerminal{Dir: root, Name: optional("BUILD")}, http.StatusConflict, api.ErrorCodeNameTaken},
		{api.CreateTerminal{Dir: root, Name: optional("a:b")}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidName},
		{api.CreateTerminal{Dir: t.TempDir()}, http.StatusNotFound, api.ErrorCodeNotFound},
		{api.CreateTerminal{Dir: "relative"}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath},
		{api.CreateTerminal{Project: optional("nope"), Dir: root}, http.StatusNotFound, api.ErrorCodeNotFound},
	} {
		r, err := td.client.CreateTerminalWithResponse(ctx, tc.body)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, tc.status, tc.code)
		if tc.code == api.ErrorCodeNameTaken && detail(t, r.ApplicationproblemJSONDefault, "terminal")["id"] != byProject.Id {
			t.Fatalf("want the taken terminal in details, got %v", *r.ApplicationproblemJSONDefault.Details)
		}
	}
}

func TestListTerminals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	lib := td.create(t, "lib").Worktrees[0].Path
	td.createTerminal(t, "", app, "b")
	td.createTerminal(t, "", app, "a")
	td.createTerminal(t, "", lib, "c")

	names := func(list []api.Terminal) (s string) {
		for _, term := range list {
			s += term.ProjectName + "/" + term.Name + " "
		}
		return s
	}
	if got := names(td.terminals(t, nil)); got != "app/a app/b lib/c " {
		t.Fatalf("want every terminal by project then name, got %s", got)
	}
	if got := names(td.terminals(t, &api.ListTerminalsParams{Project: optional("LIB")})); got != "lib/c " {
		t.Fatalf("want lib's, got %s", got)
	}
	if got := names(td.terminals(t, &api.ListTerminalsParams{Dir: optional(filepath.Join(app, "deeper"))})); got != "app/a app/b " {
		t.Fatalf("want app's, got %s", got)
	}

	r, err := td.client.ListTerminalsWithResponse(ctx, &api.ListTerminalsParams{Dir: optional(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	r, err = td.client.ListTerminalsWithResponse(ctx, &api.ListTerminalsParams{Project: optional("app"), Dir: optional(app)})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusBadRequest, api.ErrorCodeUsage)
}

func TestGetTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	td.create(t, "lib")
	term := td.createTerminal(t, "", app, "build")

	for _, tc := range []struct {
		ref    string
		params api.GetTerminalParams
	}{
		{"id:" + term.Id, api.GetTerminalParams{}},
		{"BUILD", api.GetTerminalParams{Project: optional("app")}},
		{"build", api.GetTerminalParams{Dir: optional(filepath.Join(app, "deeper"))}},
	} {
		r, err := td.client.GetTerminalWithResponse(ctx, tc.ref, &tc.params)
		if err != nil || r.JSON200 == nil || r.JSON200.Terminal.Id != term.Id || r.JSON200.Terminal.State != api.TerminalStateRunning {
			t.Fatalf("%s: want build, got %v %s", tc.ref, err, r.Body)
		}
	}

	for _, tc := range []struct {
		ref    string
		params api.GetTerminalParams
		status int
		code   api.ErrorCode
	}{
		{"build", api.GetTerminalParams{}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"build", api.GetTerminalParams{Project: optional("lib")}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"id:" + term.Id, api.GetTerminalParams{Project: optional("lib")}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"id:term-00000000", api.GetTerminalParams{}, http.StatusNotFound, api.ErrorCodeNotFound},
	} {
		r, err := td.client.GetTerminalWithResponse(ctx, tc.ref, &tc.params)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, tc.status, tc.code)
	}
}

func TestGetProjectTerminals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	lib := td.create(t, "lib").Worktrees[0].Path
	td.createTerminal(t, "", app, "b")
	td.createTerminal(t, "", app, "a")
	td.createTerminal(t, "", lib, "c")

	for project, want := range map[string]string{"app": "a b ", "lib": "c "} {
		r, err := td.client.GetProjectWithResponse(ctx, project)
		if err != nil || r.JSON200 == nil || r.JSON200.Project.Terminals == nil {
			t.Fatalf("get %s: %v %s", project, err, r.Body)
		}
		var got string
		for _, term := range *r.JSON200.Project.Terminals {
			got += term.Name + " "
		}
		if got != want {
			t.Errorf("%s: want terminals %s, got %s", project, want, got)
		}
	}

	td.create(t, "empty")
	r, err := td.client.GetProjectWithResponse(ctx, "empty")
	if err != nil || r.JSON200 == nil || r.JSON200.Project.Terminals == nil || len(*r.JSON200.Project.Terminals) != 0 {
		t.Fatalf("want an empty list of terminals, got %v %s", err, r.Body)
	}
}

func TestRemoveTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	td.create(t, "lib")
	term := td.createTerminal(t, "", app, "build")

	for _, tc := range []struct {
		ref    string
		params api.DeleteTerminalParams
		status int
		code   api.ErrorCode
	}{
		{"build", api.DeleteTerminalParams{}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"build", api.DeleteTerminalParams{Project: optional("app"), Dir: optional(app)}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"build", api.DeleteTerminalParams{Project: optional("lib")}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"id:" + term.Id, api.DeleteTerminalParams{Project: optional("lib")}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"id:term-00000000", api.DeleteTerminalParams{}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"build", api.DeleteTerminalParams{Dir: optional(app)}, http.StatusConflict, api.ErrorCodeTerminalRunning},
	} {
		r, err := td.client.DeleteTerminalWithResponse(ctx, tc.ref, &tc.params)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, tc.status, tc.code)
	}

	r, err := td.client.DeleteTerminalWithResponse(ctx, "BUILD", &api.DeleteTerminalParams{Project: optional("app"), StopRunning: optional(true)})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultRemoved || r.JSON200.Terminal.Id != term.Id {
		t.Fatalf("want build removed, got %v %s", err, r.Body)
	}
	if list := td.terminals(t, nil); len(list) != 0 {
		t.Fatalf("want no terminal left, got %+v", list)
	}
	if syscall.Kill(*term.Pid, 0) == nil {
		t.Fatalf("want shell %d gone", *term.Pid)
	}
	wantNoTerminalDir(t, td.home, term.Id)
}

// wantNoTerminalDir checks what terminal id kept on disk is deleted.
func wantNoTerminalDir(t *testing.T, home, id string) {
	t.Helper()
	if _, err := os.Stat(identity.TerminalDir(home, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want the directory of %s deleted, got %v", id, err)
	}
}

func TestTerminalHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	term := td.createTerminal(t, "", app, "build")
	quiet := td.createTerminal(t, "", app, "quiet")
	// The test terminals keep no history: it is written as a holder would.
	dir := filepath.Join(identity.TerminalDir(td.home, term.Id), "history")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "0000000000.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		ref         string
		params      api.GetTerminalHistoryParams
		from, count int
	}{
		{"default tail", "build", api.GetTerminalHistoryParams{Project: optional("app")}, 0, 30},
		{"tail", "id:" + term.Id, api.GetTerminalHistoryParams{Tail: optional(5)}, 25, 5},
		{"from", "id:" + term.Id, api.GetTerminalHistoryParams{From: optional(int64(10)), Count: optional(3)}, 10, 3},
		{"from to the end", "id:" + term.Id, api.GetTerminalHistoryParams{From: optional(int64(28))}, 28, 2},
	} {
		r, err := td.client.GetTerminalHistoryWithResponse(ctx, tc.ref, &tc.params)
		if err != nil || r.JSON200 == nil {
			t.Fatalf("%s: %v %s", tc.name, err, r.Body)
		}
		h := r.JSON200.History
		if h.From != int64(tc.from) || h.Next != 30 || h.First != 0 || !slices.Equal(h.Lines, lines[tc.from:tc.from+tc.count]) {
			t.Fatalf("%s: got %+v", tc.name, h)
		}
	}

	r, err := td.client.GetTerminalHistoryWithResponse(ctx, "id:"+quiet.Id, nil)
	if err != nil || r.JSON200 == nil || r.JSON200.History.Next != 0 || r.JSON200.History.Lines == nil || len(r.JSON200.History.Lines) != 0 {
		t.Fatalf("want an empty history, got %v %s", err, r.Body)
	}

	for _, tc := range []struct {
		ref    string
		params api.GetTerminalHistoryParams
		status int
		code   api.ErrorCode
	}{
		{"id:" + term.Id, api.GetTerminalHistoryParams{From: optional(int64(3)), Tail: optional(5)}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"id:" + term.Id, api.GetTerminalHistoryParams{Tail: optional(10_001)}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"build", api.GetTerminalHistoryParams{}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"id:term-00000000", api.GetTerminalHistoryParams{}, http.StatusNotFound, api.ErrorCodeNotFound},
	} {
		r, err := td.client.GetTerminalHistoryWithResponse(ctx, tc.ref, &tc.params)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, tc.status, tc.code)
	}
}

// TestSweepTerminalDirs starts a daemon on a home where a terminal that no
// longer exists left its directory.
func TestSweepTerminalDirs(t *testing.T) {
	t.Parallel()
	home := shortDir(t)
	stray := identity.TerminalDir(home, "term-0badc0de")
	if err := os.MkdirAll(filepath.Join(stray, "history"), 0o700); err != nil {
		t.Fatal(err)
	}
	startOn(t, home, telemetry.Options{})
	wantNoTerminalDir(t, home, "term-0badc0de")
}

func TestStartTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	missing := filepath.Join(app, "missing")

	// A shell that cannot start in its dir leaves the terminal failed.
	r, err := td.client.CreateTerminalWithResponse(ctx, api.CreateTerminal{Dir: missing, Name: optional("build")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusInternalServerError, api.ErrorCodeStartFailed)
	if failed := detail(t, r.ApplicationproblemJSONDefault, "terminal"); failed["state"] != "failed" || failed["error"] == nil {
		t.Fatalf("want the failed terminal in details, got %v", failed)
	}

	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := td.client.StartTerminalWithResponse(ctx, "build", &api.StartTerminalParams{Dir: optional(app)})
	if err != nil || s.JSON200 == nil || s.JSON200.Result != api.ResultStarted || s.JSON200.Terminal.State != api.TerminalStateRunning || s.JSON200.Terminal.Error != nil {
		t.Fatalf("want build started, got %v %s", err, s.Body)
	}
	s, err = td.client.StartTerminalWithResponse(ctx, "id:"+s.JSON200.Terminal.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, s.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeTerminalRunning)
}

func TestCloseAndOpenProjectWithTerminals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	term := td.createTerminal(t, "", app, "build")

	u, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, u.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectInUse)
	if live, _ := (*u.ApplicationproblemJSONDefault.Details)["terminals"].([]any); len(live) != 1 {
		t.Fatalf("want the live terminal in details, got %v", *u.ApplicationproblemJSONDefault.Details)
	}

	u, err = td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed, StopRunning: optional(true)})
	if err != nil || u.JSON200 == nil || u.JSON200.Result != api.ResultClosed || u.JSON200.Stopped == nil {
		t.Fatalf("want app closed, got %v %s", err, u.Body)
	}
	if stopped := (*u.JSON200.Stopped)[0]; len(*u.JSON200.Stopped) != 1 || stopped.Id != term.Id || stopped.State != api.TerminalStateStopped || stopped.Pid != nil {
		t.Fatalf("want build stopped, got %+v", *u.JSON200.Stopped)
	}
	if syscall.Kill(*term.Pid, 0) == nil {
		t.Fatalf("want shell %d gone", *term.Pid)
	}

	c, err := td.client.CreateTerminalWithResponse(ctx, api.CreateTerminal{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, c.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectClosed)
	s, err := td.client.StartTerminalWithResponse(ctx, "build", &api.StartTerminalParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, s.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectClosed)

	u, err = td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil || u.JSON200 == nil || u.JSON200.Result != api.ResultOpened || u.JSON200.Started == nil {
		t.Fatalf("want app opened, got %v %s", err, u.Body)
	}
	if started := (*u.JSON200.Started)[0]; len(*u.JSON200.Started) != 1 || started.Id != term.Id || started.State != api.TerminalStateRunning || started.Pid == nil {
		t.Fatalf("want build relaunched, got %+v", *u.JSON200.Started)
	}
}

func TestRemoveProjectWithTerminals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	term := td.createTerminal(t, "", app, "")

	d, err := td.client.DeleteProjectWithResponse(ctx, "app", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, d.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectInUse)

	d, err = td.client.DeleteProjectWithResponse(ctx, "app", &api.DeleteProjectParams{StopRunning: optional(true)})
	if err != nil || d.JSON200 == nil || d.JSON200.Result != api.ResultRemoved || d.JSON200.Stopped == nil || (*d.JSON200.Stopped)[0].Id != term.Id {
		t.Fatalf("want app removed with its terminal stopped, got %v %s", err, d.Body)
	}
	if list := td.terminals(t, nil); len(list) != 0 {
		t.Fatalf("want the terminals removed with the project, got %+v", list)
	}
	wantNoTerminalDir(t, td.home, term.Id)
}

func TestTerminalsOutliveDaemon(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	term := td.createTerminal(t, "", app, "")
	td.stop()
	if err := syscall.Kill(*term.Pid, 0); err != nil {
		t.Fatalf("want shell %d running after the daemon stopped: %v", *term.Pid, err)
	}

	// The next daemon adopts it before it serves.
	next := startOn(t, td.home, telemetry.Options{})
	list := next.terminals(t, nil)
	if len(list) != 1 || list[0].State != api.TerminalStateRunning || *list[0].Pid != *term.Pid {
		t.Fatalf("want %s adopted running, got %+v", term.Name, list)
	}
}
