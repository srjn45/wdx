package hublink

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"os"
	"strings"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

// nodesClient is the hub's API on the node endpoint of the login, and the
// login; ErrNotLoggedIn without one.
func (l *Link) nodesClient() (*hubapi.ClientWithResponses, *credentials, func(), error) {
	l.mu.Lock()
	c := l.creds
	l.mu.Unlock()
	if c == nil {
		return nil, nil, nil, ErrNotLoggedIn
	}
	transport := nodeTransport(c, l.opts.Limits)
	var rt http.RoundTripper = transport
	if l.opts.WrapNodes != nil {
		rt = l.opts.WrapNodes(rt)
	}
	client, err := hubapi.NewClientWithResponses(c.endpoint(), hubapi.WithHTTPClient(&http.Client{Transport: rt}), hubapi.WithRequestEditorFn(versionHeader))
	if err != nil {
		return nil, nil, nil, err
	}
	return client, c, transport.CloseIdleConnections, nil
}

// Nodes is the nodes of the account, from the hub.
func (l *Link) Nodes(ctx context.Context) ([]hubapi.AccountNode, error) {
	if err := l.hubLacks(featureNodes); err != nil {
		return nil, err
	}
	client, c, done, err := l.nodesClient()
	if err != nil {
		return nil, err
	}
	defer done()
	r, err := client.ListNodesWithResponse(ctx)
	if err != nil {
		return nil, &UnreachableError{URL: c.endpoint(), Err: err}
	}
	if r.JSON200 == nil {
		return nil, refused(c.endpoint(), r.StatusCode(), r.ApplicationproblemJSONDefault)
	}
	logging.FromContext(ctx).Info("hub nodes listed", "count", len(r.JSON200.Nodes))
	return r.JSON200.Nodes, nil
}

// Node is the node of the account ref names: its name, whatever its case,
// or id: and its id.
func (l *Link) Node(ctx context.Context, ref string) (hubapi.AccountNodeDetail, error) {
	if err := l.hubLacks(featureNodes); err != nil {
		return hubapi.AccountNodeDetail{}, err
	}
	client, c, done, err := l.nodesClient()
	if err != nil {
		return hubapi.AccountNodeDetail{}, err
	}
	defer done()
	r, err := client.GetNodeWithResponse(ctx, ref)
	if err != nil {
		return hubapi.AccountNodeDetail{}, &UnreachableError{URL: c.endpoint(), Err: err}
	}
	if r.JSON200 == nil {
		return hubapi.AccountNodeDetail{}, refused(c.endpoint(), r.StatusCode(), r.ApplicationproblemJSONDefault)
	}
	logging.FromContext(ctx).Info("hub node found", "ref", ref, "node_id", r.JSON200.Node.Id)
	return r.JSON200.Node, nil
}

// isThis reports whether ref names the node of c.
func isThis(c *credentials, ref string) bool {
	if id, ok := strings.CutPrefix(ref, "id:"); ok {
		return id == c.Node.ID
	}
	return strings.EqualFold(ref, c.Node.Name)
}

// RemoveNode revokes the node of the account ref names, which the hub cuts
// off at once, returning it as it was; this node is ErrNodeIsThis, as
// leaving is Logout.
func (l *Link) RemoveNode(ctx context.Context, ref string) (hubapi.AccountNode, error) {
	if err := l.hubLacks(featureNodes); err != nil {
		return hubapi.AccountNode{}, err
	}
	client, c, done, err := l.nodesClient()
	if err != nil {
		return hubapi.AccountNode{}, err
	}
	defer done()
	if isThis(c, ref) {
		return hubapi.AccountNode{}, ErrNodeIsThis
	}
	r, err := client.RemoveNodeWithResponse(ctx, ref)
	if err != nil {
		return hubapi.AccountNode{}, &UnreachableError{URL: c.endpoint(), Err: err}
	}
	if r.JSON200 == nil {
		return hubapi.AccountNode{}, refused(c.endpoint(), r.StatusCode(), r.ApplicationproblemJSONDefault)
	}
	logging.FromContext(ctx).Info("hub node removed", "ref", ref, "node_id", r.JSON200.Node.Id)
	return r.JSON200.Node, nil
}

// Logout leaves the hub: it asks the hub to revoke this node, waiting at
// most Limits.Logout, then closes the control connection and deletes
// <home>/hub/, whether the hub heard or not. hubTold is whether it did.
func (l *Link) Logout(ctx context.Context) (hubTold bool, err error) {
	log := logging.FromContext(ctx)
	l.starting.Lock()
	defer l.starting.Unlock()
	client, c, done, err := l.nodesClient()
	if err != nil {
		return false, err
	}
	telling, cancel := context.WithTimeout(ctx, l.opts.Limits.Logout)
	r, err := client.RemoveNodeWithResponse(telling, "id:"+c.Node.ID)
	cancel()
	done()
	switch {
	case err != nil:
		log.Info("hub not told of the logout", "err", &UnreachableError{URL: c.endpoint(), Err: err})
	case r.JSON200 != nil:
		hubTold = true
	default:
		err := refused(c.endpoint(), r.StatusCode(), r.ApplicationproblemJSONDefault)
		// Revoked already: the hub has it so.
		hubTold = Code(err) == hubapi.ErrorCodeNodeRevoked
		log.Info("hub refused the logout", "err", err, "hub_told", hubTold)
	}

	l.mu.Lock()
	control := l.control
	l.creds, l.control, l.hub, l.hubAPI, l.inRange, l.behind = nil, nil, "", "", nil, ""
	l.setConn(LoggedOut)
	l.mu.Unlock()
	if control != nil {
		control.stop()
	}
	l.clearOutbox(ctx)
	if err := os.RemoveAll(l.dir); err != nil {
		log.Error("hub login not deleted", "path", l.dir, "err", err)
		return hubTold, err
	}
	log.Info("logged out", "url", c.URL, "node_id", c.Node.ID, "hub_told", hubTold)
	return hubTold, nil
}

// Identity is this node, as logged in, and its key, which TLS between two
// nodes presents; ErrNotLoggedIn without a login.
func (l *Link) Identity() (NodeRef, ed25519.PrivateKey, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.creds == nil {
		return NodeRef{}, nil, ErrNotLoggedIn
	}
	return l.creds.Node, l.creds.Key, nil
}
