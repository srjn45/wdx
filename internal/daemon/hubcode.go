package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/nodeaudit"
)

// The waits after a wrong recovery key: the first, doubled at each wrong key
// after it, up to the last.
const (
	firstRecoveryWait = time.Second
	lastRecoveryWait  = 5 * time.Minute
)

// hubAccess is what decides who may open sessions into this node: its
// authenticator code, the setup pending, the waits after wrong recovery keys
// and the hub.* settings, with their acts in the node's audit chain (the wdx
// doc HUB_TUNNEL).
type hubAccess struct {
	files *authcode.Files
	db    *sql.DB
	now   func() time.Time

	remoteAccess atomic.Bool  // hub.remote_access
	idleTimeout  atomic.Int64 // hub.session_idle_timeout, in nanoseconds

	mu sync.Mutex
	// pending is the setup started last, nil when none is.
	pending *pendingCode
	// wrongKeys is the wrong recovery keys since the last right one, and
	// tryAfter when the next may be tried.
	wrongKeys int
	tryAfter  time.Time
}

// pendingCode is a secret made for a setup, kept until a code of it is
// verified.
type pendingCode struct {
	secret string
	url    string // the otpauth URL a QR code holds, of the secret
	label  string
	nodeID string // the node it was made for, so that a login since voids it
	action string // what verifying it is in the audit chain
}

func newHubAccess(home string, db *sql.DB, now func() time.Time, remoteAccess bool, idleTimeout time.Duration) *hubAccess {
	a := &hubAccess{files: authcode.Open(identity.HubDir(home)), db: db, now: now}
	a.remoteAccess.Store(remoteAccess)
	a.idleTimeout.Store(int64(idleTimeout))
	return a
}

// apply takes the hub.* settings read again, recording remote access turned
// on or off in the audit chain.
func (a *hubAccess) apply(ctx context.Context, remoteAccess bool, idleTimeout time.Duration) {
	a.idleTimeout.Store(int64(idleTimeout))
	if a.remoteAccess.Swap(remoteAccess) == remoteAccess {
		return
	}
	action := nodeaudit.ActionRemoteAccessOff
	if remoteAccess {
		action = nodeaudit.ActionRemoteAccessOn
	}
	logging.FromContext(ctx).Info("remote access changed", "remote_access", remoteAccess)
	if err := nodeaudit.AppendOne(ctx, a.db, nodeaudit.Entry{At: a.now(), Action: action}); err != nil {
		logging.FromContext(ctx).Error("remote access change not in the audit chain", "err", err)
	}
}

// codeSetUp reports whether a code is set up, false when it cannot tell.
func (a *hubAccess) codeSetUp(ctx context.Context) bool {
	on, err := a.files.SetUp()
	if err != nil {
		logging.FromContext(ctx).Error("code files unreadable", "err", err)
	}
	return on
}

// start makes a pending secret for status's node, replacing the code set up
// only when replace says to.
func (a *hubAccess) start(ctx context.Context, status hublink.Status, replace bool) (pendingCode, error) {
	if !status.LoggedIn() {
		return pendingCode{}, &hubError{code: api.ErrorCodeNotLoggedIn, err: errors.New("this node is not logged in to a hub")}
	}
	on, err := a.files.SetUp()
	if err != nil {
		return pendingCode{}, err
	}
	action := nodeaudit.ActionCodeSetUp
	if on {
		if !replace {
			return pendingCode{}, &hubError{code: api.ErrorCodeCodeExists, err: errors.New("a code is set up on this node already")}
		}
		action = nodeaudit.ActionCodeReplaced
	}
	return a.makePending(ctx, status, action)
}

func (a *hubAccess) makePending(ctx context.Context, status hublink.Status, action string) (pendingCode, error) {
	account := status.User + "@" + status.Node.Name
	p, err := authcode.Generate(identity.Name, account)
	if err != nil {
		return pendingCode{}, err
	}
	pending := pendingCode{secret: p.Secret, url: p.URL, label: identity.Name + ": " + account, nodeID: status.Node.ID, action: action}
	a.mu.Lock()
	a.pending = &pending
	a.mu.Unlock()
	logging.FromContext(ctx).Info("code setup pending", "action", action, "node_id", status.Node.ID)
	return pending, nil
}

