package hubtest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
)

// Node is a machine made a node of an account by a device login, and what
// it keeps: its key, its certificate and the hub's credentials.
type Node struct {
	ID, Name    string
	Key         ed25519.PrivateKey
	Certificate tls.Certificate
	Credentials hubapi.NodeCredentials
}

// WebClient is the hub's API on the web endpoint.
func (h *Hub) WebClient() *hubapi.ClientWithResponses {
	h.t.Helper()
	c, err := hubapi.NewClientWithResponses(h.URL, hubapi.WithHTTPClient(h.Client(nil)))
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

// NodesClient is the hub's API on the node endpoint, as n.
func (h *Hub) NodesClient(n Node) *hubapi.ClientWithResponses {
	h.t.Helper()
	c, err := hubapi.NewClientWithResponses(h.NodesURL, hubapi.WithHTTPClient(h.NodeClient(n.Certificate)))
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

// NewNode makes a machine a node of u named name, by the device flow: it
// starts the login, approves it in a browser of u's and polls once for the
// credentials.
func (h *Hub) NewNode(u User, name string) Node {
	h.t.Helper()
	t := h.t
	ctx := context.Background()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	web := h.WebClient()
	started, err := web.StartNodeLoginWithResponse(ctx, hubapi.StartNodeLogin{
		Flow: hubapi.NodeLoginFlowDevice, Name: name, Os: "linux/amd64", WdxVersion: "0.5.0", PublicKey: pub,
	})
	if err != nil || started.JSON201 == nil {
		t.Fatalf("hubtest: start a login: %v %d %s", err, started.StatusCode(), started.Body)
	}
	device := *started.JSON201.DeviceCode
	h.Secret(device, *started.JSON201.UserCode)
	if err := h.Browser(u).ApproveCode(*started.JSON201.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	creds, err := web.PollNodeLoginWithResponse(ctx, hubapi.PollNodeLogin{DeviceCode: device})
	if err != nil || creds.StatusCode() != http.StatusOK || creds.JSON200 == nil {
		t.Fatalf("hubtest: poll: %v %d %s", err, creds.StatusCode(), creds.Body)
	}
	c := *creds.JSON200
	return Node{ID: c.Node.Id, Name: c.Node.Name, Key: key, Certificate: Certificate(t, c.Certificate, key), Credentials: c}
}

// Certificate is the node certificate certPEM with its key.
func Certificate(t *testing.T, certPEM string, key ed25519.PrivateKey) tls.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatalf("hubtest: no certificate in %q", certPEM)
	}
	return tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key}
}

// Admin is the hub's API on its admin socket, as its operator's commands
// call it.
func (h *Hub) Admin() *hubapi.ClientWithResponses {
	h.t.Helper()
	socket := h.Server.Addresses().Admin
	hc := h.Client(&http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}})
	c, err := hubapi.NewClientWithResponses("http://wdx-hub", hubapi.WithHTTPClient(hc))
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

// RemoveNode removes the node of n's account ref names, as n, the way wdx hub
// node remove on n does.
func (h *Hub) RemoveNode(n Node, ref string) {
	h.t.Helper()
	r, err := h.NodesClient(n).RemoveNodeWithResponse(context.Background(), ref)
	if err != nil {
		h.t.Fatalf("hubtest: remove node %s: %v", ref, err)
	}
	if r.JSON200 == nil {
		h.t.Fatalf("hubtest: remove node %s: %d %s", ref, r.StatusCode(), r.Body)
	}
}

// DisableUser disables username, as the hub's admin does.
func (h *Hub) DisableUser(username string) {
	h.t.Helper()
	r, err := h.Admin().UpdateUserWithResponse(context.Background(), hubapi.Username(username), hubapi.UpdateUser{Disabled: true})
	if err != nil {
		h.t.Fatalf("hubtest: disable %s: %v", username, err)
	}
	if r.StatusCode() != http.StatusOK {
		h.t.Fatalf("hubtest: disable %s: %d %s", username, r.StatusCode(), r.Body)
	}
}

