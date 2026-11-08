package hubconfig

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/settings"
)

// env is a fake environment.
type env map[string]string

func (e env) getenv(key string) string { return e[key] }

// homeWith returns a home holding a config file with content, and the
// environment that points at it.
func homeWith(t *testing.T, content string) (string, env) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, env{"WDX_HUB_HOME": home}
}

// problems returns the keys and reasons Load reports for content and the
// environment e, home aside.
func problems(t *testing.T, content string, e env) map[string]string {
	t.Helper()
	_, homeEnv := homeWith(t, content)
	maps.Copy(homeEnv, e)
	_, err := Load(homeEnv.getenv)
	cfgErr, ok := errors.AsType[*settings.Error](err)
	if !ok {
		t.Fatalf("want *settings.Error, got %v", err)
	}
	got := map[string]string{}
	for _, p := range cfgErr.Problems {
		got[p.Key] = p.Reason
	}
	return got
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	home, e := homeWith(t, "public_url: http://localhost:8080\n")
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Home: home, PublicURL: "http://localhost:8080", ListenWeb: ":8080", ListenNodes: ":8443", WebTLS: TLSOff,
		LogLevel: slog.LevelInfo, LogRetentionDays: 7, OtelInterval: 15 * time.Second, Registration: RegistrationClosed,
	}
	c.From = nil
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	d := Default()
	if !strings.HasSuffix(d.Home, "/.wdx-hub") || d.PublicURL != "" || d.OtelEndpoint != "" {
		t.Fatalf("unexpected defaults %+v", d)
	}
	for _, s := range Settings() {
		if s.Key != "public_url" && d.From[s.Key] != "default" {
			t.Errorf("%s: want from default, got %q", s.Key, d.From[s.Key])
		}
		if want := "WDX_HUB_" + strings.ToUpper(strings.ReplaceAll(s.Key, ".", "_")); s.EnvVar != want {
			t.Errorf("%s: read from %q, want %q", s.Key, s.EnvVar, want)
		}
	}
}

func TestPublicURLRequired(t *testing.T) {
	t.Parallel()
	if got := problems(t, "", nil); got["public_url"] != "required" || len(got) != 1 {
		t.Fatalf("want public_url required, got %v", got)
	}
}

// Every setting but home comes from the file, and from its variable over the
// file.
func TestEnvironmentOverFile(t *testing.T) {
	t.Parallel()
	home, e := homeWith(t, "public_url: https://file.example.com\nlisten:\n  web: :1\n  nodes: :2\nweb:\n  tls: files\n  tls_cert: /file/cert\n  tls_key: /file/key\nlog:\n  level: debug\n  retention_days: 3\notel:\n  endpoint: http://file:4318\n  interval_seconds: 5\n")
	fromFile, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile.PublicURL != "https://file.example.com" || fromFile.ListenWeb != ":1" || fromFile.WebTLSKey != "/file/key" || fromFile.LogLevel != slog.LevelDebug || fromFile.OtelInterval != 5*time.Second {
		t.Fatalf("want the file's values, got %+v", fromFile)
	}
	if from := fromFile.From["log.level"]; from != filepath.Join(home, "config.yaml")+":10" {
		t.Errorf("log.level from %q, want the file and its line", from)
	}

	vars := env{
		"WDX_HUB_PUBLIC_URL": "https://env.example.com/", "WDX_HUB_LISTEN_WEB": "127.0.0.1:3", "WDX_HUB_LISTEN_NODES": "127.0.0.1:4",
		"WDX_HUB_WEB_TLS": "off", "WDX_HUB_WEB_TLS_CERT": "/env/cert", "WDX_HUB_WEB_TLS_KEY": "/env/key",
		"WDX_HUB_LOG_LEVEL": "warn", "WDX_HUB_LOG_RETENTION_DAYS": "9",
		"WDX_HUB_OTEL_ENDPOINT": "https://env:4318", "WDX_HUB_OTEL_INTERVAL_SECONDS": "6",
		"WDX_HUB_MASTER_KEY": testKey, "WDX_HUB_REGISTRATION": "open", "WDX_HUB_TRUSTED_PROXIES": "10.0.0.1",
		"WDX_HUB_NODES_URL": "https://nodes.example.com:9443/",
	}
	maps.Copy(e, vars)
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Home: home, PublicURL: "https://env.example.com", ListenWeb: "127.0.0.1:3", ListenNodes: "127.0.0.1:4",
		WebTLS: TLSOff, WebTLSCert: "/env/cert", WebTLSKey: "/env/key", LogLevel: slog.LevelWarn, LogRetentionDays: 9,
		OtelEndpoint: "https://env:4318", OtelInterval: 6 * time.Second,
		MasterKey: testKeyBytes, Registration: RegistrationOpen, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/32")},
		NodesURL: "https://nodes.example.com:9443",
	}
	from := c.From
	c.From = nil
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	for _, s := range Settings() {
		if from[s.Key] != s.EnvVar {
			t.Errorf("%s from %q, want %s", s.Key, from[s.Key], s.EnvVar)
		}
	}
	if c.PublicHost() != "env.example.com" {
		t.Errorf("public host %q", c.PublicHost())
	}
}

