// Package hubconfig holds the settings of wdx-hub, a table of the settings
// engine, and the layout of its home. They are loaded once when wdx-hub starts
// and passed down as a value; nothing else reads the environment or the
// config file.
package hubconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/settings" //nolint:depguard // hubconfig is the hub's config on settings, as config is wdx's; the finish task adds it to the rule's exceptions
)

// Name is the hub's command name, which its home, socket and variables are
// named after.
const Name = "wdx-hub"

// EnvPrefix starts the name of every setting's environment variable:
// WDX_HUB_HOME, WDX_HUB_PUBLIC_URL.
const EnvPrefix = "WDX_HUB_"

// The choices of web.tls.
const (
	// TLSOff serves the web endpoint in plain HTTP, behind a proxy that
	// terminates TLS.
	TLSOff = "off"
	// TLSFiles serves it with the certificate and key at web.tls_cert and
	// web.tls_key.
	TLSFiles = "files"
)

// Config is every setting.
type Config struct {
	Home string
	// PublicURL is the address browsers and nodes reach the hub at, without
	// a trailing slash.
	PublicURL        string
	ListenWeb        string
	ListenNodes      string
	WebTLS           string // TLSOff or TLSFiles
	WebTLSCert       string
	WebTLSKey        string
	LogLevel         slog.Level
	LogRetentionDays int
	OtelEndpoint     string
	OtelInterval     time.Duration

	// From tells where each setting was taken from, by key: "default", the
	// environment variable, or the config file and line.
	From map[string]string
}

// PublicHost is the host of PublicURL, without its port.
func (c Config) PublicHost() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

var logLevels = []string{"debug", "info", "warn", "error"}

var tlsChoices = []string{TLSOff, TLSFiles}

// localHosts are the hosts public_url may name with http.
var localHosts = []string{"localhost", "127.0.0.1"}

var table = settings.Table[Config]{
	Rows:       rows,
	EnvPrefix:  EnvPrefix,
	AllFromEnv: true,
	Check:      tlsProblems,
	FileHeader: "# " + Name + " settings. " + Name + " setting set changes one; the running hub reads the file again.\n" +
		"# Every setting can also come from the environment, as " + EnvPrefix + "<KEY>, which wins over this file.\n" +
		"# public_url is required; every other setting is optional: uncomment one to change it.\n" +
		"# " + Name + " setting set and " + Name + " setting write rewrite this file: they keep the values set\n" +
		"# and add new settings, but not comments of your own.\n",
	DroppedHeading: "# No longer read by " + Name + ", kept for reference:",
}

var rows = []settings.Row[Config]{
	{
		Key:         "home",
		Description: "directory holding the config file, database, certificate authority, socket and logs",
		// The config file lives in home, so home cannot come from it.
		FromEnv: true,
		Default: defaultHome(),
		Set:     func(c *Config, v string) error { c.Home = v; return nil },
		Get:     func(c Config) string { return c.Home },
	},
	{
		Key:         "public_url",
		Description: "address browsers and nodes reach the hub at, such as https://hub.example.com; http only for localhost and 127.0.0.1",
		Required:    true,
		Set: func(c *Config, v string) error {
			if err := checkPublicURL(v); err != nil {
				return err
			}
			c.PublicURL = strings.TrimSuffix(v, "/")
			return nil
		},
		Get: func(c Config) string { return c.PublicURL },
	},
	{
		Key:         "listen.web",
		Description: "address the web endpoint, for browsers, listens on, as host:port; an empty host listens on every interface",
		Default:     ":8080",
		Set:         func(c *Config, v string) error { return setAddress(&c.ListenWeb, v) },
		Get:         func(c Config) string { return c.ListenWeb },
	},
	{
		Key:         "listen.nodes",
		Description: "address the node endpoint, for wdx daemons, listens on, as host:port; it always serves TLS with the hub's own certificate",
		Default:     ":8443",
		Set:         func(c *Config, v string) error { return setAddress(&c.ListenNodes, v) },
		Get:         func(c Config) string { return c.ListenNodes },
	},
	{
		Key:         "web.tls",
		Description: "how the web endpoint serves TLS: off, behind a proxy that terminates it, or files, with web.tls_cert and web.tls_key",
		Default:     TLSOff,
		Choices:     tlsChoices,
		Set: func(c *Config, v string) error {
			if !slices.Contains(tlsChoices, v) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(tlsChoices, ", "))
			}
			c.WebTLS = v
			return nil
		},
		Get: func(c Config) string { return c.WebTLS },
	},
	{
		Key:         "web.tls_cert",
		Description: "absolute path of the web endpoint's certificate in PEM, with its chain; required with web.tls files",
		Default:     "",
		Set:         func(c *Config, v string) error { return setPath(&c.WebTLSCert, v) },
		Get:         func(c Config) string { return c.WebTLSCert },
	},
	{
		Key:         "web.tls_key",
		Description: "absolute path of the web endpoint's private key in PEM; required with web.tls files",
		Default:     "",
		Set:         func(c *Config, v string) error { return setPath(&c.WebTLSKey, v) },
		Get:         func(c Config) string { return c.WebTLSKey },
	},
	{
		Key:         "log.level",
		Description: "lowest level logged: " + strings.Join(logLevels, ", "),
		Default:     "info",
		Choices:     logLevels,
		Set: func(c *Config, v string) error {
			if !slices.Contains(logLevels, strings.ToLower(v)) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(logLevels, ", "))
			}
			return c.LogLevel.UnmarshalText([]byte(v))
		},
		Get: func(c Config) string { return strings.ToLower(c.LogLevel.String()) },
	},
	{
		Key:         "log.retention_days",
		Description: "days of the commands' log files kept besides today's; wdx-hub serve logs to its output",
		Default:     "7",
		Set: func(c *Config, v string) error {
			days, err := strconv.Atoi(v)
			if err != nil || days < 1 {
				return fmt.Errorf("%q is not a whole number of at least 1", v)
			}
			c.LogRetentionDays = days
			return nil
		},
		Get: func(c Config) string { return strconv.Itoa(c.LogRetentionDays) },
	},
	{
		Key:         "otel.endpoint",
		Description: "OpenTelemetry collector the hub sends its metrics to over OTLP/HTTP; empty sends none",
		Default:     "",
		Set: func(c *Config, v string) error {
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("%q is not an http or https URL", v)
				}
			}
			c.OtelEndpoint = v
			return nil
		},
		Get: func(c Config) string { return c.OtelEndpoint },
	},
	{
		Key:         "otel.interval_seconds",
		Description: "seconds between two sends of the metrics",
		Default:     "15",
		Set: func(c *Config, v string) error {
			seconds, err := strconv.Atoi(v)
			if err != nil || seconds < 1 {
				return fmt.Errorf("%q is not a whole number of at least 1", v)
			}
			c.OtelInterval = time.Duration(seconds) * time.Second
			return nil
		},
		Get: func(c Config) string { return strconv.Itoa(int(c.OtelInterval / time.Second)) },
	},
}

