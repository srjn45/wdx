package logtest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errorer records what Want reported.
type errorer struct{ errors []string }

func (e *errorer) Helper() {}

func (e *errorer) Errorf(format string, args ...any) {
	e.errors = append(e.errors, fmt.Sprintf(format, args...))
}

func lines(ids ...string) []map[string]any {
	var lines []map[string]any
	for _, id := range ids {
		lines = append(lines, map[string]any{"msg": "request received", "debug_id": id}, map[string]any{"msg": "request returned", "debug_id": id})
	}
	return lines
}

func TestRequests(t *testing.T) {
	t.Parallel()
	// The server names its own id for the second request, as the hub's pages
	// do, and answers under the one sent otherwise.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := req.Header.Get("Wdx-Debug-Id")
		if req.URL.Path == "/page" {
			id = "dbg-000000000001"
		}
		w.Header().Set("Wdx-Debug-Id", id)
	}))
	t.Cleanup(server.Close)
	r := NewRequests("Wdx-Debug-Id")
	c := r.Client(nil)
	var sent []string
	for _, path := range []string{"/api", "/page"} {
		resp, err := c.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		sent = append(sent, resp.Header.Get("Wdx-Debug-Id"))
	}
	if !strings.HasPrefix(sent[0], "dbg-") || len(sent[0]) != len("dbg-000000000000") || sent[1] != "dbg-000000000001" {
		t.Fatalf("unexpected ids %q", sent)
	}
	server.Close()
	if _, err := c.Get(server.URL + "/api"); err == nil {
		t.Fatal("want a request to a closed server failing")
	}
	unanswered := ""
	for id := range r.failed {
		unanswered = id
	}

	for _, tc := range []struct {
		name  string
		lines []map[string]any
		want  string
	}{
		{"each once", lines(sent[0], sent[1]), ""},
		{"the unanswered one too", lines(sent[0], sent[1], unanswered), ""},
		{"a pair missing", lines(sent[0]), sent[1] + ": logged 0 times, of 1 answered"},
		{"no lines at all", nil, sent[1] + ": logged 0 times, of 1 answered\n" + sent[0] + ": logged 0 times, of 1 answered"},
		{"a pair twice", lines(sent[0], sent[1], sent[1]), sent[1] + ": logged 2 times, of 1 answered"},
		{"the unanswered one twice", lines(sent[0], sent[1], unanswered, unanswered), unanswered + ": logged 2 times, of 0 answered and 1 unanswered"},
		{"a returned line missing", append(lines(sent[0], sent[1]), map[string]any{"msg": "request received", "debug_id": sent[0]}), sent[0] + ": logged 2 received and 1 returned"},
		{"a request no client sent", lines(sent[0], sent[1], "dbg-000000000002"), "dbg-000000000002: logged 1 received and 1 returned, of a request no client of the test sent"},
	} {
		var e errorer
		r.Want(&e, tc.lines)
		if got := strings.Join(e.errors, "\n"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRequestsServed(t *testing.T) {
	t.Parallel()
	r := NewRequests("Wdx-Debug-Id")
	var id string
	h := r.Handler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id = req.Header.Get("Wdx-Debug-Id")
		w.Header().Set("Wdx-Debug-Id", id)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	var e errorer
	r.Want(&e, nil)
	if want := id + ": logged 0 times, of 1 answered"; id == "" || strings.Join(e.errors, "\n") != want {
		t.Fatalf("got %q, want %q", e.errors, want)
	}
}
