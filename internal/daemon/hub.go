package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/remote"
)

// HubOptions are the waits of the daemon's link to the hub and of its
// sessions with other nodes, which tests change; zero values take hublink's
// and remote's defaults.
type HubOptions struct {
	// Clock is the time the link goes by: when logins expire, how long it
	// waits between polls and reconnections; the agents' limits go by it too,
	// when a reset passes. The real time when nil.
	Clock interface {
		Now() time.Time
		After(d time.Duration) <-chan time.Time
	}
	MinBackoff, MaxBackoff time.Duration
	// Link are how long the link waits on the hub and other nodes, Remote
	// how long a session waits on the other node.
	Link   hublink.Limits
	Remote remote.Limits
}

// realClock is the time when the daemon's options give none.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (o HubOptions) link() hublink.Options {
	opts := hublink.Options{MinBackoff: o.MinBackoff, MaxBackoff: o.MaxBackoff, Limits: o.Link}
	if o.Clock != nil {
		opts.Clock = o.Clock
	}
	return opts
}

// hubLogins is the login the daemon started last, which its stream, its
// cancel and its code name by the id the daemon gave it. It is kept once it
// ended, so that a stream opened after the end still learns how.
type hubLogins struct {
	// starting is one login started at a time, so that the one kept is the
	// one the link started last.
	starting sync.Mutex
	mu       sync.Mutex
	id       string
	login    *hublink.Login
}

// set makes login, of id, the login kept.
func (h *hubLogins) set(id string, login *hublink.Login) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.id, h.login = id, login
}

// get is the login of id, nil when another replaced it or there is none.
func (h *hubLogins) get(id string) *hublink.Login {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id != h.id {
		return nil
	}
	return h.login
}

// last is the login started last, nil when there is none.
func (h *hubLogins) last() *hublink.Login {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.login
}

// hubError is a failure of the hub's routes, with its code.
type hubError struct {
	code    api.ErrorCode
	err     error
	details map[string]any // nil for none
}

func (e *hubError) Error() string { return e.err.Error() }
func (e *hubError) Unwrap() error { return e.err }

// hubFailure is err, from the link, with the code the API gives it.
func hubFailure(err error) error {
	code := api.ErrorCodeInternal
	switch {
	case errors.Is(err, hublink.ErrNotLoggedIn):
		code = api.ErrorCodeNotLoggedIn
	case errors.Is(err, hublink.ErrAlreadyLoggedIn):
		code = api.ErrorCodeAlreadyLoggedIn
	case errors.Is(err, hublink.ErrNodeIsThis):
		code = api.ErrorCodeNodeIsThis
	default:
		if _, ok := errors.AsType[*hublink.URLError](err); ok {
			code = api.ErrorCodeInvalidArgument
		} else if _, ok := errors.AsType[*hublink.UnreachableError](err); ok {
			code = api.ErrorCodeHubUnreachable
		}
	}
	switch hublink.Code(err) {
	case hubapi.ErrorCodeNotFound, hubapi.ErrorCodeNodeNotFound:
		code = api.ErrorCodeNodeNotFound
	case hubapi.ErrorCodeNodeOffline:
		code = api.ErrorCodeNodeOffline
	case hubapi.ErrorCodeTooManyStreams:
		code = api.ErrorCodeTooManyStreams
	case hubapi.ErrorCodeNameTaken:
		code = api.ErrorCodeNameTaken
	case hubapi.ErrorCodeHubVersionUnsupported, hubapi.ErrorCodeNodeVersionUnsupported:
		// The hub's problem names the side to update, which the hint tells.
		var details map[string]any
		if e, ok := errors.AsType[*hublink.HubError](err); ok && e.Problem.Details != nil {
			if update, ok := (*e.Problem.Details)["update"].(string); ok {
				details = map[string]any{"update": update}
			}
		}
		return &hubError{code: api.ErrorCodeHubVersionUnsupported, err: err, details: details}
	case hubapi.ErrorCodeInvalidArgument:
		code = api.ErrorCodeInvalidArgument
	case hubapi.ErrorCodeNodeRevoked:
		code, err = api.ErrorCodeNotLoggedIn, fmt.Errorf("the hub cut this node off, as it was removed from its account: %w", err)
	case hubapi.ErrorCodeAccountDisabled:
		code, err = api.ErrorCodeNotLoggedIn, fmt.Errorf("the hub cut this node off, as its account is disabled: %w", err)
	}
	return &hubError{code: code, err: err}
}

