package server_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/waittest"
)

// openStream asks, as client, for a stream to the node hostID.
func openStream(t *testing.T, h *hubtest.Hub, client hubtest.Node, hostID string) *hubapi.OpenStreamResponse {
	t.Helper()
	r, err := h.NodesClient(client).OpenStreamWithResponse(context.Background(), hubapi.OpenStream{NodeId: hostID})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// streamOpened asks, as client, for a stream to host and returns its id.
func streamOpened(t *testing.T, h *hubtest.Hub, client, host hubtest.Node) string {
	t.Helper()
	r := openStream(t, h, client, host.ID)
	if r.JSON201 == nil || r.JSON201.Stream.Node.Id != host.ID || r.JSON201.Stream.Node.Name != host.Name {
		t.Fatalf("open a stream: %d %s", r.StatusCode(), r.Body)
	}
	return r.JSON201.Stream.Id
}

// joined is the outcome of joining a stream.
type joined struct {
	conn *websocket.Conn
	resp *http.Response
	err  error
}

// join joins the stream id as n, in the background: it returns once the
// other node joined too.
func join(t *testing.T, h *hubtest.Hub, n hubtest.Node, id string) <-chan joined {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), waittest.Deadline(t))
	t.Cleanup(cancel)
	done := make(chan joined, 1)
	go func() {
		conn, resp, err := websocket.Dial(ctx, h.NodesURL+"/v1/streams/"+id, &websocket.DialOptions{HTTPClient: h.NodeClient(n.Certificate)})
		if conn != nil {
			t.Cleanup(func() { _ = conn.CloseNow() })
		}
		done <- joined{conn: conn, resp: resp, err: err}
	}()
	return done
}

func (j joined) want(t *testing.T) *websocket.Conn {
	t.Helper()
	if j.err != nil {
		t.Fatalf("join: %v (%v)", j.err, j.resp)
	}
	return j.conn
}

// wantRefused checks the join was refused with status and code.
func (j joined) wantRefused(t *testing.T, what string, status int, want hubapi.ErrorCode) {
	t.Helper()
	if j.err == nil {
		t.Fatalf("%s: joined, want %s", what, want)
	}
	if j.resp == nil {
		t.Fatalf("%s: %v, want %s", what, j.err, want)
	}
	var b bytes.Buffer
	_, _ = b.ReadFrom(j.resp.Body)
	wantProblem(t, what, j.resp.StatusCode, b.Bytes(), status, want)
}

func wait(t *testing.T, c <-chan joined) joined {
	t.Helper()
	select {
	case j := <-c:
		return j
	case <-waittest.After(t):
		t.Fatal("the join did not return")
		return joined{}
	}
}

// twoNodes is a hub with alice's laptop and desktop, the desktop connected.
func twoNodes(t *testing.T, opts ...hubtest.Option) (h *hubtest.Hub, laptop, desktop hubtest.Node, desktopControl control) {
	t.Helper()
	h = hubtest.Start(t, opts...)
	laptop, desktop = h.NewNode(h.User, "laptop"), h.NewNode(h.User, "desktop")
	desktopControl = connect(t, h, desktop, "1")
	desktopControl.want("welcome")
	return h, laptop, desktop, desktopControl
}

// waitLogged waits for the hub to have logged msg n times.
func waitLogged(t *testing.T, h *hubtest.Hub, msg string, n int) {
	t.Helper()
	waittest.For(t, msg, func() bool { return len(h.Logged(msg)) >= n })
}

