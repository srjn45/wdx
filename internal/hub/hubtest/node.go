package hubtest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"net"
	"net/http"
	"testing"

	"github.com/srjn45/wdx/internal/hubapi"
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
	c, err := hubapi.NewClientWithResponses(h.URL)
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
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
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
