package hublink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// Flow is how a login is approved.
type Flow string

const (
	// FlowBrowser is approved in a browser on this machine, which brings the
	// authorization code back to the CLI's callback on 127.0.0.1.
	FlowBrowser Flow = "browser"
	// FlowDevice is approved by typing a code on the hub's device page, in
	// any browser, while the link polls.
	FlowDevice Flow = "device"
)

// slowDownStep is how much a device login's interval grows at each
// slow_down, as RFC 8628 has it.
const slowDownStep = 5 * time.Second

// LoginRequest is a login to start.
type LoginRequest struct {
	// URL is the hub's web endpoint: https, or http on localhost or
	// 127.0.0.1.
	URL string
	// Name is the node's name in the account.
	Name string
	Flow Flow
	// Callback is, for FlowBrowser, where the hub sends the browser back
	// to: http://127.0.0.1:<port>/<path>, the CLI's.
	Callback string
}

// LoginPhase is where a login is.
type LoginPhase string

const (
	LoginPending  LoginPhase = "pending"
	LoginLoggedIn LoginPhase = "logged_in"
	LoginFailed   LoginPhase = "failed"
)

// ProblemCode says why a login failed.
type ProblemCode string

const (
	ProblemDenied      ProblemCode = "login_denied"
	ProblemExpired     ProblemCode = "login_expired"
	ProblemCancelled   ProblemCode = "login_cancelled"
	ProblemNameTaken   ProblemCode = "name_taken"
	ProblemUnreachable ProblemCode = "hub_unreachable"
	// ProblemInternal is anything else: the hub's answer made no sense, or
	// the files could not be written.
	ProblemInternal ProblemCode = "internal"
)

// LoginProblem is why a login failed.
type LoginProblem struct {
	Code   ProblemCode
	Detail string
}

func (p *LoginProblem) Error() string { return p.Detail + " [" + string(p.Code) + "]" }

// LoginState is a login's state: pending, then logged in with the link's
// status, or failed with its problem.
type LoginState struct {
	Phase   LoginPhase
	Status  Status
	Problem *LoginProblem
}

// Login is a login pending, until it ends. Its UserCode is a secret, as are
// the key and the codes it keeps: none is ever logged.
type Login struct {
	Flow      Flow
	ExpiresAt time.Time
	// LoginURL is, for FlowBrowser, the approval page to open.
	LoginURL string
	// VerificationURL and UserCode are, for FlowDevice, the page to type
	// the code on and the code.
	VerificationURL, UserCode string

	link     *Link
	url      string
	id       string // the login's id, the state the callback brings back
	key      ed25519.PrivateKey
	verifier string
	device   string
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc

	mu      sync.Mutex
	state   LoginState
	changed chan struct{}
	// exchanging is a callback's code being exchanged.
	exchanging bool
}

// Watch is the login's state now and a channel closed when it changes.
func (g *Login) Watch() (LoginState, <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state, g.changed
}

// State is the login's state now.
func (g *Login) State() LoginState {
	s, _ := g.Watch()
	return s
}

// Cancel ends the login, unless it ended, with login_cancelled.
func (g *Login) Cancel() {
	if g.end(LoginState{Phase: LoginFailed, Problem: &LoginProblem{Code: ProblemCancelled, Detail: "the login was cancelled"}}) {
		logging.FromContext(g.ctx).Info("login cancelled", "flow", string(g.Flow))
	}
}

// end ends the login in s, reporting whether it was pending.
func (g *Login) end(s LoginState) bool {
	g.mu.Lock()
	if g.state.Phase != LoginPending {
		g.mu.Unlock()
		return false
	}
	g.state = s
	g.closeChanged()
	g.mu.Unlock()
	g.cancel()
	g.link.mu.Lock()
	if g.link.pending == g {
		g.link.pending = nil
	}
	g.link.mu.Unlock()
	return true
}

func (g *Login) fail(code ProblemCode, detail string) {
	if g.end(LoginState{Phase: LoginFailed, Problem: &LoginProblem{Code: code, Detail: detail}}) {
		logging.FromContext(g.ctx).Info("login failed", "flow", string(g.Flow), "code", string(code), "detail", detail)
	}
}

