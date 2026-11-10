package server

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/ca"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
	"github.com/srjn45/wdx/internal/unixsocket"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

type testHub struct {
	home   string
	server *Server
	addrs  Addresses
	web    *http.Client
	admin  *http.Client
	stop   func() error // stops the hub once it has served
}

// testConfig is the settings of a hub on a new home, listening on ports the
// system chooses and sending no metrics.
func testConfig(t *testing.T) hubconfig.Config {
	t.Helper()
	cfg := hubconfig.Default()
	cfg.Home = runtest.Dir(t) // it holds the socket
	cfg.PublicURL = "http://localhost:8080"
	cfg.ListenWeb, cfg.ListenNodes = "127.0.0.1:0", "127.0.0.1:0"
	cfg.MasterKey = make([]byte, hubconfig.MasterKeySize)
	if _, err := rand.Read(cfg.MasterKey); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// start runs a hub on a new home until the test ends, with the Options
// adjust changes when not nil, and checks then that every request was
// logged from received to returned.
func start(t *testing.T, adjust func(*Options)) *testHub {
	t.Helper()
	o := Options{Config: testConfig(t), Argon2: account.Argon2{Memory: 8, Time: 1, Threads: 1}}
	if adjust != nil {
		adjust(&o)
	}
	home := o.Config.Home
	sqlitetest.Seed(t, hubconfig.DBPath(home), hubdb.Open)
	logtest.OnFailure(t, home) // registered first, so it runs once the hub stopped
	logs := logging.NewDailyFile(hubconfig.LogsDir(home), hubconfig.Name, 7, time.Now)
	o.Log = logging.New(logs, slog.LevelDebug)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Listen(ctx, o)
	if err != nil {
		cancel()
		logs.Close()
		t.Fatal(err)
	}
	th := &testHub{home: home, server: s, addrs: s.Addresses()}
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	th.stop = sync.OnceValue(func() error {
		cancel()
		return <-served
	})
	t.Cleanup(func() {
		if err := th.stop(); err != nil {
			t.Errorf("serve: %v", err)
		}
		logs.Close()
		wantRequestsLogged(t, home)
	})
	th.web = &http.Client{}
	th.admin = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", th.addrs.Admin)
		},
	}}
	return th
}

// client returns the API's client on the endpoint at base through hc.
func client(t *testing.T, base string, hc *http.Client) *hubapi.ClientWithResponses {
	t.Helper()
	c, err := hubapi.NewClientWithResponses(base, hubapi.WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (th *testHub) webClient(t *testing.T) *hubapi.ClientWithResponses {
	t.Helper()
	return client(t, "http://"+th.addrs.Web, th.web)
}

func (th *testHub) adminClient(t *testing.T) *hubapi.ClientWithResponses {
	t.Helper()
	return client(t, "http://wdx-hub", th.admin)
}

// nodes returns an HTTP client of the node endpoint that trusts the hub's CA
// and shows cert, or no certificate when it is nil.
func (th *testHub) nodes(cert *tls.Certificate) *http.Client {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: th.server.CA().Pool()}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
}

// nodeCert returns a client certificate of a node, which c issued.
func nodeCert(t *testing.T, c *ca.CA) *tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := c.Issue(pub, ca.Request{CommonName: "node", ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, Validity: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// nodeCert returns the client certificate of a node of a user, both made
// in the hub's database, as a login makes them.
func (th *testHub) nodeCert(t *testing.T) *tls.Certificate {
	t.Helper()
	ctx := context.Background()
	user, err := sqlite.NewID("usr")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.server.db.Exec("INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, 'x', 0)", user, user); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	by := node.By{Actor: audit.ActorUser, Address: "127.0.0.1"}
	started, err := th.server.nodes.StartLogin(ctx, by, node.StartRequest{Flow: node.FlowDevice, Name: "desktop", OS: "linux/amd64", Version: "0.5.0", PublicKey: pub})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.server.nodes.Approve(ctx, by, user, started.ID, "desktop"); err != nil {
		t.Fatal(err)
	}
	c, err := th.server.nodes.Poll(ctx, started.DeviceCode, th.server.issue)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(c.Certificate)
	return &tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key}
}

// do sends a request with body, when not empty, and returns the response
// and its body.
func do(t *testing.T, hc *http.Client, method, url, body string, header http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func problemCode(t *testing.T, b []byte) hubapi.ErrorCode {
	t.Helper()
	var p hubapi.Problem
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("not a problem: %s", b)
	}
	return p.Code
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
			if line["page"] == true {
				// A page's line has its status alone.
				if line["status"] == nil || line["query"] != nil || line["body"] != nil || line["response"] != nil {
					t.Errorf("page line %v", line)
				}
			} else if line["response"] == nil || line["status"] == nil || line["endpoint"] == nil {
				t.Errorf("incomplete line %v", line)
			} else if failed, _ := line["status"].(float64); (failed >= 400) != (line["code"] != nil) {
				t.Errorf("want a code exactly when the request failed: %v", line)
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
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(home), "*.log"))
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
			var line map[string]any
			if err := json.Unmarshal(s.Bytes(), &line); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			lines = append(lines, line)
		}
		file.Close()
	}
	return lines
}

