package pages_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

// clock is the hub's time, which a test moves.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// hub is a hub served in the test on 127.0.0.1, its clock and the secrets
// the test saw, which no log line may hold.
type hub struct {
	t      *testing.T
	home   string
	base   string // the web endpoint's URL
	server *server.Server
	clock  *clock
	ctx    context.Context

	mu      sync.Mutex
	secrets []string
}

// startHub serves a hub until the test ends, with the Options adjust changes
// when not nil. When it ends, every page request must have been logged from
// received to returned, and no log line may hold a secret the test saw.
func startHub(t *testing.T, adjust func(*server.Options)) *hub {
	t.Helper()
	cfg := hubconfig.Default()
	cfg.Home = runtest.Dir(t) // it holds the socket
	cfg.PublicURL = "http://localhost:8080"
	cfg.ListenWeb, cfg.ListenNodes = "127.0.0.1:0", "127.0.0.1:0"
	cfg.MasterKey = make([]byte, hubconfig.MasterKeySize)
	if _, err := rand.Read(cfg.MasterKey); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	o := server.Options{Config: cfg, Now: c.now, Argon2: account.Argon2{Memory: 8, Time: 1, Threads: 1}, SecretRand: newFixedRand()}
	if adjust != nil {
		adjust(&o)
	}
	sqlitetest.Seed(t, hubconfig.DBPath(cfg.Home), hubdb.Open)
	logtest.OnFailure(t, cfg.Home) // registered first, so it runs once the hub stopped
	logs := logging.NewDailyFile(hubconfig.LogsDir(cfg.Home), hubconfig.Name, 7, time.Now)
	o.Log = logging.New(logs, slog.LevelDebug)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := server.Listen(ctx, o)
	if err != nil {
		cancel()
		logs.Close()
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	h := &hub{t: t, home: cfg.Home, base: "http://" + s.Addresses().Web, server: s, clock: c, ctx: context.Background()}
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
		logs.Close()
		h.wantLogged()
	})
	return h
}

// secret notes values no log line may hold.
func (h *hub) secret(values ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, v := range values {
		if v != "" {
			h.secrets = append(h.secrets, v)
		}
	}
}

