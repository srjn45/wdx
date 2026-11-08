package ca

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// start is when the tests' clocks begin.
var start = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// clock is a time a test moves on.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// logged is a context whose logger writes to the buffer returned.
func logged() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	return logging.NewContext(context.Background(), logging.New(&buf, slog.LevelInfo)), &buf
}

func load(t *testing.T, dir string, clk *clock) (*CA, string) {
	t.Helper()
	ctx, log := logged()
	c, err := Load(ctx, dir, WithClock(clk.Now))
	if err != nil {
		t.Fatal(err)
	}
	return c, log.String()
}

func nodeEndpoint(t *testing.T, c *CA, host string) (tls.Certificate, string) {
	t.Helper()
	ctx, log := logged()
	cert, err := c.NodeEndpoint(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	return cert, log.String()
}

func TestMadeOnceLoadedAfter(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ca")
	clk := &clock{now: start}
	made, log := load(t, dir, clk)
	if !strings.Contains(log, `"msg":"ca created"`) {
		t.Fatalf("want ca created logged, got %s", log)
	}
	cert := made.Certificate()
	if cert.Subject.CommonName != "wdx-hub CA" || !cert.IsCA || !cert.NotAfter.Equal(start.Add(10*365*24*time.Hour).Truncate(time.Second)) {
		t.Fatalf("CA certificate %s, CA %v, until %v", cert.Subject, cert.IsCA, cert.NotAfter)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Fatalf("want it self-signed: %v", err)
	}
	if _, ok := cert.PublicKey.(ed25519.PublicKey); !ok {
		t.Fatalf("want an Ed25519 key, got %T", cert.PublicKey)
	}

	clk.set(start.Add(time.Hour))
	loaded, log := load(t, dir, clk)
	if strings.Contains(log, "ca created") || !strings.Contains(log, `"msg":"ca loaded"`) {
		t.Fatalf("want the CA loaded, not made, got %s", log)
	}
	if !loaded.Certificate().Equal(cert) || !loaded.key.Equal(made.key) {
		t.Fatal("want the same CA loaded")
	}
}

func TestFilesOnlyTheUsers(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ca")
	c, _ := load(t, dir, &clock{now: start})
	nodeEndpoint(t, c, "hub.example.com")
	for _, name := range []string{"ca.key", "ca.crt", "nodes.key", "nodes.crt"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v, want 0600", name, info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Errorf("want the four files alone, got %v", entries)
	}
}

func TestBrokenCARefused(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ca")
	load(t, dir, &clock{now: start})
	other := filepath.Join(t.TempDir(), "other")
	load(t, other, &clock{now: start})
	crt, err := os.ReadFile(filepath.Join(other, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), crt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "is not the certificate of") {
		t.Fatalf("want another CA's certificate refused, got %v", err)
	}
}

// verifyServer checks cert verifies against c for host, at time at.
func verifyServer(cert *x509.Certificate, c *CA, host string, at time.Time) error {
	_, err := cert.Verify(x509.VerifyOptions{Roots: c.Pool(), DNSName: host, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

func TestNodeEndpointNamesTheHost(t *testing.T) {
	t.Parallel()
	c, _ := load(t, filepath.Join(t.TempDir(), "ca"), &clock{now: start})
	cert, log := nodeEndpoint(t, c, "hub.example.com")
	if !strings.Contains(log, `"msg":"node endpoint certificate issued"`) || !strings.Contains(log, `"reason":"missing"`) {
		t.Fatalf("want it issued for being missing, got %s", log)
	}
	leaf := cert.Leaf
	if leaf.Subject.CommonName != "hub.example.com" || !slices.Equal(leaf.DNSNames, []string{"hub.example.com"}) || len(leaf.IPAddresses) != 0 {
		t.Fatalf("names %s, %v, %v", leaf.Subject, leaf.DNSNames, leaf.IPAddresses)
	}
	if !leaf.NotAfter.Equal(start.Add(365 * 24 * time.Hour).Truncate(time.Second)) {
		t.Errorf("valid until %v, want a year", leaf.NotAfter)
	}
	if err := verifyServer(leaf, c, "hub.example.com", start); err != nil {
		t.Fatalf("want it verified against the CA: %v", err)
	}
	if err := verifyServer(leaf, c, "other.example.com", start); err == nil {
		t.Fatal("want it refused for another host")
	}
	other, _ := load(t, filepath.Join(t.TempDir(), "other"), &clock{now: start})
	if err := verifyServer(leaf, other, "hub.example.com", start); err == nil {
		t.Fatal("want it refused by another CA")
	}
}

// A local host names both, so that a node reaches the hub by either.
func TestNodeEndpointLocal(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"localhost", "127.0.0.1"} {
		c, _ := load(t, filepath.Join(t.TempDir(), "ca"), &clock{now: start})
		cert, _ := nodeEndpoint(t, c, host)
		leaf := cert.Leaf
		if leaf.Subject.CommonName != host || !slices.Equal(leaf.DNSNames, []string{"localhost"}) || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
			t.Errorf("%s: names %s, %v, %v", host, leaf.Subject, leaf.DNSNames, leaf.IPAddresses)
		}
		for _, name := range []string{"localhost", "127.0.0.1"} {
			if err := verifyServer(leaf, c, name, start); err != nil {
				t.Errorf("%s: want it verified for %s: %v", host, name, err)
			}
		}
	}
}

// The node endpoint's certificate is loaded until two thirds of its life have
// passed, then issued anew.
func TestNodeEndpointReissuedAtTwoThirds(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ca")
	clk := &clock{now: start}
	c, _ := load(t, dir, clk)
	first, _ := nodeEndpoint(t, c, "hub.example.com")
	leaf := first.Leaf
	twoThirds := leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) * 2 / 3)

	for _, at := range []time.Time{start, start.Add(24 * time.Hour), twoThirds.Add(-time.Second)} {
		clk.set(at)
		c, _ := load(t, dir, clk)
		cert, log := nodeEndpoint(t, c, "hub.example.com")
		if !cert.Leaf.Equal(leaf) || !strings.Contains(log, `"msg":"node endpoint certificate loaded"`) {
			t.Fatalf("at %v: want the certificate loaded, got %s", at, log)
		}
	}

	clk.set(twoThirds)
	c, _ = load(t, dir, clk)
	renewed, log := nodeEndpoint(t, c, "hub.example.com")
	if renewed.Leaf.Equal(leaf) || !strings.Contains(log, `"reason":"two thirds of its life passed"`) {
		t.Fatalf("at two thirds: want it issued anew, got %s", log)
	}
	if !renewed.Leaf.NotAfter.Equal(twoThirds.Add(365 * 24 * time.Hour).Truncate(time.Second)) {
		t.Errorf("renewed until %v", renewed.Leaf.NotAfter)
	}
	again, log := nodeEndpoint(t, c, "hub.example.com")
	if !again.Leaf.Equal(renewed.Leaf) || !strings.Contains(log, "certificate loaded") {
		t.Fatalf("want the renewed certificate kept, got %s", log)
	}
}