func (s *server) GetHub(ctx context.Context, _ api.GetHubRequestObject) (api.GetHubResponseObject, error) {
	return api.GetHub200JSONResponse{Hub: s.hubStatusView(ctx, s.hub.Status())}, nil
}

func (s *server) StartHubLogin(ctx context.Context, req api.StartHubLoginRequestObject) (api.StartHubLoginResponseObject, error) {
	b := req.Body
	flow := hublink.Flow(b.Flow)
	callback := deref(b.Callback)
	if flow == hublink.FlowBrowser && callback == "" {
		return nil, &hubError{code: api.ErrorCodeInvalidArgument, err: errors.New("a browser login needs the callback the hub sends the browser back to")}
	}
	s.hubLogins.starting.Lock()
	defer s.hubLogins.starting.Unlock()
	g, err := s.hub.StartLogin(ctx, hublink.LoginRequest{URL: b.Url, Name: b.Name, Flow: flow, Callback: callback})
	if err != nil {
		return nil, hubFailure(err)
	}
	id := "login-" + rand.Text()[:12]
	s.hubLogins.set(id, g)
	logging.FromContext(ctx).Info("hub login kept", "login_id", id, "flow", string(g.Flow))
	v := api.HubLogin{Id: id, Flow: api.HubLoginFlow(g.Flow), ExpiresAt: g.ExpiresAt.UTC()}
	if g.Flow == hublink.FlowBrowser {
		v.LoginUrl = &g.LoginURL
	} else {
		v.VerificationUrl, v.UserCode = &g.VerificationURL, &g.UserCode
	}
	return api.StartHubLogin201JSONResponse{Login: v}, nil
}

func (s *server) StreamHubLogin(ctx context.Context, req api.StreamHubLoginRequestObject) (api.StreamHubLoginResponseObject, error) {
	return &hubLoginStream{ctx: ctx, s: s, id: req.Params.Login, login: s.hubLogins.get(req.Params.Login)}, nil
}

// hubLoginStream sends a login's state, then again at every change, until it
// ends.
type hubLoginStream struct {
	ctx   context.Context
	s     *server
	id    string
	login *hublink.Login // nil when the daemon has it no longer
}

func (st *hubLoginStream) VisitStreamHubLoginResponse(w http.ResponseWriter) error {
	log := logging.FromContext(st.ctx)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(w).Flush
	send := func(e api.HubLoginEvent) bool {
		data, err := json.Marshal(e)
		if err != nil {
			log.Error("login event not encoded", "err", err)
			return false
		}
		if _, err := fmt.Fprintf(w, "event: login\ndata: %s\n\n", data); err != nil {
			return false // the client is gone
		}
		return flush() == nil
	}
	cancelled := func(detail string) {
		send(api.HubLoginEvent{Phase: api.HubLoginEventPhaseFailed, Problem: st.problem(api.ErrorCodeLoginCancelled, detail)})
	}
	if st.login == nil {
		log.Info("login stream ended", "login_id", st.id, "reason", "no such login")
		cancelled("another login replaced it, or the daemon no longer has it")
		return nil
	}
	reason := "client left"
	defer func() { log.Info("login stream ended", "login_id", st.id, "reason", reason) }()
	var last api.HubLoginEventPhase
	for {
		state, changed := st.login.Watch()
		e := st.event(state)
		if e.Phase != last {
			if !send(e) {
				return nil
			}
			last = e.Phase
		}
		if e.Phase != api.HubLoginEventPhasePending {
			reason = "login " + string(e.Phase)
			return nil
		}
		select {
		case <-st.ctx.Done():
			return nil
		case <-st.s.stopping:
			reason = "daemon stopping"
			cancelled("the daemon is stopping")
			return nil
		case <-changed:
		}
	}
}