// Pending is the login pending, nil when there is none.
func (l *Link) Pending() *Login {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pending
}

// CheckURL is the hub's address raw as the link uses it, or a *URLError:
// https, or plain http to localhost or 127.0.0.1 only, with no path.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", &URLError{URL: raw, Reason: "it must be https://<host>[:<port>]"}
	}
	switch u.Scheme {
	case "https":
	case "http":
		if host := u.Hostname(); host != "localhost" && host != "127.0.0.1" {
			return "", &URLError{URL: raw, Reason: "plain http only to localhost or 127.0.0.1: use https"}
		}
	default:
		return "", &URLError{URL: raw, Reason: "it must be https://<host>[:<port>]"}
	}
	return u.Scheme + "://" + u.Host, nil
}

// webClient is the hub's API on its web endpoint at hubURL.
func (l *Link) webClient(hubURL string) (*hubapi.ClientWithResponses, error) {
	return hubapi.NewClientWithResponses(hubURL, hubapi.WithHTTPClient(l.opts.HTTPClient), hubapi.WithRequestEditorFn(versionHeader))
}

func versionHeader(_ context.Context, req *http.Request) error {
	req.Header.Set(hubapi.VersionHeader, identity.Version)
	return nil
}

// refused is the problem of a hub's response that is not the one wanted,
// or an *UnreachableError when it is no hub's problem.
func refused(hubURL string, status int, p *hubapi.Problem) error {
	if p == nil || p.Code == "" {
		return &UnreachableError{URL: hubURL, Err: errors.New("it answered " + http.StatusText(status) + ", not as a hub does")}
	}
	return &HubError{Problem: *p}
}

// StartLogin starts a login, which ends any login pending with
// login_cancelled. It returns once the hub has the login, failing with
// ErrAlreadyLoggedIn, a *URLError, an *UnreachableError or a *HubError; then
// the Login says how it goes on.
func (l *Link) StartLogin(ctx context.Context, req LoginRequest) (*Login, error) {
	log := logging.FromContext(ctx)
	hubURL, err := CheckURL(req.URL)
	if err != nil {
		return nil, err
	}
	l.starting.Lock()
	defer l.starting.Unlock()
	if s := l.Status(); s.LoggedIn() {
		return nil, ErrAlreadyLoggedIn
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	body := hubapi.StartNodeLogin{Flow: hubapi.NodeLoginFlow(req.Flow), Name: req.Name, Os: osName(), WdxVersion: identity.Version, PublicKey: pub}
	var verifier string
	if req.Flow == FlowBrowser {
		verifier = rand.Text() + rand.Text()
		sum := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(sum[:])
		body.PkceChallenge, body.Callback = &challenge, &req.Callback
	}
	web, err := l.webClient(hubURL)
	if err != nil {
		return nil, err
	}
	r, err := web.StartNodeLoginWithResponse(ctx, body)
	if err != nil {
		return nil, &UnreachableError{URL: hubURL, Err: err}
	}
	started := r.JSON201
	if started == nil {
		err := refused(hubURL, r.StatusCode(), r.ApplicationproblemJSONDefault)
		log.Info("login refused", "url", hubURL, "flow", string(req.Flow), "err", err)
		return nil, err
	}
	g := &Login{Flow: req.Flow, ExpiresAt: started.ExpiresAt, link: l, url: hubURL, key: key, verifier: verifier, state: LoginState{Phase: LoginPending}, changed: make(chan struct{})}
	switch req.Flow {
	case FlowBrowser:
		if started.LoginUrl == nil {
			return nil, &UnreachableError{URL: hubURL, Err: errors.New("no login_url in its answer")}
		}
		g.LoginURL = *started.LoginUrl
		if u, err := url.Parse(g.LoginURL); err == nil {
			g.id = u.Query().Get("login")
		}
	case FlowDevice:
		if started.DeviceCode == nil || started.UserCode == nil || started.VerificationUrl == nil || started.Interval == nil {
			return nil, &UnreachableError{URL: hubURL, Err: errors.New("no device code in its answer")}
		}
		g.device, g.UserCode, g.VerificationURL = *started.DeviceCode, *started.UserCode, *started.VerificationUrl
		g.interval = time.Duration(*started.Interval) * time.Second
	}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, ErrClosed
	}
	earlier := l.pending
	l.pending = g
	// The login outlives the request that started it, not the link.
	g.ctx, g.cancel = context.WithCancel(logging.NewContext(l.ctx, log))
	l.finished.Add(1)
	l.mu.Unlock()
	if earlier != nil {
		earlier.end(LoginState{Phase: LoginFailed, Problem: &LoginProblem{Code: ProblemCancelled, Detail: "another login replaced it"}})
		log.Info("login replaced", "flow", string(earlier.Flow))
	}
	log.Info("login started", "url", hubURL, "flow", string(req.Flow), "name", req.Name, "expires_at", g.ExpiresAt)
	go func() {
		defer l.finished.Done()
		if g.Flow == FlowDevice {
			g.poll()
		} else {
			g.expire()
		}
	}()
	return g, nil
}