func TestStreamBothWays(t *testing.T) {
	t.Parallel()
	h, laptop, desktop, ctl := twoNodes(t)
	id := streamOpened(t, h, laptop, desktop)
	waiting := ctl.want("stream_waiting")
	if from, _ := waiting["node"].(map[string]any); waiting["stream"] != id || from["id"] != laptop.ID || from["name"] != "laptop" {
		t.Fatalf("stream_waiting %v", waiting)
	}
	fromLaptop, fromDesktop := join(t, h, laptop, id), join(t, h, desktop, id)
	l, d := wait(t, fromLaptop).want(t), wait(t, fromDesktop).want(t)

	ctx, cancel := context.WithDeadline(context.Background(), waittest.Deadline(t))
	defer cancel()
	up, down := []byte("stream-bytes-up-6f1e2a"), []byte("stream-bytes-down-93bd07")
	h.Secret(string(up), string(down))
	exchange := func(from, to *websocket.Conn, b []byte) {
		t.Helper()
		if err := from.Write(ctx, websocket.MessageBinary, b); err != nil {
			t.Fatal(err)
		}
		typ, got, err := to.Read(ctx)
		if err != nil || typ != websocket.MessageBinary || !bytes.Equal(got, b) {
			t.Fatalf("read %v %q %v, want %q", typ, got, err, b)
		}
	}
	exchange(l, d, up)
	exchange(d, l, down)
	exchange(l, d, up)

	if err := l.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("the desktop read %v, want the stream closed", err)
	}
	waitLogged(t, h, "stream closed", 1)
	closed := h.Logged("stream closed")[0]
	if closed["client_bytes"] != float64(2*len(up)) || closed["host_bytes"] != float64(len(down)) || closed["client_node_id"] != laptop.ID || closed["host_node_id"] != desktop.ID {
		t.Errorf("stream closed %v", closed)
	}
}

func TestStreamOnlyToOwnOnlineNodes(t *testing.T) {
	t.Parallel()
	h, laptop, _, _ := twoNodes(t)
	bob := h.AddUser("bob")
	tower := h.NewNode(bob, "tower")
	connect(t, h, tower, "1").want("welcome")
	tablet := h.NewNode(h.User, "tablet") // never connected

	for _, c := range []struct {
		what, to string
		status   int
		code     hubapi.ErrorCode
	}{
		{"another user's node", tower.ID, http.StatusNotFound, hubapi.ErrorCodeNodeNotFound},
		{"no node", "node-0000", http.StatusNotFound, hubapi.ErrorCodeNodeNotFound},
		{"the caller", laptop.ID, http.StatusNotFound, hubapi.ErrorCodeNodeNotFound},
		{"a node offline", tablet.ID, http.StatusConflict, hubapi.ErrorCodeNodeOffline},
	} {
		r := openStream(t, h, laptop, c.to)
		wantProblem(t, c.what, r.StatusCode(), r.Body, c.status, c.code)
	}
	if n := len(h.Logged("stream asked")); n != 0 {
		t.Errorf("%d streams asked, want none", n)
	}
}

// expiry is a join timeout the test ends when it says.
type expiry chan time.Time

func (e expiry) after(time.Duration) <-chan time.Time { return e }

func TestStreamSecondLate(t *testing.T) {
	t.Parallel()
	expire := make(expiry)
	h, laptop, desktop, _ := twoNodes(t, hubtest.StreamExpiry(expire.after))
	id := streamOpened(t, h, laptop, desktop)
	first := join(t, h, laptop, id)
	waitLogged(t, h, "stream join waiting", 1)
	expire <- time.Now()
	wait(t, first).wantRefused(t, "the first, alone", http.StatusConflict, hubapi.ErrorCodeNodeOffline)
	wait(t, join(t, h, desktop, id)).wantRefused(t, "the second, late", http.StatusNotFound, hubapi.ErrorCodeNotFound)
}

func TestStreamJoinedOnceByEach(t *testing.T) {
	t.Parallel()
	h, laptop, desktop, _ := twoNodes(t)
	tablet := h.NewNode(h.User, "tablet")
	id := streamOpened(t, h, laptop, desktop)
	wait(t, join(t, h, tablet, id)).wantRefused(t, "a third node", http.StatusNotFound, hubapi.ErrorCodeNotFound)
	first := join(t, h, laptop, id)
	waitLogged(t, h, "stream join waiting", 1)
	wait(t, join(t, h, laptop, id)).wantRefused(t, "a second join while waiting", http.StatusNotFound, hubapi.ErrorCodeNotFound)
	second := join(t, h, desktop, id)
	wait(t, first).want(t)
	wait(t, second).want(t)
	wait(t, join(t, h, desktop, id)).wantRefused(t, "a join once open", http.StatusNotFound, hubapi.ErrorCodeNotFound)
	wait(t, join(t, h, tablet, id)).wantRefused(t, "a third node once open", http.StatusNotFound, hubapi.ErrorCodeNotFound)
}

