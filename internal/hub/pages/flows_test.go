package pages_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/server"
)

// From an invite to the Security page: a username and password, an
// authenticator confirmed by a code, the recovery codes saved.
func TestSignUp(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	h.secret(password)
	link := h.invite()
	form := b.get(link)
	form.want(t, http.StatusOK, "", "Sign up", `name="invite"`)
	enrol := b.submit(form, "/signup", map[string]string{"username": "Alice", "password": password, "password2": password})
	enrol.want(t, http.StatusOK, "/enrol")
	key := keyOf(t, enrol)
	h.secret(key)
	before := b.session()

	// Before the factor is confirmed the account can do nothing else.
	b.get("/security").want(t, http.StatusOK, "/enrol")
	b.get("/login/second-factor").want(t, http.StatusOK, "/enrol")
	// The page shows the same key again, not a new one.
	if again := keyOf(t, b.get("/enrol")); again != key {
		t.Fatalf("enrolment showed another key")
	}
	wrong := b.submit(enrol, "/enrol", map[string]string{"code": "000000"})
	wrong.want(t, http.StatusUnprocessableEntity, "/enrol", "That code is wrong")
	if keyOf(t, wrong) != key {
		t.Fatalf("a wrong code showed another key")
	}

	codes := b.submit(enrol, "/enrol", map[string]string{"code": h.code(key)})
	codes.want(t, http.StatusOK, "/enrol", "Save your recovery codes")
	h.secret(codesOf(t, codes)...)
	if b.session() == before || b.session() == "" {
		t.Fatal("the session kept its token past the second factor")
	}
	// The box must be ticked to go on.
	b.submit(codes, "/recovery-codes/saved", nil).want(t, http.StatusUnprocessableEntity, "", "Tick")
	b.submit(codes, "/recovery-codes/saved", map[string]string{"saved": "yes"}).want(t, http.StatusOK, "/security", "Logged in as <strong>Alice</strong>", "this browser", "10 of 10 left")

	// The invite works once.
	h.browser().get(link).want(t, http.StatusNotFound, "", "This invite has expired or been used")
	if got := h.actions(); count(got, account.ActionSignedUp) != 1 || count(got, account.ActionFactorEnrolled) != 1 {
		t.Errorf("actions %v", got)
	}
	if len(h.logged("user signed up")) != 1 {
		t.Error("no user signed up logged")
	}
}

func TestSignUpRefused(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	h.secret(password)
	h.signUp(h.browser(), "alice")
	b := h.browser()
	form := b.get(h.invite())
	for _, tc := range []struct {
		name               string
		username, password string
		again              string
		status             int
		text               string
	}{
		{"passwords differ", "bob", password, password + "!", http.StatusUnprocessableEntity, "The two passwords differ."},
		{"a short password", "bob", "short", "short", http.StatusUnprocessableEntity, "Password must be 12 to 128 characters."},
		{"a username of other characters", "bob smith", password, password, http.StatusUnprocessableEntity, "Username must be 1 to 39"},
		{"a username taken in another case", "ALICE", password, password, http.StatusConflict, "That username is taken"},
	} {
		got := b.submit(form, "/signup", map[string]string{"username": tc.username, "password": tc.password, "password2": tc.again})
		if got.status != tc.status || !strings.Contains(got.body, tc.text) || !strings.Contains(got.body, `name="invite"`) {
			t.Errorf("%s: got %d:\n%s", tc.name, got.status, got.body)
		}
	}
	// The invite was not used by any of them.
	b.submit(form, "/signup", map[string]string{"username": "bob", "password": password, "password2": password}).want(t, http.StatusOK, "/enrol")

	b.get("/signup?invite=nope").want(t, http.StatusNotFound, "", "This invite has expired or been used")
	b.get("/signup").want(t, http.StatusForbidden, "", "Signing up needs an invite")
	expired := h.invite()
	h.clock.advance(time.Hour)
	b.get(expired).want(t, http.StatusNotFound, "", "This invite has expired or been used")
}

