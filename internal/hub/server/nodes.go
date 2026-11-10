package server

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"net/http"
	"net/url"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/ca"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

type (
	nodeKey    struct{}
	addressKey struct{}
	requestKey struct{}
)

// address is the client's address of the request in ctx.
func address(ctx context.Context) string {
	a, _ := ctx.Value(addressKey{}).(string)
	return a
}

// caller is the node the request in ctx came from, on the node endpoint.
func caller(ctx context.Context) node.Node {
	n, _ := ctx.Value(nodeKey{}).(node.Node)
	return n
}

// checkNode lets a request on the node endpoint through only from a node
// that may connect: the handshake checked the CA issued its certificate,
// and this checks the database, at every request, for the node the
// certificate names, so that a node revoked or a user disabled is refused at
// its next request.
func (s *Server) checkNode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var cert *x509.Certificate
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cert = r.TLS.PeerCertificates[0]
		}
		var id string
		var ok bool
		if cert != nil {
			id, ok = ca.NodeID(cert)
		}
		if !ok {
			logging.FromContext(ctx).Info("node refused", "reason", "the certificate names no node")
			failed(w, r, node.ErrRevoked)
			return
		}
		pub, _ := cert.PublicKey.(ed25519.PublicKey)
		n, err := s.nodes.Authenticate(ctx, id, pub)
		if err != nil {
			failed(w, r, err)
			return
		}
		ctx = context.WithValue(context.WithValue(ctx, nodeKey{}, n), requestKey{}, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RateLimitedError is a client that tried too often, which may try again
// after Wait.
type RateLimitedError struct {
	Wait time.Duration
}

func (e *RateLimitedError) Error() string {
	return "too many logins started from this address: try again in " + e.Wait.Round(time.Second).String()
}

// nodeError is a failure of an act on the node ref names.
type nodeError struct {
	ref string
	err error
}

func (e *nodeError) Error() string { return e.err.Error() + ": " + e.ref }
func (e *nodeError) Unwrap() error { return e.err }

func (a *api) StartNodeLogin(ctx context.Context, req hubapi.StartNodeLoginRequestObject) (hubapi.StartNodeLoginResponseObject, error) {
	s := a.server
	addr := address(ctx)
	if wait := s.pages.AllowLoginStart(addr); wait > 0 {
		logging.FromContext(ctx).Info("login refused", "reason", "rate_limited", "address", addr, "wait_ms", wait.Milliseconds())
		return nil, &RateLimitedError{Wait: wait}
	}
	b := req.Body
	started, err := s.nodes.StartLogin(ctx, node.By{Actor: audit.ActorUser, Address: addr}, node.StartRequest{
		Flow: node.Flow(b.Flow), Name: b.Name, OS: b.Os, Version: b.WdxVersion, PublicKey: b.PublicKey,
		PKCEChallenge: deref(b.PkceChallenge), Callback: deref(b.Callback),
	})
	if err != nil {
		return nil, err
	}
	r := hubapi.StartNodeLogin201JSONResponse{Flow: b.Flow, ExpiresAt: started.ExpiresAt}
	public := s.opts.Config.PublicURL
	if started.Flow == node.FlowBrowser {
		link := public + "/nodes/approve?" + url.Values{"login": {started.ID}}.Encode()
		r.LoginUrl = &link
	} else {
		verify, interval := public+"/device", int(node.PollInterval/time.Second)
		r.DeviceCode, r.UserCode, r.VerificationUrl, r.Interval = &started.DeviceCode, &started.UserCode, &verify, &interval
	}
	return r, nil
}

// deref is *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// issue issues a node its certificate.
func (s *Server) issue(n node.Node) ([]byte, time.Time, error) {
	return s.ca.IssueNode(n.PublicKey, n.ID)
}

func (s *Server) credentials(c node.Credentials) hubapi.NodeCredentials {
	return hubapi.NodeCredentials{
		Node:          hubapi.NodeRef{Id: c.Node.ID, Name: c.Node.Name},
		User:          c.Username,
		Certificate:   string(c.Certificate),
		CaCertificate: string(s.ca.CertificatePEM()),
		NodesUrl:      s.nodesURL,
	}
}

func (a *api) ExchangeNodeLogin(ctx context.Context, req hubapi.ExchangeNodeLoginRequestObject) (hubapi.ExchangeNodeLoginResponseObject, error) {
	c, err := a.server.nodes.Exchange(ctx, req.Body.Code, req.Body.PkceVerifier, a.server.issue)
	if err != nil {
		return nil, err
	}
	return hubapi.ExchangeNodeLogin200JSONResponse(a.server.credentials(c)), nil
}

func (a *api) PollNodeLogin(ctx context.Context, req hubapi.PollNodeLoginRequestObject) (hubapi.PollNodeLoginResponseObject, error) {
	c, err := a.server.nodes.Poll(ctx, req.Body.DeviceCode, a.server.issue)
	if err != nil {
		return nil, err
	}
	return hubapi.PollNodeLogin200JSONResponse(a.server.credentials(c)), nil
}

func (s *Server) nodeOf(n node.Node) hubapi.Node {
	r := hubapi.Node{
		Id: n.ID, Name: n.Name, Os: n.OS, Version: n.Version, CreatedAt: n.CreatedAt, Online: s.nodes.Registry().Online(n.ID),
		RemoteAccess: n.RemoteAccess, CodeSetup: n.CodeSetup,
	}
	if !n.LastSeenAt.IsZero() {
		r.LastSeenAt = &n.LastSeenAt
	}
	return r
}

func (s *Server) accountNodeOf(n node.Node, this node.Node) hubapi.AccountNode {
	r := s.nodeOf(n)
	return hubapi.AccountNode{
		Id: r.Id, Name: r.Name, Os: r.Os, Version: r.Version, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, Online: r.Online,
		RemoteAccess: r.RemoteAccess, CodeSetup: r.CodeSetup, This: n.ID == this.ID,
	}
}

func (a *api) ListNodes(ctx context.Context, _ hubapi.ListNodesRequestObject) (hubapi.ListNodesResponseObject, error) {
	this := caller(ctx)
	list, err := a.server.nodes.Nodes(ctx, this.UserID)
	if err != nil {
		return nil, err
	}
	nodes := make([]hubapi.AccountNode, 0, len(list))
	for _, n := range list {
		nodes = append(nodes, a.server.accountNodeOf(n, this))
	}
	logging.FromContext(ctx).Info("nodes listed", "user_id", this.UserID, "count", len(nodes))
	return hubapi.ListNodes200JSONResponse{Nodes: nodes}, nil
}

func (a *api) GetNode(ctx context.Context, req hubapi.GetNodeRequestObject) (hubapi.GetNodeResponseObject, error) {
	this := caller(ctx)
	n, err := a.server.nodes.Node(ctx, this.UserID, req.Node)
	if err != nil {
		return nil, &nodeError{ref: req.Node, err: err}
	}
	r := a.server.accountNodeOf(n, this)
	return hubapi.GetNode200JSONResponse{Node: hubapi.AccountNodeDetail{
		Id: r.Id, Name: r.Name, Os: r.Os, Version: r.Version, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, Online: r.Online,
		RemoteAccess: r.RemoteAccess, CodeSetup: r.CodeSetup, This: r.This,
		PublicKey: n.PublicKey, Fingerprint: node.Fingerprint(n.PublicKey),
	}}, nil
}

func (a *api) RemoveNode(ctx context.Context, req hubapi.RemoveNodeRequestObject) (hubapi.RemoveNodeResponseObject, error) {
	this := caller(ctx)
	s := a.server
	n, err := s.nodes.Node(ctx, this.UserID, req.Node)
	if err != nil {
		return nil, &nodeError{ref: req.Node, err: err}
	}
	// As it was: online until it is cut off.
	was := s.accountNodeOf(n, this)
	if _, err := s.nodes.Remove(ctx, node.By{Actor: audit.ActorUser, Address: address(ctx), NodeID: this.ID}, this.UserID, "id:"+n.ID); err != nil {
		return nil, &nodeError{ref: req.Node, err: err}
	}
	s.relay.CutNode(ctx, n.ID)
	return hubapi.RemoveNode200JSONResponse{Result: hubapi.ResultRemoved, Node: was}, nil
}

func (a *api) OpenControl(ctx context.Context, _ hubapi.OpenControlRequestObject) (hubapi.OpenControlResponseObject, error) {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return controlResponse{server: a.server, r: r, node: caller(ctx)}, nil
}
