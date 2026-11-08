package hubconfig

import (
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/settings" //nolint:depguard // hubconfig is the hub's config on settings, as config is wdx's; the finish task adds it to the rule's exceptions
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
		LogLevel: slog.LevelInfo, LogRetentionDays: 7, OtelInterval: 15 * time.Second,
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
	r, err := Rewrite(path, nil)
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
	if _, err := Set(home, "web.tls", "files"); err == nil {
		t.Fatal("want web.tls files refused without its files")
	}
	if _, err := Set(home, "log.level", "debug"); err != nil {
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