func TestStreamLimit(t *testing.T) {
	t.Parallel()
	expire := make(expiry)
	h, laptop, desktop, _ := twoNodes(t, hubtest.StreamsPerUser(2), hubtest.StreamExpiry(expire.after))
	bob := h.AddUser("bob")
	tower, shed := h.NewNode(bob, "tower"), h.NewNode(bob, "shed")
	connect(t, h, shed, "1").want("welcome")
	streamOpened(t, h, laptop, desktop)
	streamOpened(t, h, laptop, desktop)
	r := openStream(t, h, laptop, desktop.ID)
	wantProblem(t, "a third stream", r.StatusCode(), r.Body, http.StatusTooManyRequests, hubapi.ErrorCodeTooManyStreams)
	streamOpened(t, h, tower, shed) // another user's count is their own

	expire <- time.Now() // one of the three waiting ends
	waitLogged(t, h, "stream expired", 1)
	if exp := h.Logged("stream expired")[0]; exp["user_id"] == bob.ID {
		// bob's expired: alice still has two.
		r := openStream(t, h, laptop, desktop.ID)
		wantProblem(t, "a third stream still", r.StatusCode(), r.Body, http.StatusTooManyRequests, hubapi.ErrorCodeTooManyStreams)
		expire <- time.Now()
		waitLogged(t, h, "stream expired", 2)
	}
	streamOpened(t, h, laptop, desktop)
}

func TestNodeState(t *testing.T) {
	t.Parallel()
	h, laptop, desktop, ctl := twoNodes(t)
	state := func() (remote, code bool) {
		r, err := h.NodesClient(laptop).GetNodeWithResponse(context.Background(), "desktop")
		if err != nil || r.JSON200 == nil {
			t.Fatalf("get: %v %s", err, r.Body)
		}
		return r.JSON200.Node.RemoteAccess, r.JSON200.Node.CodeSetup
	}
	if remote, code := state(); remote || code {
		t.Fatalf("a new node says remote_access %v, code_setup %v", remote, code)
	}
	if err := wsjson.Write(ctl.ctx, ctl.conn, hubapi.ControlState{Type: hubapi.ControlStateTypeState, RemoteAccess: true, CodeSetup: true}); err != nil {
		t.Fatal(err)
	}
	waitLogged(t, h, "node state saved", 1)
	if remote, code := state(); !remote || !code {
		t.Fatalf("remote_access %v, code_setup %v, want both", remote, code)
	}
	list, err := h.NodesClient(laptop).ListNodesWithResponse(context.Background())
	if err != nil || list.JSON200 == nil {
		t.Fatal(err)
	}
	for _, n := range list.JSON200.Nodes {
		if (n.Id == desktop.ID) != (n.RemoteAccess && n.CodeSetup) {
			t.Errorf("listed %+v", n)
		}
	}
	user, err := h.Admin().GetUserWithResponse(context.Background(), hubapi.Username(h.User.Username))
	if err != nil || user.JSON200 == nil {
		t.Fatal(err)
	}
	for _, n := range user.JSON200.User.NodeList {
		if (n.Id == desktop.ID) != (n.RemoteAccess && n.CodeSetup) {
			t.Errorf("the admin's list %+v", n)
		}
	}
}

// joinedStream is a stream from the laptop to the desktop both joined.
func joinedStream(t *testing.T) (h *hubtest.Hub, laptop, desktop hubtest.Node, l, d *websocket.Conn) {
	t.Helper()
	h, laptop, desktop, _ = twoNodes(t)
	id := streamOpened(t, h, laptop, desktop)
	fromLaptop, fromDesktop := join(t, h, laptop, id), join(t, h, desktop, id)
	return h, laptop, desktop, wait(t, fromLaptop).want(t), wait(t, fromDesktop).want(t)
}

// wantCut checks both ends of a stream read that it closed.
func wantCut(t *testing.T, ends ...*websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), waittest.Deadline(t))
	defer cancel()
	for _, end := range ends {
		if _, _, err := end.Read(ctx); err == nil || ctx.Err() != nil {
			t.Errorf("read %v, want the stream cut", err)
		}
	}
}

func TestStreamCutRemovingNode(t *testing.T) {
	t.Parallel()
	h, laptop, desktop, l, d := joinedStream(t)
	h.RemoveNode(laptop, "id:"+desktop.ID)
	wantCut(t, l, d)
}

func TestStreamCutDisablingUser(t *testing.T) {
	t.Parallel()
	h, _, _, l, d := joinedStream(t)
	h.DisableUser(h.User.Username)
	wantCut(t, l, d)
}