// logged reports whether the hub logged msg under the debug id id, or under
// any when id is empty.
func logged(t *testing.T, home, msg, id string) bool {
	t.Helper()
	for _, line := range logLines(t, home) {
		if line["msg"] == msg && (id == "" || line["debug_id"] == id) {
			return true
		}
	}
	return false
}

func wantVersion(t *testing.T, what string, v *hubapi.Version, status int, err error) {
	t.Helper()
	if err != nil || v == nil {
		t.Fatalf("%s: %d %v", what, status, err)
	}
	if v.Name != hubconfig.Name || v.ApiVersion != "1" {
		t.Errorf("%s: %+v", what, v)
	}
}

func TestVersionOnEachEndpoint(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	ctx := context.Background()

	web, err := th.webClient(t).GetVersionWithResponse(ctx)
	wantVersion(t, "web", web.JSON200, web.StatusCode(), err)
	nodes, err := client(t, "https://"+th.addrs.Nodes, th.nodes(th.nodeCert(t))).GetNodeVersionWithResponse(ctx)
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	wantVersion(t, "nodes", nodes.JSON200, nodes.StatusCode(), err)
	admin, err := th.adminClient(t).GetAdminVersionWithResponse(ctx)
	wantVersion(t, "admin", admin.JSON200, admin.StatusCode(), err)
}

func TestNodeEndpointRefusesOtherCertificates(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	other, err := ca.Load(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		cert  *tls.Certificate
		alert string
	}{
		{"none", nil, "certificate required"},
		{"another ca's", nodeCert(t, other), "unknown certificate authority"},
	} {
		_, err := th.nodes(tc.cert).Get("https://" + th.addrs.Nodes + "/v1/node/version")
		// The alert the hub sends at the handshake, as the client reads it.
		if err == nil || !strings.Contains(err.Error(), "remote error: tls: "+tc.alert) {
			t.Errorf("%s: want the handshake refused with %q, got %v", tc.name, tc.alert, err)
		}
	}
	if logged(t, th.home, "request received", "") {
		t.Error("a request got past the handshake")
	}
}