// wantLogged checks every page request was logged as received and returned,
// its line holding no query nor form, and no line holds a secret.
func (h *hub) wantLogged() {
	t := h.t
	t.Helper()
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(h.home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	received := map[string]int{}
	for _, f := range files {
		logtest.Scan(t, f, 4<<20, func(raw []byte) {
			var line map[string]any
			if err := json.Unmarshal(raw, &line); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if leaked := h.leaked(line); leaked != "" {
				t.Errorf("a secret, %q, logged: %s", leaked, raw)
			}
			if line["page"] != true {
				return
			}
			if line["query"] != nil || line["body"] != nil || line["response"] != nil {
				t.Errorf("a page's line logs more than its path: %v", line)
			}
			id, _ := line["debug_id"].(string)
			switch line["msg"] {
			case "request received":
				received[id]++
			case "request returned":
				received[id]--
			}
		})
	}
	for id, n := range received {
		if n != 0 {
			t.Errorf("%s: received and returned do not pair up", id)
		}
	}
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// leaked returns a secret the log line holds, or empty. The line's time is
// left out, and a code of digits must stand alone, so that a code is not
// found in a timestamp's digits.
func (h *hub) leaked(line map[string]any) string {
	delete(line, "time")
	b, err := json.Marshal(line)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, secret := range h.secrets {
		if digits.MatchString(secret) {
			if regexp.MustCompile(`(^|[^0-9A-Za-z])` + secret + `([^0-9A-Za-z]|$)`).Match(b) {
				return secret
			}
		} else if bytes.Contains(b, []byte(secret)) {
			return secret
		}
	}
	return ""
}

// logged returns the lines of the hub's log whose msg is msg.
func (h *hub) logged(msg string) []map[string]any {
	h.t.Helper()
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(h.home), "*.log"))
	if err != nil {
		h.t.Fatal(err)
	}
	var lines []map[string]any
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, raw := range bytes.Split(b, []byte("\n")) {
			var line map[string]any
			if json.Unmarshal(raw, &line) == nil && line["msg"] == msg {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// actions are the audit chain's actions, in order.
func (h *hub) actions() []string {
	h.t.Helper()
	db, err := hubdb.Open(h.ctx, hubconfig.DBPath(h.home))
	if err != nil {
		h.t.Fatal(err)
	}
	defer db.Close()
	return column(h.t, db, "SELECT action FROM audit ORDER BY id")
}

func column(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		list = append(list, s)
	}
	return list
}

func count(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// invite is the path of a new invite's link.
func (h *hub) invite() string {
	h.t.Helper()
	inv, err := h.server.Accounts().CreateInvite(h.ctx, account.Admin, time.Hour)
	if err != nil {
		h.t.Fatal(err)
	}
	h.secret(inv.Token)
	return "/signup?invite=" + inv.Token
}

// userID is the id of username.
func (h *hub) userID(username string) string {
	h.t.Helper()
	u, err := h.server.Accounts().UserByUsername(h.ctx, username)
	if err != nil {
		h.t.Fatal(err)
	}
	return u.ID
}

// reset is the path of a new reset link of username.
func (h *hub) reset(username string, secondFactor bool) string {
	h.t.Helper()
	r, err := h.server.Accounts().CreateReset(h.ctx, account.Admin, h.userID(username), secondFactor)
	if err != nil {
		h.t.Fatal(err)
	}
	h.secret(r.Token)
	return "/reset?token=" + r.Token
}

// browser is a person's browser: a client with a cookie jar, and headers
// it adds to every request.
type browser struct {
	t      *testing.T
	hub    *hub
	client *http.Client
	header http.Header
}

func (h *hub) browser() *browser {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return &browser{t: h.t, hub: h, client: &http.Client{Jar: jar}, header: http.Header{}}
}

// session is the browser's session cookie, empty when it has none.
func (b *browser) session() string {
	u, _ := url.Parse(b.hub.base)
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == "wdx_hub_session" {
			return c.Value
		}
	}
	return ""
}

// response is a page as the browser shows it, once it followed the
// redirects.
type response struct {
	status int
	path   string // where it ended
	header http.Header
	body   string
}

func (b *browser) do(method, path string, form url.Values) response {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, b.hub.base+path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	for k, v := range b.header {
		req.Header[k] = v
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	b.hub.secret(b.session())
	return response{status: resp.StatusCode, path: resp.Request.URL.RequestURI(), header: resp.Header, body: string(page)}
}

func (b *browser) get(path string) response {
	b.t.Helper()
	return b.do(http.MethodGet, path, nil)
}

var (
	formPattern  = regexp.MustCompile(`(?s)<form method="post" action="([^"]*)">(.*?)</form>`)
	inputPattern = regexp.MustCompile(`<input ([^>]*)>`)
	attrPattern  = regexp.MustCompile(`([a-z]+)="([^"]*)"`)
)

// submit posts the form of r whose action is action, with its hidden
// fields and the values of fill, which must each be a field of it; a
// checkbox is sent only when filled.
func (b *browser) submit(r response, action string, fill map[string]string) response {
	b.t.Helper()
	for _, m := range formPattern.FindAllStringSubmatch(r.body, -1) {
		if html.UnescapeString(m[1]) != action {
			continue
		}
		form := url.Values{}
		names := map[string]bool{}
		for _, in := range inputPattern.FindAllStringSubmatch(m[2], -1) {
			attrs := map[string]string{}
			for _, a := range attrPattern.FindAllStringSubmatch(in[1], -1) {
				attrs[a[1]] = html.UnescapeString(a[2])
			}
			name := attrs["name"]
			names[name] = true
			if v, ok := fill[name]; ok {
				form.Set(name, v)
			} else if attrs["type"] != "checkbox" {
				form.Set(name, attrs["value"])
			}
		}
		for name := range fill {
			if !names[name] {
				b.t.Fatalf("the form %s has no field %s:\n%s", action, name, m[2])
			}
		}
		return b.do(http.MethodPost, action, form)
	}
	b.t.Fatalf("no form posting to %s on %s (%d):\n%s", action, r.path, r.status, r.body)
	return response{}
}

// want fails unless r is the page at path with status and has every one of
// texts.
func (r response) want(t *testing.T, status int, path string, texts ...string) {
	t.Helper()
	if r.status != status || (path != "" && r.path != path) {
		t.Fatalf("got %d at %s, want %d at %s:\n%s", r.status, r.path, status, path, r.body)
	}
	for _, s := range texts {
		if !strings.Contains(r.body, s) {
			t.Fatalf("the page at %s has no %q:\n%s", r.path, s, r.body)
		}
	}
}

var (
	secretPattern = regexp.MustCompile(`<code class="secret">([^<]*)</code>`)
	codePattern   = regexp.MustCompile(`<li><code>([^<]*)</code></li>`)
)

// keyOf is the authenticator key a page shows as text.
func keyOf(t *testing.T, r response) string {
	t.Helper()
	m := secretPattern.FindStringSubmatch(r.body)
	if m == nil {
		t.Fatalf("no key on %s:\n%s", r.path, r.body)
	}
	if !strings.Contains(r.body, `<img src="data:image/png;base64,`) {
		t.Fatalf("no QR code on %s", r.path)
	}
	return strings.ReplaceAll(m[1], " ", "")
}

// codesOf are the recovery codes a page shows.
func codesOf(t *testing.T, r response) []string {
	t.Helper()
	var codes []string
	for _, m := range codePattern.FindAllStringSubmatch(r.body, -1) {
		codes = append(codes, m[1])
	}
	if len(codes) != account.RecoveryCodes {
		t.Fatalf("%d recovery codes on %s:\n%s", len(codes), r.path, r.body)
	}
	return codes
}

// code is the authenticator's code for key at the hub's time, which then
// moves to the next 30-second step, so that the next code is of a step not
// used yet: the string may still be one used before.
func (h *hub) code(key string) string {
	h.t.Helper()
	c := h.codeAt(key, 0)
	h.clock.advance(30 * time.Second)
	h.secret(c)
	return c
}

// codeAt is the code for key steps 30-second steps from the hub's time.
func (h *hub) codeAt(key string, steps int) string {
	h.t.Helper()
	at := h.clock.now().Add(time.Duration(steps) * 30 * time.Second)
	c, err := totp.GenerateCodeCustom(key, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

// wrongCode is a code that is key's at no step the hub accepts now, one
// either side of its time's.
func (h *hub) wrongCode(key string) string {
	h.t.Helper()
	right := map[string]bool{}
	for steps := -1; steps <= 1; steps++ {
		right[h.codeAt(key, steps)] = true
	}
	for _, c := range []string{"000000", "111111", "222222", "333333"} {
		if !right[c] {
			return c
		}
	}
	panic("four codes taken")
}

// codeNotOf is code for key, failing first when it is also other's at a
// step the hub accepts now: the secrets are fixed, so it fails in every run
// or in none.
func (h *hub) codeNotOf(key, other string) string {
	h.t.Helper()
	c := h.codeAt(key, 0)
	for steps := -1; steps <= 1; steps++ {
		if h.codeAt(other, steps) == c {
			h.t.Fatalf("the code %s is both keys': change fixedRand's seed", c)
		}
	}
	return h.code(key)
}

// fixedRand is the same bytes in every run: the authenticators' secrets made
// from it, and so their codes at the hub's fixed time, are fixed.
type fixedRand struct {
	mu sync.Mutex
	r  *mrand.ChaCha8
}

func newFixedRand() *fixedRand { return &fixedRand{r: mrand.NewChaCha8([32]byte{'w', 'd', 'x'})} }

func (f *fixedRand) Read(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.r.Read(b)
}

const password = "correct horse battery"

// person is someone signed up with an authenticator, and what they keep.
type person struct {
	username, password, key string
	codes                   []string
}

// signUp signs username up from an invite in b, enrolling an authenticator
// and saving the recovery codes, and lands on the Security page.
func (h *hub) signUp(b *browser, username string) person {
	h.t.Helper()
	t := h.t
	h.secret(password)
	form := b.get(h.invite())
	form.want(t, http.StatusOK, "", "Sign up")
	enrol := b.submit(form, "/signup", map[string]string{"username": username, "password": password, "password2": password})
	enrol.want(t, http.StatusOK, "/enrol", "Set up your authenticator")
	key := keyOf(t, enrol)
	h.secret(key)
	codes := b.submit(enrol, "/enrol", map[string]string{"code": h.code(key)})
	codes.want(t, http.StatusOK, "/enrol", "Save your recovery codes", "I have saved them")
	p := person{username: username, password: password, key: key, codes: codesOf(t, codes)}
	h.secret(p.codes...)
	b.submit(codes, "/recovery-codes/saved", map[string]string{"saved": "yes"}).want(t, http.StatusOK, "/security", "Logged in as <strong>"+username+"</strong>")
	return p
}

// login logs p in from b with the password and a code.
func (h *hub) login(b *browser, p person) response {
	h.t.Helper()
	second := b.submit(b.get("/login"), "/login", map[string]string{"username": p.username, "password": p.password})
	second.want(h.t, http.StatusOK, "/login/second-factor", "Your authenticator")
	return b.submit(second, "/login/second-factor", map[string]string{"code": h.code(p.key)})
}

// setSession gives the browser a session cookie of token.
func (b *browser) setSession(token string) {
	u, _ := url.Parse(b.hub.base)
	b.client.Jar.SetCookies(u, []*http.Cookie{{Name: "wdx_hub_session", Value: token, Path: "/"}})
}

// reload has the hub read its settings again, as setting set does.
func (h *hub) reload() {
	h.t.Helper()
	admin := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", h.server.Addresses().Admin)
		},
	}}
	resp, err := admin.Post("http://wdx-hub/v1/reload", "application/json", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("reload: %d", resp.StatusCode)
	}
}

// post posts form to path.
func (b *browser) post(path string, form map[string]string) response {
	b.t.Helper()
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	return b.do(http.MethodPost, path, values)
}
