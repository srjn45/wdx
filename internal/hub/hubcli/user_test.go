package hubcli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
)

// newUser signs up username on the hub s from an invite, with a browser
// session, and enrols an authenticator when enrol is true.
func newUser(t *testing.T, s *server.Server, username string, enrol bool) {
	t.Helper()
	ctx := context.Background()
	store := s.Accounts()
	inv, err := store.CreateInvite(ctx, account.Admin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, started, err := store.SignUp(ctx, account.By{Actor: audit.ActorUser}, account.SignUp{Invite: inv.Token, Username: username, Password: "a password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	if !enrol {
		return
	}
	key, err := store.EnrolTOTP(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfirmTOTP(ctx, account.By{Actor: audit.ActorUser}, u.ID, started.ID, code); err != nil {
		t.Fatal(err)
	}
}

// linkOf is the link under key in v, an invite or a reset.
func linkOf(t *testing.T, v map[string]any, key string) string {
	t.Helper()
	m, _ := v[key].(map[string]any)
	link, _ := m["link"].(string)
	if link == "" {
		t.Fatalf("no link in %v", v)
	}
	return link
}

// wantNotLogged fails if any log line of home holds secret.
func wantNotLogged(t *testing.T, home, secret string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(home), "*.log"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no logs in %s: %v", home, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), secret) {
			t.Fatalf("%s holds a secret", f)
		}
	}
}

// wantLinkSecret checks link holds its token in the query parameter key,
// and that neither is in a log line of home.
func wantLinkSecret(t *testing.T, home, link, path, key string) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Path != path || u.Query().Get(key) == "" || !strings.HasPrefix(link, "http://localhost:8080/") {
		t.Fatalf("unexpected link %q: %v", link, err)
	}
	wantNotLogged(t, home, u.Query().Get(key))
}

func TestUserInvite(t *testing.T) {
	t.Parallel()
	home := newHub(t)
	v, exit := hubJSON(t, home, "user", "invite")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultInvited))
	wantLinkSecret(t, home, linkOf(t, v, "invite"), "/signup", "invite")
	expires, err := time.Parse(time.RFC3339, v["invite"].(map[string]any)["expires_at"].(string))
	if err != nil || time.Until(expires) < 23*time.Hour || time.Until(expires) > 24*time.Hour {
		t.Fatalf("want 24 hours, got %v: %v", v, err)
	}
	v, exit = hubJSON(t, home, "user", "invite", "--expires", "72h")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultInvited))
	if expires, _ := time.Parse(time.RFC3339, v["invite"].(map[string]any)["expires_at"].(string)); time.Until(expires) < 71*time.Hour {
		t.Fatalf("want 72 hours, got %v", v)
	}

	out := hub(t, home, "user", "invite")
	lines := strings.Split(out.Stdout, "\n")
	if out.Exit != clikit.ExitOK || len(lines) != 3 || !strings.HasPrefix(lines[0], "invited: the link works once, until ") {
		t.Fatalf("unexpected text %+v", out)
	}
	wantLinkSecret(t, home, lines[1], "/signup", "invite")
}

func TestUserInviteRefused(t *testing.T) {
	t.Parallel()
	home := newHub(t)
	for _, expires := range []string{"0s", "721h", "-1h"} {
		v, exit := hubJSON(t, home, "user", "invite", "--expires", expires)
		want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodeInvalidArgument))
		if d, _ := v["details"].(map[string]any); d["argument"] != "expires" {
			t.Fatalf("want expires named, got %v", v)
		}
	}
	v, exit := hubJSON(t, home, "user", "invite", "--expires", "1500ms")
	want(t, v, exit, clikit.ExitUsage, "code", clikit.CodeUsage)
}