func TestOperationOfAnotherEndpoint(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	nodes := th.nodes(th.nodeCert(t))
	web, node, admin := "http://"+th.addrs.Web, "https://"+th.addrs.Nodes, "http://wdx-hub"
	for _, tc := range []struct {
		name        string
		hc          *http.Client
		method, url string
		body        string
	}{
		{"admin's version on web", th.web, http.MethodGet, web + "/v1/admin/version", ""},
		// The spec refuses the body: refused for its endpoint before that.
		{"a backup on web", th.web, http.MethodPost, web + "/v1/backup", `{"nope": 1}`},
		{"web's version on nodes", nodes, http.MethodGet, node + "/v1/version", ""},
		{"a reload on nodes", nodes, http.MethodPost, node + "/v1/reload", ""},
		{"web's version on admin", th.admin, http.MethodGet, admin + "/v1/version", ""},
		{"node's version on admin", th.admin, http.MethodGet, admin + "/v1/node/version", ""},
		{"no operation", th.admin, http.MethodGet, admin + "/v1/nothing", ""},
	} {
		resp, b := do(t, tc.hc, tc.method, tc.url, tc.body, nil)
		if resp.StatusCode != http.StatusNotFound || problemCode(t, b) != hubapi.ErrorCodeNotFound {
			t.Errorf("%s: want 404 not_found, got %d %s", tc.name, resp.StatusCode, b)
		}
	}
	// Its own endpoint validates it.
	resp, b := do(t, th.admin, http.MethodPost, admin+"/v1/backup", `{"nope": 1}`, nil)
	if resp.StatusCode != http.StatusBadRequest || problemCode(t, b) != hubapi.ErrorCodeUsage {
		t.Errorf("an invalid backup on admin: want 400 usage, got %d %s", resp.StatusCode, b)
	}
}

func TestRequestsLoggedUnderTheClientsDebugID(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	nodes := th.nodes(th.nodeCert(t))
	for i, tc := range []struct {
		hc  *http.Client
		url string
	}{
		{th.web, "http://" + th.addrs.Web + "/v1/version"},
		{nodes, "https://" + th.addrs.Nodes + "/v1/node/version"},
		{th.admin, "http://wdx-hub/v1/admin/version"},
	} {
		id := "dbg-00000000000" + string(rune('a'+i))
		resp, _ := do(t, tc.hc, http.MethodGet, tc.url, "", http.Header{hubapi.DebugIDHeader: {id}})
		if got := resp.Header.Get(hubapi.DebugIDHeader); got != id {
			t.Errorf("%s: response under %q, want %q", tc.url, got, id)
		}
		if !logged(t, th.home, "request received", id) || !logged(t, th.home, "request returned", id) {
			t.Errorf("%s: not logged received and returned under %s", tc.url, id)
		}
	}
}

func TestRequestsLoggedWithTheirAddress(t *testing.T) {
	t.Parallel()
	th := start(t, func(o *Options) {
		o.Config.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	})
	nodes := th.nodes(th.nodeCert(t))
	forwarded := http.Header{"X-Forwarded-For": {"203.0.113.7"}}
	for i, tc := range []struct {
		hc     *http.Client
		url    string
		header http.Header
		want   map[string]any
	}{
		// Through the proxy the hub trusts, the client's and the proxy's.
		{th.web, "http://" + th.addrs.Web + "/v1/version", forwarded, map[string]any{"address": "203.0.113.7", "peer": "127.0.0.1"}},
		{th.web, "http://" + th.addrs.Web + "/v1/version", nil, map[string]any{"address": "127.0.0.1", "peer": nil}},
		{th.web, "http://" + th.addrs.Web + "/sign-in", forwarded, map[string]any{"address": "203.0.113.7", "peer": "127.0.0.1"}},
		{nodes, "https://" + th.addrs.Nodes + "/v1/node/version", forwarded, map[string]any{"address": nil, "peer": "127.0.0.1"}},
		{th.admin, "http://wdx-hub/v1/admin/version", forwarded, map[string]any{"address": nil, "peer": nil}},
	} {
		id := "dbg-00000000001" + string(rune('a'+i))
		header := http.Header{hubapi.DebugIDHeader: {id}}
		for k, v := range tc.header {
			header[k] = v
		}
		resp, _ := do(t, tc.hc, http.MethodGet, tc.url, "", header)
		// A page's lines are under an id of their own.
		id = resp.Header.Get(hubapi.DebugIDHeader)
		for _, msg := range []string{"request received", "request returned"} {
			var line map[string]any
			for _, l := range logLines(t, th.home) {
				if l["msg"] == msg && l["debug_id"] == id {
					line = l
				}
			}
			if line == nil {
				t.Errorf("%s: no %s under %s", tc.url, msg, id)
				continue
			}
			for k, v := range tc.want {
				if line[k] != v {
					t.Errorf("%s %v: %s has %s %v, want %v", tc.url, tc.header, msg, k, line[k], v)
				}
			}
		}
	}
}

