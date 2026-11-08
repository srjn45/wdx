package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

// clock is a store's time, which a test moves.
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

// light are argon2's least parameters, so that the tests stay light.
var light = Argon2{Memory: 8, Time: 1, Threads: 1}

// fixture is a store on a migrated database of its own, its clock and the
// log of what it did.
type fixture struct {
	*Store
	db    *sql.DB
	clock *clock
	ctx   context.Context
	log   *bytes.Buffer
	key   []byte
}

// at is the start of every fixture's clock, at the start of a 30-second
// step.
var at = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.db")
	sqlitetest.Seed(t, path, hubdb.Open)
	d, err := hubdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: at}
	s, err := New(d, Options{MasterKey: key, Now: c.now, Argon2: light})
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	ctx := logging.NewContext(context.Background(), logging.New(&log, slog.LevelDebug))
	return &fixture{Store: s, db: d, clock: c, ctx: ctx, log: &log, key: key}
}

const password = "correct horse battery"

// user is a user signed up in a test, with what they were given.
type user struct {
	User
	session Started  // their browser's session, its second factor passed once enrolled
	key     *otp.Key // their authenticator
	codes   []string // their recovery codes
}

// signUp signs username up from an invite, without enrolling a factor.
func (f *fixture) signUp(t *testing.T, username string) user {
	t.Helper()
	inv, err := f.CreateInvite(f.ctx, Admin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, started, err := f.SignUp(f.ctx, browser, SignUp{Invite: inv.Token, Username: username, Password: password, UserAgent: "Firefox"})
	if err != nil {
		t.Fatalf("sign up %s: %v", username, err)
	}
	return user{User: u, session: started}
}

// enrolled signs username up and enrols their authenticator.
func (f *fixture) enrolled(t *testing.T, username string) user {
	t.Helper()
	u := f.signUp(t, username)
	key, err := f.EnrolTOTP(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.ConfirmTOTP(f.ctx, browser, u.ID, u.session.ID, code(t, key, f.step(0)))
	if err != nil {
		t.Fatalf("confirm %s: %v", username, err)
	}
	u.session, u.key, u.codes = c.Started, key, c.RecoveryCodes
	u.SecondFactor = FactorTOTP
	// The next code each test uses is of a step after this one's.
	f.clock.advance(totpPeriod * time.Second)
	return u
}

// login logs u in with their password, the second factor not passed.
func (f *fixture) login(t *testing.T, u user) Started {
	t.Helper()
	_, started, err := f.Login(f.ctx, browser, u.Username, password, "Safari")
	if err != nil {
		t.Fatal(err)
	}
	return started
}

// step is the 30-second step of the clock, moved by d steps.
func (f *fixture) step(d int64) int64 { return f.clock.now().Unix()/totpPeriod + d }

// code is the authenticator's code at step.
func code(t *testing.T, key *otp.Key, step int64) string {
	t.Helper()
	c, err := hotp.GenerateCodeCustom(key.Secret(), uint64(step), hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}) //nolint:gosec // a step after 1970
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var browser = By{Actor: audit.ActorUser, Address: "192.0.2.7"}

// actions are the audit chain's actions, in order, after checking it is
// unbroken.
func (f *fixture) actions(t *testing.T) []string {
	t.Helper()
	v, err := audit.Verify(f.ctx, f.db)
	if err != nil || v.Broken != nil {
		t.Fatalf("audit chain: %+v, %v", v, err)
	}
	rows, err := f.db.Query("SELECT action FROM audit ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		list = append(list, a)
	}
	return list
}

// lastAction is the action of the chain's last row.
func (f *fixture) lastAction(t *testing.T) string {
	t.Helper()
	list := f.actions(t)
	if len(list) == 0 {
		return ""
	}
	return list[len(list)-1]
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func wantInvalid(t *testing.T, what string, err error, field string) {
	t.Helper()
	if e, ok := errors.AsType[*InvalidError](err); !ok || e.Field != field {
		t.Fatalf("%s: got %v, want %s invalid", what, err, field)
	}
}

func TestNewNeedsAKeyOf32Bytes(t *testing.T) {
	t.Parallel()
	if _, err := New(nil, Options{MasterKey: make([]byte, 16)}); err == nil {
		t.Fatal("want a 16-byte key refused")
	}
}

func TestSignUpFromInvite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	inv, err := f.CreateInvite(f.ctx, Admin, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inv.ID, "inv-") || len(inv.Token) != 43 || !inv.ExpiresAt.Equal(at.Add(24*time.Hour)) {
		t.Fatalf("invite %+v", inv)
	}
	if err := f.CheckInvite(f.ctx, inv.Token); err != nil {
		t.Fatal(err)
	}
	u, started, err := f.SignUp(f.ctx, browser, SignUp{Invite: inv.Token, Username: "Alice", Password: password, UserAgent: "Firefox"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.ID, "usr-") || u.Username != "Alice" || u.SecondFactor != "" {
		t.Fatalf("user %+v", u)
	}
	if !strings.HasPrefix(started.ID, "ws-") || started.UserID != u.ID || !started.SecondFactor.IsZero() || started.Address != browser.Address || started.UserAgent != "Firefox" {
		t.Fatalf("session %+v, want one without its second factor", started)
	}
	if se, err := f.Session(f.ctx, started.Token); err != nil || se.ID != started.ID {
		t.Fatalf("session from its token: %+v, %v", se, err)
	}
	if got := f.lastAction(t); got != ActionSignedUp {
		t.Errorf("last action %s", got)
	}
	_, _, err = f.SignUp(f.ctx, browser, SignUp{Invite: inv.Token, Username: "bob", Password: password})
	wantErr(t, "the invite used again", err, ErrInvalidInvite)
	wantErr(t, "the invite checked once used", f.CheckInvite(f.ctx, inv.Token), ErrInvalidInvite)
	wantErr(t, "an invite unknown", f.CheckInvite(f.ctx, "nope"), ErrInvalidInvite)
	if n := f.count(t, "SELECT COUNT(*) FROM users"); n != 1 {
		t.Errorf("%d users, want only Alice", n)
	}
}

func TestInviteExpires(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	inv, err := f.CreateInvite(f.ctx, Admin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.advance(time.Hour - time.Millisecond)
	if err := f.CheckInvite(f.ctx, inv.Token); err != nil {
		t.Fatalf("an invite before its expiry: %v", err)
	}
	f.clock.advance(time.Millisecond)
	wantErr(t, "an invite at its expiry", f.CheckInvite(f.ctx, inv.Token), ErrInvalidInvite)
	_, _, err = f.SignUp(f.ctx, browser, SignUp{Invite: inv.Token, Username: "alice", Password: password})
	wantErr(t, "sign-up from an invite expired", err, ErrInvalidInvite)
}

func TestInviteLife(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, life := range []time.Duration{0, -time.Hour, MaxInviteLife + time.Millisecond} {
		_, err := f.CreateInvite(f.ctx, Admin, life)
		wantInvalid(t, life.String(), err, "expires")
	}
	if _, err := f.CreateInvite(f.ctx, Admin, MaxInviteLife); err != nil {
		t.Fatalf("30 days: %v", err)
	}
	if got := f.actions(t); len(got) != 1 || got[0] != ActionInvited {
		t.Errorf("actions %v, want the one invite", got)
	}
}

func TestRegistration(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, _, err := f.SignUp(f.ctx, browser, SignUp{Username: "alice", Password: password})
	wantErr(t, "closed", err, ErrRegistrationClosed)
	u, _, err := f.SignUp(f.ctx, browser, SignUp{RegistrationOpen: true, Username: "alice", Password: password})
	if err != nil || u.Username != "alice" {
		t.Fatalf("open: %+v, %v", u, err)
	}
	// An invite works either way, and must be valid when given.
	_, _, err = f.SignUp(f.ctx, browser, SignUp{RegistrationOpen: true, Invite: "nope", Username: "bob", Password: password})
	wantErr(t, "open, with a bad invite", err, ErrInvalidInvite)
}

func TestUsernames(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	open := func(name string) error {
		_, _, err := f.SignUp(f.ctx, browser, SignUp{RegistrationOpen: true, Username: name, Password: password})
		return err
	}
	for _, name := range []string{"a", "7", "Alice", "a-b-", "x" + strings.Repeat("-", 38)} {
		if err := open(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"", "-a", "a_b", "a.b", "a b", "ä", strings.Repeat("y", 40)} {
		wantInvalid(t, name, open(name), "username")
	}
	wantErr(t, "alice in another case", open("aLiCe"), ErrUsernameTaken)
	if u, err := f.UserByUsername(f.ctx, "ALICE"); err != nil || u.Username != "Alice" {
		t.Errorf("by username in another case: %+v, %v", u, err)
	}
}

func TestRemovedUsernameNeverGivenAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	if err := f.Remove(f.ctx, Admin, u.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.SignUp(f.ctx, browser, SignUp{RegistrationOpen: true, Username: "Alice", Password: password})
	wantErr(t, "a removed user's name", err, ErrUsernameTaken)
}

func TestPasswordLength(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for name, pw := range map[string]string{"short": strings.Repeat("p", 11), "long": strings.Repeat("p", 129), "short in runes": strings.Repeat("é", 11)} {
		_, _, err := f.SignUp(f.ctx, browser, SignUp{RegistrationOpen: true, Username: "u-" + strings.ReplaceAll(name, " ", "-"), Password: pw})
		wantInvalid(t, name, err, "password")
	}
	for name, pw := range map[string]string{"twelve": strings.Repeat("p", 12), "max": strings.Repeat("p", 128), "twelve runes": strings.Repeat("é", 12)} {
		u, _, err := f.SignUp(f.ctx, browser, SignUp{RegistrationOpen: true, Username: "u-" + strings.ReplaceAll(name, " ", "-"), Password: pw})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, _, err := f.Login(f.ctx, browser, u.Username, pw, ""); err != nil {
			t.Errorf("%s: login %v", name, err)
		}
	}
}

// A wrong password and a username that does not exist take the same path:
// a hash checked, and the same error.
func TestLoginRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	removed := f.signUp(t, "bob")
	if err := f.Remove(f.ctx, Admin, removed.ID); err != nil {
		t.Fatal(err)
	}
	dummies := 0
	dummy := f.dummy
	f.dummy = func() string { dummies++; return dummy() }

	_, _, err := f.Login(f.ctx, browser, "alice", "wrong password!", "")
	wantErr(t, "a wrong password", err, ErrWrongPassword)
	if dummies != 0 {
		t.Fatal("a user's wrong password checked against the dummy hash")
	}
	for _, name := range []string{"carol", "bob", "-not-a-name"} {
		_, _, err := f.Login(f.ctx, browser, name, password, "")
		wantErr(t, name, err, ErrWrongPassword)
	}
	if dummies != 3 {
		t.Fatalf("the dummy hash checked %d times, want once for each unknown user", dummies)
	}
	if _, _, err := f.Login(f.ctx, browser, "ALICE", password, ""); err != nil {
		t.Fatalf("the right password, the name in another case: %v", err)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM web_sessions WHERE user_id = ?", u.ID); n != 2 {
		t.Errorf("%d sessions, want the sign-up's and the one login's", n)
	}
	if !strings.HasPrefix(f.dummy(), "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Errorf("the dummy hash %q is not of the store's parameters", f.dummy())
	}
}

func TestPasswordHash(t *testing.T) {
	t.Parallel()
	hash, err := OWASP.hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q, want OWASP's parameters in PHC", hash)
	}
	// A hash of other parameters still verifies: they are in the string.
	for _, h := range []string{hash, must(light.hash(password))} {
		if ok, err := matches(h, password); !ok || err != nil {
			t.Errorf("%s: %v, %v", h, ok, err)
		}
		if ok, err := matches(h, password+"!"); ok || err != nil {
			t.Errorf("%s with a wrong password: %v, %v", h, ok, err)
		}
	}
	if _, err := matches("$2a$10$bcrypt", password); err == nil {
		t.Error("want a hash not argon2id refused")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestEnrolment(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	key, err := f.EnrolTOTP(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key.Issuer() != "wdx-hub" || key.AccountName() != "alice" || key.Period() != 30 || key.Digits() != otp.DigitsSix || key.Algorithm() != otp.AlgorithmSHA1 {
		t.Fatalf("key %s", key.URL())
	}
	if _, err := key.Image(200, 200); err != nil {
		t.Fatalf("the QR code: %v", err)
	}
	// Enrolling again before confirming replaces the key.
	again, err := f.EnrolTOTP(f.ctx, u.ID)
	if err != nil || again.Secret() == key.Secret() {
		t.Fatalf("enrolling again: %v", err)
	}
	if got, err := f.User(f.ctx, u.ID); err != nil || got.SecondFactor != "" {
		t.Fatalf("before confirming, user %+v, %v", got, err)
	}
	_, err = f.ConfirmTOTP(f.ctx, browser, u.ID, u.session.ID, code(t, key, f.step(0)))
	wantErr(t, "a code of the key replaced", err, ErrWrongCode)
	c, err := f.ConfirmTOTP(f.ctx, browser, u.ID, u.session.ID, code(t, again, f.step(0)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Token == u.session.Token || c.SecondFactor.IsZero() || len(c.RecoveryCodes) != RecoveryCodes {
		t.Fatalf("confirmed %+v", c)
	}
	_, err = f.Session(f.ctx, u.session.Token)
	wantErr(t, "the token before the second factor", err, ErrNoSession)
	if se, err := f.Session(f.ctx, c.Token); err != nil || se.SecondFactor.IsZero() {
		t.Fatalf("the new token: %+v, %v", se, err)
	}
	got, err := f.User(f.ctx, u.ID)
	if err != nil || got.SecondFactor != FactorTOTP || !got.LastLoginAt.Equal(at) {
		t.Fatalf("after confirming, user %+v, %v", got, err)
	}
	_, err = f.EnrolTOTP(f.ctx, u.ID)
	wantErr(t, "enrolling once confirmed", err, ErrFactorConfirmed)
	_, err = f.ConfirmTOTP(f.ctx, browser, u.ID, c.ID, code(t, again, f.step(1)))
	wantErr(t, "confirming once confirmed", err, ErrNoEnrolment)
	if got := f.actions(t); strings.Join(got[len(got)-2:], " ") != ActionFactorEnrolled+" "+ActionLoggedIn {
		t.Errorf("actions %v", got)
	}
}

// A user who left before enrolling goes from their password to enrolment.
func TestLoginWithoutFactor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	lu, started, err := f.Login(f.ctx, browser, "alice", password, "")
	if err != nil || lu.SecondFactor != "" {
		t.Fatalf("login %+v, %v", lu, err)
	}
	_, err = f.PassTOTP(f.ctx, browser, u.ID, started.ID, "123456")
	wantErr(t, "a code without a factor", err, ErrNoFactor)
	key, err := f.EnrolTOTP(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ConfirmTOTP(f.ctx, browser, u.ID, started.ID, code(t, key, f.step(0))); err != nil {
		t.Fatal(err)
	}
}

func TestTOTPTolerance(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	// The step before is not the one enrolling used.
	f.clock.advance(totpPeriod * time.Second)
	pass := func(step int64) error {
		started := f.login(t, u)
		_, err := f.PassTOTP(f.ctx, browser, u.ID, started.ID, code(t, u.key, step))
		return err
	}
	wantErr(t, "two steps before", pass(f.step(-2)), ErrWrongCode)
	wantErr(t, "two steps after", pass(f.step(2)), ErrWrongCode)
	if err := pass(f.step(-1)); err != nil {
		t.Fatalf("the step before: %v", err)
	}
	if err := pass(f.step(1)); err != nil {
		t.Fatalf("the step after: %v", err)
	}
	// The step after was used: the current one, before it, is refused too.
	wantErr(t, "the step after used again", pass(f.step(1)), ErrWrongCode)
	wantErr(t, "a step before the last used", pass(f.step(0)), ErrWrongCode)
	f.clock.advance(2 * totpPeriod * time.Second)
	if err := pass(f.step(0)); err != nil {
		t.Fatalf("a later step: %v", err)
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		started := f.login(t, u)
		_, err := f.PassTOTP(f.ctx, browser, u.ID, started.ID, bad)
		wantErr(t, bad, err, ErrWrongCode)
	}
}

func TestPassTOTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	started := f.login(t, u)
	if !started.SecondFactor.IsZero() {
		t.Fatal("a login has its second factor before the code")
	}
	f.clock.advance(time.Minute)
	passed, err := f.PassTOTP(f.ctx, browser, u.ID, started.ID, " "+code(t, u.key, f.step(0))+" ")
	if err != nil {
		t.Fatal(err)
	}
	if passed.ID != started.ID || passed.Token == started.Token || !passed.SecondFactor.Equal(f.clock.now()) {
		t.Fatalf("passed %+v", passed)
	}
	_, err = f.Session(f.ctx, started.Token)
	wantErr(t, "the token seen before the second factor", err, ErrNoSession)
	if got, err := f.User(f.ctx, u.ID); err != nil || !got.LastLoginAt.Equal(f.clock.now()) {
		t.Fatalf("last login %+v, %v", got, err)
	}
	var details string
	if err := f.db.QueryRow("SELECT details FROM audit WHERE action = ? ORDER BY id DESC LIMIT 1", ActionLoggedIn).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"recovery_code":false`) {
		t.Errorf("logged in with %s, want no recovery code said", details)
	}
}

func TestRecoveryCodes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	seen := map[string]bool{}
	for _, c := range u.codes {
		if len(c) != 11 || c[5] != '-' || strings.ContainsAny(c, "ILOU") || seen[c] {
			t.Fatalf("code %q of %v", c, u.codes)
		}
		seen[c] = true
	}
	started := f.login(t, u)
	passed, left, err := f.PassRecoveryCode(f.ctx, browser, u.ID, started.ID, strings.ToLower(strings.ReplaceAll(u.codes[0], "-", "")))
	if err != nil || left != RecoveryCodes-1 || passed.Token == started.Token || passed.SecondFactor.IsZero() {
		t.Fatalf("a code typed in lower case without its dash: %+v, %d, %v", passed, left, err)
	}
	var details string
	if err := f.db.QueryRow("SELECT details FROM audit WHERE action = ? ORDER BY id DESC LIMIT 1", ActionLoggedIn).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"recovery_code":true`) {
		t.Errorf("logged in with %s, want the recovery code said", details)
	}
	again := f.login(t, u)
	_, _, err = f.PassRecoveryCode(f.ctx, browser, u.ID, again.ID, u.codes[0])
	wantErr(t, "a code used again", err, ErrWrongCode)
	_, _, err = f.PassRecoveryCode(f.ctx, browser, u.ID, again.ID, "ABCDE-FGHJK")
	wantErr(t, "a code unknown", err, ErrWrongCode)
	if n, err := f.RecoveryCodesLeft(f.ctx, u.ID); err != nil || n != RecoveryCodes-1 {
		t.Fatalf("left %d, %v", n, err)
	}

	_, err = f.RenewRecoveryCodes(f.ctx, browser, u.ID, "wrong password!")
	wantErr(t, "renewed with a wrong password", err, ErrWrongPassword)
	renewed, err := f.RenewRecoveryCodes(f.ctx, browser, u.ID, password)
	if err != nil || len(renewed) != RecoveryCodes {
		t.Fatalf("renewed %v, %v", renewed, err)
	}
	if got := f.lastAction(t); got != ActionRecoveryCodesRenewed {
		t.Errorf("last action %s", got)
	}
	_, _, err = f.PassRecoveryCode(f.ctx, browser, u.ID, again.ID, u.codes[1])
	wantErr(t, "an old code once renewed", err, ErrWrongCode)
	if _, left, err := f.PassRecoveryCode(f.ctx, browser, u.ID, again.ID, renewed[9]); err != nil || left != RecoveryCodes-1 {
		t.Fatalf("a new code: %d, %v", left, err)
	}
}

func TestReplaceTOTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	_, err := f.ReplaceTOTP(f.ctx, u.ID, "wrong password!")
	wantErr(t, "replaced with a wrong password", err, ErrWrongPassword)
	err = f.ConfirmReplacement(f.ctx, browser, u.ID, code(t, u.key, f.step(0)))
	wantErr(t, "confirmed with no replacement", err, ErrNoEnrolment)
	key, err := f.ReplaceTOTP(f.ctx, u.ID, password)
	if err != nil {
		t.Fatal(err)
	}
	// The old authenticator works until the new one is confirmed.
	if _, err := f.PassTOTP(f.ctx, browser, u.ID, f.login(t, u).ID, code(t, u.key, f.step(0))); err != nil {
		t.Fatalf("the old one while replacing: %v", err)
	}
	wantErr(t, "a code of the old one", f.ConfirmReplacement(f.ctx, browser, u.ID, code(t, u.key, f.step(1))), ErrWrongCode)
	// The step was used by the old secret, not the new.
	if err := f.ConfirmReplacement(f.ctx, browser, u.ID, code(t, key, f.step(0))); err != nil {
		t.Fatal(err)
	}
	if got := f.lastAction(t); got != ActionFactorReplaced {
		t.Errorf("last action %s", got)
	}
	_, err = f.PassTOTP(f.ctx, browser, u.ID, f.login(t, u).ID, code(t, u.key, f.step(1)))
	wantErr(t, "the old one once replaced", err, ErrWrongCode)
	if _, err := f.PassTOTP(f.ctx, browser, u.ID, f.login(t, u).ID, code(t, key, f.step(1))); err != nil {
		t.Fatalf("the new one: %v", err)
	}
	if n, err := f.RecoveryCodesLeft(f.ctx, u.ID); err != nil || n != RecoveryCodes {
		t.Errorf("recovery codes %d, %v; want them kept", n, err)
	}
	none := f.signUp(t, "bob")
	_, err = f.ReplaceTOTP(f.ctx, none.ID, password)
	wantErr(t, "replacing none", err, ErrNoFactor)
}

func TestChangePassword(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	other := f.login(t, u)
	const newPassword = "a new password, long"
	wantErr(t, "a wrong current password", f.ChangePassword(f.ctx, browser, u.ID, u.session.ID, "wrong password!", newPassword), ErrWrongPassword)
	wantInvalid(t, "a short new one", f.ChangePassword(f.ctx, browser, u.ID, u.session.ID, password, "short"), "password")
	if err := f.ChangePassword(f.ctx, browser, u.ID, u.session.ID, password, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Session(f.ctx, u.session.Token); err != nil {
		t.Errorf("this session ended: %v", err)
	}
	_, err := f.Session(f.ctx, other.Token)
	wantErr(t, "another session", err, ErrNoSession)
	_, _, err = f.Login(f.ctx, browser, "alice", password, "")
	wantErr(t, "the old password", err, ErrWrongPassword)
	if _, _, err := f.Login(f.ctx, browser, "alice", newPassword, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.actions(t); !contains(got, ActionPasswordChanged) {
		t.Errorf("actions %v", got)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestReset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	first, err := f.CreateReset(f.ctx, Admin, u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.ID, "rst-") || !first.ExpiresAt.Equal(f.clock.now().Add(24*time.Hour)) {
		t.Fatalf("reset %+v", first)
	}
	second, err := f.CreateReset(f.ctx, Admin, u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.CheckReset(f.ctx, first.Token)
	wantErr(t, "a reset voided by a later one", err, ErrInvalidReset)
	if sf, err := f.CheckReset(f.ctx, second.Token); err != nil || sf {
		t.Fatalf("the later reset: %v, %v", sf, err)
	}
	const newPassword = "a new password, long"
	_, err = f.UseReset(f.ctx, browser, second.Token, "short")
	wantInvalid(t, "a short password", err, "password")
	id, err := f.UseReset(f.ctx, browser, second.Token, newPassword)
	if err != nil || id != u.ID {
		t.Fatalf("used: %s, %v", id, err)
	}
	_, err = f.Session(f.ctx, u.session.Token)
	wantErr(t, "a session after the reset", err, ErrNoSession)
	_, err = f.UseReset(f.ctx, browser, second.Token, newPassword)
	wantErr(t, "a reset used again", err, ErrInvalidReset)
	lu, _, err := f.Login(f.ctx, browser, "alice", newPassword, "")
	if err != nil || lu.SecondFactor != FactorTOTP {
		t.Fatalf("login after the reset: %+v, %v; want the factor kept", lu, err)
	}
	if got := f.actions(t); strings.Join(got[len(got)-3:], " ") != ActionResetMade+" "+ActionResetMade+" "+ActionResetUsed {
		t.Errorf("actions %v", got)
	}
}

func TestResetSecondFactor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	r, err := f.CreateReset(f.ctx, Admin, u.ID, true)
	if err != nil || !r.SecondFactor {
		t.Fatalf("reset %+v, %v", r, err)
	}
	if sf, err := f.CheckReset(f.ctx, r.Token); err != nil || !sf {
		t.Fatalf("checked: %v, %v", sf, err)
	}
	if _, err := f.UseReset(f.ctx, browser, r.Token, "a new password, long"); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT (SELECT COUNT(*) FROM totp_factors WHERE user_id = ?1) + (SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?1)", u.ID); n != 0 {
		t.Fatalf("%d factors and codes left", n)
	}
	lu, started, err := f.Login(f.ctx, browser, "alice", "a new password, long", "")
	if err != nil || lu.SecondFactor != "" {
		t.Fatalf("login %+v, %v; want it to go on to enrolment", lu, err)
	}
	key, err := f.EnrolTOTP(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ConfirmTOTP(f.ctx, browser, u.ID, started.ID, code(t, key, f.step(0))); err != nil {
		t.Fatal(err)
	}
}

func TestResetExpires(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	r, err := f.CreateReset(f.ctx, Admin, u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.advance(ResetLife)
	_, err = f.UseReset(f.ctx, browser, r.Token, "a new password, long")
	wantErr(t, "a reset at its expiry", err, ErrInvalidReset)
	_, err = f.CreateReset(f.ctx, Admin, "usr-00000000", false)
	wantErr(t, "a reset of nobody", err, ErrNotFound)
}

func TestDisable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	changed, err := f.Disable(f.ctx, Admin, u.ID)
	if err != nil || !changed {
		t.Fatalf("disabled: %v, %v", changed, err)
	}
	if got := f.lastAction(t); got != ActionDisabled {
		t.Errorf("last action %s", got)
	}
	_, err = f.Session(f.ctx, u.session.Token)
	wantErr(t, "a session of a disabled user", err, ErrNoSession)
	if changed, err := f.Disable(f.ctx, Admin, u.ID); err != nil || changed {
		t.Fatalf("disabled again: %v, %v", changed, err)
	}
	_, _, err = f.Login(f.ctx, browser, "alice", password, "")
	wantErr(t, "the right password of a disabled user", err, ErrDisabled)
	_, _, err = f.Login(f.ctx, browser, "alice", "wrong password!", "")
	wantErr(t, "a wrong password of a disabled user", err, ErrWrongPassword)
	if got, err := f.User(f.ctx, u.ID); err != nil || !got.Disabled() || got.Sessions != 0 {
		t.Fatalf("user %+v, %v", got, err)
	}
	if changed, err := f.Enable(f.ctx, Admin, u.ID); err != nil || !changed {
		t.Fatalf("enabled: %v, %v", changed, err)
	}
	if changed, err := f.Enable(f.ctx, Admin, u.ID); err != nil || changed {
		t.Fatalf("enabled again: %v, %v", changed, err)
	}
	if got := f.lastAction(t); got != ActionEnabled {
		t.Errorf("last action %s, want the unchanged enable unrecorded", got)
	}
	f.login(t, u)
	_, err = f.Disable(f.ctx, Admin, "usr-00000000")
	wantErr(t, "disabling nobody", err, ErrNotFound)
}

func TestRemove(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	if _, err := f.CreateReset(f.ctx, Admin, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(f.ctx, Admin, u.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.lastAction(t); got != ActionRemoved {
		t.Errorf("last action %s", got)
	}
	for table, where := range map[string]string{"totp_factors": "user_id", "recovery_codes": "user_id", "web_sessions": "user_id", "password_resets": "user_id", "invites": "used_by"} {
		if n := f.count(t, "SELECT COUNT(*) FROM "+table+" WHERE "+where+" = ?", u.ID); n != 0 {
			t.Errorf("%d rows of %s left", n, table)
		}
	}
	if n := f.count(t, "SELECT COUNT(*) FROM users WHERE id = ? AND username = 'alice' AND password_hash IS NULL AND removed_at IS NOT NULL", u.ID); n != 1 {
		t.Error("want the row kept with its name, without its password hash")
	}
	_, err := f.User(f.ctx, u.ID)
	wantErr(t, "a removed user", err, ErrNotFound)
	_, err = f.UserByUsername(f.ctx, "alice")
	wantErr(t, "a removed user by name", err, ErrNotFound)
	if list, err := f.Users(f.ctx); err != nil || len(list) != 0 {
		t.Errorf("users %+v, %v", list, err)
	}
	wantErr(t, "removed again", f.Remove(f.ctx, Admin, u.ID), ErrNotFound)
}

func TestUsers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.enrolled(t, "bob")
	f.signUp(t, "Alice")
	list, err := f.Users(f.ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("users %+v, %v", list, err)
	}
	if list[0].Username != "Alice" || list[0].SecondFactor != "" || list[0].Sessions != 1 || !list[0].LastLoginAt.IsZero() {
		t.Errorf("Alice %+v", list[0])
	}
	if list[1].Username != "bob" || list[1].SecondFactor != FactorTOTP || list[1].LastLoginAt.IsZero() {
		t.Errorf("bob %+v", list[1])
	}
}

func TestSessionIdle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	token := u.session.Token
	f.clock.advance(SessionIdle - time.Millisecond)
	se, err := f.Session(f.ctx, token)
	if err != nil {
		t.Fatalf("a session just before its idle end: %v", err)
	}
	if !se.ExpiresAt.Equal(f.clock.now().Add(SessionIdle)) {
		t.Errorf("expires %v, want 12 hours after now", se.ExpiresAt)
	}
	f.clock.advance(SessionIdle)
	_, err = f.Session(f.ctx, token)
	wantErr(t, "a session idle 12 hours", err, ErrNoSession)
}

func TestSessionLastSeenAtMostOnceAMinute(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	seen := func() int64 {
		return int64(f.count(t, "SELECT last_seen_at FROM web_sessions WHERE id = ?", u.session.ID))
	}
	f.clock.advance(time.Minute - time.Millisecond)
	if _, err := f.Session(f.ctx, u.session.Token); err != nil {
		t.Fatal(err)
	}
	if got := seen(); got != at.UnixMilli() {
		t.Fatalf("last seen written within a minute: %d", got)
	}
	f.clock.advance(time.Millisecond)
	if _, err := f.Session(f.ctx, u.session.Token); err != nil {
		t.Fatal(err)
	}
	if got := seen(); got != f.clock.now().UnixMilli() {
		t.Fatalf("last seen %d, want written once a minute passed", got)
	}
}

func TestSessionAtMostSevenDays(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	for f.clock.now().Before(at.Add(SessionMax - 11*time.Hour)) {
		f.clock.advance(11 * time.Hour)
		if _, err := f.Session(f.ctx, u.session.Token); err != nil {
			t.Fatalf("used every 11 hours, at %v: %v", f.clock.now(), err)
		}
	}
	se, err := f.Session(f.ctx, u.session.Token)
	if err != nil || !se.ExpiresAt.Equal(at.Add(SessionMax)) {
		t.Fatalf("near its end: %+v, %v; want it to expire 7 days after it started", se, err)
	}
	f.clock.advance(at.Add(SessionMax).Sub(f.clock.now()))
	_, err = f.Session(f.ctx, u.session.Token)
	wantErr(t, "a session 7 days old", err, ErrNoSession)
}

func TestSessions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	other := f.login(t, u)
	list, err := f.Sessions(f.ctx, u.ID)
	if err != nil || len(list) != 2 || list[0].ID != u.session.ID || list[1].ID != other.ID || list[1].UserAgent != "Safari" || list[1].Address != browser.Address {
		t.Fatalf("sessions %+v, %v", list, err)
	}
	if err := f.EndSession(f.ctx, browser, u.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.lastAction(t); got != ActionSessionEnded {
		t.Errorf("last action %s", got)
	}
	_, err = f.Session(f.ctx, other.Token)
	wantErr(t, "a session ended", err, ErrNoSession)
	wantErr(t, "a session ended again", f.EndSession(f.ctx, browser, u.ID, other.ID), ErrNoSession)
	if got, err := f.User(f.ctx, u.ID); err != nil || got.Sessions != 1 {
		t.Errorf("user %+v, %v; want one session counted", got, err)
	}
}

func TestDeleteExpired(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice") // its invite used, which stays
	if _, err := f.CreateInvite(f.ctx, Admin, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CreateReset(f.ctx, Admin, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if e, err := f.DeleteExpired(f.ctx); err != nil || e != (Expired{}) {
		t.Fatalf("nothing expired yet: %+v, %v", e, err)
	}
	f.clock.advance(ResetLife)
	keep := f.login(t, u)
	if e, err := f.DeleteExpired(f.ctx); err != nil || e != (Expired{Sessions: 1, Invites: 1, Resets: 1}) {
		t.Fatalf("deleted %+v, %v; want the session idle, the invite and the reset", e, err)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM invites"); n != 1 {
		t.Errorf("%d invites, want the one used kept", n)
	}
	if _, err := f.Session(f.ctx, keep.Token); err != nil {
		t.Errorf("the live session: %v", err)
	}
}

func TestRecordLockout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.signUp(t, "alice")
	for _, name := range []string{"Alice", "nobody"} {
		if err := f.RecordLockout(f.ctx, browser, name); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.db.Query("SELECT COALESCE(user_id, ''), details FROM audit WHERE action = ? ORDER BY id", ActionLockedOut)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, details string
		if err := rows.Scan(&id, &details); err != nil {
			t.Fatal(err)
		}
		got = append(got, id+" "+details)
	}
	if strings.Join(got, ",") != u.ID+" {}, {}" {
		t.Errorf("lockouts %v, want alice's id, then none, and nothing typed", got)
	}
}

// Every act writes its row of the audit chain, in order, unbroken.
func TestEveryActAudited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.enrolled(t, "alice")
	started := f.login(t, u)
	if _, err := f.PassTOTP(f.ctx, browser, u.ID, started.ID, code(t, u.key, f.step(0))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.PassRecoveryCode(f.ctx, browser, u.ID, f.login(t, u).ID, u.codes[0]); err != nil {
		t.Fatal(err)
	}
	key, err := f.ReplaceTOTP(f.ctx, u.ID, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ConfirmReplacement(f.ctx, browser, u.ID, code(t, key, f.step(0))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.RenewRecoveryCodes(f.ctx, browser, u.ID, password); err != nil {
		t.Fatal(err)
	}
	if err := f.ChangePassword(f.ctx, browser, u.ID, u.session.ID, password, password+"!"); err != nil {
		t.Fatal(err)
	}
	r, err := f.CreateReset(f.ctx, Admin, u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.UseReset(f.ctx, browser, r.Token, password); err != nil {
		t.Fatal(err)
	}
	if err := f.EndSession(f.ctx, browser, u.ID, f.login(t, u).ID); err != nil {
		t.Fatal(err)
	}
	if err := f.RecordLockout(f.ctx, browser, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, act := range []func() (bool, error){
		func() (bool, error) { return f.Disable(f.ctx, Admin, u.ID) },
		func() (bool, error) { return f.Enable(f.ctx, Admin, u.ID) },
		func() (bool, error) { return true, f.Remove(f.ctx, Admin, u.ID) },
	} {
		if _, err := act(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		ActionInvited, ActionSignedUp, ActionFactorEnrolled, ActionLoggedIn,
		ActionLoggedIn, ActionLoggedIn, ActionFactorReplaced, ActionRecoveryCodesRenewed, ActionPasswordChanged,
		ActionResetMade, ActionResetUsed, ActionSessionEnded, ActionLockedOut, ActionDisabled, ActionEnabled, ActionRemoved,
	}
	if got := f.actions(t); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("actions\n%v\nwant\n%v", got, want)
	}
	var actors string
	if err := f.db.QueryRow("SELECT group_concat(actor || '@' || COALESCE(address, ''), ' ') FROM audit WHERE action IN (?, ?)", ActionResetMade, ActionResetUsed).Scan(&actors); err != nil {
		t.Fatal(err)
	}
	if actors != "admin@ user@"+browser.Address {
		t.Errorf("the reset's actors %q, want the admin's then the browser's", actors)
	}
}

// Every method that reads or changes one user's rows, given another user's
// id, finds nothing of the first user's and leaves it as it was.
func TestScopedToTheUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.enrolled(t, "alice"), f.enrolled(t, "bob")
	enrolling := f.signUp(t, "carol")
	key, err := f.EnrolTOTP(f.ctx, enrolling.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := f.login(t, a) // alice's session, its second factor not passed

	wantErr(t, "EndSession", f.EndSession(f.ctx, browser, b.ID, a.session.ID), ErrNoSession)
	_, err = f.PassTOTP(f.ctx, browser, b.ID, pending.ID, code(t, b.key, f.step(0)))
	wantErr(t, "PassTOTP with bob's code", err, ErrNoSession)
	_, err = f.PassTOTP(f.ctx, browser, b.ID, pending.ID, code(t, a.key, f.step(0)))
	wantErr(t, "PassTOTP with alice's code", err, ErrWrongCode)
	_, _, err = f.PassRecoveryCode(f.ctx, browser, b.ID, pending.ID, b.codes[0])
	wantErr(t, "PassRecoveryCode with bob's code", err, ErrNoSession)
	_, _, err = f.PassRecoveryCode(f.ctx, browser, b.ID, b.session.ID, a.codes[0])
	wantErr(t, "PassRecoveryCode with alice's code", err, ErrWrongCode)
	_, err = f.ConfirmTOTP(f.ctx, browser, enrolling.ID, pending.ID, code(t, key, f.step(0)))
	wantErr(t, "ConfirmTOTP", err, ErrNoSession)
	_, err = f.ConfirmTOTP(f.ctx, browser, b.ID, enrolling.session.ID, code(t, key, f.step(0)))
	wantErr(t, "ConfirmTOTP of carol's key", err, ErrNoEnrolment)
	err = f.ConfirmReplacement(f.ctx, browser, b.ID, code(t, a.key, f.step(0)))
	wantErr(t, "ConfirmReplacement", err, ErrNoEnrolment)
	_, err = f.ReplaceTOTP(f.ctx, b.ID, "wrong password!")
	wantErr(t, "ReplaceTOTP", err, ErrWrongPassword)
	_, err = f.RenewRecoveryCodes(f.ctx, browser, enrolling.ID, password)
	wantErr(t, "RenewRecoveryCodes", err, ErrNoFactor)
	if err := f.ChangePassword(f.ctx, browser, b.ID, a.session.ID, password, password+"!"); err != nil {
		t.Fatal(err)
	}
	if list, err := f.Sessions(f.ctx, b.ID); err != nil || len(list) != 0 {
		t.Fatalf("bob's sessions %+v, %v; want none, his others ended and alice's not his", list, err)
	}
	if n, err := f.RecoveryCodesLeft(f.ctx, b.ID); err != nil || n != RecoveryCodes {
		t.Fatalf("bob's codes left %d, %v", n, err)
	}
	if _, err := f.CreateReset(f.ctx, Admin, b.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Disable(f.ctx, Admin, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(f.ctx, Admin, b.ID); err != nil {
		t.Fatal(err)
	}

	// Alice is as she was: her sessions, her factor, her codes, her password.
	for _, token := range []string{a.session.Token, pending.Token} {
		if _, err := f.Session(f.ctx, token); err != nil {
			t.Fatalf("alice's session: %v", err)
		}
	}
	if list, err := f.Sessions(f.ctx, a.ID); err != nil || len(list) != 2 {
		t.Fatalf("alice's sessions %+v, %v", list, err)
	}
	if n, err := f.RecoveryCodesLeft(f.ctx, a.ID); err != nil || n != RecoveryCodes {
		t.Fatalf("alice's codes left %d, %v", n, err)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM password_resets WHERE user_id = ?", a.ID); n != 0 {
		t.Fatalf("alice has %d resets", n)
	}
	u, err := f.User(f.ctx, a.ID)
	if err != nil || u.Disabled() || u.SecondFactor != FactorTOTP {
		t.Fatalf("alice %+v, %v", u, err)
	}
	if _, err := f.PassTOTP(f.ctx, browser, a.ID, pending.ID, code(t, a.key, f.step(0))); err != nil {
		t.Fatalf("alice's code: %v", err)
	}
	if _, err := f.EnrolTOTP(f.ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("EnrolTOTP of bob removed: %v", err)
	}
}

// A sealed secret copied to another user's row does not open there.
func TestSealedSecretBoundToItsUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.enrolled(t, "alice"), f.enrolled(t, "bob")
	if _, err := f.db.Exec("UPDATE totp_factors SET secret = (SELECT secret FROM totp_factors WHERE user_id = ?) WHERE user_id = ?", a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.PassTOTP(f.ctx, browser, b.ID, f.login(t, b).ID, code(t, a.key, f.step(0)))
	if err == nil || errors.Is(err, ErrWrongCode) {
		t.Fatalf("alice's secret in bob's row: %v, want it not to open", err)
	}
	var sealed []byte
	if err := f.db.QueryRow("SELECT secret FROM totp_factors WHERE user_id = ?", a.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(a.key.Secret())) {
		t.Fatal("the secret is stored in the clear")
	}
}

// No secret the store makes or is given reaches its log: passwords, tokens,
// codes, the TOTP secret, their hashes, the sealed secret, the master key.
func TestNoSecretLogged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	inv, err := f.CreateInvite(f.ctx, Admin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, signup, err := f.SignUp(f.ctx, browser, SignUp{Invite: inv.Token, Username: "alice", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.EnrolTOTP(f.ctx, signup.UserID)
	if err != nil {
		t.Fatal(err)
	}
	enrolCode := code(t, key, f.step(0))
	c, err := f.ConfirmTOTP(f.ctx, browser, signup.UserID, signup.ID, enrolCode)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.advance(totpPeriod * time.Second)
	_, wrong, _ := f.Login(f.ctx, browser, "alice", "a wrong password", "")
	_, login, err := f.Login(f.ctx, browser, "alice", password, "")
	if err != nil {
		t.Fatal(err)
	}
	loginCode := code(t, key, f.step(0))
	passed, err := f.PassTOTP(f.ctx, browser, signup.UserID, login.ID, loginCode)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = f.PassRecoveryCode(f.ctx, browser, signup.UserID, f.login(t, user{User: User{Username: "alice"}}).ID, c.RecoveryCodes[0])
	replacement, err := f.ReplaceTOTP(f.ctx, signup.UserID, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ConfirmReplacement(f.ctx, browser, signup.UserID, code(t, replacement, f.step(0))); err != nil {
		t.Fatal(err)
	}
	renewed, err := f.RenewRecoveryCodes(f.ctx, browser, signup.UserID, password)
	if err != nil {
		t.Fatal(err)
	}
	const changed = "the password changed"
	if err := f.ChangePassword(f.ctx, browser, signup.UserID, passed.ID, password, changed); err != nil {
		t.Fatal(err)
	}
	r, err := f.CreateReset(f.ctx, Admin, signup.UserID, true)
	if err != nil {
		t.Fatal(err)
	}
	const reset = "the password reset"
	if _, err := f.UseReset(f.ctx, browser, r.Token, reset); err != nil {
		t.Fatal(err)
	}

	tokens := []string{inv.Token, r.Token, signup.Token, c.Token, wrong.Token, login.Token, passed.Token}
	secrets := []string{
		password, "a wrong password", changed, reset,
		key.Secret(), replacement.Secret(), enrolCode, loginCode, base64.StdEncoding.EncodeToString(f.key),
	}
	secrets = append(secrets, tokens...)
	for _, token := range tokens {
		secrets = append(secrets, hashForms(hashOf(token))...)
	}
	for _, code := range append(c.RecoveryCodes, renewed...) {
		secrets = append(secrets, code, strings.ReplaceAll(code, "-", ""))
		secrets = append(secrets, hashForms(hashOf(normalCode(code)))...)
	}
	rows, err := f.db.Query("SELECT password_hash FROM users UNION ALL SELECT secret FROM totp_factors")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v []byte
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		secrets = append(secrets, string(v))
		secrets = append(secrets, hashForms(v)...)
	}
	log := f.log.String()
	if !strings.Contains(log, `"msg":"sql exec"`) || !strings.Contains(log, "[secret, ") {
		t.Fatalf("want the debug SQL logged, with its secrets' lengths:\n%s", log)
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(log, s) {
			t.Errorf("%q is in the log", s)
		}
	}
}

// hashForms are the forms bytes take in a log line: base64, as JSON has
// them, and hex.
func hashForms(b []byte) []string {
	return []string{base64.StdEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b), hex.EncodeToString(b)}
}