func TestHomeOnlyFromEnvironment(t *testing.T) {
	t.Parallel()
	if got := problems(t, "public_url: http://localhost\nhome: /elsewhere\n", nil); got["home"] == "" {
		t.Fatalf("want home refused in the file, got %v", got)
	}
}

func TestPublicURL(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"http://localhost", "http://localhost:8080/", "http://127.0.0.1:9", "https://hub.example.com", "https://10.0.0.1:8443"} {
		if got := problems(t, "", env{"WDX_HUB_PUBLIC_URL": ok, "WDX_HUB_WEB_TLS": "nope"}); got["public_url"] != "" {
			t.Errorf("%s: want it taken, got %q", ok, got["public_url"])
		}
	}
	for _, bad := range []string{"http://hub.example.com", "http://10.0.0.1", "http://localhost.example.com", "ftp://localhost", "hub.example.com", "https://", "https://hub.example.com/path", "https://u:p@hub.example.com", "https://hub.example.com?q"} {
		if got := problems(t, "", env{"WDX_HUB_PUBLIC_URL": bad}); got["public_url"] == "" || got["public_url"] == "required" {
			t.Errorf("%s: want it refused, got %v", bad, got)
		}
	}
	if got := problems(t, "", env{"WDX_HUB_PUBLIC_URL": "http://hub.example.com"}); !strings.Contains(got["public_url"], "must be https") {
		t.Errorf("want http off localhost refused for being http, got %q", got["public_url"])
	}
}

func TestTLSFilesRequiresBoth(t *testing.T) {
	t.Parallel()
	const url = "public_url: https://hub.example.com\n"
	got := problems(t, url+"web:\n  tls: files\n", nil)
	if want := map[string]string{"web.tls_cert": "required with web.tls files", "web.tls_key": "required with web.tls files"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = problems(t, url+"web:\n  tls: files\n  tls_cert: /c\n", nil)
	if _, ok := got["web.tls_key"]; !ok || len(got) != 1 {
		t.Fatalf("want only the key missing, got %v", got)
	}
	home, e := homeWith(t, url+"web:\n  tls: files\n  tls_cert: /c\n")
	e["WDX_HUB_WEB_TLS_KEY"] = "/k"
	if c, err := Load(e.getenv); err != nil || c.WebTLSCert != "/c" || c.WebTLSKey != "/k" {
		t.Fatalf("want the files from the file and the environment, got %+v, %v (%s)", c, err, home)
	}
	if got := problems(t, url+"web:\n  tls: acme\n", nil); got["web.tls"] == "" {
		t.Fatalf("want acme refused, got %v", got)
	}
	if got := problems(t, url+"web:\n  tls_cert: cert.pem\n", nil); got["web.tls_cert"] == "" {
		t.Fatalf("want a relative path refused, got %v", got)
	}
}

func TestNodesURL(t *testing.T) {
	t.Parallel()
	c := Config{PublicURL: "https://hub.example.com"}
	if got := c.NodesEndpoint(8443); got != "https://hub.example.com:8443" {
		t.Errorf("derived %q, want public_url's host with the port", got)
	}
	c.NodesURL = "https://nodes.example.com:9443"
	if got := c.NodesEndpoint(8443); got != c.NodesURL {
		t.Errorf("got %q, want the setting", got)
	}
	for _, bad := range []string{"http://localhost:8443", "nodes.example.com:8443", "https://", "https://nodes.example.com/path", "https://u:p@nodes.example.com"} {
		if got := problems(t, "public_url: http://localhost\n", env{"WDX_HUB_NODES_URL": bad}); got["nodes_url"] == "" {
			t.Errorf("%s: want it refused, got %v", bad, got)
		}
	}
}

func TestListenAddresses(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"8080", "localhost", ":http", ":70000", ":-1"} {
		if got := problems(t, "public_url: http://localhost\n", env{"WDX_HUB_LISTEN_WEB": bad}); got["listen.web"] == "" {
			t.Errorf("%s: want it refused, got %v", bad, got)
		}
	}
}

