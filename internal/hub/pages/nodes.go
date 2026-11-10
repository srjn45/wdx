package pages

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/logging"
)

// freshFactor is how recently a browser's second factor must have passed
// for it to approve a node: a browser signed in a week ago logs in again to
// add a machine.
const freshFactor = 10 * time.Minute

// approval is what the approval page shows of a login, and the hidden field
// that names it: the login's id for the browser flow, the user code for the
// device flow, so that a device login can be approved only by whoever typed
// its code.
type approval struct {
	ID, UserCode               string
	Name, OS, Version, Address string
	Started                    string
	callback                   string // the browser flow's
}

func approvalOf(l node.Login, userCode string) *approval {
	a := &approval{Name: l.Name, OS: l.OS, Version: l.Version, Address: l.Address, Started: when(l.CreatedAt), callback: l.Callback}
	if l.Flow == node.FlowDevice {
		a.UserCode = userCode
	} else {
		a.ID = l.ID
	}
	return a
}

// factorFresh reports whether v passed their second factor within
// freshFactor.
func (p *pages) factorFresh(v visitor) bool {
	return p.opts.Now().Sub(v.session.SecondFactor) <= freshFactor
}

// fresh reports whether v passed their second factor within freshFactor;
// otherwise it sends the browser through the login, back to next. A post
// signs it out first; a GET, which another site can make the browser send,
// keeps the session, which the login replaces once it passes.
func (p *pages) fresh(w http.ResponseWriter, r *http.Request, v visitor, next string) bool {
	if p.factorFresh(v) {
		return true
	}
	ctx := r.Context()
	logging.FromContext(ctx).Info("page needs a fresh login", "path", r.URL.Path, "user_id", v.user.ID, "second_factor_at", v.session.SecondFactor)
	if r.Method != http.MethodGet {
		if err := p.opts.Accounts.EndSession(ctx, p.by(r), v.user.ID, v.session.ID); err != nil && !errors.Is(err, account.ErrNoSession) {
			p.failed(w, r, err)
			return false
		}
		clearSession(w)
	}
	redirect(w, r, withNext("/login", next))
	return false
}

func (p *pages) deviceForm(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok || !p.fresh(w, r, v, "/device") {
		return
	}
	p.render(w, r, http.StatusOK, "device", page{Title: "Add a machine"})
}

func (p *pages) device(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	// Every code typed counts against the address, as a password does.
	if !ok || !p.fresh(w, r, v, "/device") || p.limited(w, r, "", "device code refused") {
		return
	}
	ctx := r.Context()
	code := r.PostFormValue("code")
	l, err := p.opts.Nodes.LoginByUserCode(ctx, code)
	switch {
	case errors.Is(err, node.ErrLoginNotFound):
		logging.FromContext(ctx).Info("device code refused", "reason", "unknown", "user_id", v.user.ID)
		p.render(w, r, http.StatusUnprocessableEntity, "device", page{Title: "Add a machine", Error: "That code is unknown, or was used already. Check it against your terminal."})
	case errors.Is(err, node.ErrLoginExpired):
		p.expired(w, r)
	case err != nil:
		p.failed(w, r, err)
	case l.Status != node.StatusPending:
		p.done(w, r, l)
	default:
		p.approvalPage(w, r, http.StatusOK, approvalOf(l, node.NormalUserCode(code)), v, "")
	}
}

// approvalPage shows the approval of a, telling when the account has a node
// of its name already, with status and, when not empty, an error.
func (p *pages) approvalPage(w http.ResponseWriter, r *http.Request, status int, a *approval, v visitor, errText string) {
	if errText == "" {
		taken, err := p.opts.Nodes.NameTaken(r.Context(), v.user.ID, a.Name)
		if err != nil {
			p.failed(w, r, err)
			return
		}
		if taken {
			errText = "Your account has a node named " + a.Name + " already: choose another name."
		}
	}
	if a.callback != "" {
		// Approving sends the browser on to the node's callback: the
		// form's redirect must be allowed to go there.
		u, _ := url.Parse(a.callback) // checked when the login started
		w.Header().Set("Content-Security-Policy", strings.Replace(contentSecurity, "form-action 'self'", "form-action 'self' "+u.Scheme+"://"+u.Host, 1))
	}
	p.render(w, r, status, "approve", page{Title: "Add this machine to your account?", Login: a, Error: errText})
}

