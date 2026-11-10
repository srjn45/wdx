package pages

import (
	"bytes"
	"encoding/base64"
	"errors"
	"html/template"
	"image/png"
	"net/http"
	"strings"

	"github.com/pquerna/otp"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/logging"
)

func (p *pages) inviteRefused(w http.ResponseWriter, r *http.Request) {
	p.message(w, r, http.StatusNotFound, page{Title: "This invite has expired or been used", Text: "Ask the hub's operator for a new one."})
}

func (p *pages) registrationClosed(w http.ResponseWriter, r *http.Request) {
	p.message(w, r, http.StatusForbidden, page{Title: "Signing up needs an invite", Text: "This hub is not open to anyone: ask its operator for an invite.", Link: "/login", LinkText: "Log in"})
}

func signUpPage(v page) page {
	v.Title = "Sign up"
	return v
}

func (p *pages) signUpForm(w http.ResponseWriter, r *http.Request) {
	invite := r.URL.Query().Get("invite")
	v := page{Invite: invite, Next: p.next(r)}
	if invite == "" {
		if !p.registrationOpen() {
			logging.FromContext(r.Context()).Info("sign-up refused", "reason", "registration_closed")
			p.registrationClosed(w, r)
			return
		}
		p.render(w, r, http.StatusOK, "signup", signUpPage(v))
		return
	}
	if wait := p.limits.takeTry(p.address(r)); wait > 0 {
		logging.FromContext(r.Context()).Info("sign-up refused", "reason", "rate_limited", "wait_ms", wait.Milliseconds())
		p.tooMany(w, r, wait)
		return
	}
	if err := p.opts.Accounts.CheckInvite(r.Context(), invite); errors.Is(err, account.ErrInvalidInvite) {
		p.inviteRefused(w, r)
		return
	} else if err != nil {
		p.failed(w, r, err)
		return
	}
	p.render(w, r, http.StatusOK, "signup", signUpPage(v))
}

func (p *pages) signUp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logging.FromContext(ctx)
	addr := p.address(r)
	if wait := p.limits.signUp(addr, r.PostFormValue("invite") != ""); wait > 0 {
		log.Info("sign-up refused", "reason", "rate_limited", "address", addr, "wait_ms", wait.Milliseconds())
		p.tooMany(w, r, wait)
		return
	}
	v := page{Invite: r.PostFormValue("invite"), Username: strings.TrimSpace(r.PostFormValue("username")), Next: p.next(r)}
	if r.PostFormValue("password") != r.PostFormValue("password2") {
		log.Info("sign-up refused", "reason", "passwords_differ")
		v.Error = "The two passwords differ."
		p.render(w, r, http.StatusUnprocessableEntity, "signup", signUpPage(v))
		return
	}
	_, started, err := p.opts.Accounts.SignUp(ctx, p.by(r), account.SignUp{
		Invite:           v.Invite,
		RegistrationOpen: p.registrationOpen(),
		Username:         v.Username,
		Password:         r.PostFormValue("password"),
		UserAgent:        userAgent(r),
	})
	var invalid *account.InvalidError
	switch {
	case errors.Is(err, account.ErrInvalidInvite):
		p.wrongToken(r)
		p.inviteRefused(w, r)
	case errors.Is(err, account.ErrRegistrationClosed):
		p.registrationClosed(w, r)
	case errors.Is(err, account.ErrUsernameTaken):
		v.Error = "That username is taken: choose another."
		p.render(w, r, http.StatusConflict, "signup", signUpPage(v))
	case errors.As(err, &invalid):
		log.Info("sign-up refused", "reason", "invalid", "field", invalid.Field)
		v.Error = sentence(invalid.Error())
		p.render(w, r, http.StatusUnprocessableEntity, "signup", signUpPage(v))
	case err != nil:
		p.failed(w, r, err)
	default:
		setSession(w, started.Token)
		redirect(w, r, withNext("/enrol", v.Next))
	}
}

// sentence is a message of the store's as a sentence.
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

// keyPage shows key, the QR code and the text of the authenticator being
// enrolled, with the form taking a code from it to action.
func keyPage(v page, key *otp.Key, action string) (page, error) {
	img, err := key.Image(200, 200)
	if err != nil {
		return page{}, err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return page{}, err
	}
	// The image is the hub's own, made just now: safe as a URL.
	v.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())) //nolint:gosec // see above
	v.Secret = groups(key.Secret())
	v.Action = action
	return v, nil
}

// groups is s in groups of four, as a person copies it.
func groups(s string) string {
	var b strings.Builder
	for i, c := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// enrolment shows the key being enrolled, starting one when there is none.
func (p *pages) enrolment(w http.ResponseWriter, r *http.Request, v visitor, status int, shown page) {
	ctx := r.Context()
	key, err := p.opts.Accounts.EnrolmentKey(ctx, v.user.ID)
	if errors.Is(err, account.ErrNoEnrolment) {
		key, err = p.opts.Accounts.EnrolTOTP(ctx, v.user.ID)
	}
	if err != nil {
		p.failed(w, r, err)
		return
	}
	shown.Title = "Set up your authenticator"
	shown, err = keyPage(shown, key, "/enrol")
	if err != nil {
		p.failed(w, r, err)
		return
	}
	p.render(w, r, status, "enrol", shown)
}

func (p *pages) enrolForm(w http.ResponseWriter, r *http.Request) {
	v, ok := p.at(w, r, "/enrol")
	if !ok {
		return
	}
	p.enrolment(w, r, v, http.StatusOK, page{Next: p.next(r)})
}

func (p *pages) enrol(w http.ResponseWriter, r *http.Request) {
	v, ok := p.at(w, r, "/enrol")
	if !ok || p.limited(w, r, v.user.Username, "enrolment refused") {
		return
	}
	ctx := r.Context()
	next := p.next(r)
	c, err := p.opts.Accounts.ConfirmTOTP(ctx, p.by(r), v.user.ID, v.session.ID, r.PostFormValue("code"))
	switch {
	case errors.Is(err, account.ErrWrongCode):
		p.wrong(r, v.user.Username)
		logging.FromContext(ctx).Info("enrolment refused", "reason", "code", "user_id", v.user.ID)
		p.enrolment(w, r, v, http.StatusUnprocessableEntity, page{Next: next, Error: "That code is wrong: type the one your app shows now."})
	case errors.Is(err, account.ErrNoEnrolment):
		redirect(w, r, withNext("/enrol", next))
	case err != nil:
		p.failed(w, r, err)
	default:
		p.passed(r, v.user.Username)
		setSession(w, c.Token)
		p.render(w, r, http.StatusOK, "codes", page{Title: "Save your recovery codes", Codes: c.RecoveryCodes, Next: next})
	}
}

// codesSaved goes on from the recovery codes once the person says they
// saved them.
func (p *pages) codesSaved(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.at(w, r, ""); !ok {
		return
	}
	next := p.next(r)
	if r.PostFormValue("saved") == "" {
		logging.FromContext(r.Context()).Info("recovery codes not confirmed saved")
		p.message(w, r, http.StatusUnprocessableEntity, page{
			Title:    "Tick “I have saved them” to go on",
			Text:     "The codes are shown only once. If you did not save them, make new ones on the Security page.",
			Link:     home,
			LinkText: "Go to the Security page",
		})
		return
	}
	redirect(w, r, done(next))
}
