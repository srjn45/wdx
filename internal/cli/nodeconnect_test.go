package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/waittest"
)

// connect asks hh's daemon, on its socket, for a stream into node, as the
// CLI does with --node: the connection switched, or the problem it got.
func (hh *hubHome) connect(node, debugID string) (net.Conn, *api.Problem, error) {
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "unix", identity.SocketPath(hh.home))
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "http://wdx/v1/hub/nodes/"+url.PathEscape(node)+"/connect", nil)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", api.StreamUpgrade)
	req.Header.Set(api.DebugIDHeader, debugID)
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	in := bufio.NewReader(conn)
	resp, err := http.ReadResponse(in, req)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer conn.Close()
		var p api.Problem
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			return nil, nil, fmt.Errorf("connect answered %d, no problem: %w", resp.StatusCode, err)
		}
		return nil, &p, nil
	}
	return &switchedConn{Conn: conn, in: in}, nil, nil
}

// switchedConn reads what the reader of the 101 had read ahead first.
type switchedConn struct {
	net.Conn
	in *bufio.Reader
}

func (c *switchedConn) Read(b []byte) (int, error) { return c.in.Read(b) }

// nodeClient speaks HTTP to node's API through hh's daemon, each connection
// a stream of the session, under one debug id and the API version given;
// "" sends none.
type nodeClient struct {
	t         *testing.T
	transport *http.Transport
	debugID   string
	version   string
}

func (hh *hubHome) nodeClient(node, version string) *nodeClient {
	c := &nodeClient{t: hh.t, debugID: logging.NewDebugID(), version: version}
	// One connection, which a request waits for rather than dial another
	// while the last one's goes back to the pool: the tests count streams.
	c.transport = &http.Transport{MaxConnsPerHost: 1, DialContext: func(context.Context, string, string) (net.Conn, error) {
		conn, p, err := hh.connect(node, c.debugID)
		if p != nil {
			return nil, fmt.Errorf("connect: %s: %s", p.Code, p.Detail)
		}
		return conn, err
	}}
	hh.t.Cleanup(c.transport.CloseIdleConnections)
	return c
}

// do sends method path with body, JSON when not nil, and returns the status
// and the response's body.
func (c *nodeClient) do(method, path string, body any) (int, http.Header, []byte, error) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://wdx"+path, r)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(api.DebugIDHeader, c.debugID)
	if c.version != "" {
		req.Header.Set(api.APIVersionHeader, c.version)
	}
	resp, err := (&http.Client{Transport: c.transport}).Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b, err
}

// want sends method path and wants status, and the problem's code when it
// is one.
func (c *nodeClient) want(method, path string, body any, status int, code api.ErrorCode) []byte {
	c.t.Helper()
	got, header, b, err := c.do(method, path, body)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	if got != status {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, got, status, b)
	}
	if v := header.Get(api.APIVersionHeader); v != strconv.Itoa(api.CurrentVersion) {
		c.t.Errorf("%s %s: the host's API version %q", method, path, v)
	}
	if code != "" {
		var p api.Problem
		if err := json.Unmarshal(b, &p); err != nil || p.Code != code {
			c.t.Fatalf("%s %s: %v, problem %s, want %s", method, path, err, b, code)
		}
	}
	return b
}

var currentVersion = strconv.Itoa(api.CurrentVersion)

// openPair is a pair with a session open from the laptop into the desktop.
func openPair(t *testing.T) *hubPair {
	t.Helper()
	return openPairShell(t, testShell)
}

// openPairShell is openPair whose desktop's terminals run script.
func openPairShell(t *testing.T, script string) *hubPair {
	t.Helper()
	p := newHubPairShell(t, script)
	p.setUp()
	p.remoteAccess(true)
	if v, exit := p.laptop.sessionJSON("hub", "session", "open", "--node", "desktop", "--code", p.code()); exit != clikit.ExitOK {
		t.Fatalf("open: %d %+v", exit, v.jsonOutput)
	}
	return p
}

// logLines are the lines logged in home under debugID, each as an object;
// every line for "".
func logLines(t *testing.T, home, debugID string) []map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(file)
		s.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for s.Scan() {
			if !bytes.Contains(s.Bytes(), []byte(debugID)) {
				continue
			}
			var line map[string]any
			if json.Unmarshal(s.Bytes(), &line) == nil && (debugID == "" || line["debug_id"] == debugID) {
				lines = append(lines, line)
			}
		}
		file.Close()
	}
	return lines
}

// rawLines are the lines logged in home that hold text, as written.
func rawLines(t *testing.T, home, text string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(file)
		s.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for s.Scan() {
			if strings.Contains(s.Text(), text) {
				lines = append(lines, s.Text())
			}
		}
		file.Close()
	}
	return lines
}