// heldReload returns a Reload that tells entered it was called and returns
// cfg once release is closed.
func heldReload(cfg hubconfig.Config) (reload func() (hubconfig.Config, error), entered chan struct{}, release chan struct{}) {
	entered, release = make(chan struct{}, 1), make(chan struct{})
	return func() (hubconfig.Config, error) {
		entered <- struct{}{}
		<-release
		return cfg, nil
	}, entered, release
}

func waitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-waittest.After(t):
		t.Fatal("the reload never started")
	}
}

type answer struct {
	status int
	body   []byte
	err    error
}

// send sends a request on the admin socket in the background, under the
// debug id id.
func (th *testHub) send(method, path, body, id string) <-chan answer {
	done := make(chan answer, 1)
	go func() {
		req, _ := http.NewRequest(method, "http://wdx-hub"+path, strings.NewReader(body))
		req.Header.Set(hubapi.DebugIDHeader, id)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := th.admin.Do(req)
		if err != nil {
			done <- answer{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		done <- answer{status: resp.StatusCode, body: b, err: err}
	}()
	return done
}

func TestChangesOneAtATime(t *testing.T) {
	t.Parallel()
	var entered, release chan struct{}
	th := start(t, func(o *Options) { o.Reload, entered, release = heldReload(o.Config) })
	reloaded := th.send(http.MethodPost, "/v1/reload", "", "dbg-000000000001")
	waitEntered(t, entered)

	backupID := "dbg-000000000002"
	out := filepath.Join(t.TempDir(), "copy.db")
	backedUp := th.send(http.MethodPost, "/v1/backup", `{"path": "`+out+`"}`, backupID)
	var early *answer
	waittest.For(t, "the backup waiting for the reload", func() bool {
		select {
		case a := <-backedUp:
			early = &a
			return true
		default:
			return logged(t, th.home, "request waits for the change in flight", backupID)
		}
	})
	if early != nil {
		close(release)
		<-reloaded
		t.Fatalf("the backup did not wait for the reload: %d %s", early.status, early.body)
	}
	close(release)
	if a := <-reloaded; a.err != nil || a.status != http.StatusOK {
		t.Fatalf("reload: %d %s %v", a.status, a.body, a.err)
	}
	if a := <-backedUp; a.err != nil || a.status != http.StatusCreated {
		t.Fatalf("backup: %d %s %v", a.status, a.body, a.err)
	}
}

func TestStopLetsRequestsFinish(t *testing.T) {
	t.Parallel()
	var entered, release chan struct{}
	th := start(t, func(o *Options) { o.Reload, entered, release = heldReload(o.Config) })
	reloaded := th.send(http.MethodPost, "/v1/reload", "", "dbg-000000000001")
	waitEntered(t, entered)

	stopped := make(chan error, 1)
	go func() { stopped <- th.stop() }()
	waittest.For(t, "the hub stopping", func() bool { return logged(t, th.home, "hub stopping", "") })
	close(release)
	if a := <-reloaded; a.err != nil || a.status != http.StatusOK {
		t.Fatalf("reload: %d %s %v", a.status, a.body, a.err)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestReload(t *testing.T) {
	t.Parallel()
	var level slog.LevelVar
	var next hubconfig.Config
	fail := errors.New("config.yaml: line 3: no setting is named nope")
	var failing atomic.Bool
	th := start(t, func(o *Options) {
		o.LogLevel = &level
		next = o.Config
		next.LogLevel = slog.LevelDebug
		next.ListenWeb = "127.0.0.1:9999"
		o.Reload = func() (hubconfig.Config, error) {
			if failing.Load() {
				return hubconfig.Config{}, fail
			}
			return next, nil
		}
	})
	ctx := context.Background()
	r, err := th.adminClient(t).ReloadWithResponse(ctx)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("reload: %v %s", err, r.Body)
	}
	if got := r.JSON200; got.Result != hubapi.ResultReloaded || len(got.NotApplied) != 1 || got.NotApplied[0] != "listen.web" {
		t.Errorf("want reloaded with listen.web not applied, got %+v", got)
	}
	if level.Level() != slog.LevelDebug {
		t.Errorf("log.level not applied: %v", level.Level())
	}

	failing.Store(true)
	resp, b := do(t, th.admin, http.MethodPost, "http://wdx-hub/v1/reload", "", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || problemCode(t, b) != hubapi.ErrorCodeReloadFailed {
		t.Errorf("want reload_failed, got %d %s", resp.StatusCode, b)
	}
}

func TestBackup(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	ctx := context.Background()
	admin := th.adminClient(t)
	out := filepath.Join(t.TempDir(), "copy.db")
	r, err := admin.BackupWithResponse(ctx, hubapi.Backup{Path: out})
	if err != nil || r.JSON201 == nil || r.JSON201.Result != hubapi.ResultBackedUp || r.JSON201.Path != out || r.JSON201.CaPath != out+".ca" {
		t.Fatalf("backup: %v %d %s", err, r.StatusCode(), r.Body)
	}
	db, err := hubdb.Open(ctx, out)
	if err != nil {
		t.Fatalf("the copy does not open: %v", err)
	}
	db.Close()
	wantMode(t, out+".ca", os.ModeDir|0o700)
	for _, name := range []string{"ca.key", "ca.crt", "nodes.key", "nodes.crt"} {
		wantMode(t, filepath.Join(out+".ca", name), 0o600)
		got, err := os.ReadFile(filepath.Join(out+".ca", name))
		if want, _ := os.ReadFile(filepath.Join(hubconfig.CADir(th.home), name)); err != nil || string(got) != string(want) {
			t.Errorf("%s: the copy differs from the hub's: %v", name, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(out), ".*")); len(left) != 0 {
		t.Errorf("left beside the backup: %v", left)
	}
	// Nothing is written when the CA's path is taken.
	taken := filepath.Join(t.TempDir(), "taken.db")
	if err := os.Mkdir(taken+".ca", 0o700); err != nil {
		t.Fatal(err)
	}
	refused, err := admin.BackupWithResponse(ctx, hubapi.Backup{Path: taken})
	if err != nil || refused.StatusCode() != http.StatusConflict || problemCode(t, refused.Body) != hubapi.ErrorCodePathExists {
		t.Errorf("a backup where its CA's copy is: want path_exists, got %v %d %s", err, refused.StatusCode(), refused.Body)
	}
	if left, _ := os.ReadDir(filepath.Dir(taken)); len(left) != 1 {
		t.Errorf("want only the directory there, got %v", left)
	}

	again, err := admin.BackupWithResponse(ctx, hubapi.Backup{Path: out})
	if err != nil || again.StatusCode() != http.StatusConflict || problemCode(t, again.Body) != hubapi.ErrorCodePathExists {
		t.Errorf("a backup where one is: want path_exists, got %v %d %s", err, again.StatusCode(), again.Body)
	}
	relative, err := admin.BackupWithResponse(ctx, hubapi.Backup{Path: "copy.db"})
	if err != nil || relative.StatusCode() != http.StatusUnprocessableEntity || problemCode(t, relative.Body) != hubapi.ErrorCodeInvalidArgument {
		t.Errorf("a relative path: want invalid_argument, got %v %d %s", err, relative.StatusCode(), relative.Body)
	}

	// Restored, the copy and its CA start a hub its nodes still trust.
	restored := testConfig(t)
	if err := os.Rename(out, hubconfig.DBPath(restored.Home)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(out+".ca", hubconfig.CADir(restored.Home)); err != nil {
		t.Fatal(err)
	}
	restored.MasterKey = th.server.opts.Config.MasterKey
	s, err := Listen(ctx, Options{Config: restored, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("the restored hub: %v", err)
	}
	if !s.CA().Certificate().Equal(th.server.CA().Certificate()) {
		t.Error("the restored hub has another CA")
	}
	s.close()
}

// wantMode checks path's type and permissions are mode.
func wantMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode() & (os.ModeType | os.ModePerm); got != mode {
		t.Errorf("%s: mode %v, want %v", path, got, mode)
	}
}

// A hub whose database has nodes refuses to start without the CA they trust,
// rather than make another; with only removed nodes, it makes one.
func TestCAMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := testConfig(t)
	sqlitetest.Seed(t, hubconfig.DBPath(cfg.Home), hubdb.Open)
	db, err := hubdb.Open(ctx, hubconfig.DBPath(cfg.Home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (id, username, created_at) VALUES ('usr-1', 'alice', 1);
		INSERT INTO nodes (id, user_id, name, public_key, os, version, created_at) VALUES ('node-1', 'usr-1', 'desktop', x'00', 'linux/amd64', '0.5.0', 1)`); err != nil {
		t.Fatal(err)
	}
	_, err = Listen(ctx, Options{Config: cfg, Log: slog.New(slog.DiscardHandler)})
	if e, ok := errors.AsType[*CAMissingError](err); !ok || e.Nodes != 1 || e.Dir != hubconfig.CADir(cfg.Home) {
		t.Fatalf("want *CAMissingError, got %v", err)
	}
	if made, err := ca.Made(hubconfig.CADir(cfg.Home)); made || err != nil {
		t.Fatalf("a CA was made: %v", err)
	}

	if _, err := db.Exec(`UPDATE nodes SET revoked_at = 2`); err != nil {
		t.Fatal(err)
	}
	s, err := Listen(ctx, Options{Config: cfg, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("with only removed nodes: %v", err)
	}
	s.close()
}

func TestAlreadyRunning(t *testing.T) {
	t.Parallel()
	th := start(t, nil)
	cfg := testConfig(t)
	cfg.Home = th.home
	_, err := Listen(context.Background(), Options{Config: cfg, Log: slog.New(slog.DiscardHandler)})
	if _, ok := errors.AsType[*AlreadyRunningError](err); !ok {
		t.Fatalf("want *AlreadyRunningError, got %v", err)
	}
}

// A hub that cannot listen on its socket, as in a home whose path is too long
// for one, fails to start and gives back what it took.
func TestSocketTooLong(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Home = filepath.Join(t.TempDir(), strings.Repeat("h", 120))
	_, err := Listen(context.Background(), Options{Config: cfg, Log: slog.New(slog.DiscardHandler)})
	if e, ok := errors.AsType[*unixsocket.TooLongError](err); !ok || e.Path != hubconfig.SocketPath(cfg.Home) {
		t.Fatalf("want the socket's path refused as too long, got %v", err)
	}
	if _, err := os.Stat(cfg.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want nothing made before the path is checked, got %v", err)
	}
}

// A socket that cannot be listened on, here for a directory in its place
// that is not removed, gives the lock back.
func TestSocketCannotListen(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	sqlitetest.Seed(t, hubconfig.DBPath(cfg.Home), hubdb.Open)
	if err := os.MkdirAll(filepath.Join(hubconfig.SocketPath(cfg.Home), "kept"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(context.Background(), Options{Config: cfg, Log: slog.New(slog.DiscardHandler)}); err == nil {
		t.Fatal("want the socket refused")
	}
	unlock, err := lock(hubconfig.LockPath(cfg.Home))
	if err != nil {
		t.Fatalf("want the lock given back, got %v", err)
	}
	unlock.Close()
}