func TestUserList(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	v, exit := hubJSON(t, home, "user", "list")
	if list, ok := v["users"].([]any); exit != clikit.ExitOK || !ok || len(list) != 0 {
		t.Fatalf("want no users, got exit %d: %v", exit, v)
	}
	if out := hub(t, home, "user", "list"); out.Stdout != "no users: invite one with wdx-hub user invite\n" {
		t.Fatalf("unexpected text %+v", out)
	}
	newUser(t, s, "bob", false)
	newUser(t, s, "alice", true)
	newUser(t, s, "carol", false)
	if v, exit := hubJSON(t, home, "user", "remove", "--username", "carol", "--yes"); exit != clikit.ExitOK {
		t.Fatalf("remove: %v", v)
	}
	v, exit = hubJSON(t, home, "user", "list")
	list, _ := v["users"].([]any)
	if exit != clikit.ExitOK || len(list) != 2 {
		t.Fatalf("want alice and bob, got exit %d: %v", exit, v)
	}
	alice, bob := list[0].(map[string]any), list[1].(map[string]any)
	if alice["username"] != "alice" || alice["second_factor"] != "totp" || alice["disabled"] != false || alice["last_login_at"] == nil ||
		bob["username"] != "bob" || bob["second_factor"] != nil || bob["last_login_at"] != nil || bob["created_at"] == nil {
		t.Fatalf("unexpected users %v", list)
	}
	if _, ok := alice["sessions"]; ok {
		t.Fatalf("want no sessions in the list, got %v", alice)
	}
	out := hub(t, home, "user", "list")
	if lines := strings.Split(out.Stdout, "\n"); out.Exit != clikit.ExitOK || len(lines) != 4 || !strings.HasPrefix(lines[0], "USERNAME") ||
		!strings.HasPrefix(lines[1], "alice     totp      enabled") || !strings.HasPrefix(lines[2], "bob       none yet  enabled") {
		t.Fatalf("unexpected text %+v", out)
	}
}

// newNode makes a node of username named name, as an approved login does.
func newNode(t *testing.T, s *server.Server, username, name string) {
	t.Helper()
	ctx := context.Background()
	u, err := s.Accounts().UserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	by := node.By{Actor: audit.ActorUser}
	started, err := s.Nodes().StartLogin(ctx, by, node.StartRequest{Flow: node.FlowDevice, Name: name, OS: "linux/amd64", Version: "0.5.0", PublicKey: pub})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Nodes().Approve(ctx, by, u.ID, started.ID, name); err != nil {
		t.Fatal(err)
	}
}

