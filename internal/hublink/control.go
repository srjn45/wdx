package hublink

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// controlReadLimit is the largest message the hub may send.
const controlReadLimit = 64 << 10

// apiVersion is the info.version of the hub.yaml this wdx was built with,
// which its hello names.
var apiVersion = sync.OnceValue(func() string {
	spec, err := hubapi.GetSpec()
	if err != nil {
		panic("hublink: the hub's spec: " + err.Error()) // built in, so never
	}
	return spec.Info.Version
})

// loop is a control connection kept open, with its reconnections, until it
// is stopped or the hub cuts the node off for good.
type loop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// stop ends the connection and waits for it to close.
func (lp *loop) stop() {
	lp.cancel()
	<-lp.done
}

func (lp *loop) wait() { <-lp.done }

// cutOff is a connection the hub ended for good, its state saying why.
type cutOff struct{ state Connection }

func (e *cutOff) Error() string { return "the hub cut the node off: " + string(e.state) }

// versionRefused is a hello the hub refused for its API version.
type versionRefused struct{ behind string }

func (e *versionRefused) Error() string {
	return "the hub serves no version of this node's API: the " + e.behind + " is behind"
}

// startControl starts keeping the control connection of c; l.mu is held.
func (l *Link) startControl(c *credentials) *loop {
	ctx, cancel := context.WithCancel(l.ctx)
	lp := &loop{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(lp.done)
		l.keepControl(ctx, c)
	}()
	return lp
}

// update changes the status with fn while c is the login, reporting
// whether it was.
func (l *Link) update(c *credentials, fn func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.creds != c {
		return false
	}
	fn()
	l.notify()
	return true
}

// jitter is a wait of d made random in its upper half, so that nodes cut
// off together do not come back together.
func jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1)
}

// keepControl keeps the control connection of c open, trying again with a
// backoff when it fails, until ctx ends or the hub cuts the node off for
// good.
func (l *Link) keepControl(ctx context.Context, c *credentials) {
	log := logging.FromContext(ctx).With("node_id", c.Node.ID)
	ctx = logging.NewContext(ctx, log)
	transport := nodeTransport(c, l.opts.Limits)
	defer transport.CloseIdleConnections()
	var rt http.RoundTripper = transport
	if l.opts.WrapNodes != nil {
		rt = l.opts.WrapNodes(rt)
	}
	client := &http.Client{Transport: rt}
	backoff := l.opts.MinBackoff
	for {
		welcomed, err := l.connectOnce(ctx, c, client)
		if ctx.Err() != nil {
			return
		}
		if cut, ok := errors.AsType[*cutOff](err); ok {
			// Logged before the state changes, so that whoever sees the state can
			// read why.
			log.Info("node cut off", "state", string(cut.state))
			l.update(c, func() { l.conn = cut.state })
			return
		}
		if welcomed {
			backoff = l.opts.MinBackoff
		}
		if _, ok := errors.AsType[*versionRefused](err); ok {
			// Only an update makes it work: try again seldom.
			backoff = l.opts.MaxBackoff
		}
		delay := jitter(backoff)
		log.Info("control lost", "err", err, "retry_in_ms", delay.Milliseconds())
		l.update(c, func() { l.conn = Offline })
		select {
		case <-ctx.Done():
			return
		case <-l.opts.Clock.After(delay):
		}
		backoff = min(backoff*2, l.opts.MaxBackoff)
		l.update(c, func() { l.conn = Connecting })
	}
}

