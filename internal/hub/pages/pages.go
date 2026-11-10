// Package pages serves the hub's sign-in pages on the web endpoint: signing
// up from an invite, enrolling an authenticator, the recovery codes, logging
// in with the second factor, the password reset, the Security page, and the
// device page and the approval of a node with its result. They
// are plain HTML from embedded templates and one stylesheet, with no
// JavaScript; every page is behind the cross-origin check and the security
// headers, and the tries are rate limited (the wdx doc HUB_ACCOUNTS).
package pages

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/logging"
)

// Options configure the pages.
type Options struct {
	Accounts *account.Store
	Nodes    *node.Store
	// PublicURL is where browsers reach the hub: its origin is trusted by
	// the cross-origin check, and https adds Strict-Transport-Security.
	PublicURL string
	// TrustedProxies are the addresses whose X-Forwarded-For is believed.
	TrustedProxies []netip.Prefix
	// Registration says, at each request, whether anyone may sign up
	// without an invite: hubconfig.RegistrationOpen or RegistrationClosed.
	Registration func() string
	// Now is the clock of the rate limits; time.Now when nil.
	Now func() time.Time
}

// cookieName is the browser session's cookie.
const cookieName = "wdx_hub_session"

// home is where a logged-in browser goes when nothing else asked.
const home = "/security"

// maxForm is the most a page's form may send.
const maxForm = 64 << 10

// contentSecurity lets a page load nothing but the hub's stylesheet and the
// QR code inline: no script runs at all.
const contentSecurity = "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

//go:embed templates/*.html style.css
var files embed.FS

// views are the pages' templates by name, each on the layout.
var views = func() map[string]*template.Template {
	layout := template.Must(template.ParseFS(files, "templates/layout.html"))
	names, err := fs.Glob(files, "templates/*.html")
	if err != nil {
		panic(err)
	}
	views := map[string]*template.Template{}
	for _, path := range names {
		name := strings.TrimSuffix(strings.TrimPrefix(path, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		views[name] = template.Must(template.Must(layout.Clone()).ParseFS(files, path))
	}
	return views
}()

var style = func() []byte {
	b, err := files.ReadFile("style.css")
	if err != nil {
		panic(err)
	}
	return b
}()

type pages struct {
	opts   Options
	limits *limits
	https  bool
}

// Pages are the pages' handler, and their protections that the hub's API on
// the web endpoint uses too.
type Pages struct {
	http.Handler
	p *pages
}

// Address is the client's address of r, as the pages know it: the
// connection's, or the one a trusted proxy forwarded.
func (h *Pages) Address(r *http.Request) string { return h.p.address(r) }

// AllowLoginStart counts a node login started from address, and returns how
// long address must wait when it has started too many, 0 when it may.
func (h *Pages) AllowLoginStart(address string) time.Duration {
	return h.p.limits.loginStart(address)
}

// New returns the handler of the pages.
func New(opts Options) (*Pages, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	u, err := url.Parse(opts.PublicURL)
	if err != nil {
		return nil, err
	}
	p := &pages{opts: opts, limits: newLimits(opts.Now), https: u.Scheme == "https"}
	mux := http.NewServeMux()
	p.routes(mux)
	cross := http.NewCrossOriginProtection()
	if err := cross.AddTrustedOrigin(u.Scheme + "://" + u.Host); err != nil {
		return nil, err
	}
	cross.SetDenyHandler(http.HandlerFunc(p.crossOrigin))
	return &Pages{Handler: p.headers(cross.Handler(mux)), p: p}, nil
}

func (p *pages) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", p.home)
	mux.HandleFunc("GET /style.css", p.style)

	mux.HandleFunc("GET /signup", p.signUpForm)
	mux.HandleFunc("POST /signup", p.signUp)
	mux.HandleFunc("GET /enrol", p.enrolForm)
	mux.HandleFunc("POST /enrol", p.enrol)
	mux.HandleFunc("POST /recovery-codes/saved", p.codesSaved)

	mux.HandleFunc("GET /login", p.loginForm)
	mux.HandleFunc("POST /login", p.login)
	mux.HandleFunc("GET /login/second-factor", p.secondFactorForm)
	mux.HandleFunc("POST /login/second-factor", p.secondFactor)
	mux.HandleFunc("GET /login/recovery-code", p.recoveryCodeForm)
	mux.HandleFunc("POST /login/recovery-code", p.recoveryCode)
	mux.HandleFunc("POST /logout", p.logout)

	mux.HandleFunc("GET /reset", p.resetForm)
	mux.HandleFunc("POST /reset", p.reset)

	mux.HandleFunc("GET /security", p.security)
	mux.HandleFunc("POST /security/password", p.changePassword)
	mux.HandleFunc("POST /security/authenticator", p.replaceAuthenticator)
	mux.HandleFunc("GET /security/authenticator", p.replacementForm)
	mux.HandleFunc("POST /security/authenticator/confirm", p.confirmReplacement)
	mux.HandleFunc("POST /security/recovery-codes", p.renewCodes)
	mux.HandleFunc("POST /security/sessions/end", p.endSession)

	// Each starts with p.loggedIn, as the Security page does, so that a
	// next of theirs leads through the login and back.
	mux.HandleFunc("GET /device", p.deviceForm)
	mux.HandleFunc("POST /device", p.device)
	mux.HandleFunc("GET /nodes/approve", p.approveForm)
	mux.HandleFunc("POST /nodes/approve", p.approve)
	mux.HandleFunc("POST /nodes/deny", p.deny)

	mux.HandleFunc("/", p.notFound)
}

// headers sets the security headers of every page and bounds its form.
func (p *pages) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurity)
		// Invite and reset links carry their token.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		if p.https {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxForm)
		next.ServeHTTP(w, r)
	})
}

