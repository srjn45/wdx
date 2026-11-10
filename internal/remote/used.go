package remote

import (
	"net"
	"sync"
	"time"
)

// useEvery is how often bytes through a stream of a session move its idle
// time on, at most: often enough that a session in use never goes idle,
// whatever its timeout, and seldom enough that a busy stream writes little.
func useEvery(timeout time.Duration) time.Duration {
	return min(time.Minute, timeout/4)
}

// usedConn is a stream of a session that calls used as bytes pass either
// way, at most every useEvery of the session's timeout: requests, an SSE
// stream that only receives and an attach alike. The first bytes count.
type usedConn struct {
	net.Conn
	now     func() time.Time
	timeout func() time.Duration
	used    func()

	mu   sync.Mutex
	last time.Time // when used was called last; zero before the first bytes
}

func (c *usedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.passed()
	}
	return n, err
}

func (c *usedConn) Write(p []byte) (int, error) {
	if len(p) > 0 {
		c.passed()
	}
	return c.Conn.Write(p)
}

func (c *usedConn) passed() {
	now := c.now()
	c.mu.Lock()
	due := c.last.IsZero() || now.Sub(c.last) >= useEvery(c.timeout())
	if due {
		c.last = now
	}
	c.mu.Unlock()
	if due {
		c.used()
	}
}