// connectOnce opens the control connection and serves it until it ends,
// reporting whether the hub welcomed the node.
func (l *Link) connectOnce(ctx context.Context, c *credentials, client *http.Client) (welcomed bool, err error) {
	log := logging.FromContext(ctx)
	dialing, cancel := context.WithTimeout(ctx, l.opts.Limits.Dial)
	//nolint:bodyclose // the websocket closes it itself: "You never need to close resp.Body yourself."
	conn, resp, err := websocket.Dial(dialing, c.endpoint()+"/v1/control", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: versionHeaders(),
	})
	cancel()
	if err != nil {
		return false, dialRefused(resp, err)
	}
	defer func() { _ = conn.CloseNow() }() // closed already, unless it failed
	conn.SetReadLimit(controlReadLimit)

	// life is the connection's: its reads and writes end with it. ctx ending
	// closes it as it should be, with a close the hub answers.
	life, end := context.WithCancel(context.WithoutCancel(ctx))
	defer end()
	var running sync.WaitGroup
	defer running.Wait()
	defer end()
	running.Go(func() {
		select {
		case <-ctx.Done():
			if err := conn.Close(websocket.StatusNormalClosure, "the node is leaving"); err != nil {
				log.Info("close not answered", "err", err)
			}
		case <-life.Done():
		}
	})
	running.Go(func() { l.ping(life, conn) })
	// What the hub says of other nodes is logged without this node's id,
	// which their own lines name.
	others := logging.NewContext(life, logging.FromContext(l.ctx))

	if err := l.write(life, conn, hubapi.ControlHello{Type: hubapi.ControlHelloTypeHello, ApiVersion: apiVersion(), WdxVersion: identity.Version, Os: osName()}); err != nil {
		return false, err
	}
	var bye hubapi.ControlByeReason
	pushing := false
	for {
		_, b, err := conn.Read(life)
		if err != nil {
			return welcomed, l.closedWith(c, err, bye)
		}
		var m hubapi.ControlMessage
		if err := json.Unmarshal(b, &m); err != nil {
			log.Info("control message ignored", "reason", "not JSON", "bytes", len(b))
			continue
		}
		switch m.Type {
		case string(hubapi.ControlWelcomeTypeWelcome):
			var w hubapi.ControlWelcome
			if err := json.Unmarshal(b, &w); err != nil {
				return welcomed, err
			}
			welcomed = true
			l.moved(c, w.NodesUrl)
			in := true
			log.Info("control connected", "hub_version", w.HubVersion, "api_version", w.ApiVersion)
			l.update(c, func() { l.conn, l.hub, l.hubAPI, l.inRange, l.behind = Connected, w.HubVersion, w.ApiVersion, &in, "" })
			if l.opts.AccountNodes != nil {
				running.Go(func() { l.catchUp(others, c, client) })
			}
			if !pushing {
				pushing = true
				running.Go(func() { l.keepPushing(life, c, client) })
				running.Go(func() { l.keepState(life, conn) })
			}
		case string(hubapi.ControlCertificateTypeCertificate):
			var cm hubapi.ControlCertificate
			if err := json.Unmarshal(b, &cm); err != nil {
				log.Info("control message ignored", "type", m.Type, "err", err)
				continue
			}
			l.renew(life, conn, c, cm)
		case string(hubapi.ControlStreamWaitingTypeStreamWaiting):
			var sw hubapi.ControlStreamWaiting
			if err := json.Unmarshal(b, &sw); err != nil {
				log.Info("control message ignored", "type", m.Type, "err", err)
				continue
			}
			// The stream outlives the control connection that told of it,
			// and ends with the link.
			answering := logging.NewContext(l.ctx, log)
			l.finished.Go(func() { l.answer(answering, c, sw) })
		case string(hubapi.ControlNodeRemovedTypeNodeRemoved):
			var nr hubapi.ControlNodeRemoved
			if err := json.Unmarshal(b, &nr); err != nil {
				log.Info("control message ignored", "type", m.Type, "err", err)
				continue
			}
			log.Info("control node removed", "removed_node_id", nr.Node.Id, "removed_node_name", nr.Node.Name)
			if l.opts.NodeRemoved != nil {
				// Lost with the connection, it is caught up at the next welcome.
				running.Go(func() { l.opts.NodeRemoved(others, NodeRef{ID: nr.Node.Id, Name: nr.Node.Name}) })
			}
		case string(hubapi.ControlByeTypeBye):
			var by hubapi.ControlBye
			if err := json.Unmarshal(b, &by); err != nil {
				log.Info("control message ignored", "type", m.Type, "err", err)
				continue
			}
			bye = by.Reason
			log.Info("control bye", "reason", string(bye))
		default:
			// A later hub may say what this node does not know.
			log.Info("control message ignored", "type", m.Type)
		}
	}
}

// catchUp gives Options.AccountNodes the account's nodes as the hub lists
// them, once per welcome, calling it before it asks: a node removed while
// this one was not connected is not among them.
func (l *Link) catchUp(ctx context.Context, c *credentials, client *http.Client) {
	log := logging.FromContext(ctx)
	hc, err := hubapi.NewClientWithResponses(c.endpoint(), hubapi.WithHTTPClient(client), hubapi.WithRequestEditorFn(versionHeader))
	if err != nil {
		log.Error("account nodes not listed", "err", err)
		return
	}
	listed := l.opts.AccountNodes(ctx)
	if listed == nil {
		return
	}
	r, err := hc.ListNodesWithResponse(ctx)
	if err != nil {
		log.Info("account nodes not listed", "err", &UnreachableError{URL: c.endpoint(), Err: err})
		return
	}
	if r.JSON200 == nil {
		log.Info("account nodes not listed", "err", refused(c.endpoint(), r.StatusCode(), r.ApplicationproblemJSONDefault))
		return
	}
	nodes := make([]NodeRef, 0, len(r.JSON200.Nodes))
	for _, n := range r.JSON200.Nodes {
		nodes = append(nodes, NodeRef{ID: n.Id, Name: n.Name})
	}
	log.Info("account nodes listed", "count", len(nodes))
	listed(nodes)
}