// lineOf is the first line of lines with msg whose keys have the values in
// want, nil when there is none.
func lineOf(lines []map[string]any, msg string, want map[string]any) map[string]any {
	for _, l := range lines {
		if l["msg"] != msg {
			continue
		}
		ok := true
		for k, v := range want {
			if fmt.Sprint(l[k]) != fmt.Sprint(v) {
				ok = false
			}
		}
		if ok {
			return l
		}
	}
	return nil
}

func TestNodeConnectServesTheHost(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	c := p.laptop.nodeClient("desktop", currentVersion)

	repo := gittest.Repo(t, "remote-app")
	c.want(http.MethodPost, "/v1/projects", map[string]any{"source": map[string]any{"type": "local", "path": repo}}, http.StatusCreated, "")
	if v, _ := p.desktop.projectsJSON(); len(v) != 1 || v[0] != "remote-app" {
		t.Errorf("the desktop's projects %v, want the one created through the session", v)
	}
	if v, _ := p.laptop.projectsJSON(); len(v) != 0 {
		t.Errorf("the laptop's projects %v, want none", v)
	}

	// The idle time moves at every request, not only at a new stream: this
	// one goes on the connection the first opened.
	var before []api.HubSession
	sessions, _ := p.desktop.sessionJSON("hub", "session", "list")
	before = sessions.Sessions
	p.clock.Advance(10 * time.Minute)
	c.want(http.MethodGet, "/v1/projects", nil, http.StatusOK, "")
	sessions, _ = p.desktop.sessionJSON("hub", "session", "list")
	if len(before) != 1 || len(sessions.Sessions) != 1 || sessions.Sessions[0].LastUsedAt.Before(before[0].LastUsedAt.Add(10*time.Minute)) {
		t.Errorf("the session's last use %+v, then %+v: want it moved 10 minutes", before, sessions.Sessions)
	}
	c.transport.CloseIdleConnections()

	// The host logged every request under the client's debug id, with the
	// session and the client node; the laptop logged the connect, its 101
	// and the stream's bytes.
	id := sessions.Sessions[0].Id
	host := logLines(t, p.desktop.home, c.debugID)
	for _, op := range []string{"createProject", "listProjects"} {
		if lineOf(host, "request received", map[string]any{"operation": op, "session_id": id, "from_node": "laptop"}) == nil {
			t.Errorf("the desktop logged no request received of %s with the session: %v", op, host)
		}
	}
	if n := len(lineOf2(host, "request returned", map[string]any{"session_id": id, "from_node": "laptop"})); n != 2 {
		t.Errorf("the desktop logged %d requests returned with the session, want 2: %v", n, host)
	}
	if lineOf(host, "request returned", map[string]any{"status": 201, "session_id": id}) == nil {
		t.Errorf("the desktop logged no createProject returned: %v", host)
	}
	// Each of the host's lines of a stream carries one debug id, the
	// stream's: JSON tools keep one of two under the same key.
	served := rawLines(t, p.desktop.home, `"msg":"session stream served"`)
	if len(served) == 0 {
		t.Error("the desktop logged no session stream served")
	}
	for _, l := range served {
		if n := strings.Count(l, `"debug_id":`); n != 1 {
			t.Errorf("the desktop logged a stream's line with %d debug ids, want 1: %s", n, l)
		}
	}
	waitLogged(t, p.laptop.home, c.debugID, "stream closed")
	client := logLines(t, p.laptop.home, c.debugID)
	if lineOf(client, "request returned", map[string]any{"path": "/v1/hub/nodes/desktop/connect", "status": 101, "response": "byte stream"}) == nil {
		t.Errorf("the laptop logged no connect returned as switched: %v", client)
	}
	closed := lineOf(client, "stream closed", nil)
	if sent, _ := closed["bytes_sent"].(float64); sent == 0 {
		t.Errorf("the stream closed with no bytes counted: %v", closed)
	}
}

// lineOf2 is every line of lines lineOf would match.
func lineOf2(lines []map[string]any, msg string, want map[string]any) []map[string]any {
	var all []map[string]any
	for _, l := range lines {
		if lineOf([]map[string]any{l}, msg, want) != nil {
			all = append(all, l)
		}
	}
	return all
}

// waitLogged waits until home has a line msg under debugID, which a
// goroutine logs after the request returned.
func waitLogged(t *testing.T, home, debugID, msg string) {
	t.Helper()
	waittest.For(t, fmt.Sprintf("a line %q under %s", msg, debugID), func() bool {
		return lineOf(logLines(t, home, debugID), msg, nil) != nil
	})
}

