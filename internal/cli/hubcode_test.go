package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/pty"
)

// codeRun is a code command running on a terminal the test types into, its
// stderr read as it runs.
type codeRun struct {
	*running
	terminal *os.File // the terminal's master side
}

// startOnTerminal runs args beside the test with a terminal for its stdin.
func (hh *hubHome) startOnTerminal(args ...string) *codeRun {
	t := hh.t
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slave.Close()
		master.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, master) }() // the echo
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{args: args, stderr: newStderr(), cancel: cancel, done: make(chan clikittest.Output, 1)}
	adjust := hh.env(func(env *Env) { env.Stderr, env.Stdin = r.stderr, slave })
	go func() { r.done <- runWdx(ctx, t, hh.home, adjust, args...) }()
	t.Cleanup(cancel)
	return &codeRun{running: r, terminal: master}
}

// typeLine types line and Enter on the terminal.
func (r *codeRun) typeLine(t *testing.T, line string) {
	t.Helper()
	if _, err := r.terminal.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// asked waits for the nth time question was asked.
func (r *codeRun) asked(t *testing.T, question string, n int) {
	t.Helper()
	r.stderr.wait(t, regexp.MustCompile(`(?s)(`+regexp.QuoteMeta(question)+`.*){`+strconv.Itoa(n)+`}`))
}

var secretShown = regexp.MustCompile(`or type the secret in it: ([A-Z2-7]+)`)

// secret waits for the secret to be shown, and names it a secret.
func (hh *hubHome) shownSecret(r *codeRun) string {
	hh.t.Helper()
	secret := r.stderr.wait(hh.t, secretShown)[1]
	hh.secret(secret)
	return secret
}

// code is the secret's code now, by the daemon's clock, named a secret.
func (hh *hubHome) code(secret string, off int64) string {
	hh.t.Helper()
	c, err := authcode.CodeAt(secret, authcode.Step(hh.clock.Now())+off)
	if err != nil {
		hh.t.Fatal(err)
	}
	hh.mu.Lock()
	hh.codes = append(hh.codes, c)
	hh.mu.Unlock()
	return c
}

// wrongCode is a code that is the secret's at no step accepted now.
func (hh *hubHome) wrongCode(secret string) string {
	right := map[string]bool{}
	for off := int64(-1); off <= 1; off++ {
		right[hh.code(secret, off)] = true
	}
	for _, c := range []string{"000000", "111111", "222222", "333333"} {
		if !right[c] {
			return c
		}
	}
	panic("four codes taken")
}

var recoveryKeyShown = regexp.MustCompile(`recovery key: ([0-9A-Z]{4}(?:-[0-9A-Z]{4}){4})\n`)

// setUp sets up a code, typing the code of the secret shown, and returns the
// secret and the recovery key.
func (hh *hubHome) setUp(args ...string) (string, string, clikittest.Output) {
	hh.t.Helper()
	r := hh.startOnTerminal(append([]string{"hub", "code", "setup"}, args...)...)
	if len(args) > 0 {
		r.asked(hh.t, "replace this node's code?", 1)
		r.typeLine(hh.t, "y")
	}
	secret := hh.shownSecret(r)
	r.asked(hh.t, "code from the app: ", 1)
	r.typeLine(hh.t, hh.code(secret, 0))
	out := hh.wait(r.running)
	m := recoveryKeyShown.FindStringSubmatch(out.Stdout)
	if out.Exit != clikit.ExitOK || m == nil {
		hh.t.Fatalf("setup: %+v", out)
	}
	hh.secret(m[1])
	return secret, m[1], out
}

// auditActions are the actions of the node's audit chain, in order.
func (hh *hubHome) auditActions() []string {
	hh.t.Helper()
	d, err := db.Open(context.Background(), identity.DBPath(hh.home))
	if err != nil {
		hh.t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query("SELECT action FROM hub_audit ORDER BY id")
	if err != nil {
		hh.t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			hh.t.Fatal(err)
		}
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		hh.t.Fatal(err)
	}
	return actions
}

func TestHubCodeSetup(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.CodeSetup || v.Hub.RemoteAccess != api.HubStatusRemoteAccessOff {
		t.Fatalf("before setup: %+v", v.Hub)
	}

	r := hh.startOnTerminal("hub", "code", "setup")
	secret := hh.shownSecret(r)
	if s := r.stderr.String(); !strings.Contains(s, "to add the entry wdx: alice@desk") || !strings.Contains(s, "▀") {
		t.Fatalf("stderr %q", s)
	}
	r.asked(t, "code from the app: ", 1)
	r.typeLine(t, hh.wrongCode(secret))
	r.asked(t, "code from the app: ", 2)
	if !strings.Contains(r.stderr.String(), "wrong code: that is not the entry's code") {
		t.Fatalf("stderr %q", r.stderr.String())
	}
	r.typeLine(t, hh.code(secret, 0))
	out := hh.wait(r.running)
	m := recoveryKeyShown.FindStringSubmatch(out.Stdout)
	if out.Exit != clikit.ExitOK || m == nil || !strings.HasPrefix(out.Stdout, "set up the authenticator code of this node, as the entry wdx: alice@desk\n") ||
		!strings.Contains(out.Stdout, "write this down: it is the only way to set up a new authenticator here") {
		t.Fatalf("%+v", out)
	}
	hh.secret(m[1])

	dir := identity.HubDir(hh.home)
	for _, name := range []string{"code.secret", "code.step", "recovery.hash"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v, %v; want it 0600", name, info, err)
		}
	}
	if v, _ := hh.hubJSON(nil, "hub", "status"); !v.Hub.CodeSetup {
		t.Fatalf("after setup: %+v", v.Hub)
	}
	if got := hh.auditActions(); strings.Join(got, ",") != "code.set_up" {
		t.Fatalf("audit %v", got)
	}
	hh.wantMeasured("/v1/hub/code/verify", "code_bytes")
	hh.wantMeasured("/v1/hub/code/verify", "recovery_key_bytes")
	hh.wantMeasured("/v1/hub/code", "secret_bytes")
	hh.wantMeasured("/v1/hub/code", "url_bytes")
}

