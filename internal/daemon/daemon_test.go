package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/gittest"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	m.Run()
}

type testDaemon struct {
	home, socket string
	client       *api.ClientWithResponses
	http         *http.Client
}

// start runs a daemon on a new home until the test ends, and checks then
// that every request was logged from received to returned.
func start(t *testing.T) *testDaemon {
	t.Helper()
	home := t.TempDir()
	td := &testDaemon{home: home, socket: socketPath(t)}
	logs := logging.NewDailyFile(identity.LogsDir(home), 7, time.Now)
	ctx, stop := context.WithCancel(context.Background())
	d, err := Listen(ctx, Options{Home: home, Socket: td.socket, Log: logging.New(logs, slog.LevelDebug)})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
		logs.Close()
		wantRequestsLogged(t, home)
	})

	td.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", td.socket)
		},
	}}
	if td.client, err = api.NewClientWithResponses("http://wdx", api.WithHTTPClient(td.http)); err != nil {
		t.Fatal(err)
	}
	return td
}

// socketPath returns a socket path short enough for the 104 bytes macOS
// allows, which a path under t.TempDir() may exceed.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "wdx.sock")
}

// wantRequestsLogged checks every request was logged as received and
// returned under one debug id.
func wantRequestsLogged(t *testing.T, home string) {
	t.Helper()
	received := map[string]int{}
	for _, line := range logLines(t, home) {
		switch line["msg"] {
		case "request received":
			received[line["debug_id"].(string)]++
		case "request returned":
			if line["response"] == nil || line["status"] == nil {
				t.Errorf("incomplete line %v", line)
			}
			received[line["debug_id"].(string)]--
		}
	}
	for id, n := range received {
		if n != 0 {
			t.Errorf("%s: received and returned do not pair up", id)
		}
	}
}

func logLines(t *testing.T, home string) []map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(strings.NewReader(string(b)))
		s.Buffer(nil, 1<<20)
		for s.Scan() {
			var line map[string]any
			if err := json.Unmarshal(s.Bytes(), &line); err != nil {
				t.Fatalf("broken log line %q: %v", s.Text(), err)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

// do sends a raw request and returns the status and the decoded body.
func (td *testDaemon) do(t *testing.T, method, path, body string, header ...string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, "http://wdx"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := td.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s %s: body is not JSON: %q", method, path, b)
	}
	return resp, v
}

func wantProblem(t *testing.T, p *api.Problem, status int, code api.ErrorCode) {
	t.Helper()
	if p == nil {
		t.Fatalf("want problem %s, got none", code)
	}
	if p.Status != status || p.Code != code || p.Type != "urn:wdx:error:"+string(code) || p.Title != api.Codes[code].Description || p.Detail == "" || !logging.IsDebugID(p.DebugId) {
		t.Fatalf("want %d %s, got %+v", status, code, p)
	}
}

func newSource(path string) api.Source {
	var s api.Source
	s.FromNewSource(api.NewSource{Type: api.NewSourceTypeNew, Path: optional(path)})
	return s
}

func localSource(path string) api.Source {
	var s api.Source
	s.FromLocalSource(api.LocalSource{Type: api.LocalSourceTypeLocal, Path: path})
	return s
}

func gitSource(url string) api.Source {
	var s api.Source
	s.FromGitSource(api.GitSource{Type: api.GitSourceTypeGit, Url: url})
	return s
}

func optional[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

func (td *testDaemon) create(t *testing.T, name string) api.Project {
	t.Helper()
	r, err := td.client.CreateProjectWithResponse(context.Background(), api.CreateProject{Source: newSource(""), Name: &name})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("create %s: %v %s", name, err, r.Body)
	}
	return r.JSON201.Project
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitRepo(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, name)
	gitCmd(t, filepath.Dir(dir), "init", "-q", dir)
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func bareRemote(t *testing.T, name string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), name+".git")
	gitCmd(t, gitRepo(t, name), "clone", "-q", "--bare", ".", bare)
	return bare
}