// PushDoc pushes, as n, the first revision of a doc with id named name in the
// repository repoKey, holding text, as wdx on n does.
func (h *Hub) PushDoc(n Node, id, repoKey, name, text string) {
	h.t.Helper()
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write([]byte(text)); err != nil {
		h.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		h.t.Fatal(err)
	}
	kind, now, rev, content := hubapi.DocKindDoc, h.Clock.Now(), 1, gz.Bytes()
	empty, format, status := "", "md", "wip"
	r, err := h.NodesClient(n).PushDocChangesWithResponse(context.Background(), hubapi.DocChanges{Changes: []hubapi.DocChange{{
		Seq: 1, DocId: id, Change: hubapi.DocChangeTypeRevision, RepoKey: &repoKey, Kind: &kind, Name: &name, Folder: &empty,
		Format: &format, Labels: &[]string{}, Status: &status, CreatedAt: &now, UpdatedAt: &now, Revision: &rev, Content: &content,
	}}})
	if err != nil || r.JSON200 == nil || len(r.JSON200.Results) != 1 || r.JSON200.Results[0].Status != hubapi.DocChangeResultStatusApplied {
		h.t.Fatalf("hubtest: push doc %s: %v %d %s", id, err, r.StatusCode(), r.Body)
	}
}

// LogIn makes a machine a node of u named name, as NewNode does, and writes
// its login into home as wdx hub login does, in <home>/hub/: a link, or a
// daemon, opened on home after is logged in to the hub as that node. Several
// homes logged in, of one user or of several, are as many machines.
func (h *Hub) LogIn(home string, u User, name string) Node {
	h.t.Helper()
	t := h.t
	n := h.NewNode(u, name)
	der, err := x509.MarshalPKCS8PrivateKey(n.Key)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	h.Secret(strings.Split(string(key), "\n")[1])
	login, err := json.MarshalIndent(map[string]any{
		"url": h.URL, "nodes_url": n.Credentials.NodesUrl, "user": n.Credentials.User,
		"node": map[string]string{"id": n.ID, "name": n.Name},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := identity.HubDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// login.json last, as without it there is no login.
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"node.key", key},
		{"node.crt", []byte(n.Credentials.Certificate)},
		{"hub-ca.crt", []byte(n.Credentials.CaCertificate)},
		{"login.json", append(login, '\n')},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return n
}

// Connect opens n's control connection, as a node that says hello and then
// nothing, reading what the hub sends until the connection closes: online,
// it answers no stream asked of it. It closes when the test ends, or at the
// close returned.
func (h *Hub) Connect(n Node) (closeNow func()) {
	h.t.Helper()
	t := h.t
	ctx, cancel := context.WithCancel(context.Background())
	//nolint:bodyclose // the websocket closes it itself
	conn, _, err := websocket.Dial(ctx, h.NodesURL+"/v1/control", &websocket.DialOptions{HTTPClient: h.NodeClient(n.Certificate)})
	if err != nil {
		cancel()
		t.Fatalf("hubtest: connect %s: %v", n.Name, err)
	}
	if err := wsjson.Write(ctx, conn, hubapi.ControlHello{Type: hubapi.ControlHelloTypeHello, ApiVersion: "1", WdxVersion: "0.5.0", Os: "linux/amd64"}); err != nil {
		cancel()
		t.Fatalf("hubtest: hello %s: %v", n.Name, err)
	}
	var welcome hubapi.ControlWelcome
	if err := wsjson.Read(ctx, conn, &welcome); err != nil || welcome.Type != hubapi.ControlWelcomeTypeWelcome {
		cancel()
		t.Fatalf("hubtest: welcome %s: %v %+v", n.Name, err, welcome)
	}
	read := make(chan struct{})
	go func() {
		defer close(read)
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	closeNow = sync.OnceFunc(func() {
		cancel()
		_ = conn.CloseNow()
		<-read
	})
	t.Cleanup(closeNow)
	return closeNow
}
