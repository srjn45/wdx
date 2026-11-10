package logtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
)

// Requests records the requests a test's clients send a server, by the debug
// id each is logged under, so that Want can check each was logged once as
// received and once as returned, and that no other request was.
type Requests struct {
	header string
	mu     sync.Mutex
	// answered counts the requests answered under each id; failed those
	// sent with it that got no answer, which may have reached the server.
	answered, failed map[string]int
}

// NewRequests records requests whose debug id travels in header, both ways.
func NewRequests(header string) *Requests {
	return &Requests{header: header, answered: map[string]int{}, failed: map[string]int{}}
}

// Wrap returns rt, through which each request is sent with a debug id of its
// own when it has none, and recorded under the id its answer names, or the
// one sent when the answer names none or none came.
func (r *Requests) Wrap(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return roundTripper(func(req *http.Request) (*http.Response, error) {
		sent := req.Header.Get(r.header)
		if sent == "" {
			sent = newDebugID()
			req = req.Clone(req.Context())
			req.Header.Set(r.header, sent)
		}
		resp, err := rt.RoundTrip(req)
		r.mu.Lock()
		defer r.mu.Unlock()
		if err != nil {
			r.failed[sent]++
			return resp, err
		}
		id := resp.Header.Get(r.header)
		if id == "" {
			id = sent
		}
		r.answered[id]++
		return resp, nil
	})
}

// Handler is h, whose requests, which a test serves itself without a
// client, are recorded as Wrap records them.
func (r *Requests) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sent := req.Header.Get(r.header)
		if sent == "" {
			sent = newDebugID()
			req.Header.Set(r.header, sent)
		}
		h.ServeHTTP(w, req)
		id := w.Header().Get(r.header)
		if id == "" {
			id = sent
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.answered[id]++
	})
}

// Client is c, nil for a client of its own, with its transport wrapped.
func (r *Requests) Client(c *http.Client) *http.Client {
	wrapped := &http.Client{}
	if c != nil {
		*wrapped = *c
	}
	wrapped.Transport = r.Wrap(wrapped.Transport)
	return wrapped
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Errorer is what Want reports to: a *testing.T.
type Errorer interface {
	Helper()
	Errorf(format string, args ...any)
}

// Want reports to t each request recorded that lines, the server's log, do
// not have once as received and once as returned under its id, and each
// line of a request no client recorded: a request sent twice under one id is
// logged twice, one that got no answer at most as often as it was sent.
func (r *Requests) Want(t Errorer, lines []map[string]any) {
	t.Helper()
	received, returned := map[string]int{}, map[string]int{}
	for _, line := range lines {
		id, _ := line["debug_id"].(string)
		switch line["msg"] {
		case "request received":
			received[id]++
		case "request returned":
			returned[id]++
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := map[string]bool{}
	for _, m := range []map[string]int{received, returned, r.answered, r.failed} {
		for id := range m {
			ids[id] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		in, out := received[id], returned[id]
		answered, failed := r.answered[id], r.failed[id]
		switch {
		case answered == 0 && failed == 0:
			t.Errorf("%s: logged %d received and %d returned, of a request no client of the test sent", id, in, out)
		case in != out:
			t.Errorf("%s: logged %d received and %d returned", id, in, out)
		case in < answered || in > answered+failed:
			t.Errorf("%s: logged %s, of %s", id, times(in), requests(answered, failed))
		}
	}
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func requests(answered, failed int) string {
	s := fmt.Sprintf("%d answered", answered)
	if failed > 0 {
		s += fmt.Sprintf(" and %d unanswered", failed)
	}
	return s
}

// newDebugID is an id as logging.NewDebugID makes them, which logging's own
// tests, users of this package, keep it from importing.
func newDebugID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "dbg-" + hex.EncodeToString(b)
}