func (p *pages) approveForm(w http.ResponseWriter, r *http.Request) {
	v, ok := p.loggedIn(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("login")
	if !p.fresh(w, r, v, "/nodes/approve?login="+url.QueryEscape(id)) {
		return
	}
	l, err := p.opts.Nodes.Login(r.Context(), id)
	switch {
	case errors.Is(err, node.ErrLoginNotFound):
		// An unknown login counts against the address, as a wrong code
		// does: its id is the browser flow's secret while it waits.
		if p.limited(w, r, "", "approval refused") {
			return
		}
		p.message(w, r, http.StatusNotFound, page{Title: "No such login", Text: "Run wdx hub login again."})
	case errors.Is(err, node.ErrLoginExpired):
		p.expired(w, r)
	case err != nil:
		p.failed(w, r, err)
	case l.Flow == node.FlowDevice:
		// A device login is approved only by typing its code.
		logging.FromContext(r.Context()).Info("approval refused", "login_id", id, "reason", "a device login without its code")
		p.message(w, r, http.StatusNotFound, page{Title: "Type the code instead", Text: "This machine logs in with a code: type the one its terminal shows.", Link: "/device", LinkText: "Type the code"})
	case l.Status != node.StatusPending:
		p.done(w, r, l)
	default:
		p.approvalPage(w, r, http.StatusOK, approvalOf(l, ""), v, "")
	}
}

// posted returns the login a posted approval or denial names, by its id for
// the browser flow or its user code for the device flow, answering the
// browser itself when it cannot be decided.
func (p *pages) posted(w http.ResponseWriter, r *http.Request) (node.Login, *approval, visitor, bool) {
	v, ok := p.loggedIn(w, r)
	if !ok {
		return node.Login{}, nil, visitor{}, false
	}
	ctx := r.Context()
	id, userCode := r.PostFormValue("login"), r.PostFormValue("user_code")
	next := "/device"
	if userCode == "" {
		next = "/nodes/approve?login=" + url.QueryEscape(id)
	}
	if !p.fresh(w, r, v, next) || p.limited(w, r, "", "approval refused") {
		return node.Login{}, nil, visitor{}, false
	}
	var l node.Login
	var err error
	if userCode != "" {
		l, err = p.opts.Nodes.LoginByUserCode(ctx, userCode)
	} else if l, err = p.opts.Nodes.Login(ctx, id); err == nil && l.Flow == node.FlowDevice {
		logging.FromContext(ctx).Info("approval refused", "login_id", id, "reason", "a device login without its code")
		err = node.ErrLoginNotFound
	}
	switch {
	case errors.Is(err, node.ErrLoginNotFound):
		p.message(w, r, http.StatusNotFound, page{Title: "No such login", Text: "Run wdx hub login again."})
	case errors.Is(err, node.ErrLoginExpired):
		p.expired(w, r)
	case err != nil:
		p.failed(w, r, err)
	default:
		return l, approvalOf(l, node.NormalUserCode(userCode)), v, true
	}
	return node.Login{}, nil, visitor{}, false
}

func (p *pages) approve(w http.ResponseWriter, r *http.Request) {
	l, a, v, ok := p.posted(w, r)
	if !ok {
		return
	}
	a.Name = strings.TrimSpace(r.PostFormValue("name"))
	approved, err := p.opts.Nodes.Approve(r.Context(), p.nodeBy(r), v.user.ID, l.ID, a.Name)
	var invalid *node.InvalidError
	switch {
	case errors.Is(err, node.ErrNameTaken):
		p.approvalPage(w, r, http.StatusConflict, a, v, "Your account has a node named "+a.Name+" already: choose another name.")
	case errors.As(err, &invalid):
		p.approvalPage(w, r, http.StatusUnprocessableEntity, a, v, "The name "+invalid.Reason+".")
	case errors.Is(err, node.ErrLoginDone):
		p.done(w, r, l)
	case errors.Is(err, node.ErrLoginExpired):
		p.expired(w, r)
	case err != nil:
		p.failed(w, r, err)
	case l.Flow == node.FlowBrowser:
		redirect(w, r, callbackURL(l, url.Values{"code": {approved.Code}}))
	default:
		p.message(w, r, http.StatusOK, page{Title: "Machine added", Text: "wdx on " + a.Name + " is a node of your account now: go back to your terminal."})
	}
}

func (p *pages) deny(w http.ResponseWriter, r *http.Request) {
	l, _, v, ok := p.posted(w, r)
	if !ok {
		return
	}
	err := p.opts.Nodes.Deny(r.Context(), p.nodeBy(r), v.user.ID, l.ID)
	switch {
	case errors.Is(err, node.ErrLoginDone):
		p.done(w, r, l)
	case errors.Is(err, node.ErrLoginExpired):
		p.expired(w, r)
	case err != nil:
		p.failed(w, r, err)
	case l.Flow == node.FlowBrowser:
		// The node's terminal learns at once, rather than when the login
		// expires.
		redirect(w, r, callbackURL(l, url.Values{"error": {"access_denied"}}))
	default:
		p.denied(w, r, l)
	}
}

// callbackURL is the browser login's callback with values and its state, the
// login's id.
func callbackURL(l node.Login, values url.Values) string {
	values.Set("state", l.ID)
	return l.Callback + "?" + values.Encode()
}

// nodeBy is the user acting on a node login from the browser of r.
func (p *pages) nodeBy(r *http.Request) node.By {
	return node.By{Actor: audit.ActorUser, Address: p.address(r)}
}

// done shows the result of a login approved or denied already.
func (p *pages) done(w http.ResponseWriter, r *http.Request, l node.Login) {
	if l.Status == node.StatusDenied {
		p.denied(w, r, l)
		return
	}
	p.message(w, r, http.StatusConflict, page{Title: "Machine added already", Text: "wdx on " + l.Name + " was approved already: go back to its terminal."})
}

func (p *pages) denied(w http.ResponseWriter, r *http.Request, l node.Login) {
	p.message(w, r, http.StatusOK, page{Title: "Machine not added", Text: "wdx on " + l.Name + " was denied: it is not a node of your account."})
}

func (p *pages) expired(w http.ResponseWriter, r *http.Request) {
	p.message(w, r, http.StatusGone, page{Title: "This login has expired", Text: "A machine has 10 minutes to be approved: run wdx hub login again."})
}
