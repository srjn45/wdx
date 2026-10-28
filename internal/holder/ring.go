package holder

import "sync"

// ringSize is how much of the shell's latest output a holder keeps.
const ringSize = 1 << 20

// ring keeps the last bytes written to it.
type ring struct {
	mu   sync.Mutex
	buf  []byte
	next int  // where the next byte goes
	full bool // the buffer has wrapped
}

func newRing(size int) *ring {
	return &ring{buf: make([]byte, size)}
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	if len(p) >= len(r.buf) {
		p = p[len(p)-len(r.buf):]
	}
	copied := copy(r.buf[r.next:], p)
	copy(r.buf, p[copied:])
	if r.next+len(p) >= len(r.buf) {
		r.full = true
	}
	r.next = (r.next + len(p)) % len(r.buf)
	return n, nil
}

// Bytes returns a copy of what the ring holds, oldest first.
func (r *ring) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]byte(nil), r.buf[:r.next]...)
	}
	return append(append([]byte(nil), r.buf[r.next:]...), r.buf[:r.next]...)
}