func TestUserGet(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	newUser(t, s, "alice", true)
	newNode(t, s, "alice", "desktop")
	v, exit := hubJSON(t, home, "user", "get", "--username", "ALICE")
	u, _ := v["user"].(map[string]any)
	if exit != clikit.ExitOK || u["username"] != "alice" || u["sessions"] != float64(1) || u["second_factor"] != "totp" || u["nodes"] != float64(1) {
		t.Fatalf("want alice with her session and node, got exit %d: %v", exit, v)
	}
	if list, _ := u["node_list"].([]any); len(list) != 1 || list[0].(map[string]any)["name"] != "desktop" || list[0].(map[string]any)["online"] != false {
		t.Fatalf("want her desktop listed offline, got %v", u["node_list"])
	}
	if out := hub(t, home, "user", "list"); !strings.Contains(out.Stdout, "NODES") {
		t.Fatalf("want the nodes counted in the list, got %+v", out)
	}
	v, exit = hubJSON(t, home, "user", "get", "--username", "nobody")
	want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodeNotFound))
	if d, _ := v["details"].(map[string]any); d["username"] != "nobody" {
		t.Fatalf("want the username in details, got %v", v)
	}
	if out := hub(t, home, "user", "get", "--username", "alice"); out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "user alice\n  factor") ||
		!strings.Contains(out.Stdout, "sessions   1\n") || !strings.Contains(out.Stdout, "nodes      1\n") || !strings.Contains(out.Stdout, "desktop") {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestUserDisableEnable(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	newUser(t, s, "alice", false)
	v, exit := hubJSON(t, home, "user", "disable", "--username", "alice")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultDisabled))
	if u, _ := v["user"].(map[string]any); u["disabled"] != true || u["sessions"] != float64(0) {
		t.Fatalf("want alice disabled, her sessions ended, got %v", v)
	}
	v, exit = hubJSON(t, home, "user", "disable", "--username", "alice")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultUnchanged))
	v, exit = hubJSON(t, home, "user", "enable", "--username", "alice")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultEnabled))
	if u, _ := v["user"].(map[string]any); u["disabled"] != false {
		t.Fatalf("want alice enabled, got %v", v)
	}
	v, exit = hubJSON(t, home, "user", "enable", "--username", "alice")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultUnchanged))
	for _, verb := range []string{"disable", "enable"} {
		v, exit = hubJSON(t, home, "user", verb, "--username", "nobody")
		want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodeNotFound))
	}
	if out := hub(t, home, "user", "disable", "--username", "alice"); out.Stdout != "disabled user alice; their browser sessions ended and their nodes were cut off\n" {
		t.Fatalf("unexpected text %+v", out)
	}
	if out := hub(t, home, "user", "disable", "--username", "alice"); out.Stdout != "user alice is disabled already\n" {
		t.Fatalf("unexpected text %+v", out)
	}
	if out := hub(t, home, "user", "enable", "--username", "alice"); out.Stdout != "enabled user alice\n" {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestUserReset(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	newUser(t, s, "alice", true)
	v, exit := hubJSON(t, home, "user", "reset", "--username", "alice")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultReset))
	wantLinkSecret(t, home, linkOf(t, v, "reset"), "/reset", "token")
	if r, _ := v["reset"].(map[string]any); r["second_factor"] != false || r["expires_at"] == nil {
		t.Fatalf("want a password reset, got %v", v)
	}
	v, exit = hubJSON(t, home, "user", "reset", "--username", "alice", "--second-factor")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultReset))
	if r, _ := v["reset"].(map[string]any); r["second_factor"] != true {
		t.Fatalf("want the second factor reset too, got %v", v)
	}
	v, exit = hubJSON(t, home, "user", "reset", "--username", "nobody")
	want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodeNotFound))

	out := hub(t, home, "user", "reset", "--username", "alice", "--second-factor")
	lines := strings.Split(out.Stdout, "\n")
	if out.Exit != clikit.ExitOK || len(lines) != 3 || !strings.HasPrefix(lines[0], "reset the password and second factor of alice: the link works once, until ") {
		t.Fatalf("unexpected text %+v", out)
	}
	wantLinkSecret(t, home, lines[1], "/reset", "token")
}

func TestUserRemove(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	newUser(t, s, "alice", false)
	v, exit := hubJSON(t, home, "user", "remove", "--username", "alice")
	want(t, v, exit, clikit.ExitUsage, "code", clikit.CodeUsage)
	v, exit = hubJSON(t, home, "user", "remove", "--username", "alice", "--yes")
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultRemoved))
	if u, _ := v["user"].(map[string]any); u["username"] != "alice" {
		t.Fatalf("want alice as she was, got %v", v)
	}
	v, exit = hubJSON(t, home, "user", "remove", "--username", "alice", "--yes")
	want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodeNotFound))
	// The username stays taken.
	inv, err := s.Accounts().CreateInvite(context.Background(), account.Admin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Accounts().SignUp(context.Background(), account.By{Actor: audit.ActorUser}, account.SignUp{Invite: inv.Token, Username: "alice", Password: "a password long enough"}); !errors.Is(err, account.ErrUsernameTaken) {
		t.Fatalf("want alice taken, got %v", err)
	}

	newUser(t, s, "bob", false)
	if out := hub(t, home, "user", "remove", "--username", "bob", "--yes"); out.Stdout != "removed user bob; the username stays taken\n" {
		t.Fatalf("unexpected text %+v", out)
	}
	// Without a terminal nothing is asked.
	newUser(t, s, "carol", false)
	if out := hub(t, home, "user", "remove", "--username", "carol"); out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "pass --yes") {
		t.Fatalf("want --yes asked for, got %+v", out)
	}
}