// event is the login's state as the API sends it.
func (st *hubLoginStream) event(state hublink.LoginState) api.HubLoginEvent {
	return st.s.loginEvent(st.ctx, state)
}

func (st *hubLoginStream) problem(code api.ErrorCode, detail string) *api.Problem {
	p := api.NewProblem(code, detail)
	p.DebugId = debugID(st.ctx)
	return &p
}

// loginEvent is state as the API sends it.
func (s *server) loginEvent(ctx context.Context, state hublink.LoginState) api.HubLoginEvent {
	switch state.Phase {
	case hublink.LoginLoggedIn:
		v := s.hubStatusView(ctx, state.Status)
		return api.HubLoginEvent{Phase: api.HubLoginEventPhaseLoggedIn, Hub: &v}
	case hublink.LoginFailed:
		code := api.ErrorCodeInternal
		detail := "the login failed"
		if p := state.Problem; p != nil {
			code, detail = api.ErrorCode(p.Code), p.Detail
		}
		p := api.NewProblem(code, detail)
		p.DebugId = debugID(ctx)
		return api.HubLoginEvent{Phase: api.HubLoginEventPhaseFailed, Problem: &p}
	}
	return api.HubLoginEvent{Phase: api.HubLoginEventPhasePending}
}

func (s *server) CancelHubLogin(ctx context.Context, req api.CancelHubLoginRequestObject) (api.CancelHubLoginResponseObject, error) {
	g := s.hubLogins.get(req.Params.Login)
	if g == nil {
		return nil, &hubError{code: api.ErrorCodeNotFound, err: errors.New("the daemon has no such login: another replaced it, or it stopped since")}
	}
	g.Cancel()
	return api.CancelHubLogin200JSONResponse(s.loginEvent(ctx, g.State())), nil
}

func (s *server) SendHubLoginCode(ctx context.Context, req api.SendHubLoginCodeRequestObject) (api.SendHubLoginCodeResponseObject, error) {
	g := s.hubLogins.last()
	if g == nil {
		return nil, &hubError{code: api.ErrorCodeNotFound, err: errors.New("no login is pending")}
	}
	b := req.Body
	err := g.Callback(ctx, hublink.Callback{Code: deref(b.Code), State: b.State, Error: deref(b.Error)})
	switch {
	case errors.Is(err, hublink.ErrNotBrowser):
		return nil, &hubError{code: api.ErrorCodeInvalidArgument, err: err}
	case errors.Is(err, hublink.ErrWrongState):
		return nil, &hubError{code: api.ErrorCodeInvalidArgument, err: err}
	case errors.Is(err, hublink.ErrLoginEnded):
		// Its state says how it ended, as when the page is loaded again.
	case err != nil:
		return nil, hubFailure(err)
	}
	return api.SendHubLoginCode200JSONResponse(s.loginEvent(ctx, g.State())), nil
}

func (s *server) LogoutHub(ctx context.Context, _ api.LogoutHubRequestObject) (api.LogoutHubResponseObject, error) {
	before := s.hub.Status()
	s.remote.LoggingOut(ctx)
	told, err := s.hub.Logout(ctx)
	if err != nil {
		return nil, hubFailure(err)
	}
	// The code's files went with <home>/hub/.
	if err := s.remote.LoggedOut(ctx); err != nil {
		logging.FromContext(ctx).Error("sessions not closed at logout", "err", err)
	}
	sendHubState(ctx, s.hub, s.access)
	return api.LogoutHub200JSONResponse{Result: api.ResultLoggedOut, HubTold: told, Hub: s.hubStatusView(ctx, before)}, nil
}