func TestVersion(t *testing.T) {
	t.Parallel()
	td := start(t)
	r, err := td.client.GetVersionWithResponse(context.Background())
	if err != nil || r.JSON200 == nil || r.JSON200.Name != identity.Name || r.JSON200.Version != identity.Version {
		t.Fatalf("unexpected version: %v %s", err, r.Body)
	}
	if got := r.HTTPResponse.Header.Get(api.VersionHeader); got != identity.Version {
		t.Fatalf("want version header %s, got %q", identity.Version, got)
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	p := td.create(t, "app")
	if p.Name != "app" || p.State != api.ProjectStateOpen || p.Origin != nil || len(p.Worktrees) != 1 || p.Worktrees[0].Path != filepath.Join(td.home, "projects", "app") {
		t.Fatalf("unexpected project %+v", p)
	}

	name := "APP"
	r, err := td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: newSource(""), Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeNameTaken)
	if d := *r.ApplicationproblemJSONDefault.Details; d["name"] != "APP" || d["project"].(map[string]any)["id"] != p.Id {
		t.Fatalf("unexpected details %v", d)
	}

	open := api.CreateProjectOnConflictOpen
	r, err = td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: newSource(""), Name: &name, OnConflict: &open})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("want opened, got %v %s", err, r.Body)
	}
	if res := r.JSON200; res.Result != api.ResultOpened || res.Project.Id != p.Id || res.Conflict == nil || res.Conflict.Code != api.ErrorCodeNameTaken {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestCreateConflictWithRepository(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := gitRepo(t, "repo")
	name := "sub"
	open := api.CreateProjectOnConflictOpen
	r, err := td.client.CreateProjectWithResponse(context.Background(), api.CreateProject{Source: newSource(repo), Name: &name, OnConflict: &open})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("want registered, got %v %s", err, r.Body)
	}
	if res := r.JSON201; res.Result != api.ResultRegistered || res.Project.Worktrees[0].Path != repo || res.Conflict.Code != api.ErrorCodeRepoExists {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestCreateInvalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	for _, tc := range []struct {
		name, path string
		code       api.ErrorCode
	}{
		{"", "", api.ErrorCodeInvalidName},
		{"a:b", "", api.ErrorCodeInvalidName},
		{"../app", "", api.ErrorCodeInvalidName},
		{"app", "relative/dir", api.ErrorCodeInvalidPath},
	} {
		r, err := td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: newSource(tc.path), Name: &tc.name})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, tc.code)
	}
}

func TestOpenLocal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := gitRepo(t, "app")

	r, err := td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: localSource(repo)})
	if err != nil || r.JSON201 == nil || r.JSON201.Result != api.ResultRegistered || r.JSON201.Project.Name != "app" {
		t.Fatalf("want registered, got %v %s", err, r.Body)
	}
	r, err = td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: localSource(repo)})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultOpened {
		t.Fatalf("want opened, got %v %s", err, r.Body)
	}

	for path, tc := range map[string]struct {
		status int
		code   api.ErrorCode
	}{
		"relative":  {http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath},
		t.TempDir(): {http.StatusUnprocessableEntity, api.ErrorCodeNotGitRepo},
	} {
		r, err := td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: localSource(path)})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, tc.status, tc.code)
	}
}

func TestOpenGitAndMissingCheckout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	remote := bareRemote(t, "app")
	body := api.CreateProject{Source: gitSource(remote)}

	r, err := td.client.CreateProjectWithResponse(ctx, body)
	if err != nil || r.JSON201 == nil || r.JSON201.Result != api.ResultCloned {
		t.Fatalf("want cloned, got %v %s", err, r.Body)
	}
	id := r.JSON201.Project.Id
	clone := r.JSON201.Project.Worktrees[0].Path
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}

	r, err = td.client.CreateProjectWithResponse(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeCheckoutMissing)
	if d := *r.ApplicationproblemJSONDefault.Details; d["clone_path"] != clone {
		t.Fatalf("want clone_path %s, got %v", clone, d)
	}

	reclone := api.CreateProjectOnMissingCheckoutReclone
	body.OnMissingCheckout = &reclone
	r, err = td.client.CreateProjectWithResponse(ctx, body)
	if err != nil || r.JSON201 == nil || r.JSON201.Result != api.ResultCloned || r.JSON201.Project.Id != id {
		t.Fatalf("want recloned, got %v %s", err, r.Body)
	}

	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	remove := api.CreateProjectOnMissingCheckoutRemove
	body.OnMissingCheckout = &remove
	r, err = td.client.CreateProjectWithResponse(ctx, body)
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultRemoved || r.JSON200.Project.Id != id {
		t.Fatalf("want removed, got %v %s", err, r.Body)
	}
}

func TestOpenGitErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	for url, tc := range map[string]struct {
		status int
		code   api.ErrorCode
	}{
		"":                                  {http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument},
		filepath.Join(t.TempDir(), "x.git"): {http.StatusBadGateway, api.ErrorCodeCloneFailed},
	} {
		r, err := td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: gitSource(url)})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, tc.status, tc.code)
	}
}

func TestProjectReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	p := td.create(t, "App")

	for _, ref := range []string{"App", "app", "id:" + p.Id} {
		r, err := td.client.GetProjectWithResponse(ctx, ref)
		if err != nil || r.JSON200 == nil || r.JSON200.Project.Id != p.Id {
			t.Fatalf("get %s: %v %s", ref, err, r.Body)
		}
		v := r.JSON200.Project
		if v.Orphaned == nil || *v.Orphaned || v.Worktrees[0].Present == nil || !*v.Worktrees[0].Present {
			t.Fatalf("get %s: want disk status, got %+v", ref, v)
		}
	}
	for _, ref := range []string{p.Id, "id:App", "nope"} {
		r, err := td.client.GetProjectWithResponse(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	}
}

func TestProblemShape(t *testing.T) {
	t.Parallel()
	td := start(t)
	resp, v := td.do(t, http.MethodGet, "/v1/projects/nope", "")
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("want a problem, got %s", ct)
	}
	for _, key := range []string{"type", "title", "status", "detail", "code", "hint", "details", "debug_id"} {
		if _, ok := v[key]; !ok {
			t.Fatalf("want %s in %v", key, v)
		}
	}
	if v["hint"] != nil || v["details"] != nil || v["debug_id"] != resp.Header.Get(api.DebugIDHeader) {
		t.Fatalf("unexpected problem %v", v)
	}
}

func TestUpdateAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app")
	td.create(t, "beta")

	r, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultClosed || r.JSON200.Project.State != api.ProjectStateClosed {
		t.Fatalf("want closed, got %v %s", err, r.Body)
	}
	for state, want := range map[api.ProjectState]string{"": "app beta", api.ProjectStateOpen: "beta", api.ProjectStateClosed: "app"} {
		l, err := td.client.ListProjectsWithResponse(ctx, &api.ListProjectsParams{State: optional(state)})
		if err != nil || l.JSON200 == nil {
			t.Fatalf("list %s: %v %s", state, err, l.Body)
		}
		var names []string
		for _, p := range l.JSON200.Projects {
			names = append(names, p.Name)
		}
		if strings.Join(names, " ") != want {
			t.Fatalf("list %q: want %s, got %v", state, want, names)
		}
	}

	r, err = td.client.UpdateProjectWithResponse(ctx, "id:"+app.Id, api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultOpened || r.JSON200.Project.State != api.ProjectStateOpen {
		t.Fatalf("want opened, got %v %s", err, r.Body)
	}

	if err := os.RemoveAll(app.Worktrees[0].Path); err != nil {
		t.Fatal(err)
	}
	r, err = td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeCheckoutMissing)
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	p := td.create(t, "app")
	r, err := td.client.DeleteProjectWithResponse(ctx, "app")
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultRemoved || r.JSON200.Project.Id != p.Id {
		t.Fatalf("want removed, got %v %s", err, r.Body)
	}
	if _, err := os.Stat(p.Worktrees[0].Path); err != nil {
		t.Fatalf("files on disk must stay: %v", err)
	}
	r, err = td.client.DeleteProjectWithResponse(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}

func TestReclone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)

	r, err := td.client.RecloneProjectWithResponse(ctx, "id:"+td.create(t, "local").Id)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)

	c, err := td.client.CreateProjectWithResponse(ctx, api.CreateProject{Source: gitSource(bareRemote(t, "app"))})
	if err != nil || c.JSON201 == nil {
		t.Fatalf("clone: %v %s", err, c.Body)
	}
	r, err = td.client.RecloneProjectWithResponse(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePathExists)

	if err := os.RemoveAll(c.JSON201.Project.Worktrees[0].Path); err != nil {
		t.Fatal(err)
	}
	r, err = td.client.RecloneProjectWithResponse(ctx, "app")
	if err != nil || r.JSON201 == nil || r.JSON201.Result != api.ResultCloned {
		t.Fatalf("want cloned, got %v %s", err, r.Body)
	}
}

