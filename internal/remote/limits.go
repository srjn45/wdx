package remote

import (
	"cmp"
	"time"
)

// How long a node waits on another through the hub, which a loaded machine
// can hold back, unless its Limits say otherwise.
const (
	// confirmTimeout bounds the wait for a client's confirm, which holds the
	// host's open lock: a confirm that does not come counts as a wrong code.
	confirmTimeout = 10 * time.Second
	// exchangeTimeout bounds a stream's TLS handshake and the messages that
	// follow it, so that a stream nobody speaks on ends.
	exchangeTimeout = 30 * time.Second
	// closeTimeout bounds telling a host a session closed, which is best
	// effort.
	closeTimeout = 10 * time.Second
)

// Limits are those waits, when not the defaults: a test lengthens them so that
// a loaded machine slows it rather than fails it, or shortens the one it tests.
type Limits struct {
	Confirm, Exchange, Close time.Duration
}

func (l Limits) orDefaults() Limits {
	return Limits{
		Confirm:  cmp.Or(l.Confirm, confirmTimeout),
		Exchange: cmp.Or(l.Exchange, exchangeTimeout),
		Close:    cmp.Or(l.Close, closeTimeout),
	}
}
