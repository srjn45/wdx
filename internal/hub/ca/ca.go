// Package ca is the hub's certificate authority: an Ed25519 key and its
// self-signed certificate, made at the first start, that sign the node
// endpoint's certificate and, later, each node's.
package ca

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// The files of the CA's directory.
const (
	keyFile      = "ca.key"
	certFile     = "ca.crt"
	nodesKeyFile = "nodes.key"
	nodesCrtFile = "nodes.crt"
)

// commonName names the CA's certificate.
const commonName = "wdx-hub CA"

// NodeValidity is how long a node's certificate lasts.
const NodeValidity = 365 * 24 * time.Hour

// nodeScheme is the scheme of the URI that names a node in its certificate:
// wdx-node:<id>.
const nodeScheme = "wdx-node"

const (
	caValidity    = 10 * 365 * 24 * time.Hour
	nodesValidity = 365 * 24 * time.Hour
	// backdate starts a certificate's validity before it is issued, for a
	// client whose clock is a little behind.
	backdate = time.Hour
)

// localNames are the names of this machine, of which a certificate naming one
// names both, so that a local node reaches the hub by either.
var localNames = []string{"localhost", "127.0.0.1"}

// CA is the hub's certificate authority.
type CA struct {
	dir  string
	now  func() time.Time
	key  ed25519.PrivateKey
	cert *x509.Certificate
}

// Option changes how Load makes and loads the CA.
type Option func(*CA)

// WithClock has the CA read the time from now, as tests do; time.Now
// otherwise.
func WithClock(now func() time.Time) Option {
	return func(c *CA) { c.now = now }
}

// Load returns the CA at dir, making it when its key is missing: an Ed25519
// key and a certificate it signs itself, valid 10 years, each in a file only
// the user may read.
func Load(ctx context.Context, dir string, opts ...Option) (*CA, error) {
	c := &CA{dir: dir, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	log := logging.FromContext(ctx)
	keyPEM, err := os.ReadFile(c.path(keyFile))
	if errors.Is(err, fs.ErrNotExist) {
		if err := c.create(); err != nil {
			return nil, err
		}
		log.Info("ca created", "dir", dir, "not_after", c.cert.NotAfter)
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := c.load(keyPEM); err != nil {
		return nil, fmt.Errorf("load the ca in %s: %w", dir, err)
	}
	log.Info("ca loaded", "dir", dir, "not_after", c.cert.NotAfter)
	return c, nil
}

func (c *CA) path(name string) string { return filepath.Join(c.dir, name) }

// create makes the CA's key and certificate and writes them, the certificate
// first: a key on disk means the CA is whole.
func (c *CA) create() error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	serial, err := serialNumber()
	if err != nil {
		return err
	}
	now := c.now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		return err
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(c.path(certFile), encodeCert(der), 0o600); err != nil {
		return err
	}
	if err := atomicfile.Write(c.path(keyFile), keyPEM, 0o600); err != nil {
		return err
	}
	c.key = key
	c.cert, err = x509.ParseCertificate(der)
	return err
}

// load reads the CA's key from keyPEM and its certificate from its file, and
// checks the one is the other's.
func (c *CA) load(keyPEM []byte) error {
	key, err := decodeKey(keyPEM)
	if err != nil {
		return fmt.Errorf("%s: %w", keyFile, err)
	}
	certPEM, err := os.ReadFile(c.path(certFile))
	if err != nil {
		return err
	}
	cert, err := decodeCert(certPEM)
	if err != nil {
		return fmt.Errorf("%s: %w", certFile, err)
	}
	if pub, ok := cert.PublicKey.(ed25519.PublicKey); !ok || !pub.Equal(key.Public()) || !cert.IsCA {
		return fmt.Errorf("%s is not the certificate of %s", certFile, keyFile)
	}
	c.key, c.cert = key, cert
	return nil
}

// Certificate is the CA's own certificate, which nodes trust the hub by.
func (c *CA) Certificate() *x509.Certificate { return c.cert }

// Pool holds the CA's certificate alone, to verify what it issued.
func (c *CA) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.cert)
	return pool
}

// Request is what a certificate Issue signs says of its holder.
type Request struct {
	CommonName  string
	DNSNames    []string
	IPAddresses []net.IP
	URIs        []*url.URL
	// ExtKeyUsage says what the certificate is for: a server's, a client's.
	ExtKeyUsage []x509.ExtKeyUsage
	Validity    time.Duration
}

