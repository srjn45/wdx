package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/srjn45/wdx/internal/hub/hubconfig"
)

// selfSigned writes a certificate for 127.0.0.1 that signs itself, and its
// key, in PEM to dir, and returns it with their paths.
func selfSigned(t *testing.T, dir string) (cert *x509.Certificate, certPath, keyPath string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "the test's"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	if cert, err = x509.ParseCertificate(der); err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "web.crt"), filepath.Join(dir, "web.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, certPath, keyPath
}

func TestWebTLSFiles(t *testing.T) {
	t.Parallel()
	cert, certPath, keyPath := selfSigned(t, t.TempDir())
	th := start(t, func(o *Options) {
		o.Config.WebTLS, o.Config.WebTLSCert, o.Config.WebTLSKey = hubconfig.TLSFiles, certPath, keyPath
	})
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	hc := th.requests.Client(&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}})
	resp, b := do(t, hc, http.MethodGet, "https://"+th.addrs.Web+"/v1/version", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("version: %d %s", resp.StatusCode, b)
	}
	if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 || !resp.TLS.PeerCertificates[0].Equal(cert) {
		t.Errorf("want TLS 1.3 with the test's certificate, got %+v", resp.TLS)
	}
}

// collector receives metrics over OTLP/HTTP as an OpenTelemetry collector
// does, keeping the last send.
type collector struct {
	mu   sync.Mutex
	last *colmetricpb.ExportMetricsServiceRequest
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	req := &colmetricpb.ExportMetricsServiceRequest{}
	if r.URL.Path != "/v1/metrics" || proto.Unmarshal(b, req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.last = req
	c.mu.Unlock()
}

// requestCounts returns the count of wdx.hub.request.duration in the last
// send by its labels, written as "code=not_found endpoint=web status=404".
func (c *collector) requestCounts(t *testing.T) map[string]uint64 {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		t.Fatal("no metrics sent")
	}
	counts := map[string]uint64{}
	for _, rm := range c.last.ResourceMetrics {
		if service := labels(rm.Resource.Attributes); !strings.Contains(service, "service.name=wdx-hub ") {
			t.Errorf("want the metrics sent as service wdx-hub, got %s", service)
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "wdx.hub.request.duration" {
					continue
				}
				if m.Unit != "s" {
					t.Errorf("want the duration in seconds, got %q", m.Unit)
				}
				for _, p := range m.GetHistogram().DataPoints {
					counts[labels(p.Attributes)] = p.Count
				}
			}
		}
	}
	return counts
}

func labels(kvs []*commonpb.KeyValue) string {
	var parts []string
	for _, kv := range kvs {
		v := kv.Value.GetStringValue()
		if _, ok := kv.Value.Value.(*commonpb.AnyValue_IntValue); ok {
			v = strconv.FormatInt(kv.Value.GetIntValue(), 10)
		}
		parts = append(parts, kv.Key+"="+v)
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

func TestRequestsMeasured(t *testing.T) {
	t.Parallel()
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	// The interval never passes: the hub sends what it measured when it
	// stops.
	th := start(t, func(o *Options) { o.Config.OtelEndpoint, o.Config.OtelInterval = srv.URL, time.Hour })
	ctx := context.Background()
	for range 2 {
		if r, err := th.webClient(t).GetVersionWithResponse(ctx); err != nil || r.StatusCode() != http.StatusOK {
			t.Fatalf("version: %v %v", r, err)
		}
	}
	if r, err := th.adminClient(t).GetAdminVersionWithResponse(ctx); err != nil || r.StatusCode() != http.StatusOK {
		t.Fatalf("admin version: %v %v", r, err)
	}
	do(t, th.web, http.MethodGet, "http://"+th.addrs.Web+"/v1/admin/version", "", nil)
	do(t, th.admin, http.MethodPost, "http://wdx-hub/v1/backup", `{"nope": 1}`, nil)
	if err := th.stop(); err != nil {
		t.Fatal(err)
	}

	want := map[string]uint64{
		"endpoint=web operation=getVersion status=200":        2,
		"endpoint=admin operation=getAdminVersion status=200": 1,
		// Another endpoint's operation is none of this one's.
		"code=not_found endpoint=web status=404": 1,
		// A request the spec does not allow is still its operation's.
		"code=usage endpoint=admin operation=backup status=400": 1,
	}
	if got := c.requestCounts(t); !maps.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