func TestInvalidRequests(t *testing.T) {
	t.Parallel()
	td := start(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/v1/nothing", "", http.StatusNotFound},
		{http.MethodPut, "/v1/version", "", http.StatusNotFound},
		{http.MethodGet, "/v1/projects?state=lost", "", http.StatusBadRequest},
		{http.MethodPost, "/v1/projects", "{", http.StatusBadRequest},
		{http.MethodPost, "/v1/projects", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/projects", `{"source": {"type": "svn"}}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/projects", `{"source": {"type": "local"}}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/projects", `{"source": {"type": "new"}, "colour": "red"}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/projects", `{"source": {"type": "new"}, "on_conflict": "maybe"}`, http.StatusBadRequest},
		{http.MethodPatch, "/v1/projects/app", `{"state": "lost"}`, http.StatusBadRequest},
	} {
		resp, v := td.do(t, tc.method, tc.path, tc.body)
		if resp.StatusCode != tc.status || v["status"] != float64(tc.status) || v["code"] != string(api.ErrorCodeUsage) || v["detail"] == "" {
			t.Errorf("%s %s %s: want %d usage, got %d %v", tc.method, tc.path, tc.body, tc.status, resp.StatusCode, v)
		}
	}
}

func TestDebugIDFollowsRequest(t *testing.T) {
	t.Parallel()
	td := start(t)
	const id = "dbg-0123456789ab"
	resp, _ := td.do(t, http.MethodGet, "/v1/projects/nope", "", api.DebugIDHeader, id, api.VersionHeader, "9.9.9")
	if got := resp.Header.Get(api.DebugIDHeader); got != id {
		t.Fatalf("want debug id %s echoed, got %q", id, got)
	}
	resp, _ = td.do(t, http.MethodGet, "/v1/version", "", api.DebugIDHeader, "not-an-id")
	other := resp.Header.Get(api.DebugIDHeader)
	if !logging.IsDebugID(other) || other == id {
		t.Fatalf("want a new debug id for a malformed one, got %q", other)
	}

	var msgs []string
	for _, line := range logLines(t, td.home) {
		if line["debug_id"] == id {
			msgs = append(msgs, line["msg"].(string))
			if line["msg"] == "request received" && line["client_version"] != "9.9.9" {
				t.Fatalf("want the client version logged, got %v", line)
			}
			if line["msg"] == "request returned" && (line["level"] != "ERROR" || line["status"] != 404.0) {
				t.Fatalf("want a failure logged as an error, got %v", line)
			}
		}
	}
	if len(msgs) < 3 || msgs[0] != "request received" || msgs[len(msgs)-1] != "request returned" {
		t.Fatalf("want the request's lines under its id, got %v", msgs)
	}
}

func TestConcurrentChanges(t *testing.T) {
	t.Parallel()
	td := start(t)
	var wg sync.WaitGroup
	for _, name := range strings.Fields("a b c d e f g h") {
		wg.Go(func() {
			r, err := td.client.CreateProjectWithResponse(context.Background(), api.CreateProject{Source: newSource(""), Name: &name})
			if err != nil || r.JSON201 == nil {
				t.Errorf("create %s: %v %s", name, err, r.Body)
			}
		})
	}
	wg.Wait()
	l, err := td.client.ListProjectsWithResponse(context.Background(), nil)
	if err != nil || len(l.JSON200.Projects) != 8 {
		t.Fatalf("want 8 projects, got %v %s", err, l.Body)
	}
}

func TestOneDaemonPerHome(t *testing.T) {
	t.Parallel()
	td := start(t)
	_, err := Listen(context.Background(), Options{Home: td.home, Socket: socketPath(t), Log: logging.New(io.Discard, 0)})
	if running, ok := errors.AsType[*AlreadyRunningError](err); !ok || running.PID != os.Getpid() {
		t.Fatalf("want already running with pid %d, got %v", os.Getpid(), err)
	}
	if _, err := td.client.GetVersionWithResponse(context.Background()); err != nil {
		t.Fatalf("the running daemon must keep serving: %v", err)
	}
}

func TestStopReleasesSocketAndLock(t *testing.T) {
	t.Parallel()
	home, socket := t.TempDir(), socketPath(t)
	opts := Options{Home: home, Socket: socket, Log: logging.New(io.Discard, 0)}
	// A socket left by a crashed daemon is replaced.
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx, stop := context.WithCancel(context.Background())
		d, err := Listen(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(socket); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("want a private socket, got %v, %v", info, err)
		}
		stop()
		if err := d.Serve(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want the socket removed, got %v", err)
		}
	}
}