// With registration open anyone signs up without an invite, from the next
// request after the setting changed.
func TestRegistrationOpen(t *testing.T) {
	t.Parallel()
	var reloaded hubconfig.Config
	h := startHub(t, func(o *server.Options) {
		reloaded = o.Config
		reloaded.Registration = hubconfig.RegistrationOpen
		o.Reload = func() (hubconfig.Config, error) { return reloaded, nil }
	})
	b := h.browser()
	b.get("/signup").want(t, http.StatusForbidden, "", "Signing up needs an invite")
	h.reload()
	form := b.get("/signup")
	form.want(t, http.StatusOK, "", "Sign up")
	h.secret(password)
	b.submit(form, "/signup", map[string]string{"username": "alice", "password": password, "password2": password}).want(t, http.StatusOK, "/enrol")
}

func TestLoginWithAuthenticator(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	p := h.signUp(h.browser(), "alice")
	b := h.browser()
	b.get("/security").want(t, http.StatusOK, "/login", "Log in")

	b.submit(b.get("/login"), "/login", map[string]string{"username": "alice", "password": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "/login", "The username or password is wrong.")
	b.submit(b.get("/login"), "/login", map[string]string{"username": "nobody", "password": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "/login", "The username or password is wrong.")
	second := b.submit(b.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password})
	second.want(t, http.StatusOK, "/login/second-factor", "Use a recovery code instead")
	before := b.session()
	// Half way, the Security page is the second factor's.
	b.get("/security").want(t, http.StatusOK, "/login/second-factor")
	b.submit(second, "/login/second-factor", map[string]string{"code": "000000"}).want(t, http.StatusUnprocessableEntity, "/login/second-factor", "That code is wrong")
	code := h.code(p.key)
	b.submit(second, "/login/second-factor", map[string]string{"code": code}).want(t, http.StatusOK, "/security", "Logged in as")
	if b.session() == before {
		t.Fatal("the session kept its token past the second factor")
	}
	// A token seen before the second factor is worthless after it.
	stolen := h.browser()
	stolen.setSession(before)
	stolen.get("/security").want(t, http.StatusOK, "/login")

	// A code works once.
	other := h.browser()
	again := other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password})
	other.submit(again, "/login/second-factor", map[string]string{"code": code}).want(t, http.StatusUnprocessableEntity, "", "That code is wrong, or was used already")

	if refused := h.logged("login refused"); !hasReason(refused, "password") || !hasReason(refused, "code") {
		t.Errorf("login refused logged %v", refused)
	}
}

func hasReason(lines []map[string]any, reason string) bool {
	for _, l := range lines {
		if l["reason"] == reason {
			return true
		}
	}
	return false
}

func TestLoginWithRecoveryCode(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	p := h.signUp(h.browser(), "alice")
	b := h.browser()
	b.submit(b.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password})
	recovery := b.get("/login/recovery-code")
	recovery.want(t, http.StatusOK, "/login/recovery-code", "A recovery code")
	b.submit(recovery, "/login/recovery-code", map[string]string{"code": "ABCDE-FGHJK"}).want(t, http.StatusUnprocessableEntity, "", "That recovery code is wrong")
	// Typed in lower case without its dash, it still is the code.
	typed := strings.ToLower(strings.ReplaceAll(p.codes[0], "-", ""))
	h.secret(typed)
	used := b.submit(recovery, "/login/recovery-code", map[string]string{"code": typed})
	used.want(t, http.StatusOK, "/login/recovery-code", "9 recovery codes remain.", "Security page")
	b.get("/security").want(t, http.StatusOK, "/security", "9 of 10 left")

	// A code works once.
	other := h.browser()
	other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password})
	other.submit(other.get("/login/recovery-code"), "/login/recovery-code", map[string]string{"code": p.codes[0]}).want(t, http.StatusUnprocessableEntity, "", "used already")
	if got := h.actions(); count(got, account.ActionLoggedIn) != 2 {
		t.Errorf("actions %v", got)
	}
}