// Issue signs a certificate for pub as r asks, valid from now for
// r.Validity, and returns it in DER.
func (c *CA) Issue(pub crypto.PublicKey, r Request) ([]byte, error) {
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := c.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: r.CommonName},
		NotBefore:    now.Add(-backdate),
		NotAfter:     now.Add(r.Validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  r.ExtKeyUsage,
		DNSNames:     r.DNSNames,
		IPAddresses:  r.IPAddresses,
		URIs:         r.URIs,
	}
	return x509.CreateCertificate(rand.Reader, tmpl, c.cert, pub, c.key)
}

// IssueNode signs the certificate of the node nodeID for its key pub: a
// client's, naming the node as URI:wdx-node:<id>, valid NodeValidity from
// now. It returns it in PEM, with when it ends.
func (c *CA) IssueNode(pub ed25519.PublicKey, nodeID string) (certPEM []byte, notAfter time.Time, err error) {
	der, err := c.Issue(pub, Request{
		CommonName:  nodeID,
		URIs:        []*url.URL{{Scheme: nodeScheme, Opaque: nodeID}},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		Validity:    NodeValidity,
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, time.Time{}, err
	}
	return encodeCert(der), cert.NotAfter, nil
}

// NodeID is the id of the node cert names, ok false when it names none.
func NodeID(cert *x509.Certificate) (id string, ok bool) {
	for _, u := range cert.URIs {
		if u.Scheme == nodeScheme && u.Opaque != "" {
			return u.Opaque, true
		}
	}
	return "", false
}

// CertificatePEM is the CA's own certificate in PEM, as nodes are given it.
func (c *CA) CertificatePEM() []byte { return encodeCert(c.cert.Raw) }

// NodeEndpoint returns the node endpoint's key and certificate for host, the
// host of public_url, which the CA issued: valid a year, its common name host
// and its names host, or localhost and 127.0.0.1 both when it is either. It
// loads them from the CA's directory, and issues them anew when they are
// missing or unreadable, name another host or another CA, or two thirds of
// their life have passed; never otherwise.
func (c *CA) NodeEndpoint(ctx context.Context, host string) (tls.Certificate, error) {
	log := logging.FromContext(ctx)
	cert, reason := c.loadNodeEndpoint(host)
	if reason == "" {
		log.Info("node endpoint certificate loaded", "host", host, "not_after", cert.Leaf.NotAfter)
		return cert, nil
	}
	cert, err := c.issueNodeEndpoint(host)
	if err != nil {
		return tls.Certificate{}, err
	}
	log.Info("node endpoint certificate issued", "host", host, "reason", reason, "not_after", cert.Leaf.NotAfter)
	return cert, nil
}

// loadNodeEndpoint returns the node endpoint's certificate on disk, or why it
// must be issued anew.
func (c *CA) loadNodeEndpoint(host string) (tls.Certificate, string) {
	cert, err := tls.LoadX509KeyPair(c.path(nodesCrtFile), c.path(nodesKeyFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return cert, "missing"
	case err != nil:
		return cert, "unreadable: " + err.Error()
	}
	leaf := cert.Leaf
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.Pool(), CurrentTime: leaf.NotBefore, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return cert, "not issued by this ca"
	}
	if !slices.Equal(leaf.DNSNames, dnsNames(host)) || !slices.EqualFunc(leaf.IPAddresses, ipAddresses(host), net.IP.Equal) {
		return cert, "names another host"
	}
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	if !c.now().Before(leaf.NotBefore.Add(life * 2 / 3)) {
		return cert, "two thirds of its life passed"
	}
	return cert, ""
}

// issueNodeEndpoint issues the node endpoint's key and certificate for host
// and writes them, the key first: a certificate whose key is not the one on
// disk is issued anew.
func (c *CA) issueNodeEndpoint(host string) (tls.Certificate, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	der, err := c.Issue(pub, Request{
		CommonName:  host,
		DNSNames:    dnsNames(host),
		IPAddresses: ipAddresses(host),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		Validity:    nodesValidity,
	})
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	if err := atomicfile.Write(c.path(nodesKeyFile), keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := atomicfile.Write(c.path(nodesCrtFile), encodeCert(der), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(encodeCert(der), keyPEM)
}

// names are the names a certificate for host holds: host, or both local
// names when it is one.
func names(host string) []string {
	if slices.Contains(localNames, host) {
		return localNames
	}
	return []string{host}
}

func dnsNames(host string) []string {
	var dns []string
	for _, n := range names(host) {
		if net.ParseIP(n) == nil {
			dns = append(dns, n)
		}
	}
	return dns
}

func ipAddresses(host string) []net.IP {
	var ips []net.IP
	for _, n := range names(host) {
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

// serialNumber is a random serial of 128 bits.
func serialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func encodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func decodeCert(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate in PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func encodeKey(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func decodeKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("no private key in PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the key is %T, not Ed25519", key)
	}
	return ed, nil
}