// checkPublicURL refuses an address that is not http or https with a host
// and nothing after it, and http for a host other than localhost and
// 127.0.0.1: browsers and nodes would send their credentials in the clear.
func checkPublicURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("%q is not an http or https URL", v)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q has more than a scheme, a host and a port", v)
	}
	if u.Scheme == "http" && !slices.Contains(localHosts, u.Hostname()) {
		return fmt.Errorf("%q must be https: http is only for %s", v, strings.Join(localHosts, " and "))
	}
	return nil
}

// setAddress sets addr to v, a host and a port.
func setAddress(addr *string, v string) error {
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return fmt.Errorf("%q is not host:port", v)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%q has no port from 0 to 65535", v)
	}
	*addr = v
	return nil
}

// setPath sets path to v, an absolute path or empty.
func setPath(path *string, v string) error {
	if v != "" && !filepath.IsAbs(v) {
		return fmt.Errorf("%q is not an absolute path", v)
	}
	*path = v
	return nil
}

// tlsProblems is what web.tls files lacks: the certificate and the key, each
// a problem of its own.
func tlsProblems(c Config, from map[string]string) []settings.Problem {
	if c.WebTLS != TLSFiles {
		return nil
	}
	var problems []settings.Problem
	for key, value := range map[string]string{"web.tls_cert": c.WebTLSCert, "web.tls_key": c.WebTLSKey} {
		if value == "" {
			problems = append(problems, settings.Problem{Key: key, From: from[key], Value: &value, Reason: "required with web.tls " + TLSFiles})
		}
	}
	slices.SortFunc(problems, func(a, b settings.Problem) int { return strings.Compare(a.Key, b.Key) })
	return problems
}

func defaultHome() string {
	home, err := os.UserHomeDir() //nolint:forbidigo // the default in the settings table, which help and Default show
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, "."+Name)
}

// Default returns the defaults of the optional settings; public_url, which is
// required, is left empty.
func Default() Config {
	c, from, _ := table.Resolve(nil)
	c.From = from
	return c
}

// Load returns the settings: home from $WDX_HUB_HOME, the others from the
// config file in home, each overridden by its variable and defaulting when
// neither sets it. A missing file sets nothing. It fails with a
// *settings.Error listing every problem: a file that cannot be read or
// parsed, an unknown key, public_url missing, a value that does not parse,
// web.tls files without its files.
func Load(getenv func(string) string) (Config, error) {
	env := table.Env(getenv)
	home, _, ok := env.Lookup("home")
	if !ok {
		home = defaultHome()
	}
	file, problems := table.File(ConfigPath(home))
	c, from, err := table.Resolve([]settings.Source{file, env})
	c.From = from
	if e, ok := errors.AsType[*settings.Error](err); ok {
		problems = append(problems, e.Problems...)
	}
	if len(problems) > 0 {
		return c, &settings.Error{Problems: problems}
	}
	return c, nil
}

// Settings describes every setting.
func Settings() []settings.Setting { return table.Describe() }

// Value returns the setting key formatted as a source would give it.
func (c Config) Value(key string) string { return table.Value(c, key) }

// Shown is Value as it may be shown: [secret] for a secret setting that is set.
func (c Config) Shown(key string) string { return table.Shown(c, key) }

// Rewrite returns the hub's config file at path written anew from its
// settings, as settings.Table.Rewrite does, checked with the environment
// getenv reads over it.
func Rewrite(path string, set map[string]string, getenv func(string) string) (settings.Rewritten, error) {
	return table.Rewrite(path, set, table.Env(getenv))
}

// Set sets the setting key in the config file of home, under its lock,
// checked with the environment getenv reads over it.
func Set(home, key, value string, getenv func(string) string) (settings.Rewritten, error) {
	return table.Set(ConfigPath(home), ConfigLockPath(home), key, value, table.Env(getenv))
}

// Lock keeps others from rewriting the config file of home, which must exist,
// until unlock is called.
func Lock(home string) (unlock func(), err error) {
	return settings.Lock(ConfigLockPath(home))
}

// WriteFile writes content to the config file at path, whole or not at all.
func WriteFile(path string, content []byte) error {
	return settings.WriteFile(path, content)
}