// wantMeasured checks the daemon logged key, a secret's length, in a
// request line of path.
func (hh *hubHome) wantMeasured(path, key string) {
	hh.t.Helper()
	files, _ := filepath.Glob(filepath.Join(identity.LogsDir(hh.home), "*.log"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			hh.t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if strings.Contains(line, `"path":"`+path+`"`) && strings.Contains(line, `"`+key+`"`) {
				return
			}
		}
	}
	hh.t.Errorf("no line of %s with %s", path, key)
}

func TestHubCodeSetupThreeWrong(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	r := hh.startOnTerminal("hub", "code", "setup")
	secret := hh.shownSecret(r)
	for n := 1; n <= 3; n++ {
		r.asked(t, "code from the app: ", n)
		r.typeLine(t, hh.wrongCode(secret))
	}
	out := hh.wait(r.running)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[code_wrong]") || strings.Count(out.Stderr, "code from the app: ") != 3 {
		t.Fatalf("%+v", out)
	}
	// The advice is the hint's, given once.
	if !strings.Contains(out.Stderr, "wdx: no code set up: the code is not the new entry's  [code_wrong]") || strings.Count(out.Stderr, "clock") != 1 {
		t.Fatalf("stderr %q", out.Stderr)
	}
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.CodeSetup {
		t.Fatal("want no code set up")
	}
}

func TestHubCodeExists(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.setUp()
	r := hh.startOnTerminal("hub", "code", "setup")
	out := hh.wait(r.running)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[code_exists]") || !strings.Contains(out.Stderr, "pass --replace") {
		t.Fatalf("%+v", out)
	}
}

func TestHubCodeReplace(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	_, oldKey, _ := hh.setUp()

	r := hh.startOnTerminal("hub", "code", "setup", "--replace")
	r.asked(t, "replace this node's code?", 1)
	r.typeLine(t, "n")
	if out := hh.wait(r.running); out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[cancelled]") {
		t.Fatalf("%+v", out)
	}

	_, newKey, _ := hh.setUp("--replace")
	if newKey == oldKey {
		t.Fatal("want a new recovery key")
	}
	if got := hh.auditActions(); strings.Join(got, ",") != "code.set_up,code.replaced" {
		t.Fatalf("audit %v", got)
	}
}

func TestHubCodeInteractiveOnly(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, args := range [][]string{{"hub", "code", "setup"}, {"hub", "code", "setup", "--replace"}, {"hub", "code", "recover"}} {
		v, exit := wdxJSON(t, home, args...)
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
		// Without a terminal too.
		if out := wdx(t, home, args...); out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "interactive only (stdin is not a terminal)") {
			t.Fatalf("%v: %+v", args, out)
		}
	}
}

func TestHubCodeNotLoggedIn(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	out := hh.wait(hh.startOnTerminal("hub", "code", "setup").running)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[not_logged_in]") {
		t.Fatalf("%+v", out)
	}
	r := hh.startOnTerminal("hub", "code", "recover")
	r.asked(t, "recovery key: ", 1)
	r.typeLine(t, "ABCD-EFGH-JKMN-PQRS-TVWX")
	if out := hh.wait(r.running); out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[not_logged_in]") {
		t.Fatalf("%+v", out)
	}
}

