package hublink

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// The files of <home>/hub/: the directory only the user may enter, and each
// file only the user may read.
const (
	dirMode  = 0o700
	fileMode = 0o600

	loginFile = "login.json"
	keyFile   = "node.key"
	certFile  = "node.crt"
	caFile    = "hub-ca.crt"
)

// loginJSON is login.json, written last: without it there is no login,
// whatever else the directory holds.
type loginJSON struct {
	URL      string `json:"url"`
	NodesURL string `json:"nodes_url"`
	User     string `json:"user"`
	Node     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"node"`
}

// credentials are a login: what login.json says, the node's key and
// certificate and the hub's CA.
type credentials struct {
	URL, NodesURL, User string
	Node                NodeRef
	Key                 ed25519.PrivateKey
	CA                  *x509.CertPool
	CAPEM               []byte

	mu   sync.Mutex
	cert tls.Certificate // replaced when renewed
}

func (c *credentials) certificate() tls.Certificate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cert
}

func (c *credentials) setCertificate(cert tls.Certificate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cert = cert
}

// expiresAt is when the node's certificate ends.
func (c *credentials) expiresAt() time.Time {
	return c.certificate().Leaf.NotAfter
}

// readLogin reads the login in dir, nil when there is none; then it removes
// what is left in dir of a login that never finished being written.
func readLogin(ctx context.Context, dir string) (*credentials, error) {
	b, err := os.ReadFile(filepath.Join(dir, loginFile))
	if errors.Is(err, fs.ErrNotExist) {
		if err := removeLeftovers(ctx, dir); err != nil {
			logging.FromContext(ctx).Error("hub login leftovers not removed", "path", dir, "err", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lj loginJSON
	if err := json.Unmarshal(b, &lj); err != nil {
		return nil, fmt.Errorf("%s: %w", loginFile, err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, keyFile))
	if err != nil {
		return nil, err
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyFile, err)
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, certFile))
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, caFile))
	if err != nil {
		return nil, err
	}
	c := &credentials{URL: lj.URL, NodesURL: lj.NodesURL, User: lj.User, Node: NodeRef{ID: lj.Node.ID, Name: lj.Node.Name}, Key: key}
	if err := c.check(certPEM, caPEM); err != nil {
		return nil, err
	}
	return c, nil
}

// removeLeftovers removes dir, which holds no login.
func removeLeftovers(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("hub login leftovers removed", "path", dir, "files", len(entries))
	return nil
}

// check sets the hub's CA from caPEM and the node's certificate from
// certPEM, which must be for the node's key, issued by that CA, and name
// https nodes_url.
func (c *credentials) check(certPEM, caPEM []byte) error {
	if u, err := url.Parse(c.NodesURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("the node endpoint %q is not https://<host>:<port>", c.NodesURL)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("no CA certificate in hub-ca.crt")
	}
	cert, err := parseCertificate(certPEM, c.Key)
	if err != nil {
		return err
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: cert.Leaf.NotBefore}); err != nil {
		return fmt.Errorf("the node's certificate is not the hub CA's: %w", err)
	}
	c.CA, c.CAPEM, c.cert = pool, caPEM, cert
	return nil
}

// parseCertificate is the certificate in certPEM, with key, which must be
// the certificate's.
func parseCertificate(certPEM []byte, key ed25519.PrivateKey) (tls.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return tls.Certificate{}, errors.New("no certificate in PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return tls.Certificate{}, err
	}
	pub, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok || !pub.Equal(key.Public()) {
		return tls.Certificate{}, errors.New("the certificate is not for this node's key")
	}
	return tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key, Leaf: leaf}, nil
}

func parseKey(b []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("no private key in PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("the key is not Ed25519")
	}
	return key, nil
}

func keyPEM(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func certPEM(cert tls.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
}

// write writes c to dir, login.json last, so that a crash part way leaves
// no login.
func (c *credentials) write(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	// MkdirAll leaves a directory that was there as it was.
	if err := os.Chmod(dir, dirMode); err != nil {
		return err
	}
	key, err := keyPEM(c.Key)
	if err != nil {
		return err
	}
	var lj loginJSON
	lj.URL, lj.NodesURL, lj.User, lj.Node.ID, lj.Node.Name = c.URL, c.NodesURL, c.User, c.Node.ID, c.Node.Name
	login, err := json.MarshalIndent(lj, "", "  ")
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{
		{keyFile, key},
		{certFile, certPEM(c.certificate())},
		{caFile, c.CAPEM},
		{loginFile, append(login, '\n')},
	} {
		if err := atomicfile.Write(filepath.Join(dir, f.name), f.data, fileMode); err != nil {
			return err
		}
	}
	logging.FromContext(ctx).Info("hub login written", "path", dir, "node_id", c.Node.ID)
	return nil
}

// writeCertificate replaces the node's certificate in dir with cert.
func writeCertificate(dir string, cert tls.Certificate) error {
	return atomicfile.Write(filepath.Join(dir, certFile), certPEM(cert), fileMode)
}