// verify sets up the pending secret, given a code of it, and returns the
// recovery key.
func (a *hubAccess) verify(ctx context.Context, status hublink.Status, code string) (pendingCode, string, error) {
	log := logging.FromContext(ctx)
	if !status.LoggedIn() {
		return pendingCode{}, "", &hubError{code: api.ErrorCodeNotLoggedIn, err: errors.New("this node is not logged in to a hub")}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending
	if p == nil || p.nodeID != status.Node.ID {
		return pendingCode{}, "", &hubError{code: api.ErrorCodeNotFound, err: errors.New("no code setup is pending: start one again")}
	}
	step, err := authcode.Match(p.secret, code, a.now(), 0)
	if err != nil {
		log.Info("code refused", "reason", "not the pending secret's")
		return pendingCode{}, "", &hubError{code: api.ErrorCodeCodeWrong, err: errors.New("the code is not the new entry's")}
	}
	key, err := a.files.Write(ctx, p.secret, step)
	if err != nil {
		return pendingCode{}, "", err
	}
	a.pending = nil
	if p.action == nodeaudit.ActionCodeRecovered {
		a.wrongKeys, a.tryAfter = 0, time.Time{}
	}
	log.Info("code set up", "action", p.action)
	if err := nodeaudit.AppendOne(ctx, a.db, nodeaudit.Entry{At: a.now(), Action: p.action}); err != nil {
		log.Error("code setup not in the audit chain", "err", err)
	}
	return *p, key, nil
}

// recover starts a setup in place of the code set up, given its recovery
// key. A wrong key makes the next wait, doubling, and a key tried before
// then is refused unread.
func (a *hubAccess) recover(ctx context.Context, status hublink.Status, key string) (pendingCode, error) {
	log := logging.FromContext(ctx)
	if !status.LoggedIn() {
		return pendingCode{}, &hubError{code: api.ErrorCodeNotLoggedIn, err: errors.New("this node is not logged in to a hub")}
	}
	a.mu.Lock()
	now := a.now()
	if wait := a.tryAfter.Sub(now); wait > 0 {
		a.mu.Unlock()
		log.Info("recovery key refused", "reason", "too soon", "wait_ms", wait.Milliseconds())
		return pendingCode{}, &hubError{code: api.ErrorCodeRecoveryKeyWrong, err: fmt.Errorf("too soon after a wrong recovery key: try again in %s", wait.Round(time.Second))}
	}
	right, err := a.files.CheckRecoveryKey(key)
	if errors.Is(err, authcode.ErrNotSetUp) {
		a.mu.Unlock()
		return pendingCode{}, &hubError{code: api.ErrorCodeCodeNotSetUp, err: errors.New("no code is set up on this node, so there is nothing to recover")}
	}
	if err != nil {
		a.mu.Unlock()
		return pendingCode{}, err
	}
	if !right {
		a.wrongKeys++
		wait := min(firstRecoveryWait<<min(a.wrongKeys-1, 16), lastRecoveryWait)
		a.tryAfter = now.Add(wait)
		wrong := a.wrongKeys
		a.mu.Unlock()
		log.Info("recovery key wrong", "wrong_keys", wrong, "wait_ms", wait.Milliseconds())
		if err := nodeaudit.AppendOne(ctx, a.db, nodeaudit.Entry{At: now, Action: nodeaudit.ActionRecoveryKeyWrong, Details: map[string]any{"wait_ms": wait.Milliseconds()}}); err != nil {
			log.Error("wrong recovery key not in the audit chain", "err", err)
		}
		return pendingCode{}, &hubError{code: api.ErrorCodeRecoveryKeyWrong, err: fmt.Errorf("the recovery key is wrong; the next try waits %s", wait)}
	}
	a.wrongKeys, a.tryAfter = 0, time.Time{}
	a.mu.Unlock()
	log.Info("recovery key right")
	return a.makePending(ctx, status, nodeaudit.ActionCodeRecovered)
}

func (p pendingCode) view() api.HubCodePendingEnvelope {
	return api.HubCodePendingEnvelope{Code: api.HubCodePending{Secret: p.secret, Url: p.url, Label: p.label, Issuer: identity.Name}}
}

func (s *server) StartHubCode(ctx context.Context, req api.StartHubCodeRequestObject) (api.StartHubCodeResponseObject, error) {
	p, err := s.access.start(ctx, s.hub.Status(), req.Body.Replace != nil && *req.Body.Replace)
	if err != nil {
		return nil, err
	}
	return api.StartHubCode201JSONResponse(p.view()), nil
}

func (s *server) VerifyHubCode(ctx context.Context, req api.VerifyHubCodeRequestObject) (api.VerifyHubCodeResponseObject, error) {
	p, key, err := s.access.verify(ctx, s.hub.Status(), req.Body.Code)
	if err != nil {
		return nil, err
	}
	sendHubState(ctx, s.hub, s.access)
	result := api.ResultSetUp
	if p.action == nodeaudit.ActionCodeRecovered {
		result = api.ResultRecovered
	}
	return api.VerifyHubCode201JSONResponse{Result: result, Label: p.label, RecoveryKey: key}, nil
}

func (s *server) RecoverHubCode(ctx context.Context, req api.RecoverHubCodeRequestObject) (api.RecoverHubCodeResponseObject, error) {
	p, err := s.access.recover(ctx, s.hub.Status(), req.Body.RecoveryKey)
	if err != nil {
		return nil, err
	}
	return api.RecoverHubCode201JSONResponse(p.view()), nil
}

func (s *server) VerifyHubAudit(ctx context.Context, _ api.VerifyHubAuditRequestObject) (api.VerifyHubAuditResponseObject, error) {
	v, err := nodeaudit.Verify(ctx, s.access.db)
	if err != nil {
		return nil, err
	}
	if b := v.Broken; b != nil {
		return nil, &hubError{
			code:    api.ErrorCodeAuditBroken,
			err:     fmt.Errorf("row %d of the audit chain does not match its hash: the chain was changed there", b.ID),
			details: map[string]any{"id": b.ID, "at": b.At.Format(time.RFC3339Nano)},
		}
	}
	return api.VerifyHubAudit200JSONResponse{Audit: api.HubAudit{Rows: v.Rows, Head: nonEmpty(v.Head)}}, nil
}
