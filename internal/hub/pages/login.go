package pages

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/logging"
)

func loginPage(v page) page {
	v.Title = "Log in"
	return v
}

func (p *pages) loginForm(w http.ResponseWriter, r *http.Request) {
	// A browser logged in, or half way, goes on to where it stands.
	if _, ok := p.at(w, r, "/login"); !ok {
		return
	}
	p.render(w, r, http.StatusOK, "login", loginPage(page{Next: p.next(r)}))
}

func (p *pages) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username := strings.TrimSpace(r.PostFormValue("username"))
	v := page{Username: username, Next: p.next(r)}
	if p.limited(w, r, username, "login refused") {
		return
	}
	u, started, err := p.opts.Accounts.Login(ctx, p.by(r), username, r.PostFormValue("password"), userAgent(r))
	switch {
	case errors.Is(err, account.ErrWrongPassword):
		p.wrong(r, username)
		v.Error = "The username or password is wrong."
		p.render(w, r, http.StatusUnprocessableEntity, "login", loginPage(v))
	case errors.Is(err, account.ErrDisabled):
		p.message(w, r, http.StatusForbidden, page{Title: "This account is disabled", Text: "Ask the hub's operator to enable it."})
	case err != nil:
		p.failed(w, r, err)
	default:
		setSession(w, started.Token)
		if u.SecondFactor == "" {
			logging.FromContext(ctx).Info("login goes on to enrolment", "user_id", u.ID, "reason", "no authenticator confirmed")
			redirect(w, r, withNext("/enrol", v.Next))
			return
		}
		redirect(w, r, withNext("/login/second-factor", v.Next))
	}
}

func (p *pages) secondFactorForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.at(w, r, "/login/second-factor"); !ok {
		return
	}
	p.render(w, r, http.StatusOK, "second_factor", page{Title: "Your authenticator's code", Next: p.next(r)})
}

func (p *pages) secondFactor(w http.ResponseWriter, r *http.Request) {
	v, ok := p.at(w, r, "/login/second-factor")
	if !ok || p.limited(w, r, v.user.Username, "login refused") {
		return
	}
	ctx := r.Context()
	next := p.next(r)
	started, err := p.opts.Accounts.PassTOTP(ctx, p.by(r), v.user.ID, v.session.ID, r.PostFormValue("code"))
	switch {
	case errors.Is(err, account.ErrWrongCode):
		p.wrong(r, v.user.Username)
		logging.FromContext(ctx).Info("login refused", "reason", "code", "user_id", v.user.ID)
		p.render(w, r, http.StatusUnprocessableEntity, "second_factor", page{Title: "Your authenticator's code", Next: next, Error: "That code is wrong, or was used already: type the one your app shows now."})
	case errors.Is(err, account.ErrNoSession), errors.Is(err, account.ErrNoFactor):
		redirect(w, r, withNext("/login", next))
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		setSession(w, started.Token)
		redirect(w, r, done(next))
	}
}

func (p *pages) recoveryCodeForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.at(w, r, "/login/second-factor"); !ok {
		return
	}
	p.render(w, r, http.StatusOK, "recovery_code", page{Title: "A recovery code", Next: p.next(r)})
}

func (p *pages) recoveryCode(w http.ResponseWriter, r *http.Request) {
	v, ok := p.at(w, r, "/login/second-factor")
	if !ok || p.limited(w, r, v.user.Username, "login refused") {
		return
	}
	ctx := r.Context()
	next := p.next(r)
	started, left, err := p.opts.Accounts.PassRecoveryCode(ctx, p.by(r), v.user.ID, v.session.ID, r.PostFormValue("code"))
	switch {
	case errors.Is(err, account.ErrWrongCode):
		p.wrong(r, v.user.Username)
		logging.FromContext(ctx).Info("login refused", "reason", "recovery_code", "user_id", v.user.ID)
		p.render(w, r, http.StatusUnprocessableEntity, "recovery_code", page{Title: "A recovery code", Next: next, Error: "That recovery code is wrong, or was used already."})
	case errors.Is(err, account.ErrNoSession):
		redirect(w, r, withNext("/login", next))
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		setSession(w, started.Token)
		p.message(w, r, http.StatusOK, page{
			Title:    "Logged in with a recovery code",
			Text:     codesLeft(left) + " The code you used will not work again: make new ones on the Security page before they run out.",
			Link:     done(next),
			LinkText: "Go on",
		})
	}
}