func (s *server) ListHubNodes(ctx context.Context, _ api.ListHubNodesRequestObject) (api.ListHubNodesResponseObject, error) {
	nodes, err := s.hub.Nodes(ctx)
	if err != nil {
		return nil, hubFailure(err)
	}
	list := make([]api.HubNode, len(nodes))
	for i, n := range nodes {
		list[i] = hubNodeView(n)
	}
	return api.ListHubNodes200JSONResponse{Nodes: list}, nil
}

func (s *server) GetHubNode(ctx context.Context, req api.GetHubNodeRequestObject) (api.GetHubNodeResponseObject, error) {
	n, err := s.hub.Node(ctx, req.Node)
	if err != nil {
		return nil, hubFailure(err)
	}
	known, err := s.remote.KeyKnown(ctx, n.Id)
	if err != nil {
		return nil, err
	}
	sessions, err := s.remote.SessionsWith(ctx, n.Id)
	if err != nil {
		return nil, err
	}
	return api.GetHubNode200JSONResponse{Node: api.HubNodeDetail{
		Id: n.Id, Name: n.Name, This: n.This, Online: n.Online, LastSeenAt: utc(n.LastSeenAt),
		Version: n.Version, Os: n.Os, CreatedAt: n.CreatedAt.UTC(), Fingerprint: n.Fingerprint,
		KeyKnown: known, Sessions: sessionViews(sessions),
	}}, nil
}

func (s *server) RemoveHubNode(ctx context.Context, req api.RemoveHubNodeRequestObject) (api.RemoveHubNodeResponseObject, error) {
	n, err := s.hub.RemoveNode(ctx, req.Node)
	if err != nil {
		return nil, hubFailure(err)
	}
	return api.RemoveHubNode200JSONResponse{Result: api.ResultRemoved, Node: hubNodeView(n)}, nil
}

// hubStatusView is the link's status as the API gives it, with whether
// sessions may come in.
func (s *server) hubStatusView(ctx context.Context, h hublink.Status) api.HubStatus {
	v := api.HubStatus{Connection: api.HubConnection(h.Connection), HubVersion: nonEmpty(h.HubVersion), InRange: h.InRange, Behind: nonEmpty(h.Behind), RemoteAccess: api.HubStatusRemoteAccessOff}
	if s.access.remoteAccess.Load() {
		v.RemoteAccess = api.HubStatusRemoteAccessOn
	}
	if h.LoggedIn() {
		v.Url, v.User = &h.URL, &h.User
		v.Node = &api.HubNodeRef{Id: h.Node.ID, Name: h.Node.Name}
		v.CodeSetup = s.access.codeSetUp(ctx)
		v.WrongCodes = s.wrongCodes(ctx)
	}
	in, out, err := s.remote.Counts(ctx)
	if err != nil {
		logging.FromContext(ctx).Error("sessions not counted", "err", err)
	}
	v.Sessions = api.HubSessionCounts{Incoming: in, Outgoing: out}
	return v
}

// wrongCodes is the wrong codes since the last session opened into this
// node, as the API gives them; none when they cannot be read, which is
// logged.
func (s *server) wrongCodes(ctx context.Context) api.HubWrongCodes {
	n, latest, err := s.remote.WrongCodes(ctx)
	if err != nil {
		logging.FromContext(ctx).Error("wrong codes not counted", "err", err)
	}
	v := api.HubWrongCodes{Count: n}
	if !latest.IsZero() {
		v.LatestAt = utc(&latest)
	}
	return v
}

func hubNodeView(n hubapi.AccountNode) api.HubNode {
	return api.HubNode{
		Id: n.Id, Name: n.Name, This: n.This, Online: n.Online, LastSeenAt: utc(n.LastSeenAt),
		Version: n.Version, Os: n.Os, CreatedAt: n.CreatedAt.UTC(),
	}
}

// nonEmpty is nil for "", a pointer to v otherwise.
func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