// until is the wait until the login expires.
func (g *Login) until() time.Duration {
	return g.ExpiresAt.Sub(g.link.opts.Clock.Now())
}

// expire ends a browser login with login_expired when it is not done in
// time.
func (g *Login) expire() {
	select {
	case <-g.ctx.Done():
	case <-g.link.opts.Clock.After(g.until()):
		g.fail(ProblemExpired, "the login was not approved in time")
	}
}

// poll polls the hub for the device login, at the hub's interval, until it
// is approved, refused or expired. A hub that cannot be reached is polled
// again; one that could not be reached when the login expired is what it
// fails with.
func (g *Login) poll() {
	log := logging.FromContext(g.ctx)
	web, err := g.link.webClient(g.url)
	if err != nil {
		g.fail(ProblemInternal, err.Error())
		return
	}
	var unreachable error
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-g.link.opts.Clock.After(min(g.interval, max(g.until(), 0))):
		}
		if g.until() <= 0 {
			if unreachable != nil {
				g.fail(ProblemUnreachable, unreachable.Error())
			} else {
				g.fail(ProblemExpired, "the login was not approved in time")
			}
			return
		}
		r, err := web.PollNodeLoginWithResponse(g.ctx, hubapi.PollNodeLogin{DeviceCode: g.device})
		if g.ctx.Err() != nil {
			return
		}
		if err != nil {
			unreachable = &UnreachableError{URL: g.url, Err: err}
			log.Info("login poll failed", "err", unreachable)
			continue
		}
		unreachable = nil
		if r.JSON200 != nil {
			g.finish(r.JSON200)
			return
		}
		err = refused(g.url, r.StatusCode(), r.ApplicationproblemJSONDefault)
		switch Code(err) {
		case hubapi.ErrorCodeAuthorizationPending:
			continue
		case hubapi.ErrorCodeSlowDown:
			g.interval += slowDownStep
			log.Info("login polling slowed down", "interval_ms", g.interval.Milliseconds())
			continue
		}
		g.refused(err)
		return
	}
}

// refused ends the login with what the hub's refusal err means for it.
func (g *Login) refused(err error) {
	if _, ok := errors.AsType[*UnreachableError](err); ok {
		g.fail(ProblemUnreachable, err.Error())
		return
	}
	switch Code(err) {
	case hubapi.ErrorCodeAccessDenied:
		g.fail(ProblemDenied, "the login was denied")
	case hubapi.ErrorCodeAccountDisabled:
		g.fail(ProblemDenied, "the account is disabled")
	case hubapi.ErrorCodeExpiredToken, hubapi.ErrorCodeNotFound:
		g.fail(ProblemExpired, "the login expired, or was finished already")
	case hubapi.ErrorCodeNameTaken:
		g.fail(ProblemNameTaken, "the account has a node of that name already")
	default:
		g.fail(ProblemInternal, err.Error())
	}
}