func TestUserRemoveAnswered(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	newUser(t, s, "alice", false)
	answering := func(answers ...string) func(*Env) {
		stdin := clikittest.Typed(t, answers...)
		return func(env *Env) { env.Stdin = stdin }
	}
	out := hubWith(t, home, answering("n"), "user", "remove", "--username", "alice")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "remove user alice? Their username stays taken. [y/N]: ") || !strings.Contains(out.Stderr, "[cancelled]") {
		t.Fatalf("want it cancelled, got %+v", out)
	}
	if v, exit := hubJSON(t, home, "user", "get", "--username", "alice"); exit != clikit.ExitOK {
		t.Fatalf("want alice kept, got %v", v)
	}
	out = hubWith(t, home, answering("maybe", "y"), "user", "remove", "--username", "alice")
	if out.Exit != clikit.ExitOK || out.Stdout != "removed user alice; the username stays taken\n" {
		t.Fatalf("want alice removed, got %+v", out)
	}
	out = hubWith(t, home, answering("y"), "user", "remove", "--username", "alice")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[not_found]") {
		t.Fatalf("want not_found before asking, got %+v", out)
	}
}

func TestAuditVerify(t *testing.T) {
	t.Parallel()
	home, s := newHubServer(t)
	v, exit := hubJSON(t, home, "audit", "verify")
	if a, _ := v["audit"].(map[string]any); exit != clikit.ExitOK || a["rows"] != float64(0) || a["head"] != nil {
		t.Fatalf("want an empty chain, got exit %d: %v", exit, v)
	}
	if out := hub(t, home, "audit", "verify"); out.Stdout != "verified the audit chain: it has no rows yet\n" {
		t.Fatalf("unexpected text %+v", out)
	}
	newUser(t, s, "alice", true) // invited, signed up, enrolled, logged in
	v, exit = hubJSON(t, home, "audit", "verify")
	a, _ := v["audit"].(map[string]any)
	if head, _ := a["head"].(string); exit != clikit.ExitOK || a["rows"] != float64(4) || len(head) != 64 {
		t.Fatalf("want four rows, got exit %d: %v", exit, v)
	}
	if out := hub(t, home, "audit", "verify"); !strings.HasPrefix(out.Stdout, "verified the audit chain: 4 rows\n  head ") {
		t.Fatalf("unexpected text %+v", out)
	}

	db, err := hubdb.Open(context.Background(), hubconfig.DBPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE audit SET address = '10.0.0.1' WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	v, exit = hubJSON(t, home, "audit", "verify")
	want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodeAuditBroken))
	if d, _ := v["details"].(map[string]any); d["id"] != float64(2) || d["at"] == nil {
		t.Fatalf("want row 2 broken, got %v", v)
	}
	if out := hub(t, home, "audit", "verify"); out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "the audit chain is broken at row 2") {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestAccountsWithoutHub(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, args := range [][]string{
		{"user", "invite"},
		{"user", "list"},
		{"user", "get", "--username", "alice"},
		{"user", "disable", "--username", "alice"},
		{"user", "enable", "--username", "alice"},
		{"user", "reset", "--username", "alice"},
		{"user", "remove", "--username", "alice", "--yes"},
		{"audit", "verify"},
	} {
		v, exit := hubJSON(t, home, args...)
		want(t, v, exit, clikit.ExitSystem, "code", string(hubapi.ErrorCodeHubUnavailable))
		if v["hint"] != "run "+name+" serve" {
			t.Fatalf("%v: want the hint, got %v", args, v)
		}
	}
}