func codesLeft(n int) string {
	switch n {
	case 0:
		return "No recovery codes remain."
	case 1:
		return "1 recovery code remains."
	default:
		return strconv.Itoa(n) + " recovery codes remain."
	}
}

func (p *pages) logout(w http.ResponseWriter, r *http.Request) {
	v, ok, err := p.visitor(r)
	if err != nil {
		p.failed(w, r, err)
		return
	}
	if ok {
		if err := p.opts.Accounts.EndSession(r.Context(), p.by(r), v.user.ID, v.session.ID); err != nil && !errors.Is(err, account.ErrNoSession) {
			p.failed(w, r, err)
			return
		}
	}
	clearSession(w)
	redirect(w, r, "/login")
}

func resetPage(v page) page {
	v.Title = "Choose a new password"
	return v
}

func (p *pages) resetRefused(w http.ResponseWriter, r *http.Request) {
	p.message(w, r, http.StatusNotFound, page{Title: "This link has expired or been used", Text: "Ask the hub's operator for a new one."})
}

func (p *pages) resetForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if wait := p.limits.takeTry(p.address(r)); wait > 0 {
		logging.FromContext(r.Context()).Info("reset refused", "reason", "rate_limited", "wait_ms", wait.Milliseconds())
		p.tooMany(w, r, wait)
		return
	}
	secondFactor, err := p.opts.Accounts.CheckReset(r.Context(), token)
	if errors.Is(err, account.ErrInvalidReset) {
		p.resetRefused(w, r)
		return
	} else if err != nil {
		p.failed(w, r, err)
		return
	}
	p.render(w, r, http.StatusOK, "reset", resetPage(page{Token: token, SecondFactor: secondFactor}))
}

func (p *pages) reset(w http.ResponseWriter, r *http.Request) {
	if p.limited(w, r, "", "reset refused") {
		return
	}
	ctx := r.Context()
	token := r.PostFormValue("token")
	secondFactor, err := p.opts.Accounts.CheckReset(ctx, token)
	if errors.Is(err, account.ErrInvalidReset) {
		p.resetRefused(w, r)
		return
	} else if err != nil {
		p.failed(w, r, err)
		return
	}
	v := resetPage(page{Token: token, SecondFactor: secondFactor})
	if r.PostFormValue("password") != r.PostFormValue("password2") {
		logging.FromContext(ctx).Info("reset refused", "reason", "passwords_differ")
		v.Error = "The two passwords differ."
		p.render(w, r, http.StatusUnprocessableEntity, "reset", v)
		return
	}
	_, err = p.opts.Accounts.UseReset(ctx, p.by(r), token, r.PostFormValue("password"))
	var invalid *account.InvalidError
	switch {
	case errors.Is(err, account.ErrInvalidReset):
		p.resetRefused(w, r)
	case errors.As(err, &invalid):
		v.Error = sentence(invalid.Error())
		p.render(w, r, http.StatusUnprocessableEntity, "reset", v)
	case err != nil:
		p.failed(w, r, err)
	default:
		// Every session of the user ended, this browser's with them.
		clearSession(w)
		text := "Every browser was signed out: log in with the new password."
		if secondFactor {
			text += " You set up your authenticator again after it."
		}
		p.message(w, r, http.StatusOK, page{Title: "Password set", Text: text, Link: "/login", LinkText: "Log in"})
	}
}