// The file written from the table lists every setting the file can hold,
// commented, and loads back to the values set.
func TestWrittenFile(t *testing.T) {
	t.Parallel()
	home, e := homeWith(t, "public_url: https://hub.example.com\nold: 1\n")
	path := ConfigPath(home)
	r, err := Rewrite(path, nil, e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(r.Content), "# wdx-hub settings.") || !slices.Equal(r.Dropped, []string{"old"}) {
		t.Fatalf("want the header and old dropped, got %v\n%s", r.Dropped, r.Content)
	}
	for _, line := range []string{"public_url: https://hub.example.com\n", "# listen:\n", "#   web: :8080\n", "# web:\n", "#   tls: off\n", "# No longer read by wdx-hub, kept for reference:\n# old: 1\n"} {
		if !strings.Contains(string(r.Content), line) {
			t.Errorf("want %q in\n%s", line, r.Content)
		}
	}
	if strings.Contains(string(r.Content), "home") {
		t.Errorf("want no home, which only the environment sets, in\n%s", r.Content)
	}
	if err := WriteFile(path, r.Content); err != nil {
		t.Fatal(err)
	}
	if _, err := Set(home, "web.tls", "files", e.getenv); err == nil {
		t.Fatal("want web.tls files refused without its files")
	}
	if _, err := Set(home, "log.level", "debug", e.getenv); err != nil {
		t.Fatal(err)
	}
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL != "https://hub.example.com" || c.LogLevel != slog.LevelDebug || c.WebTLS != TLSOff {
		t.Fatalf("read back %+v", c)
	}
}

func TestMakeHome(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "a", "hub")
	if err := MakeHome(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MakeHome(home); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("home is %v, want 0700", info.Mode().Perm())
	}
	for path, name := range map[string]string{
		ConfigPath(home): "config.yaml", ConfigLockPath(home): "config.lock", DBPath(home): "hub.db", CADir(home): "ca",
		SocketPath(home): "wdx-hub.sock", LockPath(home): "wdx-hub.lock", LogsDir(home): "logs",
	} {
		if path != filepath.Join(home, name) {
			t.Errorf("%s, want %s in home", path, name)
		}
	}
}

// testKeyBytes is a master key, 32 bytes, and testKey the same in base64.
var testKeyBytes = bytes.Repeat([]byte{0x5a}, MasterKeySize)

var testKey = base64.StdEncoding.EncodeToString(testKeyBytes)

func TestMasterKey(t *testing.T) {
	t.Parallel()
	const url = "public_url: http://localhost\n"
	home, e := homeWith(t, url+"master_key: "+testKey+"\n")
	c, err := Load(e.getenv)
	if err != nil || !bytes.Equal(c.MasterKey, testKeyBytes) {
		t.Fatalf("want the key read, got %x, %v", c.MasterKey, err)
	}
	if got := c.Value("master_key"); got != testKey {
		t.Errorf("Value = %q, want the key in base64", got)
	}
	if got := c.Shown("master_key"); got != "[secret]" {
		t.Errorf("Shown = %q, want [secret]", got)
	}
	if c, err := Load(env{"WDX_HUB_HOME": home, "WDX_HUB_MASTER_KEY": ""}.getenv); err != nil || c.MasterKey == nil {
		t.Errorf("an empty variable sets nothing, got %x, %v", c.MasterKey, err)
	}
	_, without := homeWith(t, url)
	if c, err := Load(without.getenv); err != nil || c.MasterKey != nil {
		t.Errorf("without a key, want none and no problem, got %x, %v", c.MasterKey, err)
	}
	short := base64.StdEncoding.EncodeToString(testKeyBytes[:16])
	for value, reason := range map[string]string{short: "is 16 bytes, not 32", "not base64!": "is not base64"} {
		if got := problems(t, url, env{"WDX_HUB_MASTER_KEY": value}); got["master_key"] != reason {
			t.Errorf("%s: got %v, want %q", value, got, reason)
		}
	}
}

func TestRegistration(t *testing.T) {
	t.Parallel()
	const url = "public_url: http://localhost\n"
	for _, v := range []string{RegistrationClosed, RegistrationOpen} {
		_, e := homeWith(t, url+"registration: "+v+"\n")
		if c, err := Load(e.getenv); err != nil || c.Registration != v {
			t.Errorf("%s: got %q, %v", v, c.Registration, err)
		}
	}
	if got := problems(t, url+"registration: invite\n", nil); got["registration"] == "" {
		t.Errorf("want invite refused, got %v", got)
	}
}

func TestTrustedProxies(t *testing.T) {
	t.Parallel()
	const url = "public_url: http://localhost\n"
	_, e := homeWith(t, url)
	e["WDX_HUB_TRUSTED_PROXIES"] = "10.0.0.1, 192.168.1.7/16,2001:db8::1,fd00::/8"
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.1/32"), netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("2001:db8::1/128"), netip.MustParsePrefix("fd00::/8"),
	}
	if !slices.Equal(c.TrustedProxies, want) {
		t.Fatalf("got %v, want %v", c.TrustedProxies, want)
	}
	if got := c.Value("trusted_proxies"); got != "10.0.0.1,192.168.0.0/16,2001:db8::1,fd00::/8" {
		t.Errorf("Value = %q", got)
	}
	for _, bad := range []string{"10.0.0.300", "proxy.example.com", "10.0.0.0/33"} {
		if got := problems(t, url, env{"WDX_HUB_TRUSTED_PROXIES": bad}); got["trusted_proxies"] == "" {
			t.Errorf("%s: want it refused, got %v", bad, got)
		}
	}
}