func (p *pages) style(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(style)
}

// page is what a template shows; each uses the fields it needs.
type page struct {
	Title  string
	Error  string
	Notice string
	// Next is where the browser goes once the flow is done, carried by
	// every form of it; empty for the Security page.
	Next string

	Username     string
	Invite       string // the token of the invite signed up from
	Token        string // the token of the reset
	SecondFactor bool   // the reset removes the authenticator too

	// Action is where the code of a new authenticator is posted.
	Action string
	QR     template.URL // the authenticator's key as a PNG in a data: URL
	Secret string       // the key as text, in groups of four
	Codes  []string     // recovery codes, shown once
	Left   int          // recovery codes not used

	Sessions []browser

	// Login is the node login the approval page asks about.
	Login *approval

	// A message page's text and its one link.
	Text, Link, LinkText string
}

// browser is a row of the Security page's table of browser sessions.
type browser struct {
	ID, Address, UserAgent string
	Started, Seen          string
	This                   bool
}

// render shows the view name with v, with status.
func (p *pages) render(w http.ResponseWriter, r *http.Request, status int, name string, v page) {
	var b bytes.Buffer
	if err := views[name].ExecuteTemplate(&b, "layout", v); err != nil {
		logging.FromContext(r.Context()).Error("page not rendered", "view", name, "err", err)
		http.Error(w, "the page could not be shown", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

// message shows a page of text with one link on.
func (p *pages) message(w http.ResponseWriter, r *http.Request, status int, v page) {
	p.render(w, r, status, "message", v)
}

// failed shows that something the person could not help went wrong, logging
// what.
// failed answers a request that failed, but for too many passwords waiting
// to be checked, which is answered as a rate limit is.
func (p *pages) failed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, account.ErrBusy) {
		w.Header().Set("Retry-After", "1")
		p.message(w, r, http.StatusTooManyRequests, page{Title: "Too many tries", Text: "Too many tries at once: try again in a moment."})
		return
	}
	logging.FromContext(r.Context()).Error("page failed", "err", err)
	p.message(w, r, http.StatusInternalServerError, page{Title: "Something went wrong", Text: "The hub could not do this. Try again; if it goes on, tell the hub's operator.", Link: "/", LinkText: "Start again"})
}

func (p *pages) notFound(w http.ResponseWriter, r *http.Request) {
	p.message(w, r, http.StatusNotFound, page{Title: "No such page", Link: "/", LinkText: "Go to the hub"})
}

func (p *pages) crossOrigin(w http.ResponseWriter, r *http.Request) {
	logging.FromContext(r.Context()).Info("request refused", "reason", "cross_origin", "address", p.address(r))
	p.message(w, r, http.StatusForbidden, page{Title: "Request refused", Text: "It came from another site.", Link: "/", LinkText: "Go to the hub"})
}

// tooMany refuses a try a rate limit makes wait, saying how long.
func (p *pages) tooMany(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	seconds := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	p.message(w, r, http.StatusTooManyRequests, page{Title: "Too many tries", Text: "Wait " + waitText(seconds) + " and try again."})
}

func waitText(seconds int) string {
	switch {
	case seconds == 1:
		return "1 second"
	case seconds < 120:
		return strconv.Itoa(seconds) + " seconds"
	default:
		return strconv.Itoa((seconds+59)/60) + " minutes"
	}
}

// address is the client's, as the audit, the Browsers table and the rate
// limits know it.
func (p *pages) address(r *http.Request) string {
	return clientAddress(r, p.opts.TrustedProxies)
}

// by is the user acting from the browser of r.
func (p *pages) by(r *http.Request) account.By {
	return account.By{Actor: audit.ActorUser, Address: p.address(r)}
}

// userAgent is the browser's name as kept with its session, cut short.
func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	return ua
}

func (p *pages) registrationOpen() bool {
	return p.opts.Registration != nil && p.opts.Registration() == hubconfig.RegistrationOpen
}

// limited refuses the try of username (empty for none) from the client of
// r when a rate limit makes it wait, logging refused with the reason, and
// auditing the first refusal of a username's lockout from the address;
// otherwise it counts the try against the address, in the same step.
func (p *pages) limited(w http.ResponseWriter, r *http.Request, username, refused string) bool {
	ctx := r.Context()
	addr := p.address(r)
	wait, first := p.limits.try(addr, username)
	if first {
		if err := p.opts.Accounts.RecordLockout(ctx, p.by(r), username); err != nil {
			logging.FromContext(ctx).Error("lockout not recorded", "err", err)
		}
	}
	if wait > 0 {
		logging.FromContext(ctx).Info(refused, "reason", "rate_limited", "address", addr, "wait_ms", wait.Milliseconds())
		p.tooMany(w, r, wait)
		return true
	}
	return false
}