// Someone who left before enrolling goes from their password to enrolment.
func TestLoginWithoutFactor(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	h.secret(password)
	b := h.browser()
	b.submit(b.get(h.invite()), "/signup", map[string]string{"username": "alice", "password": password, "password2": password}).want(t, http.StatusOK, "/enrol")
	later := h.browser()
	enrol := later.submit(later.get("/login"), "/login", map[string]string{"username": "alice", "password": password})
	enrol.want(t, http.StatusOK, "/enrol", "Set up your authenticator")
	later.get("/security").want(t, http.StatusOK, "/enrol")
	key := keyOf(t, enrol)
	h.secret(key)
	codes := later.submit(enrol, "/enrol", map[string]string{"code": h.code(key)})
	h.secret(codesOf(t, codes)...)
	later.submit(codes, "/recovery-codes/saved", map[string]string{"saved": "yes"}).want(t, http.StatusOK, "/security")
}

func TestDisabled(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	p := h.signUp(b, "alice")
	if _, err := h.server.Accounts().Disable(h.ctx, account.Admin, h.userID("alice")); err != nil {
		t.Fatal(err)
	}
	b.get("/security").want(t, http.StatusOK, "/login")
	other := h.browser()
	// A wrong password says nothing of the account.
	other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "", "The username or password is wrong.")
	other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password}).want(t, http.StatusForbidden, "", "This account is disabled")
	if !hasReason(h.logged("login refused"), "disabled") {
		t.Error("no login refused for disabled logged")
	}
}

func TestReset(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	p := h.signUp(b, "alice")
	link := h.reset("alice", false)
	// The reset is opened where alice is logged in.
	r := h.browser()
	h.login(r, p).want(t, http.StatusOK, "/security")
	form := r.get(link)
	form.want(t, http.StatusOK, "", "Choose a new password")
	if strings.Contains(form.body, "authenticator and recovery codes are removed") {
		t.Error("a reset without the second factor says it removes it")
	}
	r.submit(form, "/reset", map[string]string{"password": "a new password, long", "password2": "another one, long"}).want(t, http.StatusUnprocessableEntity, "", "The two passwords differ.")
	r.submit(form, "/reset", map[string]string{"password": "short", "password2": "short"}).want(t, http.StatusUnprocessableEntity, "", "Password must be")
	const newPassword = "a new password, long"
	h.secret(newPassword)
	r.submit(form, "/reset", map[string]string{"password": newPassword, "password2": newPassword}).want(t, http.StatusOK, "", "Password set")
	// Every browser was signed out, and this one's cookie is gone.
	if r.session() != "" {
		t.Error("the reset left its browser's cookie")
	}
	b.get("/security").want(t, http.StatusOK, "/login")
	r.get(link).want(t, http.StatusNotFound, "", "This link has expired or been used")
	r.get("/reset?token=nope").want(t, http.StatusNotFound, "", "This link has expired or been used")

	b.submit(b.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password}).want(t, http.StatusUnprocessableEntity, "", "wrong")
	p.password = newPassword
	h.login(b, p).want(t, http.StatusOK, "/security")
	if got := h.actions(); count(got, account.ActionResetUsed) != 1 {
		t.Errorf("actions %v", got)
	}
}

// A reset with the second factor removes the authenticator: the login goes
// on to enrol a new one.
func TestResetSecondFactor(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	h.signUp(h.browser(), "alice")
	r := h.browser()
	form := r.get(h.reset("alice", true))
	form.want(t, http.StatusOK, "", "authenticator and recovery codes are removed too")
	const newPassword = "a new password, long"
	h.secret(newPassword)
	r.submit(form, "/reset", map[string]string{"password": newPassword, "password2": newPassword}).want(t, http.StatusOK, "", "Password set", "set up your authenticator again")
	enrol := r.submit(r.get("/login"), "/login", map[string]string{"username": "alice", "password": newPassword})
	enrol.want(t, http.StatusOK, "/enrol", "Set up your authenticator")
	h.secret(keyOf(t, enrol))
}

// A session ends 12 hours after it was last used.
func TestSessionExpires(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	h.signUp(b, "alice")
	h.clock.advance(11 * time.Hour)
	b.get("/security").want(t, http.StatusOK, "/security")
	h.clock.advance(11 * time.Hour)
	b.get("/security").want(t, http.StatusOK, "/security")
	h.clock.advance(12 * time.Hour)
	b.get("/security").want(t, http.StatusOK, "/login")
}