func TestHubCodeRecover(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	_, key, _ := hh.setUp()

	recoverWith := func(typed string) *codeRun {
		r := hh.startOnTerminal("hub", "code", "recover")
		r.asked(t, "recovery key: ", 1)
		r.typeLine(t, typed)
		return r
	}
	out := hh.wait(recoverWith("ABCD-EFGH-JKMN-PQRS-TVWX").running)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[recovery_key_wrong]") || !strings.Contains(out.Stderr, "the next try waits 1s") {
		t.Fatalf("%+v", out)
	}
	// Too soon: the right key is refused unread.
	out = hh.wait(recoverWith(key).running)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "too soon after a wrong recovery key") {
		t.Fatalf("%+v", out)
	}
	hh.clock.Advance(time.Second)
	out = hh.wait(recoverWith("ABCD-EFGH-JKMN-PQRS-TVWX").running)
	if !strings.Contains(out.Stderr, "the next try waits 2s") {
		t.Fatalf("want the wait doubled: %+v", out)
	}
	hh.clock.Advance(2 * time.Second)

	r := recoverWith(strings.ToLower(key))
	secret := hh.shownSecret(r)
	r.asked(t, "code from the app: ", 1)
	r.typeLine(t, hh.code(secret, 0))
	out = hh.wait(r.running)
	m := recoveryKeyShown.FindStringSubmatch(out.Stdout)
	if out.Exit != clikit.ExitOK || m == nil || !strings.HasPrefix(out.Stdout, "recovered the authenticator code of this node") || m[1] == key {
		t.Fatalf("%+v", out)
	}
	hh.secret(m[1])
	if !strings.Contains(r.stderr.String(), "recovery key: \n") {
		t.Fatalf("want the key hidden as it is typed: %q", r.stderr.String())
	}

	// The old key is spent.
	if out := hh.wait(recoverWith(key).running); !strings.Contains(out.Stderr, "[recovery_key_wrong]") {
		t.Fatalf("want the old key spent: %+v", out)
	}
	want := "code.set_up,recovery_key.wrong,recovery_key.wrong,code.recovered,recovery_key.wrong"
	if got := hh.auditActions(); strings.Join(got, ",") != want {
		t.Fatalf("audit %v, want %s", got, want)
	}
	hh.wantMeasured("/v1/hub/code/recover", "recovery_key_bytes")
}

func TestHubCodeRecoverNotSetUp(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	r := hh.startOnTerminal("hub", "code", "recover")
	r.asked(t, "recovery key: ", 1)
	r.typeLine(t, "ABCD-EFGH-JKMN-PQRS-TVWX")
	if out := hh.wait(r.running); out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[code_not_set_up]") {
		t.Fatalf("%+v", out)
	}
}

// auditOutput is what hub audit verify prints with --json.
type auditOutput struct {
	jsonOutput
	Audit *api.HubAudit `json:"audit"`
}

func TestHubAuditVerify(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	audit := func() (auditOutput, int) {
		args := []string{"hub", "audit", "verify", "--json"}
		out := wdxWith(t, hh.home, hh.env(nil), args...)
		var v auditOutput
		if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
			t.Fatal(err)
		}
		return v, out.Exit
	}
	if v, exit := audit(); exit != clikit.ExitOK || v.Audit.Rows != 0 || v.Audit.Head != nil {
		t.Fatalf("empty chain: %d, %+v", exit, v)
	}
	hh.loggedIn("desk")
	hh.setUp()
	if out := wdx(t, hh.home, "setting", "set", "--name", "hub.remote_access", "--value", "true"); out.Exit != clikit.ExitOK {
		t.Fatalf("%+v", out)
	}
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.RemoteAccess != api.HubStatusRemoteAccessOn {
		t.Fatalf("want remote access on: %+v", v.Hub)
	}
	if out := wdx(t, hh.home, "setting", "set", "--name", "hub.remote_access", "--value", "false"); out.Exit != clikit.ExitOK {
		t.Fatalf("%+v", out)
	}
	if got := hh.auditActions(); strings.Join(got, ",") != "code.set_up,remote_access.on,remote_access.off" {
		t.Fatalf("audit %v", got)
	}
	v, exit := audit()
	if exit != clikit.ExitOK || v.Audit.Rows != 3 || v.Audit.Head == nil {
		t.Fatalf("%d, %+v", exit, v)
	}
	if out := wdx(t, hh.home, "hub", "audit", "verify"); out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "verified the node's audit chain: 3 rows\n") {
		t.Fatalf("%+v", out)
	}

	d, err := db.Open(context.Background(), identity.DBPath(hh.home))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE hub_audit SET action = 'remote_access.off' WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	v, exit = audit()
	wantError(t, v.jsonOutput, exit, api.ErrorCodeAuditBroken, clikit.ExitRejected)
	args := []string{"hub", "audit", "verify", "--json"}
	out := wdxWith(t, hh.home, hh.env(nil), args...)
	var p api.Problem
	if err := json.Unmarshal([]byte(out.Stdout), &p); err != nil || p.Details == nil || (*p.Details)["id"] != float64(2) {
		t.Fatalf("details %+v, %v", p.Details, err)
	}
}

// wantNoCodes checks no line the home logged holds a code the test typed, as
// a number of its own: a code is six digits, which a longer number may hold.
func (hh *hubHome) wantNoCodes() {
	t := hh.t
	hh.mu.Lock()
	codes := hh.codes
	hh.mu.Unlock()
	files, _ := filepath.Glob(filepath.Join(identity.LogsDir(hh.home), "*.log"))
	for _, f := range files {
		logtest.Scan(t, f, 4<<20, func(raw []byte) {
			for _, c := range codes {
				if regexp.MustCompile(`(^|[^0-9])` + c + `([^0-9]|$)`).Match(raw) {
					t.Errorf("a code, %s, logged: %s", c, raw)
				}
			}
		})
	}
}