// Callback is what the hub sent the browser back to the callback with: the
// authorization code, or the error, and the state.
type Callback struct {
	Code, State, Error string
}

var (
	// ErrNotBrowser is a callback given to a device login.
	ErrNotBrowser = errors.New("the login is not a browser login")
	// ErrWrongState is a callback whose state is not the login's: it came
	// from another login, or from someone else.
	ErrWrongState = errors.New("the callback's state is not the login's")
	// ErrLoginEnded is a callback given to a login that ended, or whose
	// code is being exchanged already.
	ErrLoginEnded = errors.New("the login is not pending")
)

// Callback finishes a browser login with what its callback received: the
// code is exchanged for the node's credentials, or the error ends it. It
// fails, leaving the login pending, when cb is not the login's; the login's
// state says the rest.
func (g *Login) Callback(ctx context.Context, cb Callback) error {
	log := logging.FromContext(ctx)
	if g.Flow != FlowBrowser {
		return ErrNotBrowser
	}
	if cb.State == "" || cb.State != g.id {
		log.Info("login callback refused", "reason", "state")
		return ErrWrongState
	}
	g.mu.Lock()
	if g.state.Phase != LoginPending || g.exchanging {
		g.mu.Unlock()
		return ErrLoginEnded
	}
	g.exchanging = true
	g.mu.Unlock()
	if cb.Error != "" {
		if cb.Error == string(hubapi.ErrorCodeAccessDenied) {
			g.fail(ProblemDenied, "the login was denied")
		} else {
			g.fail(ProblemInternal, "the hub sent the browser back with the error "+cb.Error)
		}
		return nil
	}
	web, err := g.link.webClient(g.url)
	if err != nil {
		g.fail(ProblemInternal, err.Error())
		return nil
	}
	r, err := web.ExchangeNodeLoginWithResponse(g.ctx, hubapi.ExchangeNodeLogin{Code: cb.Code, PkceVerifier: g.verifier})
	switch {
	case g.ctx.Err() != nil:
		// Cancelled meanwhile.
	case err != nil:
		g.fail(ProblemUnreachable, (&UnreachableError{URL: g.url, Err: err}).Error())
	case r.JSON200 != nil:
		g.finish(r.JSON200)
	default:
		g.refused(refused(g.url, r.StatusCode(), r.ApplicationproblemJSONDefault))
	}
	return nil
}

// finish makes the node logged in with creds: it checks them, writes them
// and starts the control connection.
func (g *Login) finish(hc *hubapi.NodeCredentials) {
	log := logging.FromContext(g.ctx)
	c := &credentials{URL: g.url, NodesURL: strings.TrimRight(hc.NodesUrl, "/"), User: hc.User, Node: NodeRef{ID: hc.Node.Id, Name: hc.Node.Name}, Key: g.key}
	if err := c.check([]byte(hc.Certificate), []byte(hc.CaCertificate)); err != nil {
		g.fail(ProblemInternal, "the hub's credentials cannot be used: "+err.Error())
		return
	}
	l := g.link
	l.mu.Lock()
	defer l.mu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	// Ended meanwhile, by a cancel or by the link closing, which would not
	// wait for a control connection started now.
	if g.state.Phase != LoginPending || l.pending != g || l.closed {
		return
	}
	l.pending = nil
	defer g.cancel()
	if err := c.write(g.ctx, l.dir); err != nil {
		log.Error("hub login not written", "path", l.dir, "err", err)
		g.state = LoginState{Phase: LoginFailed, Problem: &LoginProblem{Code: ProblemInternal, Detail: "the login could not be written: " + err.Error()}}
		g.closeChanged()
		return
	}
	log.Info("login approved", "flow", string(g.Flow), "user", c.User, "node_id", c.Node.ID, "name", c.Node.Name)
	l.loggedIn(c)
	g.state = LoginState{Phase: LoginLoggedIn, Status: l.status()}
	g.closeChanged()
}

// closeChanged wakes those watching the login; g.mu is held.
func (g *Login) closeChanged() {
	close(g.changed)
	g.changed = make(chan struct{})
}
