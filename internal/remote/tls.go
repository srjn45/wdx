package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"time"
)

// exporterLabel is the TLS exporter's label for the value CPace and the MACs
// bind a session to: the one TLS channel both nodes are in.
const exporterLabel = "EXPORTER-wdx-remote-session"

// certificate is a self-signed certificate of key, which TLS between two
// nodes presents: the key is what counts, never who signed it.
func certificate(key ed25519.PrivateKey) (tls.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "wdx node"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// config is TLS 1.3 between two nodes, presenting cert. The other node's
// certificate is checked by its key, not a chain: VerifyConnection takes an
// Ed25519 key and the handshake proves the peer holds it, then the key is
// checked against the pin, or, on a session's first stream, proven through
// CPace. So the chain check that InsecureSkipVerify turns off, which would
// need a CA, is replaced, never skipped.
func config(cert tls.Certificate, server bool) *tls.Config {
	c := &tls.Config{
		MinVersion:       tls.VersionTLS13,
		Certificates:     []tls.Certificate{cert},
		VerifyConnection: func(cs tls.ConnectionState) error { _, err := peerKey(cs); return err },
	}
	if server {
		c.ClientAuth = tls.RequireAnyClientCert
	} else {
		c.InsecureSkipVerify = true //nolint:gosec // the peer is verified by its pinned key or CPace, in VerifyConnection and after
		c.ServerName = "wdx-node"
	}
	return c
}

// peerKey is the other node's key, from its one certificate.
func peerKey(cs tls.ConnectionState) (ed25519.PublicKey, error) {
	if len(cs.PeerCertificates) != 1 {
		return nil, errors.New("the other node did not present one certificate")
	}
	pub, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("the other node's key is not Ed25519")
	}
	return pub, nil
}

// exporter is the connection's TLS exporter value for the session's binding.
func exporter(cs tls.ConnectionState) ([]byte, error) {
	return cs.ExportKeyingMaterial(exporterLabel, nil, 32)
}
