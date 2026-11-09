package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/waittest"
)

// nodeOf is a client of a node whose daemon handler is, each connection
// dialed to it on its socket as a stream would be.
func nodeOf(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	socket := serve(t, handler)
	c := New(Options{DebugID: logging.NewDebugID(), Node: "desktop", Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}})
	t.Cleanup(c.Close)
	return c
}

// answer writes v with status and the API version given, none for "".
func answer(w http.ResponseWriter, version string, status int, v any) {
	if version != "" {
		w.Header().Set(api.APIVersionHeader, version)
	}
	w.Header().Set("Content-Type", "application/json")
	if status >= 400 {
		w.Header().Set("Content-Type", "application/problem+json")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestNodeSendsAPIVersion(t *testing.T) {
	t.Parallel()
	var got string
	c := nodeOf(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(api.APIVersionHeader)
		answer(w, strconv.Itoa(api.CurrentVersion), http.StatusOK, api.Version{Name: "wdx", Version: "1.2.3", Commit: "abc"})
	})
	v, err := c.Version(context.Background())
	if err != nil || v.Version != "1.2.3" || got != strconv.Itoa(api.CurrentVersion) || c.APIVersion() != api.CurrentVersion {
		t.Fatalf("version %+v, err %v, sent %q, host's %d", v, err, got, c.APIVersion())
	}
}

func TestNodeOlderHostRefused(t *testing.T) {
	t.Parallel()
	for _, version := range []string{strconv.Itoa(api.OldestCompatible - 1), ""} {
		c := nodeOf(t, func(w http.ResponseWriter, _ *http.Request) {
			answer(w, version, http.StatusOK, api.Version{Name: "wdx"})
		})
		_, err := c.Version(context.Background())
		p, ok := errors.AsType[*ProblemError](err)
		if !ok || p.Problem.Code != api.ErrorCodeNodeVersionUnsupported || p.Problem.Details == nil || (*p.Problem.Details)["update"] != "host" {
			t.Errorf("a host of API version %q: %v, want node_version_unsupported naming the host to update", version, err)
		}
	}
}

func TestNodeProblemPassedOn(t *testing.T) {
	t.Parallel()
	c := nodeOf(t, func(w http.ResponseWriter, _ *http.Request) {
		p := api.NewProblem(api.ErrorCodeRouteNotAllowed, "this node has no GET /v1/version")
		p.Details = &map[string]any{"update": "host"}
		answer(w, strconv.Itoa(api.CurrentVersion), http.StatusForbidden, p)
	})
	_, err := c.Version(context.Background())
	if p, ok := errors.AsType[*ProblemError](err); !ok || p.Problem.Code != api.ErrorCodeRouteNotAllowed {
		t.Fatalf("%v, want the host's route_not_allowed", err)
	}
}

func TestNodeStreamCutIsSessionClosed(t *testing.T) {
	t.Parallel()
	c := nodeOf(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	})
	_, err := c.Version(context.Background())
	if p, ok := errors.AsType[*ProblemError](err); !ok || p.Problem.Code != api.ErrorCodeSessionClosed {
		t.Fatalf("%v, want session_closed", err)
	}
}

// A stream of events holds its stream to the node for as long as it is
// followed, as remote TIDE's does: another request opens a stream of its own
// rather than wait for that one.
func TestNodeRequestBesideOpenStream(t *testing.T) {
	t.Parallel()
	c := nodeOf(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/version" {
			answer(w, strconv.Itoa(api.CurrentVersion), http.StatusOK, api.Version{Name: "wdx", Version: "1.2.3"})
			return
		}
		w.Header().Set(api.APIVersionHeader, strconv.Itoa(api.CurrentVersion))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithDeadline(context.Background(), waittest.Deadline(t))
	defer cancel()
	stream, err := c.ProjectEvents(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if v, err := c.Version(ctx); err != nil || v.Version != "1.2.3" {
		t.Fatalf("version %+v, %v, with a stream of events open: want it answered", v, err)
	}
}

func TestNodeDialErrorPassedOn(t *testing.T) {
	t.Parallel()
	refused := errors.New("refused here")
	c := New(Options{DebugID: logging.NewDebugID(), Node: "desktop", Dial: func(context.Context) (net.Conn, error) { return nil, refused }})
	// Not wrapped in the request's: its message is what the command says.
	if _, err := c.Version(context.Background()); err != refused { //nolint:errorlint // the very error, unwrapped
		t.Fatalf("%v, want the dialer's error as it is", err)
	}
}

func TestConnect(t *testing.T) {
	t.Parallel()
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/hub/nodes/desktop/connect" || r.Header.Get("Upgrade") != api.StreamUpgrade {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(api.DebugIDHeader) == "none" {
			p := api.NewProblem(api.ErrorCodeSessionRequired, "no session into node desktop is open")
			answer(w, "", http.StatusConflict, p)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + api.StreamUpgrade + "\r\n\r\nhello")
		_ = buf.Flush()
	})
	conn, err := New(Options{Socket: socket, DebugID: logging.NewDebugID()}).Connect(context.Background(), "desktop")
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if _, err := conn.Read(b); err != nil || string(b) != "hello" {
		t.Errorf("read %q, %v: want the stream's first bytes", b, err)
	}
	conn.Close()
	_, err = New(Options{Socket: socket, DebugID: "none"}).Connect(context.Background(), "desktop")
	if p, ok := errors.AsType[*ProblemError](err); !ok || p.Problem.Code != api.ErrorCodeSessionRequired {
		t.Fatalf("%v, want session_required", err)
	}
}