// A changed public_url gets a certificate for its host.
func TestNodeEndpointReissuedForAnotherHost(t *testing.T) {
	t.Parallel()
	c, _ := load(t, filepath.Join(t.TempDir(), "ca"), &clock{now: start})
	first, _ := nodeEndpoint(t, c, "hub.example.com")
	moved, log := nodeEndpoint(t, c, "new.example.com")
	if moved.Leaf.Equal(first.Leaf) || moved.Leaf.Subject.CommonName != "new.example.com" || !strings.Contains(log, `"reason":"names another host"`) {
		t.Fatalf("want it issued for the new host, got %s", log)
	}
}

// A node's certificate verifies as a client's against the CA, with the URI
// that names the node.
func TestIssueClientCertificate(t *testing.T) {
	t.Parallel()
	clk := &clock{now: start}
	c, _ := load(t, filepath.Join(t.TempDir(), "ca"), clk)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	node := &url.URL{Scheme: "wdx-node", Opaque: "node-1"}
	der, err := c.Issue(pub, Request{CommonName: "desktop", URIs: []*url.URL{node}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, Validity: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	opts := x509.VerifyOptions{Roots: c.Pool(), CurrentTime: start, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := cert.Verify(opts); err != nil {
		t.Fatalf("want it verified as a client: %v", err)
	}
	if len(cert.URIs) != 1 || cert.URIs[0].String() != "wdx-node:node-1" || !cert.PublicKey.(ed25519.PublicKey).Equal(pub) {
		t.Fatalf("names %v for key %v", cert.URIs, cert.PublicKey)
	}
	opts.CurrentTime = start.Add(2 * time.Hour)
	if _, err := cert.Verify(opts); err == nil {
		t.Fatal("want it expired after its validity")
	}
}
