package holder

import "testing"

// TestToldOfTheExitOnlyOnceEnded: what hello says of the exit, read under the
// lock, is never ahead of ended, or the release of a client told of it is
// ignored and the holder stays for good.
func TestToldOfTheExitOnlyOnceEnded(t *testing.T) {
	t.Parallel()
	for range 500 {
		h := &holder{ended: make(chan struct{})}
		go h.markExited(&Exit{})
		for told := false; !told; {
			h.mu.Lock()
			told = h.exit != nil
			ended := false
			select {
			case <-h.ended:
				ended = true
			default:
			}
			h.mu.Unlock()
			if told && !ended {
				t.Fatal("told of the exit before the shell's end was marked")
			}
		}
	}
}