// projectsJSON is the names of hh's projects.
func (hh *hubHome) projectsJSON() ([]string, int) {
	hh.t.Helper()
	out := wdxWith(hh.t, hh.home, hh.env(nil), "project", "list", "--json")
	var v struct {
		Projects []api.Project `json:"projects"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &v); err != nil {
		hh.t.Fatalf("project list: %v\n%s", err, out.Stdout)
	}
	names := []string{}
	for _, p := range v.Projects {
		names = append(names, p.Name)
	}
	return names, out.Exit
}

func TestNodeConnectRefusesRoutes(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	c := p.laptop.nodeClient("desktop", currentVersion)
	refused := []struct{ method, path string }{
		{http.MethodGet, "/v1/settings"},
		{http.MethodPatch, "/v1/settings/hub.remote_access"},
		{http.MethodPost, "/v1/daemon/reload"},
		{http.MethodGet, "/v1/daemon"},
		{http.MethodGet, "/v1/tide/view"},
		{http.MethodGet, "/v1/projects/app/view"},
		{http.MethodPut, "/v1/projects/app/tabs"},
		{http.MethodGet, "/v1/hub"},
		{http.MethodGet, "/v1/hub/sessions"},
		{http.MethodPost, "/v1/hub/logout"},
		{http.MethodPost, "/v1/hub/nodes/laptop/connect"},
		// An operation this host does not have, as a newer client's.
		{http.MethodGet, "/v1/nothing"},
	}
	for _, r := range refused {
		var body any
		if r.method == http.MethodPatch {
			body = map[string]any{"value": "false"}
		}
		c.want(r.method, r.path, body, http.StatusForbidden, api.ErrorCodeRouteNotAllowed)
	}
	// The setting the session was refused is unchanged, and the hub's
	// routes are served locally.
	if s, exit := p.desktop.hubJSON(nil, "hub", "status"); exit != clikit.ExitOK || s.Hub.RemoteAccess != api.HubStatusRemoteAccessOn || s.Hub.Sessions.Incoming != 1 {
		t.Errorf("the desktop's hub status, locally: exit %d %+v", exit, s.Hub)
	}
}

func TestNodeConnectNoSession(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	_, problem, err := p.laptop.connect("desktop", logging.NewDebugID())
	if err != nil || problem == nil || problem.Code != api.ErrorCodeSessionRequired || problem.Status != http.StatusConflict {
		t.Fatalf("connect with no session: %v %+v", err, problem)
	}
}

func TestNodeConnectSessionClosed(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	c := p.laptop.nodeClient("desktop", currentVersion)
	c.want(http.MethodGet, "/v1/projects", nil, http.StatusOK, "")
	sessions, _ := p.desktop.sessionJSON("hub", "session", "list")
	id := sessions.Sessions[0].Id

	// Closed in the host's table behind its back, its stream open: the next
	// request on the stream is refused, as one in flight when it closed.
	d, err := db.Open(context.Background(), identity.DBPath(p.desktop.home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE hub_sessions SET closed_at = 1, closed_reason = 'closed' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	d.Close()
	c.want(http.MethodGet, "/v1/projects", nil, http.StatusConflict, api.ErrorCodeSessionClosed)
	// The host closed the stream after: the next connect finds the session
	// closed, as good as none.
	if _, _, _, err := c.do(http.MethodGet, "/v1/projects", nil); err == nil || !strings.Contains(err.Error(), string(api.ErrorCodeSessionRequired)) {
		t.Errorf("a request after the session closed: %v, want session_required at connect", err)
	}
}

func TestNodeConnectStreamCutWhenSessionCloses(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	conn, problem, err := p.laptop.connect("desktop", logging.NewDebugID())
	if err != nil || problem != nil {
		t.Fatalf("connect: %v %+v", err, problem)
	}
	defer conn.Close()
	sessions, _ := p.desktop.sessionJSON("hub", "session", "list")
	if v, exit := p.desktop.sessionJSON("hub", "session", "close", "--id", sessions.Sessions[0].Id); exit != clikit.ExitOK {
		t.Fatalf("close: %d %+v", exit, v.jsonOutput)
	}
	// The host cut the stream: the laptop's connection ends, unasked.
	_ = conn.SetReadDeadline(waittest.Deadline(t))
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("the stream after its session closed: %v, want it ended", err)
	}
	// Ended by the cut, not by the session server's idle timeout.
	if lineOf(logLines(t, p.desktop.home, ""), "session streams cut", map[string]any{"streams": 1}) == nil {
		t.Error("the desktop logged no stream cut as the session closed")
	}
}

func TestNodeConnectVersions(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	for _, version := range []string{strconv.Itoa(api.OldestCompatible - 1), ""} {
		c := p.laptop.nodeClient("desktop", version)
		b := c.want(http.MethodGet, "/v1/projects", nil, http.StatusConflict, api.ErrorCodeNodeVersionUnsupported)
		var problem api.Problem
		_ = json.Unmarshal(b, &problem)
		if problem.Details == nil || (*problem.Details)["update"] != "client" || !strings.Contains(problem.Detail, "update wdx on laptop") {
			t.Errorf("version %q: %+v, want the client named to update", version, problem)
		}
	}
	p.laptop.nodeClient("desktop", strconv.Itoa(api.OldestCompatible)).want(http.MethodGet, "/v1/projects", nil, http.StatusOK, "")
}