// moved makes nodesURL, the node endpoint the hub's welcome names, the
// login's when it is another, so that the next connection reaches the hub
// where it moved: the welcome came over TLS under the pinned CA, as the
// login's own address did.
func (l *Link) moved(c *credentials, nodesURL *string) {
	if nodesURL == nil {
		return // an older hub, which names none
	}
	log := logging.FromContext(l.ctx).With("node_id", c.Node.ID)
	to, from := strings.TrimRight(*nodesURL, "/"), c.endpoint()
	if to == from {
		return
	}
	if err := checkEndpoint(to); err != nil {
		log.Info("hub nodes url ignored", "nodes_url", to, "err", err)
		return
	}
	// The link's lock keeps a logout from removing the directory between
	// the check that c is still the login and the write.
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.creds != c {
		return
	}
	c.setEndpoint(to)
	if err := c.writeEndpoint(l.dir); err != nil {
		c.setEndpoint(from)
		log.Error("hub nodes url not written", "path", l.dir, "nodes_url", to, "err", err)
		return
	}
	log.Info("hub nodes url changed", "from", from, "to", to)
}

// closedWith is what a control connection that ended with err after bye
// means.
func (l *Link) closedWith(c *credentials, err error, bye hubapi.ControlByeReason) error {
	switch bye {
	case hubapi.ControlByeReasonRevoked:
		return &cutOff{state: Revoked}
	case hubapi.ControlByeReasonDisabled:
		return &cutOff{state: Disabled}
	}
	if closed, ok := errors.AsType[websocket.CloseError](err); ok && closed.Code == hubapi.CloseVersionUnsupported {
		out := false
		l.update(c, func() { l.inRange, l.behind = &out, closed.Reason })
		return &versionRefused{behind: closed.Reason}
	}
	return err
}

// dialRefused is what a failed dial means: the node cut off when the hub
// says so in its problem, else err.
func dialRefused(resp *http.Response, err error) error {
	if resp == nil || resp.Body == nil {
		return err
	}
	// The dial leaves what it read of the body readable, and closed.
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var p hubapi.Problem
	if json.Unmarshal(b, &p) != nil {
		return err
	}
	switch p.Code {
	case hubapi.ErrorCodeNodeRevoked:
		return &cutOff{state: Revoked}
	case hubapi.ErrorCodeAccountDisabled:
		return &cutOff{state: Disabled}
	}
	return &HubError{Problem: p}
}

// renew saves the certificate the hub sent, once it checked it is for the
// node's key and of the hub's CA, then tells the hub it did.
func (l *Link) renew(ctx context.Context, conn *websocket.Conn, c *credentials, m hubapi.ControlCertificate) {
	log := logging.FromContext(ctx)
	cert, err := parseCertificate([]byte(m.Certificate), c.Key)
	if err == nil {
		_, err = cert.Leaf.Verify(x509.VerifyOptions{Roots: c.CA, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: cert.Leaf.NotBefore})
	}
	if err != nil {
		log.Info("certificate refused", "err", err)
		return
	}
	if err := writeCertificate(l.dir, cert); err != nil {
		log.Error("certificate not written", "path", l.dir, "err", err)
		return
	}
	// The hub is told before the certificate is used, so that whoever sees
	// it renewed knows the hub was told.
	if err := l.write(ctx, conn, hubapi.ControlCertificateSaved{Type: hubapi.ControlCertificateSavedTypeCertificateSaved, ExpiresAt: m.ExpiresAt}); err != nil {
		log.Info("certificate saved not sent", "err", err)
	}
	c.setCertificate(cert)
	log.Info("certificate renewed", "expires_at", cert.Leaf.NotAfter)
	l.update(c, func() {}) // what Watch tells of
}

// ping pings the hub every Limits.Ping, closing the connection when no
// pong comes in time, until ctx ends.
func (l *Link) ping(ctx context.Context, conn *websocket.Conn) {
	tick := time.NewTicker(l.opts.Limits.Ping)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		pinged, stop := context.WithTimeout(ctx, l.opts.Limits.Ping)
		err := conn.Ping(pinged)
		stop()
		if err != nil {
			if ctx.Err() == nil {
				logging.FromContext(ctx).Info("control ping not answered", slog.Any("err", err))
				_ = conn.CloseNow() // the read fails, which tries again
			}
			return
		}
	}
}

func (l *Link) write(ctx context.Context, conn *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(ctx, l.opts.Limits.Write)
	defer cancel()
	return wsjson.Write(ctx, conn, v)
}
