package pages

import (
	"errors"
	"net/http"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/logging"
)

// notices are what the Security page says after an act, by its done query.
var notices = map[string]string{
	"password":      "Password changed. Every other browser was signed out.",
	"authenticator": "Authenticator replaced: use the new entry in your app from now on.",
	"ended":         "That browser was signed out.",
}

// securityPage shows the Security page of v, with status and, when not
// empty, an error.
func (p *pages) securityPage(w http.ResponseWriter, r *http.Request, v visitor, status int, errText string) {
	ctx := r.Context()
	sessions, err := p.opts.Accounts.Sessions(ctx, v.user.ID)
	if err != nil {
		p.failed(w, r, err)
		return
	}
	left, err := p.opts.Accounts.RecoveryCodesLeft(ctx, v.user.ID)
	if err != nil {
		p.failed(w, r, err)
		return
	}
	shown := page{Title: "Security", Username: v.user.Username, Left: left, Error: errText, Notice: notices[r.URL.Query().Get("done")]}
	for _, se := range sessions {
		shown.Sessions = append(shown.Sessions, browser{
			ID: se.ID, Address: se.Address, UserAgent: se.UserAgent,
			Started: when(se.CreatedAt), Seen: when(se.LastSeenAt),
			This: se.ID == v.session.ID,
		})
	}
	p.render(w, r, status, "security", shown)
}

func when(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func (p *pages) security(w http.ResponseWriter, r *http.Request) {
	if v, ok := p.loggedIn(w, r); ok {
		p.securityPage(w, r, v, http.StatusOK, "")
	}
}

// wrongCurrent answers a current password that is wrong, counting it.
func (p *pages) wrongCurrent(w http.ResponseWriter, r *http.Request, v visitor) {
	p.wrong(r, v.user.Username)
	logging.FromContext(r.Context()).Info("change refused", "reason", "password", "user_id", v.user.ID)
	p.securityPage(w, r, v, http.StatusUnprocessableEntity, "The current password is wrong.")
}

func (p *pages) changePassword(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok || p.limited(w, r, v.user.Username, "change refused") {
		return
	}
	ctx := r.Context()
	if r.PostFormValue("password") != r.PostFormValue("password2") {
		logging.FromContext(ctx).Info("change refused", "reason", "passwords_differ")
		p.securityPage(w, r, v, http.StatusUnprocessableEntity, "The two new passwords differ.")
		return
	}
	err := p.opts.Accounts.ChangePassword(ctx, p.by(r), v.user.ID, v.session.ID, r.PostFormValue("current"), r.PostFormValue("password"))
	var invalid *account.InvalidError
	switch {
	case errors.Is(err, account.ErrWrongPassword):
		p.wrongCurrent(w, r, v)
	case errors.As(err, &invalid):
		p.securityPage(w, r, v, http.StatusUnprocessableEntity, "The new "+sentence(invalid.Error()))
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		redirect(w, r, home+"?done=password")
	}
}

func (p *pages) replaceAuthenticator(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok || p.limited(w, r, v.user.Username, "change refused") {
		return
	}
	_, err := p.opts.Accounts.ReplaceTOTP(r.Context(), v.user.ID, r.PostFormValue("current"))
	switch {
	case errors.Is(err, account.ErrWrongPassword):
		p.wrongCurrent(w, r, v)
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		redirect(w, r, "/security/authenticator")
	}
}

// replacement shows the authenticator replacing v's, or goes back to the
// Security page when none is.
func (p *pages) replacement(w http.ResponseWriter, r *http.Request, v visitor, status int, errText string) {
	key, err := p.opts.Accounts.ReplacementKey(r.Context(), v.user.ID)
	if errors.Is(err, account.ErrNoEnrolment) {
		redirect(w, r, home)
		return
	}
	if err != nil {
		p.failed(w, r, err)
		return
	}
	shown, err := keyPage(page{Title: "Replace your authenticator", Error: errText}, key, "/security/authenticator/confirm")
	if err != nil {
		p.failed(w, r, err)
		return
	}
	p.render(w, r, status, "enrol", shown)
}

func (p *pages) replacementForm(w http.ResponseWriter, r *http.Request) {
	if v, ok := p.loggedIn(w, r); ok {
		p.replacement(w, r, v, http.StatusOK, "")
	}
}

func (p *pages) confirmReplacement(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok || p.limited(w, r, v.user.Username, "change refused") {
		return
	}
	ctx := r.Context()
	err := p.opts.Accounts.ConfirmReplacement(ctx, p.by(r), v.user.ID, r.PostFormValue("code"))
	switch {
	case errors.Is(err, account.ErrWrongCode):
		p.wrong(r, v.user.Username)
		logging.FromContext(ctx).Info("change refused", "reason", "code", "user_id", v.user.ID)
		p.replacement(w, r, v, http.StatusUnprocessableEntity, "That code is wrong: type the one the new entry in your app shows now.")
	case errors.Is(err, account.ErrNoEnrolment):
		redirect(w, r, home)
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		redirect(w, r, home+"?done=authenticator")
	}
}

func (p *pages) renewCodes(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok || p.limited(w, r, v.user.Username, "change refused") {
		return
	}
	codes, err := p.opts.Accounts.RenewRecoveryCodes(r.Context(), p.by(r), v.user.ID, r.PostFormValue("current"))
	switch {
	case errors.Is(err, account.ErrWrongPassword):
		p.wrongCurrent(w, r, v)
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		p.render(w, r, http.StatusOK, "codes", page{Title: "Your new recovery codes", Codes: codes, Notice: "The old codes no longer work."})
	}
}

// endSession signs out one of the user's browsers; this one's is logging
// out.
func (p *pages) endSession(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok {
		return
	}
	id := r.PostFormValue("session")
	err := p.opts.Accounts.EndSession(r.Context(), p.by(r), v.user.ID, id)
	switch {
	case errors.Is(err, account.ErrNoSession):
		logging.FromContext(r.Context()).Info("session not ended", "reason", "no such session of the user")
		redirect(w, r, home)
	case err != nil:
		p.failed(w, r, err)
	case id == v.session.ID:
		clearSession(w)
		redirect(w, r, "/login")
	default:
		redirect(w, r, home+"?done=ended")
	}
}
