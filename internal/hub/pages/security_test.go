package pages_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/hub/account"
)

func TestChangePassword(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	p := h.signUp(b, "alice")
	other := h.browser()
	h.login(other, p).want(t, http.StatusOK, "/security")
	const newPassword = "a new password, long"
	h.secret(newPassword)
	page := b.get("/security")
	b.submit(page, "/security/password", map[string]string{"current": "wrong password!", "password": newPassword, "password2": newPassword}).want(t, http.StatusUnprocessableEntity, "", "The current password is wrong.")
	b.submit(page, "/security/password", map[string]string{"current": p.password, "password": newPassword, "password2": newPassword + "!"}).want(t, http.StatusUnprocessableEntity, "", "The two new passwords differ.")
	b.submit(page, "/security/password", map[string]string{"current": p.password, "password": "short", "password2": "short"}).want(t, http.StatusUnprocessableEntity, "", "The new Password must be")
	b.submit(page, "/security/password", map[string]string{"current": p.password, "password": newPassword, "password2": newPassword}).want(t, http.StatusOK, "/security?done=password", "Password changed.")
	other.get("/security").want(t, http.StatusOK, "/login")
	p.password = newPassword
	h.login(other, p).want(t, http.StatusOK, "/security")
	if got := h.actions(); count(got, account.ActionPasswordChanged) != 1 {
		t.Errorf("actions %v", got)
	}
}

func TestReplaceAuthenticator(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	p := h.signUp(b, "alice")
	page := b.get("/security")
	b.submit(page, "/security/authenticator", map[string]string{"current": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "", "The current password is wrong.")
	// Without a replacement started, its page is the Security page.
	b.get("/security/authenticator").want(t, http.StatusOK, "/security")
	replace := b.submit(page, "/security/authenticator", map[string]string{"current": p.password})
	replace.want(t, http.StatusOK, "/security/authenticator", "Replace your authenticator")
	key := keyOf(t, replace)
	h.secret(key)
	if key == p.key {
		t.Fatal("the new key is the old")
	}
	wrong := b.submit(replace, "/security/authenticator/confirm", map[string]string{"code": h.code(p.key)})
	wrong.want(t, http.StatusUnprocessableEntity, "", "That code is wrong")
	if keyOf(t, wrong) != key {
		t.Fatal("a wrong code showed another key")
	}
	b.submit(replace, "/security/authenticator/confirm", map[string]string{"code": h.code(key)}).want(t, http.StatusOK, "/security?done=authenticator", "Authenticator replaced")

	other := h.browser()
	second := other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password})
	other.submit(second, "/login/second-factor", map[string]string{"code": h.code(p.key)}).want(t, http.StatusUnprocessableEntity, "", "That code is wrong")
	p.key = key
	other.submit(second, "/login/second-factor", map[string]string{"code": h.code(p.key)}).want(t, http.StatusOK, "/security")
	if got := h.actions(); count(got, account.ActionFactorReplaced) != 1 {
		t.Errorf("actions %v", got)
	}
}

func TestRenewRecoveryCodes(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	p := h.signUp(b, "alice")
	page := b.get("/security")
	b.submit(page, "/security/recovery-codes", map[string]string{"current": "wrong password!"}).want(t, http.StatusUnprocessableEntity, "", "The current password is wrong.")
	renewed := b.submit(page, "/security/recovery-codes", map[string]string{"current": p.password})
	renewed.want(t, http.StatusOK, "/security/recovery-codes", "Your new recovery codes", "The old codes no longer work.")
	codes := codesOf(t, renewed)
	h.secret(codes...)
	b.submit(renewed, "/recovery-codes/saved", map[string]string{"saved": "yes"}).want(t, http.StatusOK, "/security", "10 of 10 left")

	other := h.browser()
	other.submit(other.get("/login"), "/login", map[string]string{"username": "alice", "password": p.password})
	recovery := other.get("/login/recovery-code")
	other.submit(recovery, "/login/recovery-code", map[string]string{"code": p.codes[0]}).want(t, http.StatusUnprocessableEntity, "", "wrong")
	other.submit(recovery, "/login/recovery-code", map[string]string{"code": codes[0]}).want(t, http.StatusOK, "", "9 recovery codes remain.")
}

var rowPattern = regexp.MustCompile(`(?s)<tr( class="this")?>\s*(<td>.*?)</tr>`)

func TestBrowsers(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	b.header.Set("User-Agent", "Firefox of the test")
	p := h.signUp(b, "alice")
	other := h.browser()
	other.header.Set("User-Agent", "Safari of the test")
	h.login(other, p).want(t, http.StatusOK, "/security")

	page := b.get("/security")
	rows := rowPattern.FindAllStringSubmatch(page.body, -1)
	if len(rows) != 2 {
		t.Fatalf("%d browsers:\n%s", len(rows), page.body)
	}
	for _, row := range rows {
		this := row[1] != ""
		if this != strings.Contains(row[2], "Firefox of the test") || this != strings.Contains(row[2], "this browser") || !strings.Contains(row[2], "127.0.0.1") {
			t.Errorf("row %s", row[0])
		}
	}
	// The other is signed out from here; this one is not offered.
	if strings.Count(page.body, `action="/security/sessions/end"`) != 1 {
		t.Fatalf("want one browser to sign out:\n%s", page.body)
	}
	b.submit(page, "/security/sessions/end", nil).want(t, http.StatusOK, "/security?done=ended", "That browser was signed out.")
	other.get("/security").want(t, http.StatusOK, "/login")
	if n := len(rowPattern.FindAllString(b.get("/security").body, -1)); n != 1 {
		t.Errorf("%d browsers once the other ended", n)
	}

	// Another user's session is not this user's to end.
	bob := h.browser()
	h.signUp(bob, "bob")
	bobsPage := bob.get("/security")
	ownID := regexp.MustCompile(`name="session" value="([^"]*)"`)
	if ownID.MatchString(bobsPage.body) {
		t.Fatal("bob has another browser")
	}
	sessions, err := h.server.Accounts().Sessions(h.ctx, h.userID("alice"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("alice's sessions %v, %v", sessions, err)
	}
	bob.post("/security/sessions/end", map[string]string{"session": sessions[0].ID}).want(t, http.StatusOK, "/security")
	b.get("/security").want(t, http.StatusOK, "/security")

	b.submit(b.get("/security"), "/logout", nil).want(t, http.StatusOK, "/login", "Log in")
	b.get("/security").want(t, http.StatusOK, "/login")
	if got := h.actions(); count(got, account.ActionSessionEnded) != 2 {
		t.Errorf("actions %v", got)
	}
	if len(h.logged("session ended")) != 2 {
		t.Error("want two session ended logged")
	}
}