// wrong counts a wrong password or code of username from the client of r,
// auditing the username's wrong tries in a day once they are many.
func (p *pages) wrong(r *http.Request, username string) {
	ctx := r.Context()
	c := p.limits.wrong(username, p.address(r))
	if c.full {
		logging.FromContext(ctx).Warn("wrong try not counted", "reason", "streaks_full", "streaks", maxStreaks)
	}
	if c.attacked {
		if err := p.opts.Accounts.RecordAttack(ctx, p.by(r), username); err != nil {
			logging.FromContext(ctx).Error("attack not recorded", "err", err)
		}
	}
}

// passed ends the streak of username from the client of r.
func (p *pages) passed(r *http.Request, username string) {
	p.limits.passed(username, p.address(r))
}

// wrongToken counts an invite token that is wrong against the address.
func (p *pages) wrongToken(r *http.Request) { p.limits.countTry(p.address(r)) }

// safeNext returns raw when it is a path on the hub, starting with a single
// /, and empty otherwise: next never leads to another site.
func safeNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\t") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return ""
	}
	return raw
}

// next is the next of r, from its query or its form, when it is safe.
func (p *pages) next(r *http.Request) string {
	raw := r.FormValue("next")
	next := safeNext(raw)
	if raw != "" && next == "" {
		logging.FromContext(r.Context()).Info("next refused", "reason", "not a path on the hub")
	}
	return next
}

// withNext is path with next in its query, when there is one.
func withNext(path, next string) string {
	if next == "" {
		return path
	}
	return path + "?next=" + url.QueryEscape(next)
}

// done is where a flow ends: next, or the Security page.
func done(next string) string {
	if next == "" {
		return home
	}
	return next
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// visitor is the user a browser's session is of.
type visitor struct {
	session account.Session
	user    account.User
}

// passed reports whether the session passed its second factor: until then
// it may only pass it, or enrol one.
func (v visitor) passed() bool { return !v.session.SecondFactor.IsZero() }

// visitor returns the visitor of r's session, ok false when it has none
// live.
func (p *pages) visitor(r *http.Request) (v visitor, ok bool, err error) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return visitor{}, false, nil
	}
	ctx := r.Context()
	if v.session, err = p.opts.Accounts.Session(ctx, c.Value); errors.Is(err, account.ErrNoSession) {
		logging.FromContext(ctx).Info("session cookie refused", "reason", "no such live session")
		return visitor{}, false, nil
	} else if err != nil {
		return visitor{}, false, err
	}
	if v.user, err = p.opts.Accounts.User(ctx, v.session.UserID); errors.Is(err, account.ErrNotFound) {
		return visitor{}, false, nil
	} else if err != nil {
		return visitor{}, false, err
	}
	return v, true, nil
}

// step is the page a visitor must go to before anything else: the login,
// the second factor or the enrolment of one; empty once logged in.
func step(v visitor, ok bool) string {
	switch {
	case !ok:
		return "/login"
	case v.passed():
		return ""
	case v.user.SecondFactor == "":
		return "/enrol"
	default:
		return "/login/second-factor"
	}
}

// at returns the visitor of r when the step it is at is want, the empty
// step being logged in; otherwise it sends the browser to its step, or to
// next once logged in, and returns false.
func (p *pages) at(w http.ResponseWriter, r *http.Request, want string) (visitor, bool) {
	v, ok, err := p.visitor(r)
	if err != nil {
		p.failed(w, r, err)
		return visitor{}, false
	}
	at := step(v, ok)
	if at == want {
		return v, true
	}
	next := p.next(r)
	to := withNext(at, next)
	if at == "" {
		to = done(next)
	}
	logging.FromContext(r.Context()).Info("page needs another step", "path", r.URL.Path, "to", strings.SplitN(to, "?", 2)[0])
	redirect(w, r, to)
	return visitor{}, false
}

// loggedIn returns the visitor of r once their session passed its second
// factor; otherwise it sends the browser through the login, back to this
// page.
func (p *pages) loggedIn(w http.ResponseWriter, r *http.Request) (visitor, bool) {
	v, ok, err := p.visitor(r)
	if err != nil {
		p.failed(w, r, err)
		return visitor{}, false
	}
	if at := step(v, ok); at != "" {
		next := ""
		if r.Method == http.MethodGet && r.URL.Path != home {
			next = r.URL.RequestURI()
		}
		logging.FromContext(r.Context()).Info("page needs another step", "path", r.URL.Path, "to", at)
		redirect(w, r, withNext(at, next))
		return visitor{}, false
	}
	return v, true
}

func (p *pages) home(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.loggedIn(w, r); ok {
		redirect(w, r, home)
	}
}
