package hublink

import (
	"cmp"
	"time"
)

// How long a link waits on the hub and on the other nodes, which a loaded
// machine can hold back, unless its Limits say otherwise.
const (
	// requestTimeout is how long a request to the web endpoint may take.
	requestTimeout = 30 * time.Second
	// dialTimeout is how long opening the control connection may take, and
	// the TLS handshake with the node endpoint.
	dialTimeout = 30 * time.Second
	// writeTimeout is how long sending one message on the control
	// connection may take.
	writeTimeout = 10 * time.Second
	// pingInterval is how often the node pings the hub, which tells it the
	// hub is gone when no pong comes within the interval.
	pingInterval = 30 * time.Second
	// logoutTimeout is how long logging out waits for the hub to hear of it.
	logoutTimeout = 5 * time.Second
	// setupTimeout is how long reaching another node may take: asking the
	// hub for a stream and joining it, or joining one the hub says waits.
	setupTimeout = 30 * time.Second
	// handoffTimeout is how long a stream another node asked for waits, once
	// joined, for the daemon to take it.
	handoffTimeout = 10 * time.Second
)

// Limits are those waits, when not the defaults: a test lengthens them so that
// a loaded machine slows it rather than fails it, or shortens the one it tests.
type Limits struct {
	Request, Dial, Write, Ping, Logout, Setup, Handoff time.Duration
}

func (l Limits) orDefaults() Limits {
	return Limits{
		Request: cmp.Or(l.Request, requestTimeout),
		Dial:    cmp.Or(l.Dial, dialTimeout),
		Write:   cmp.Or(l.Write, writeTimeout),
		Ping:    cmp.Or(l.Ping, pingInterval),
		Logout:  cmp.Or(l.Logout, logoutTimeout),
		Setup:   cmp.Or(l.Setup, setupTimeout),
		Handoff: cmp.Or(l.Handoff, handoffTimeout),
	}
}
