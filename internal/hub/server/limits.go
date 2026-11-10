package server

import (
	"cmp"
	"time"

	"github.com/srjn45/wdx/internal/hub/relay"
)

// How long the hub waits on the nodes, which a loaded machine can hold back,
// unless its Limits say otherwise.
const (
	// helloTimeout is how long a node has to say hello once its control
	// connection opened.
	helloTimeout = 10 * time.Second
	// pingInterval is how often the hub pings a node, which tells it the node
	// is gone when no pong comes within the interval.
	pingInterval = 30 * time.Second
	// byeTimeout is how long saying goodbye may take.
	byeTimeout = 5 * time.Second
)

// Limits are those waits, when not the defaults: a test lengthens them so that
// a loaded machine slows it rather than fails it, or shortens the one it tests.
// StreamJoin and StreamClose are the relay's JoinTimeout and CloseTimeout.
type Limits struct {
	Hello, Ping, Bye, StreamJoin, StreamClose time.Duration
}

func (l Limits) orDefaults() Limits {
	return Limits{
		Hello:       cmp.Or(l.Hello, helloTimeout),
		Ping:        cmp.Or(l.Ping, pingInterval),
		Bye:         cmp.Or(l.Bye, byeTimeout),
		StreamJoin:  cmp.Or(l.StreamJoin, relay.DefaultJoinTimeout),
		StreamClose: cmp.Or(l.StreamClose, relay.DefaultCloseTimeout),
	}
}
